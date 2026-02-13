package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	// Server settings
	ServerURL string `toml:"server_url"`
	APIToken  string `toml:"api_token"`

	// Sync settings
	SyncIntervalMinutes int `toml:"sync_interval_minutes"`
	SyncBatchSize       int `toml:"sync_batch_size"`

	// Socket path
	SocketPath string `toml:"socket_path"`

	// Database path
	DBPath string `toml:"db_path"`

	// Machine identifier
	Machine string `toml:"machine"`
}

func DefaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	dataDir := filepath.Join(homeDir, ".local", "share", "blastd")

	hostname, _ := os.Hostname()

	return &Config{
		ServerURL:           "https://blast.taigrr.com",
		SyncIntervalMinutes: 10,
		SyncBatchSize:       100,
		SocketPath:          filepath.Join(dataDir, "blastd.sock"),
		DBPath:              filepath.Join(dataDir, "blast.db"),
		Machine:             hostname,
	}
}

func Load() (*Config, error) {
	cfg := DefaultConfig()

	// Try to load from config file
	var configPaths []string
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		configPaths = append(configPaths, filepath.Join(xdg, "blastd", "config.toml"))
	}
	if home := os.Getenv("HOME"); home != "" {
		configPaths = append(configPaths, filepath.Join(home, ".config", "blastd", "config.toml"))
	}

	for _, path := range configPaths {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, cfg); err != nil {
				return nil, err
			}
			break
		}
	}

	// Ensure data directory exists
	dataDir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	return cfg, nil
}
