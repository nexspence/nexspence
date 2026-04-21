-- +goose Up
ALTER TABLE privileges
  DROP CONSTRAINT IF EXISTS privileges_type_check;

ALTER TABLE privileges
  ADD CONSTRAINT privileges_type_check
    CHECK (type IN ('wildcard','repository-view','repository-admin','application','script','repository-content-selector'));

-- +goose Down
ALTER TABLE privileges
  DROP CONSTRAINT IF EXISTS privileges_type_check;

ALTER TABLE privileges
  ADD CONSTRAINT privileges_type_check
    CHECK (type IN ('wildcard','repository-view','repository-admin','application','script'));
