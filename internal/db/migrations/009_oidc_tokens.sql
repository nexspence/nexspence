-- +goose Up
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS oidc_id_token      TEXT,
    ADD COLUMN IF NOT EXISTS oidc_refresh_token TEXT;

-- +goose Down
ALTER TABLE users
    DROP COLUMN IF EXISTS oidc_id_token,
    DROP COLUMN IF EXISTS oidc_refresh_token;
