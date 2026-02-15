-- +goose Up
-- +goose StatementBegin
ALTER TABLE activities RENAME COLUMN git_commit TO git_branch;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE activities RENAME COLUMN git_branch TO git_commit;
-- +goose StatementEnd
