-- +goose Up
CREATE TABLE scan_results (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    component_id UUID NOT NULL REFERENCES components(id) ON DELETE CASCADE,
    scanner      TEXT NOT NULL,
    status       TEXT NOT NULL,
    critical     INT NOT NULL DEFAULT 0,
    high         INT NOT NULL DEFAULT 0,
    medium       INT NOT NULL DEFAULT 0,
    low          INT NOT NULL DEFAULT 0,
    unknown      INT NOT NULL DEFAULT 0,
    total        INT NOT NULL DEFAULT 0,
    scanned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    raw          JSONB,
    error        TEXT
);

CREATE INDEX idx_scan_results_component_scanned ON scan_results (component_id, scanned_at DESC);

-- +goose Down
DROP TABLE IF EXISTS scan_results;
