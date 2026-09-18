-- +goose Up
-- Singleton config for scheduled full-instance backups (spec 37). One row,
-- id pinned to 'default' by the CHECK, so Upsert never has to pick a target.
CREATE TABLE backup_settings (
    id              TEXT PRIMARY KEY DEFAULT 'default' CHECK (id = 'default'),
    enabled         BOOLEAN NOT NULL DEFAULT false,
    schedule_cron   TEXT NOT NULL DEFAULT '0 3 * * *',
    blob_store_id   UUID REFERENCES blob_stores(id) ON DELETE SET NULL,
    retention_count INTEGER NOT NULL DEFAULT 7,
    last_run_at     TIMESTAMPTZ,
    last_run_key    TEXT,
    last_run_error  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE backup_settings;
