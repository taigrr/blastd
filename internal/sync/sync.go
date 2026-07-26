package sync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/taigrr/blastd/internal/db"
)

// ErrSyncInProgress is returned by SyncNow when a drain is already running.
// It signals that the call did no work (issued no request), so callers can
// treat it as a no-op rather than a real sync attempt.
var ErrSyncInProgress = errors.New("sync already in progress")

type Syncer struct {
	db          *db.DB
	serverURL   string
	apiToken    string
	interval    time.Duration
	batchSize   int
	metricsOnly bool
	backoff     time.Duration
	minBackoff  time.Duration
	maxBackoff  time.Duration
	done        chan struct{}
	stopped     chan struct{}
	client      *http.Client

	// lifecycleMu guards started/stopping and the close of done so Start and
	// Stop hand off cleanly: if Stop wins the race, Start must not touch the
	// DB (the daemon closes it right after Stop returns).
	lifecycleMu sync.Mutex
	started     bool
	stopping    bool

	// drainMu serializes drainBacklog so the ticker goroutine and
	// socket-triggered SyncNow can never send the same activities twice or
	// race on backoff.
	drainMu sync.Mutex
}

type activityPayload struct {
	ClientUUID       string  `json:"clientUUID"`
	Project          string  `json:"project,omitempty"`
	GitRemote        string  `json:"gitRemote,omitempty"`
	StartedAt        string  `json:"startedAt"`
	EndedAt          string  `json:"endedAt"`
	Filename         string  `json:"filename,omitempty"`
	Filetype         string  `json:"filetype,omitempty"`
	LinesAdded       int     `json:"linesAdded"`
	LinesRemoved     int     `json:"linesRemoved"`
	GitBranch        string  `json:"gitBranch,omitempty"`
	ActionsPerMinute float64 `json:"actionsPerMinute,omitempty"`
	WordsPerMinute   float64 `json:"wordsPerMinute,omitempty"`
	Editor           string  `json:"editor"`
	Machine          string  `json:"machine,omitempty"`
}

type syncRequest struct {
	Activities []activityPayload `json:"activities"`
}

type syncResponse struct {
	Success    bool `json:"success"`
	Count      int  `json:"count"`
	Activities []struct {
		ID string `json:"id"`
	} `json:"activities"`
}

const (
	httpTimeout           = 30 * time.Second
	shutdownDrainDeadline = 15 * time.Second
)

func NewSyncer(database *db.DB, serverURL, apiToken string, intervalMinutes, batchSize int, metricsOnly bool) *Syncer {
	if intervalMinutes < 1 {
		intervalMinutes = 1
	}
	if batchSize < 1 {
		batchSize = 1
	}
	return &Syncer{
		db:          database,
		serverURL:   serverURL,
		apiToken:    apiToken,
		interval:    time.Duration(intervalMinutes) * time.Minute,
		batchSize:   batchSize,
		metricsOnly: metricsOnly,
		minBackoff:  30 * time.Second,
		maxBackoff:  30 * time.Minute,
		done:        make(chan struct{}),
		stopped:     make(chan struct{}),
		client:      &http.Client{Timeout: httpTimeout},
	}
}

func (s *Syncer) Start() {
	s.lifecycleMu.Lock()
	if s.stopping {
		// Stop already fired before we started; do no work and never touch
		// the DB, since the daemon is about to close it.
		s.lifecycleMu.Unlock()
		close(s.stopped)
		return
	}
	s.started = true
	s.lifecycleMu.Unlock()

	defer close(s.stopped)
	s.drainBacklog()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			s.finalDrain()
			return
		case <-ticker.C:
			s.drainBacklog()
		}
	}
}

// Stop signals the syncer to stop and blocks until the Start goroutine has
// finished its final drain, so callers may safely close the DB afterwards.
// It is safe to call more than once.
func (s *Syncer) Stop() {
	s.lifecycleMu.Lock()
	wait := s.started
	if !s.stopping {
		s.stopping = true
		close(s.done)
	}
	s.lifecycleMu.Unlock()

	// Only wait for the Start goroutine if it actually began running;
	// otherwise stopped will still be closed by the Start guard above.
	if wait {
		<-s.stopped
	}
}

func (s *Syncer) drainBacklog() {
	s.drain(false)
}

