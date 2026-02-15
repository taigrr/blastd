-- +goose Up
-- +goose StatementBegin
ALTER TABLE activities ADD COLUMN client_id TEXT;

UPDATE activities SET client_id = lower(
    hex(randomblob(4)) || '-' ||
    hex(randomblob(2)) || '-4' ||
    substr(hex(randomblob(2)),2) || '-' ||
    substr('89ab', abs(random()) % 4 + 1, 1) ||
    substr(hex(randomblob(2)),2) || '-' ||
    hex(randomblob(6))
)
WHERE client_id IS NULL OR client_id = '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- SQLite does not support DROP COLUMN before 3.35.0;
-- this is a best-effort rollback for newer SQLite versions.
ALTER TABLE activities DROP COLUMN client_id;
-- +goose StatementEnd
