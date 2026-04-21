-- +goose Up
ALTER TABLE repositories ADD COLUMN allow_anonymous BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE repositories DROP COLUMN allow_anonymous;
