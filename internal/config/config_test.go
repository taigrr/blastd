package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.ServerURL != "https://nvimblast.com" {
		t.Errorf("ServerURL = %q, want %q", cfg.ServerURL, "https://nvimblast.com")
	}
	if cfg.SyncIntervalMinutes != 10 {
		t.Errorf("SyncIntervalMinutes = %d, want 10", cfg.SyncIntervalMinutes)
	}
	if cfg.SyncBatchSize != 100 {
		t.Errorf("SyncBatchSize = %d, want 100", cfg.SyncBatchSize)
	}
	if cfg.Machine == "" {
		t.Error("Machine should default to hostname, got empty string")
	}
	if cfg.SocketPath == "" {
		t.Error("SocketPath should not be empty")
	}
	if cfg.DBPath == "" {
		t.Error("DBPath should not be empty")
	}
}

func TestLoadFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "blastd")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configContent := `
server_url = "https://custom.example.com"
auth_token = "blast_test123"
sync_interval_minutes = 5
sync_batch_size = 50
machine = "test-machine"
`
	configPath := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("HOME", tmpDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.ServerURL != "https://custom.example.com" {
		t.Errorf("ServerURL = %q, want %q", cfg.ServerURL, "https://custom.example.com")
	}
	if cfg.APIToken != "blast_test123" {
		t.Errorf("APIToken = %q, want %q", cfg.APIToken, "blast_test123")
	}
	if cfg.SyncIntervalMinutes != 5 {
		t.Errorf("SyncIntervalMinutes = %d, want 5", cfg.SyncIntervalMinutes)
	}
	if cfg.SyncBatchSize != 50 {
		t.Errorf("SyncBatchSize = %d, want 50", cfg.SyncBatchSize)
	}
	if cfg.Machine != "test-machine" {
		t.Errorf("Machine = %q, want %q", cfg.Machine, "test-machine")
	}
}

func TestLoadEnvVarOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	t.Setenv("BLAST_SERVER_URL", "https://env.example.com")
	t.Setenv("BLAST_AUTH_TOKEN", "env_token_123")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.ServerURL != "https://env.example.com" {
		t.Errorf("ServerURL = %q, want %q", cfg.ServerURL, "https://env.example.com")
	}
	if cfg.APIToken != "env_token_123" {
		t.Errorf("APIToken = %q, want %q", cfg.APIToken, "env_token_123")
	}
}

func TestLoadFileOverridesEnv(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "blastd")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configContent := `server_url = "https://file.example.com"`
	configPath := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("BLAST_SERVER_URL", "https://env.example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.ServerURL != "https://file.example.com" {
		t.Errorf("ServerURL = %q, want file value, got env value", cfg.ServerURL)
	}
}

func TestLoadNoXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.ServerURL != "https://nvimblast.com" {
		t.Errorf("ServerURL = %q, want default", cfg.ServerURL)
	}
}

func TestLoadNoHOME(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")

	_, err := Load()
	if err != nil {
		t.Fatalf("Load() should not error with missing HOME, got: %v", err)
	}
}
