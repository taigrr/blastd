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
	db        *db.DB
	serverURL string
	apiToken  string
	interval  time.Duration
	done      chan struct{}
}

type activityPayload struct {
	Project          string  `json:"project,omitempty"`
	GitRemote        string  `json:"gitRemote,omitempty"`
	StartedAt        string  `json:"startedAt"`
	EndedAt          string  `json:"endedAt"`
	Filetype         string  `json:"filetype,omitempty"`
	LinesAdded       int     `json:"linesAdded"`
	LinesRemoved     int     `json:"linesRemoved"`
	GitCommit        string  `json:"gitCommit,omitempty"`
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

func NewSyncer(database *db.DB, serverURL, apiToken string, intervalMinutes int) *Syncer {
	return &Syncer{
		db:        database,
		serverURL: serverURL,
		apiToken:  apiToken,
		interval:  time.Duration(intervalMinutes) * time.Minute,
		done:      make(chan struct{}),
	}
}

func (s *Syncer) Start() {
	// Sync immediately on start
	s.syncOnce()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			// Final sync before shutdown
			s.syncOnce()
			return
		case <-ticker.C:
			s.syncOnce()
		}
	}
}

func (s *Syncer) Stop() {
	close(s.done)
}

func (s *Syncer) syncOnce() {
	if s.apiToken == "" {
		log.Println("sync: no API token configured, skipping")
		return
	}

	activities, err := s.db.GetUnsyncedActivities(100)
	if err != nil {
		log.Printf("sync: failed to get unsynced activities: %v", err)
		return
	}

	if len(activities) == 0 {
		return
	}

	log.Printf("sync: syncing %d activities", len(activities))

	// Convert to payload
	payloads := make([]activityPayload, len(activities))
	for i, a := range activities {
		payloads[i] = activityPayload{
			Project:          a.Project,
			GitRemote:        a.GitRemote,
			StartedAt:        a.StartedAt.Format(time.RFC3339),
			EndedAt:          a.EndedAt.Format(time.RFC3339),
			Filetype:         a.Filetype,
			LinesAdded:       a.LinesAdded,
			LinesRemoved:     a.LinesRemoved,
			GitCommit:        a.GitCommit,
			ActionsPerMinute: a.ActionsPerMinute,
			WordsPerMinute:   a.WordsPerMinute,
			Editor:           a.Editor,
			Machine:          a.Machine,
		}
	}

	body, err := json.Marshal(syncRequest{Activities: payloads})
	if err != nil {
		log.Printf("sync: failed to marshal request: %v", err)
		return
	}

	req, err := http.NewRequest("POST", s.serverURL+"/api/activities", bytes.NewReader(body))
	if err != nil {
		log.Printf("sync: failed to create request: %v", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("sync: request failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("sync: server returned status %d", resp.StatusCode)
		return
	}

	var syncResp syncResponse
	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		log.Printf("sync: failed to decode response: %v", err)
		return
	}

	if !syncResp.Success {
		log.Printf("sync: server returned success=false")
		return
	}

	// Mark activities as synced
	ids := make([]int64, len(activities))
	for i, a := range activities {
		ids[i] = a.ID
	}

	if err := s.db.MarkSynced(ids); err != nil {
		log.Printf("sync: failed to mark as synced: %v", err)
		return
	}

	log.Printf("sync: successfully synced %d activities", len(activities))
}

func (s *Syncer) SyncNow() error {
	if s.apiToken == "" {
		return fmt.Errorf("no API token configured")
	}
	s.syncOnce()
	return nil
}
