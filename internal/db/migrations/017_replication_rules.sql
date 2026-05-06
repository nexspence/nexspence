-- +goose Up
CREATE TABLE replication_rules (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 TEXT NOT NULL UNIQUE,
    source_repo          TEXT NOT NULL,
    target_url           TEXT NOT NULL,
    target_repo          TEXT NOT NULL,
    target_username      TEXT NOT NULL DEFAULT '',
    target_password_enc  TEXT NOT NULL DEFAULT '',
    cron_expr            TEXT NOT NULL DEFAULT '0 2 * * *',
    enabled              BOOLEAN NOT NULL DEFAULT true,
    last_run_at          TIMESTAMPTZ,
    last_run_status      TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE replication_history (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id           UUID NOT NULL REFERENCES replication_rules(id) ON DELETE CASCADE,
    started_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at       TIMESTAMPTZ,
    duration_ms       BIGINT,
    pushed_count      INT NOT NULL DEFAULT 0,
    skipped_count     INT NOT NULL DEFAULT 0,
    failed_count      INT NOT NULL DEFAULT 0,
    transferred_bytes BIGINT NOT NULL DEFAULT 0,
    error             TEXT
);

CREATE INDEX idx_replication_history_rule ON replication_history (rule_id, started_at DESC);

-- +goose Down
DROP TABLE IF EXISTS replication_history;
DROP TABLE IF EXISTS replication_rules;
