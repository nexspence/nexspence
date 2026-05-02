-- +goose Up
CREATE TABLE blob_store_migrations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repository_name TEXT    NOT NULL,
    source_store_id UUID    REFERENCES blob_stores(id),
    target_store_id UUID    NOT NULL REFERENCES blob_stores(id),
    status          TEXT    NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','running','cancelled','done','failed')),
    total_assets    INT     NOT NULL DEFAULT 0,
    done_assets     INT     NOT NULL DEFAULT 0,
    total_bytes     BIGINT  NOT NULL DEFAULT 0,
    done_bytes      BIGINT  NOT NULL DEFAULT 0,
    error_message   TEXT,
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ON blob_store_migrations (repository_name);
CREATE INDEX ON blob_store_migrations (status) WHERE status IN ('pending','running');

-- +goose Down
DROP TABLE IF EXISTS blob_store_migrations;
