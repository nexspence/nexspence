-- +goose Up
CREATE TABLE IF NOT EXISTS content_selectors (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL UNIQUE,
    description TEXT        NOT NULL DEFAULT '',
    expression  TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE privileges
    ADD COLUMN IF NOT EXISTS content_selector_id UUID REFERENCES content_selectors(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_privileges_selector ON privileges(content_selector_id)
    WHERE content_selector_id IS NOT NULL;

-- +goose Down
ALTER TABLE privileges DROP COLUMN IF EXISTS content_selector_id;
DROP TABLE IF EXISTS content_selectors;
