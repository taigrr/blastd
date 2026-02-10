# blastd

Local daemon for [Blast](https://github.com/taigrr/blast) activity tracking. Caches activity data in SQLite and syncs to the Blast server every 15 minutes.

## Installation

```bash
go install github.com/taigrr/blastd@latest
```

## Configuration

Create `~/.config/blastd/config.toml`:

```toml
# Blast server URL
server_url = "https://blast.taigrr.com"

# API token from blast.taigrr.com/settings
api_token = "blast_xxxxx"

# Sync interval in minutes (default: 15)
sync_interval_minutes = 15

# Machine identifier (default: hostname)
machine = "macbook-pro"
```

## Usage

Run the daemon:

```bash
blastd
```

For systemd, create `~/.config/systemd/user/blastd.service`:

```ini
[Unit]
Description=Blast activity tracking daemon
After=network.target

[Service]
ExecStart=%h/go/bin/blastd
Restart=on-failure

[Install]
WantedBy=default.target
```

Then:

```bash
systemctl --user enable --now blastd
```

## Socket Protocol

The daemon listens on a Unix socket at `~/.local/share/blastd/blastd.sock`.

### Activity tracking

```json
{"type": "activity", "data": {
  "project": "blast",
  "git_remote": "git@github.com:taigrr/blast.git",
  "started_at": "2024-01-01T00:00:00Z",
  "ended_at": "2024-01-01T00:05:00Z",
  "filetype": "go",
  "lines_added": 10,
  "lines_removed": 5,
  "actions_per_minute": 45.5,
  "words_per_minute": 60.2
}}
```

### Ping

```json
{"type": "ping"}
```

## Related Projects

- [blast](https://github.com/taigrr/blast) - Web dashboard and API
- [blast.nvim](https://github.com/taigrr/blast.nvim) - Neovim plugin
