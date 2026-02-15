package config

import (
	"os"
	"path/filepath"

	"github.com/taigrr/jety"
)

type Config struct {
	ServerURL           string
	APIToken            string
	SyncIntervalMinutes int
	SyncBatchSize       int
	SocketPath          string
	DBPath              string
	Machine             string
}

func Load() (*Config, error) {
	homeDir, _ := os.UserHomeDir()
	dataDir := filepath.Join(homeDir, ".local", "share", "blastd")
	hostname, _ := os.Hostname()

	cm := jety.NewConfigManager().WithEnvPrefix("BLAST_")
	cm.SetConfigType("toml")

	cm.SetDefault("server_url", "https://nvimblast.com")
	cm.SetDefault("auth_token", "")
	cm.SetDefault("sync_interval_minutes", 10)
	cm.SetDefault("sync_batch_size", 100)
	cm.SetDefault("socket_path", filepath.Join(dataDir, "blastd.sock"))
	cm.SetDefault("db_path", filepath.Join(dataDir, "blast.db"))
	cm.SetDefault("machine", hostname)

	var configPaths []string
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		configPaths = append(configPaths, filepath.Join(xdg, "blastd", "config.toml"))
	}
	if home := os.Getenv("HOME"); home != "" {
		configPaths = append(configPaths, filepath.Join(home, ".config", "blastd", "config.toml"))
	}

	for _, path := range configPaths {
		if _, err := os.Stat(path); err == nil {
			cm.SetConfigFile(path)
			if err := cm.ReadInConfig(); err != nil {
				return nil, err
			}
			break
		}
	}

	cfg := &Config{
		ServerURL:           cm.GetString("server_url"),
		APIToken:            cm.GetString("auth_token"),
		SyncIntervalMinutes: cm.GetInt("sync_interval_minutes"),
		SyncBatchSize:       cm.GetInt("sync_batch_size"),
		SocketPath:          cm.GetString("socket_path"),
		DBPath:              cm.GetString("db_path"),
		Machine:             cm.GetString("machine"),
	}

	dbDir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return nil, err
	}

	return cfg, nil
}
