-- +goose Up
-- #342: user migration is scoped to explicit source realms ("default" = the
-- local realm). Empty means local-only — the only realm guaranteed to make
-- sense on a fresh target.
ALTER TABLE migration_jobs
    ADD COLUMN user_realms TEXT[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE migration_jobs
    DROP COLUMN user_realms;
