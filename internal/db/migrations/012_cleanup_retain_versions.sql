-- +goose Up
ALTER TABLE cleanup_policies ADD COLUMN retain_n_versions INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE cleanup_policies DROP COLUMN retain_n_versions;
