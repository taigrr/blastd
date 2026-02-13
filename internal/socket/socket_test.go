package socket

import (
	"bufio"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/taigrr/blastd/internal/db"
)

func setupTestSocket(t *testing.T) (*Server, *db.DB) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	sockPath := filepath.Join(t.TempDir(), "test.sock")
	server := NewServer(sockPath, database, "test-machine")

	if err := server.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	t.Cleanup(func() { server.Stop() })

	return server, database
}

func dial(t *testing.T, server *Server) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("unix", server.path, 2*time.Second)
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func sendAndRecv(t *testing.T, conn net.Conn, req any) Response {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatal("no response from server")
	}

	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func TestPing(t *testing.T) {
	server, _ := setupTestSocket(t)
	conn := dial(t, server)

	resp := sendAndRecv(t, conn, Request{Type: "ping"})
	if !resp.OK {
		t.Errorf("ping: OK = false, error = %q", resp.Error)
	}
}

func TestActivityInsertion(t *testing.T) {
	server, database := setupTestSocket(t)
	conn := dial(t, server)

	now := time.Now().UTC()
	activity := map[string]any{
		"project":            "blast",
		"git_remote":         "git@github.com:taigrr/blast.git",
		"started_at":         now.Add(-5 * time.Minute).Format(time.RFC3339),
		"ended_at":           now.Format(time.RFC3339),
		"filetype":           "go",
		"lines_added":        10,
		"lines_removed":      5,
		"actions_per_minute": 45.5,
		"words_per_minute":   60.2,
	}

	req := map[string]any{
		"type": "activity",
		"data": activity,
	}

	resp := sendAndRecv(t, conn, req)
	if !resp.OK {
		t.Fatalf("activity: OK = false, error = %q", resp.Error)
	}

	activities, err := database.GetUnsyncedActivities(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}

	a := activities[0]
	if a.Project != "blast" {
		t.Errorf("Project = %q, want %q", a.Project, "blast")
	}
	if a.Machine != "test-machine" {
		t.Errorf("Machine = %q, want %q", a.Machine, "test-machine")
	}
	if a.Editor != "neovim" {
		t.Errorf("Editor = %q, want %q (default)", a.Editor, "neovim")
	}
}

func TestActivityWithEditor(t *testing.T) {
	server, database := setupTestSocket(t)
	conn := dial(t, server)

	now := time.Now().UTC()
	activity := map[string]any{
		"project":    "blast",
		"started_at": now.Add(-5 * time.Minute).Format(time.RFC3339),
		"ended_at":   now.Format(time.RFC3339),
		"editor":     "vscode",
	}

	req := map[string]any{
		"type": "activity",
		"data": activity,
	}

	resp := sendAndRecv(t, conn, req)
	if !resp.OK {
		t.Fatalf("activity: OK = false, error = %q", resp.Error)
	}

	activities, err := database.GetUnsyncedActivities(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 1 {
		t.Fatalf("got %d activities, want 1", len(activities))
	}
	if activities[0].Editor != "vscode" {
		t.Errorf("Editor = %q, want %q", activities[0].Editor, "vscode")
	}
}

func TestUnknownRequestType(t *testing.T) {
	server, _ := setupTestSocket(t)
	conn := dial(t, server)

	resp := sendAndRecv(t, conn, Request{Type: "unknown"})
	if resp.OK {
		t.Error("expected OK = false for unknown type")
	}
	if resp.Error != "unknown request type" {
		t.Errorf("Error = %q, want %q", resp.Error, "unknown request type")
	}
}

func TestInvalidJSON(t *testing.T) {
	server, _ := setupTestSocket(t)
	conn := dial(t, server)

	conn.Write([]byte("not json\n"))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatal("no response")
	}

	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Error("expected OK = false for invalid json")
	}
}
