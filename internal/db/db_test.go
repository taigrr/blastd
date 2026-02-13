package db

import (
	"path/filepath"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) error: %v", dbPath, err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestOpenAndMigrate(t *testing.T) {
	database := setupTestDB(t)
	if database == nil {
		t.Fatal("expected non-nil DB")
	}
}

func TestInsertActivity(t *testing.T) {
	database := setupTestDB(t)

	a := &Activity{
		Project:          "blast",
		GitRemote:        "git@github.com:taigrr/blast.git",
		StartedAt:        time.Now().Add(-5 * time.Minute),
		EndedAt:          time.Now(),
		Filetype:         "go",
		LinesAdded:       10,
		LinesRemoved:     5,
		ActionsPerMinute: 45.5,
		WordsPerMinute:   60.2,
		Editor:           "neovim",
		Machine:          "test-machine",
	}

	if err := database.InsertActivity(a); err != nil {
		t.Fatalf("InsertActivity() error: %v", err)
	}
	if a.ID == 0 {
		t.Error("expected non-zero ID after insert")
	}
}

func TestGetUnsyncedActivities(t *testing.T) {
	database := setupTestDB(t)

	now := time.Now()
	for i := range 3 {
		a := &Activity{
			Project:   "blast",
			StartedAt: now.Add(time.Duration(-3+i) * time.Minute),
			EndedAt:   now.Add(time.Duration(-2+i) * time.Minute),
			Editor:    "neovim",
			Machine:   "test",
		}
		if err := database.InsertActivity(a); err != nil {
			t.Fatal(err)
		}
	}

	activities, err := database.GetUnsyncedActivities(10)
	if err != nil {
		t.Fatalf("GetUnsyncedActivities() error: %v", err)
	}
	if len(activities) != 3 {
		t.Errorf("got %d activities, want 3", len(activities))
	}

	for i := 1; i < len(activities); i++ {
		if activities[i].StartedAt.Before(activities[i-1].StartedAt) {
			t.Error("activities should be ordered by started_at ASC")
		}
	}
}

func TestGetUnsyncedActivitiesLimit(t *testing.T) {
	database := setupTestDB(t)

	now := time.Now()
	for i := range 5 {
		a := &Activity{
			Project:   "blast",
			StartedAt: now.Add(time.Duration(i) * time.Minute),
			EndedAt:   now.Add(time.Duration(i+1) * time.Minute),
			Editor:    "neovim",
		}
		if err := database.InsertActivity(a); err != nil {
			t.Fatal(err)
		}
	}

	activities, err := database.GetUnsyncedActivities(2)
	if err != nil {
		t.Fatalf("GetUnsyncedActivities() error: %v", err)
	}
	if len(activities) != 2 {
		t.Errorf("got %d activities, want 2", len(activities))
	}
}

func TestMarkSynced(t *testing.T) {
	database := setupTestDB(t)

	now := time.Now()
	var ids []int64
	for i := range 3 {
		a := &Activity{
			Project:   "blast",
			StartedAt: now.Add(time.Duration(i) * time.Minute),
			EndedAt:   now.Add(time.Duration(i+1) * time.Minute),
			Editor:    "neovim",
		}
		if err := database.InsertActivity(a); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}

	if err := database.MarkSynced(ids[:2]); err != nil {
		t.Fatalf("MarkSynced() error: %v", err)
	}

	activities, err := database.GetUnsyncedActivities(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 1 {
		t.Errorf("got %d unsynced activities, want 1", len(activities))
	}
	if activities[0].ID != ids[2] {
		t.Errorf("remaining activity ID = %d, want %d", activities[0].ID, ids[2])
	}
}

func TestMarkSyncedEmpty(t *testing.T) {
	database := setupTestDB(t)
	if err := database.MarkSynced(nil); err != nil {
		t.Fatalf("MarkSynced(nil) error: %v", err)
	}
}
