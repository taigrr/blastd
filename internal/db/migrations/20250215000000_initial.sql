-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS activities (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project TEXT,
    git_remote TEXT,
    started_at DATETIME NOT NULL,
    ended_at DATETIME NOT NULL,
    filetype TEXT,
    lines_added INTEGER DEFAULT 0,
    lines_removed INTEGER DEFAULT 0,
    git_commit TEXT,
    actions_per_minute REAL,
    words_per_minute REAL,
    editor TEXT DEFAULT 'neovim',
    machine TEXT,
    synced BOOLEAN DEFAULT FALSE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_activities_synced ON activities(synced);
CREATE INDEX IF NOT EXISTS idx_activities_started_at ON activities(started_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_activities_started_at;
DROP INDEX IF EXISTS idx_activities_synced;
DROP TABLE IF EXISTS activities;
-- +goose StatementEnd
