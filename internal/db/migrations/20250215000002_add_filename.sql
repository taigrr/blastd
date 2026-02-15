-- +goose Up
-- +goose StatementBegin
ALTER TABLE activities ADD COLUMN filename TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE activities DROP COLUMN filename;
-- +goose StatementEnd
