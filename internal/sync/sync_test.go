package sync

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taigrr/blastd/internal/db"
)

func setupTestSyncer(t *testing.T, handler http.Handler) (*Syncer, *db.DB) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	syncer := NewSyncer(database, server.URL, "test-token", 60, 10)
	return syncer, database
}

func insertActivities(t *testing.T, database *db.DB, n int) {
	t.Helper()
	now := time.Now().UTC()
	for i := range n {
		a := &db.Activity{
			Project:   "blast",
			GitRemote: "git@github.com:taigrr/blast.git",
			StartedAt: now.Add(time.Duration(i) * time.Minute),
			EndedAt:   now.Add(time.Duration(i+1) * time.Minute),
			Filetype:  "go",
			Editor:    "neovim",
			Machine:   "test",
		}
		if err := database.InsertActivity(a); err != nil {
			t.Fatal(err)
		}
	}
}

func okHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req syncRequest
		json.NewDecoder(r.Body).Decode(&req)
		resp := syncResponse{Success: true, Count: len(req.Activities)}
		for range req.Activities {
			resp.Activities = append(resp.Activities, struct {
				ID string `json:"id"`
			}{ID: "test-id"})
		}
		json.NewEncoder(w).Encode(resp)
	}
}

func TestSyncBatchSuccess(t *testing.T) {
	syncer, database := setupTestSyncer(t, okHandler())

	insertActivities(t, database, 5)

	n, err := syncer.syncBatch()
	if err != nil {
		t.Fatalf("syncBatch() error: %v", err)
	}
	if n != 5 {
		t.Errorf("synced %d, want 5", n)
	}

	remaining, err := database.GetUnsyncedActivities(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("%d unsynced remaining, want 0", len(remaining))
	}
}

func TestSyncBatchEmpty(t *testing.T) {
	syncer, _ := setupTestSyncer(t, okHandler())

	n, err := syncer.syncBatch()
	if err != nil {
		t.Fatalf("syncBatch() error: %v", err)
	}
	if n != 0 {
		t.Errorf("synced %d, want 0", n)
	}
}

func TestSyncBatchServerError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	syncer, database := setupTestSyncer(t, handler)
	insertActivities(t, database, 3)

	_, err := syncer.syncBatch()
	if err == nil {
		t.Fatal("expected error on 500 response")
	}

	remaining, err := database.GetUnsyncedActivities(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 3 {
		t.Errorf("%d unsynced remaining, want 3 (should not mark synced on error)", len(remaining))
	}
}

func TestSyncBatchServerFailure(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(syncResponse{Success: false})
	})

	syncer, database := setupTestSyncer(t, handler)
	insertActivities(t, database, 2)

	_, err := syncer.syncBatch()
	if err == nil {
		t.Fatal("expected error on success=false")
	}

	remaining, err := database.GetUnsyncedActivities(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Errorf("%d unsynced remaining, want 2", len(remaining))
	}
}

func TestDrainBacklogMultipleBatches(t *testing.T) {
	syncer, database := setupTestSyncer(t, okHandler())
	syncer.batchSize = 3

	insertActivities(t, database, 7)

	syncer.drainBacklog()

	remaining, err := database.GetUnsyncedActivities(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("%d unsynced remaining, want 0", len(remaining))
	}
}

func TestDrainBacklogNoToken(t *testing.T) {
	syncer, database := setupTestSyncer(t, okHandler())
	syncer.apiToken = ""

	insertActivities(t, database, 3)
	syncer.drainBacklog()

	remaining, err := database.GetUnsyncedActivities(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 3 {
		t.Errorf("%d unsynced remaining, want 3 (no token = no sync)", len(remaining))
	}
}

func TestBackoffIncreases(t *testing.T) {
	syncer, _ := setupTestSyncer(t, okHandler())

	syncer.increaseBackoff()
	if syncer.backoff != syncer.minBackoff {
		t.Errorf("first backoff = %s, want %s", syncer.backoff, syncer.minBackoff)
	}

	syncer.increaseBackoff()
	if syncer.backoff != 2*syncer.minBackoff {
		t.Errorf("second backoff = %s, want %s", syncer.backoff, 2*syncer.minBackoff)
	}

	syncer.backoff = syncer.maxBackoff
	syncer.increaseBackoff()
	if syncer.backoff != syncer.maxBackoff {
		t.Errorf("backoff should cap at %s, got %s", syncer.maxBackoff, syncer.backoff)
	}
}

func TestBackoffResets(t *testing.T) {
	syncer, _ := setupTestSyncer(t, okHandler())

	syncer.increaseBackoff()
	syncer.increaseBackoff()
	syncer.resetBackoff()

	if syncer.backoff != 0 {
		t.Errorf("backoff after reset = %s, want 0", syncer.backoff)
	}
}

func TestDrainBacklogRetriesOnError(t *testing.T) {
	var callCount atomic.Int32

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var req syncRequest
		json.NewDecoder(r.Body).Decode(&req)
		resp := syncResponse{Success: true, Count: len(req.Activities)}
		for range req.Activities {
			resp.Activities = append(resp.Activities, struct {
				ID string `json:"id"`
			}{ID: "test-id"})
		}
		json.NewEncoder(w).Encode(resp)
	})

	syncer, database := setupTestSyncer(t, handler)
	syncer.batchSize = 100
	syncer.minBackoff = 10 * time.Millisecond
	syncer.maxBackoff = 50 * time.Millisecond

	insertActivities(t, database, 2)

	syncer.drainBacklog()

	calls := callCount.Load()
	if calls < 3 {
		t.Errorf("expected at least 3 calls (2 failures + 1 success), got %d", calls)
	}

	remaining, err := database.GetUnsyncedActivities(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("%d unsynced remaining, want 0 (should eventually succeed)", len(remaining))
	}
}

func TestSyncPayloadFormat(t *testing.T) {
	var receivedBody syncRequest

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q, want %q", r.Header.Get("Authorization"), "Bearer test-token")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want %q", r.Header.Get("Content-Type"), "application/json")
		}

		json.NewDecoder(r.Body).Decode(&receivedBody)

		resp := syncResponse{Success: true, Count: len(receivedBody.Activities)}
		for range receivedBody.Activities {
			resp.Activities = append(resp.Activities, struct {
				ID string `json:"id"`
			}{ID: "test-id"})
		}
		json.NewEncoder(w).Encode(resp)
	})

	syncer, database := setupTestSyncer(t, handler)
	insertActivities(t, database, 1)

	n, err := syncer.syncBatch()
	if err != nil {
		t.Fatalf("syncBatch() error: %v", err)
	}
	if n != 1 {
		t.Fatalf("synced %d, want 1", n)
	}

	if len(receivedBody.Activities) != 1 {
		t.Fatalf("server received %d activities, want 1", len(receivedBody.Activities))
	}

	a := receivedBody.Activities[0]
	if a.Project != "blast" {
		t.Errorf("Project = %q, want %q", a.Project, "blast")
	}
	if a.Editor != "neovim" {
		t.Errorf("Editor = %q, want %q", a.Editor, "neovim")
	}
	if a.Machine != "test" {
		t.Errorf("Machine = %q, want %q", a.Machine, "test")
	}
	if a.StartedAt == "" {
		t.Error("StartedAt should not be empty")
	}
}
