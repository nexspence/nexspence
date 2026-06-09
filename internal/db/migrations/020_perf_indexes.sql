-- +goose Up
CREATE INDEX IF NOT EXISTS idx_assets_blob_key ON assets (blob_key);
CREATE INDEX IF NOT EXISTS idx_audit_username ON audit_events (username);

-- +goose Down
DROP INDEX IF EXISTS idx_assets_blob_key;
DROP INDEX IF EXISTS idx_audit_username;
