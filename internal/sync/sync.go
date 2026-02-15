package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/taigrr/blastd/internal/db"
)

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

func NewSyncer(database *db.DB, serverURL, apiToken string, intervalMinutes, batchSize int, metricsOnly bool) *Syncer {
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
	}
}

func (s *Syncer) Start() {
	s.drainBacklog()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			s.drainBacklog()
			return
		case <-ticker.C:
			s.drainBacklog()
		}
	}
}

func (s *Syncer) Stop() {
	close(s.done)
}

func (s *Syncer) drainBacklog() {
	if s.apiToken == "" {
		log.Println("sync: no API token configured, skipping")
		return
	}

	for {
		select {
		case <-s.done:
			return
		default:
		}

		n, err := s.syncBatch()
		if err != nil {
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

func (s *Syncer) syncBatch() (int, error) {
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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

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

func (s *Syncer) SyncNow() error {
	if s.apiToken == "" {
		return fmt.Errorf("no API token configured")
	}
	s.drainBacklog()
	return nil
}
