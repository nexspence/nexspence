-- +goose Up
CREATE TABLE IF NOT EXISTS webhooks (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    url        TEXT        NOT NULL,
    secret     TEXT        NOT NULL DEFAULT '',
    events     TEXT[]      NOT NULL DEFAULT '{}',
    active     BOOLEAN     NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS webhooks;