// finalDrain runs a best-effort drain during shutdown. done is already closed
// at this point, so it must not consult done for early-exit; instead it is
// bounded by shutdownDrainDeadline so shutdown latency stays predictable.
func (s *Syncer) finalDrain() {
	s.drain(true)
}

func (s *Syncer) drain(final bool) {
	if s.apiToken == "" {
		if !final {
			log.Println("sync: no API token configured, skipping")
		}
		return
	}

	s.drainMu.Lock()
	defer s.drainMu.Unlock()

	var deadline time.Time
	if final {
		deadline = time.Now().Add(shutdownDrainDeadline)
	}

	for {
		if final {
			if time.Now().After(deadline) {
				log.Printf("sync: final drain deadline reached, leaving backlog for next start")
				return
			}
		} else {
			select {
			case <-s.done:
				return
			default:
			}
		}

		n, err := s.syncBatch()
		if err != nil {
			if final {
				log.Printf("sync: final drain error, giving up: %v", err)
				return
			}
			s.increaseBackoff()
			log.Printf("sync: error (retrying in %s): %v", s.backoff, err)

			select {
			case <-s.done:
				return
			case <-time.After(s.backoff):
				continue
			}
		}

		s.resetBackoff()

		if n < s.batchSize {
			return
		}
	}
}

func (s *Syncer) syncBatch() (synced int, err error) {
	activities, err := s.db.GetUnsyncedActivities(s.batchSize)
	if err != nil {
		return 0, fmt.Errorf("get unsynced activities: %w", err)
	}

	if len(activities) == 0 {
		return 0, nil
	}

	log.Printf("sync: syncing %d activities", len(activities))

	payloads := make([]activityPayload, len(activities))
	for i, a := range activities {
		project := a.Project
		gitRemote := a.GitRemote
		filename := a.Filename
		if s.metricsOnly {
			project = "private"
			gitRemote = "private"
			filename = ""
		}
		payloads[i] = activityPayload{
			ClientUUID:       a.ClientID,
			Project:          project,
			GitRemote:        gitRemote,
			StartedAt:        a.StartedAt.Format(time.RFC3339),
			EndedAt:          a.EndedAt.Format(time.RFC3339),
			Filename:         filename,
			Filetype:         a.Filetype,
			LinesAdded:       a.LinesAdded,
			LinesRemoved:     a.LinesRemoved,
			GitBranch:        a.GitBranch,
			ActionsPerMinute: a.ActionsPerMinute,
			WordsPerMinute:   a.WordsPerMinute,
			Editor:           a.Editor,
			Machine:          a.Machine,
		}
	}

	body, err := json.Marshal(syncRequest{Activities: payloads})
	if err != nil {
		return 0, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", s.serverURL+"/api/activities", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var syncResp syncResponse
	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	if !syncResp.Success {
		return 0, fmt.Errorf("server returned success=false")
	}

	ids := make([]int64, len(activities))
	for i, a := range activities {
		ids[i] = a.ID
	}

	if err := s.db.MarkSynced(ids); err != nil {
		return 0, fmt.Errorf("mark as synced: %w", err)
	}

	log.Printf("sync: successfully synced %d activities", len(activities))
	return len(activities), nil
}

func (s *Syncer) increaseBackoff() {
	if s.backoff == 0 {
		s.backoff = s.minBackoff
	} else {
		s.backoff *= 2
		if s.backoff > s.maxBackoff {
			s.backoff = s.maxBackoff
		}
	}
}

func (s *Syncer) resetBackoff() {
	s.backoff = 0
}

// SyncNow performs a single, non-retrying drain of the backlog and returns the
// real error so the socket caller learns whether the sync actually succeeded.
// It does not block on backoff or behind an in-progress drain.
func (s *Syncer) SyncNow() error {
	if s.apiToken == "" {
		return fmt.Errorf("no API token configured")
	}

	// Don't block behind an in-progress drain (which may be sleeping on
	// backoff for minutes); report it instead.
	if !s.drainMu.TryLock() {
		return ErrSyncInProgress
	}
	defer s.drainMu.Unlock()

	for {
		select {
		case <-s.done:
			return fmt.Errorf("shutting down")
		default:
		}

		n, err := s.syncBatch()
		if err != nil {
			return err
		}
		if n < s.batchSize {
			return nil
		}
	}
}
