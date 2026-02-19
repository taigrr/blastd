# blastd

Local daemon for [Blast](https://nvimblast.com) activity tracking. Caches activity data in SQLite and syncs to the Blast server every 10 minutes (with exponential backoff on failures).

## Installation

```bash
go install github.com/taigrr/blastd@latest
```

blastd is automatically started by the [blast.nvim](https://github.com/taigrr/blast.nvim) plugin, so running it manually is optional.
Note, that if blastd hasn't received any data for a while, it will also automatically stop itself to save resources, and will be restarted by blast.nvim when you start coding again.

## Configuration

Create `~/.config/blastd/config.toml`:

```toml
# Blast server URL
server_url = "https://nvimblast.com"

# API token from nvimblast.com/dashboard
auth_token = "blast_xxxxx"

# Sync interval in minutes (default: 10)
sync_interval_minutes = 10

# Machine identifier (default: hostname)
machine = "macbook-pro"

# Metrics-only mode — sends "private" for project name and git remote
# Useful if you want time/filetype/APM stats without revealing what you work on
# metrics_only = true
```

All config fields can also be set via environment variables with the `BLAST_` prefix:

| Config Key              | Env Var                       | Default                             |
| ----------------------- | ----------------------------- | ----------------------------------- |
| `server_url`            | `BLAST_SERVER_URL`            | `https://nvimblast.com`             |
| `auth_token`            | `BLAST_AUTH_TOKEN`            | _(empty)_                           |
| `sync_interval_minutes` | `BLAST_SYNC_INTERVAL_MINUTES` | `10`                                |
| `sync_batch_size`       | `BLAST_SYNC_BATCH_SIZE`       | `100`                               |
| `socket_path`           | `BLAST_SOCKET_PATH`           | `~/.local/share/blastd/blastd.sock` |
| `db_path`               | `BLAST_DB_PATH`               | `~/.local/share/blastd/blast.db`    |
| `machine`               | `BLAST_MACHINE`               | OS hostname                         |
| `metrics_only`          | `BLAST_METRICS_ONLY`          | `false`                             |

Config file values take precedence over env vars, which take precedence over defaults.

## Usage

```bash
blastd
blastd --version
blastd --help
```

## Privacy

Project names are never shown publicly, but they are sent to the Blast server so you can see a per-project breakdown on your own profile.
If you'd rather not share project names at all — whether it's a secret project or just a preference — there are two ways to opt out:

### Per-project: `.blast.toml`

Create a `.blast.toml` anywhere between a file and its git root:

```toml
# Override the project name (default: git directory name)
name = "my-project"

# Mark as private — activity is still synced, but project name and git remote
# are replaced with "private" so the server only sees time, filetype, and metrics
private = true
```

The editor plugin (e.g. blast.nvim) walks up from the current file to the git root, using the closest `.blast.toml` it finds. This supports monorepos — place `.blast.toml` in subdirectories to give them distinct names or mark specific folders as private.

### Global: metrics-only mode

Set `metrics_only = true` in `config.toml` or `BLAST_METRICS_ONLY=true` in your environment. This replaces **all** project names and git remotes with `"private"` at sync time, regardless of per-project `.blast.toml` settings. Useful if you want to track your coding habits without revealing any project information.

## Socket Protocol

The daemon listens on a Unix socket at `~/.local/share/blastd/blastd.sock`.

### Activity tracking

```json
{
  "type": "activity",
  "data": {
    "project": "blast",
    "git_remote": "git@github.com:taigrr/blastd.git",
    "started_at": "2024-01-01T00:00:00Z",
    "ended_at": "2024-01-01T00:05:00Z",
    "filetype": "go",
    "lines_added": 10,
    "lines_removed": 5,
    "actions_per_minute": 45.5,
    "words_per_minute": 60.2
  }
}
```

### Ping

```json
{ "type": "ping" }
```

### Sync

Trigger an immediate sync (rate-limited to 10 requests per 10-minute window):

```json
{ "type": "sync" }
```

## Related Projects

- [blast.nvim](https://github.com/taigrr/blast.nvim) - Neovim plugin (FOSS)
