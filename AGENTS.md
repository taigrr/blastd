# AGENTS.md

> Guide for AI agents working in the blastd codebase.

## Project Overview

**blastd** is a local daemon for [Blast](https://github.com/taigrr/blast) activity tracking. It receives editor activity events over a Unix domain socket, caches them in a local SQLite database, and periodically syncs them to a remote Blast server via HTTP API.

Related projects (sibling directories):

- [blast](https://github.com/taigrr/blast) — Next.js web dashboard and API server (PostgreSQL/Prisma)
- [blast.nvim](https://github.com/taigrr/blast.nvim) — Neovim plugin (primary client for the socket)

## Commands

```bash
go build ./...           # Build all packages
go test ./...            # Run all tests
go vet ./...             # Static analysis
go run .                 # Run the daemon locally
go install .             # Install to $GOPATH/bin
```

No Makefile, no CI pipeline, no linter config. Use `goimports` for formatting and `staticcheck` if available.

## Code Organization

```
main.go                     # Entry point — loads config, creates daemon, handles SIGINT/SIGTERM
internal/
  config/config.go          # TOML config loading from ~/.config/blastd/config.toml
  config/config_test.go     # Config loading and defaults tests
  daemon/daemon.go          # Daemon orchestrator — wires together DB, socket, syncer
  db/db.go                  # SQLite database layer (modernc.org/sqlite, pure-Go)
  db/db_test.go             # Insert, query, mark-synced tests
  socket/socket.go          # Unix domain socket server — accepts JSON-line requests
  socket/socket_test.go     # End-to-end socket protocol tests
  sync/sync.go              # HTTP sync client — batches unsynced activities to server with exponential backoff
  sync/sync_test.go          # Sync batch, backlog drain, backoff, retry, and payload format tests
```

Everything is in `internal/` — no public API packages.

## Architecture & Data Flow

```
Editor plugin (blast.nvim)
    │ JSON over Unix socket (snake_case fields)
    ▼
socket.Server  ──► db.DB (SQLite)  ◄── sync.Syncer
                                         │ JSON over HTTP (camelCase fields)
                                         ▼
                                   Blast API server (Next.js)
                                   POST /api/activities
```

1. **Socket server** listens at `~/.local/share/blastd/blastd.sock` (permissions `0600`)
2. Clients send newline-delimited JSON messages (`{"type": "activity", "data": {...}}`, `{"type": "ping"}`, or `{"type": "sync"}`)
3. Activities are inserted into SQLite with `synced = FALSE`
4. **Syncer** runs on a ticker (default 10 min), drains all unsynced activities in batches (default 100 per HTTP request), looping until the backlog is empty
5. On failure, retries with exponential backoff (30s → 30min cap) before resuming the drain loop
6. On successful sync, activities are marked `synced = TRUE`
7. Syncer also drains on startup and on graceful shutdown
8. Clients can trigger an immediate sync via `{"type": "sync"}` — rate-limited to 10 requests per 10-minute window

## Integration With blast.nvim

The Neovim plugin (`blast.nvim/lua/blast/tracker.lua`) sends activity data over the socket with these fields:

| Field                | Type   | Notes                              |
| -------------------- | ------ | ---------------------------------- |
| `project`            | string | From git dir name or `.blast.toml` |
| `git_remote`         | string | `origin` remote URL                |
| `started_at`         | string | RFC 3339 UTC                       |
| `ended_at`           | string | RFC 3339 UTC                       |
| `filetype`           | string | Vim filetype                       |
| `actions_per_minute` | float  | Vim commands/min                   |
| `words_per_minute`   | float  | Typing speed                       |

The plugin does **not** send: `lines_added`, `lines_removed`, `git_commit`, or `editor`. These are optional in the socket protocol. The `editor` field defaults to `"neovim"` if omitted.

## Integration With blast Server

The server API (`blast/app/api/activities/route.ts`) expects `POST /api/activities` with:

- Auth: `Authorization: Bearer <token>` (SHA-256 hashed, matched against `ApiToken.tokenHash`)
- Body: `{"activities": [...]}` with camelCase field names
- The server auto-creates `Project` records from `project`/`gitRemote` on first sync
- Server computes `duration` from `startedAt`/`endedAt`

Server Zod schema fields (all camelCase): `project`, `gitRemote`, `startedAt`, `endedAt`, `filetype`, `linesAdded`, `linesRemoved`, `gitCommit`, `actionsPerMinute`, `wordsPerMinute`, `editor`, `machine`.

The sync payload in `sync.go` uses matching camelCase JSON tags — these must stay aligned.

## Key Dependencies

| Dependency                   | Purpose                                                      |
| ---------------------------- | ------------------------------------------------------------ |
| `github.com/taigrr/jety`     | Config loading (TOML files + env vars with `BLAST_` prefix)  |
| `modernc.org/sqlite`         | Pure-Go SQLite driver (no CGO required)                      |

No HTTP framework — uses `net/http` stdlib. No logging framework — uses `log` stdlib.

## Configuration

Config file: `$XDG_CONFIG_HOME/blastd/config.toml` or `~/.config/blastd/config.toml`

| Field                   | Env Var                          | Default                             | Notes                                                                 |
| ----------------------- | -------------------------------- | ----------------------------------- | --------------------------------------------------------------------- |
| `server_url`            | `BLAST_SERVER_URL`               | `https://nvimblast.com`             | Blast server base URL                                                 |
| `auth_token`            | `BLAST_AUTH_TOKEN`               | _(empty)_                           | Required for sync; without it, sync is skipped with a log warning     |
| `sync_interval_minutes` | `BLAST_SYNC_INTERVAL_MINUTES`    | `10`                                | How often to push activities                                          |
| `sync_batch_size`       | `BLAST_SYNC_BATCH_SIZE`          | `100`                               | Max activities per HTTP request (backlog is fully drained each cycle) |
| `socket_path`           | `BLAST_SOCKET_PATH`              | `~/.local/share/blastd/blastd.sock` | Unix socket location                                                  |
| `db_path`               | `BLAST_DB_PATH`                  | `~/.local/share/blastd/blast.db`    | SQLite database location                                              |
| `machine`               | `BLAST_MACHINE`                  | OS hostname                         | Machine identifier sent with each activity                            |

All config fields can be set via environment variables with the `BLAST_` prefix. Config file values take precedence over env vars, which take precedence over defaults.

## Code Patterns & Conventions

### Style

- Standard Go conventions, `goimports` formatting
- Structs use TOML tags for config (`toml:"field_name"`), JSON tags for socket/API
- Socket protocol uses snake_case JSON (`started_at`, `git_remote`)
- Sync API uses camelCase JSON (`startedAt`, `gitRemote`) to match the blast server
- Unexported types for internal payloads (`activityPayload`, `syncRequest`, `syncResponse`)
- Exported types for domain models (`db.Activity`, `config.Config`)

### Error Handling

- `main.go` uses `log.Fatalf` for startup failures
- Internal packages return errors to callers (no panics)
- `sync.go` retries with exponential backoff (30s min, 30min max) on HTTP or server errors; backoff resets on success
- Socket handler sends JSON error responses to clients, never crashes on bad input

### Concurrency

- Socket server runs in a goroutine via `go s.accept()`, spawns `go s.handle(conn)` per connection
- Syncer blocks in `Start()` with a `time.Ticker` loop — daemon relies on this blocking behavior
- `drainBacklog()` loops sending batches until the backlog is empty or the `done` channel fires; on error it sleeps with exponential backoff then retries
- Shutdown coordinated via `done` channels (`chan struct{}`) closed from `Stop()` methods
- Signal handling in `main.go` via `os/signal.Notify` for SIGINT/SIGTERM

### Database

- Schema auto-migrated on startup via `db.migrate()` using `CREATE TABLE IF NOT EXISTS`
- Uses `database/sql` directly (no ORM, no query builder)
- Indexes on `synced` and `started_at` columns
- Transactions used for batch updates (`MarkSynced`)
- Pure-Go SQLite (`modernc.org/sqlite`) — no CGO dependency

### Testing

- Tests use `t.TempDir()` for isolated DB and socket paths
- Socket tests are end-to-end: real Unix socket connections with JSON encoding
- Config tests use `t.Setenv()` to isolate environment variables
- Sync tests use `httptest.Server` to verify payload format and retry/backoff behavior
- Run `go test ./...` — no external dependencies or fixtures needed

## Gotchas

1. **Syncer.Start() blocks** — it's the last thing called in `Daemon.Run()`. The socket server runs in the background. Don't call `Start()` before `socket.Start()`.
2. **No CGO** — SQLite uses `modernc.org/sqlite` (pure Go). Cross-compilation works without a C compiler.
3. **Socket cleanup** — the server calls `os.Remove` on the socket path both at start (stale socket) and stop. If the daemon crashes without cleanup, the stale socket file must be removed manually.
4. **camelCase vs snake_case** — the sync API payload (to blast server) uses camelCase JSON keys, but the Unix socket protocol (from blast.nvim) uses snake_case. These are intentionally different to match their respective consumers. Don't unify them.
5. **Editor field default** — the socket protocol accepts an optional `editor` field. If omitted (as blast.nvim currently does), it defaults to `"neovim"`. Future editor plugins should send their own value.
