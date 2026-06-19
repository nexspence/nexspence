-- +goose Up
ALTER TABLE users ADD COLUMN tokens_valid_after TIMESTAMPTZ NOT NULL DEFAULT now();

-- +goose Down
ALTER TABLE users DROP COLUMN tokens_valid_after;
