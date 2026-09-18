-- A migration used to copy every repository on the source. Operators need to
-- move one hosted repo (or a handful) without a full-instance sync. Empty means
-- "every repository", so a job created before this column keeps its original
-- meaning.

-- +goose Up
ALTER TABLE migration_jobs
    ADD COLUMN IF NOT EXISTS repositories TEXT[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE migration_jobs
    DROP COLUMN IF EXISTS repositories;
