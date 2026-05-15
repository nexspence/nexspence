-- +goose Up

CREATE TABLE promotion_rules (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    TEXT NOT NULL UNIQUE,
    from_repo               TEXT NOT NULL REFERENCES repositories(name) ON UPDATE CASCADE ON DELETE CASCADE,
    to_repo                 TEXT NOT NULL REFERENCES repositories(name) ON UPDATE CASCADE ON DELETE CASCADE,
    path_filter             TEXT,
    require_scan_pass       BOOLEAN NOT NULL DEFAULT false,
    require_manual_approval BOOLEAN NOT NULL DEFAULT false,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE promotion_requests (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id      UUID NOT NULL REFERENCES promotion_rules(id) ON DELETE CASCADE,
    component_id UUID NOT NULL REFERENCES components(id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending','approved','rejected','completed','failed')),
    requested_by UUID NOT NULL REFERENCES users(id),
    reviewed_by  UUID REFERENCES users(id),
    reviewed_at  TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_promotion_requests_status ON promotion_requests (status);
CREATE INDEX idx_promotion_requests_rule   ON promotion_requests (rule_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS promotion_requests;
DROP TABLE IF EXISTS promotion_rules;
