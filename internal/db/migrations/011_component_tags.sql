-- +goose Up
ALTER TABLE components ADD COLUMN IF NOT EXISTS tags text[] NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS idx_components_tags ON components USING GIN (tags);

-- +goose Down
DROP INDEX IF EXISTS idx_components_tags;
ALTER TABLE components DROP COLUMN IF EXISTS tags;
