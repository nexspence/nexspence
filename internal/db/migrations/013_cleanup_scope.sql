-- +goose Up
ALTER TABLE cleanup_policies
  ADD COLUMN scope JSONB NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE cleanup_policies
  DROP COLUMN scope;
