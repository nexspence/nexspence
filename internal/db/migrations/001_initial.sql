-- +goose Up
-- +goose StatementBegin

CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "pg_trgm";

-- ── Blob stores ──────────────────────────────────────────────
CREATE TABLE blob_stores (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    type        TEXT NOT NULL CHECK (type IN ('local', 's3')),
    config      JSONB NOT NULL DEFAULT '{}',
    quota_bytes BIGINT,
    used_bytes  BIGINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Routing rules ────────────────────────────────────────────
CREATE TABLE routing_rules (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    mode        TEXT NOT NULL DEFAULT 'BLOCK' CHECK (mode IN ('ALLOW', 'BLOCK')),
    matchers    TEXT[] NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Repositories ─────────────────────────────────────────────
CREATE TABLE repositories (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT NOT NULL UNIQUE,
    format              TEXT NOT NULL CHECK (format IN (
                            'maven2','npm','docker','pypi','go','nuget','helm','raw',
                            'apt','yum','cargo','conan','conda','terraform'
                        )),
    type                TEXT NOT NULL CHECK (type IN ('hosted','proxy','group')),
    blob_store_id       UUID REFERENCES blob_stores(id),
    online              BOOLEAN NOT NULL DEFAULT TRUE,
    format_config       JSONB NOT NULL DEFAULT '{}',
    http_config         JSONB NOT NULL DEFAULT '{}',
    proxy_config        JSONB NOT NULL DEFAULT '{}',
    cleanup_policy_ids  UUID[] NOT NULL DEFAULT '{}',
    quota_bytes         BIGINT,
    routing_rule_id     UUID REFERENCES routing_rules(id) ON DELETE SET NULL,
    description         TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_repositories_format ON repositories(format);
CREATE INDEX idx_repositories_type   ON repositories(type);

-- ── Components ───────────────────────────────────────────────
CREATE TABLE components (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repository_id   UUID NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    format          TEXT NOT NULL,
    group_id        TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL,
    version         TEXT NOT NULL,
    version_sort    TEXT NOT NULL DEFAULT '',
    extra           JSONB NOT NULL DEFAULT '{}',
    last_downloaded TIMESTAMPTZ,
    download_count  BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repository_id, format, group_id, name, version)
);

CREATE INDEX idx_components_repo    ON components(repository_id);
CREATE INDEX idx_components_name    ON components(name);
CREATE INDEX idx_components_fts     ON components USING gin(
    to_tsvector('english', group_id || ' ' || name || ' ' || version)
);
CREATE INDEX idx_components_name_trgm  ON components USING gin(name gin_trgm_ops);
CREATE INDEX idx_components_group_trgm ON components USING gin(group_id gin_trgm_ops);

-- ── Assets ───────────────────────────────────────────────────
CREATE TABLE assets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    component_id    UUID NOT NULL REFERENCES components(id) ON DELETE CASCADE,
    repository_id   UUID NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    path            TEXT NOT NULL,
    blob_store_id   UUID NOT NULL REFERENCES blob_stores(id),
    blob_key        TEXT NOT NULL,
    size_bytes      BIGINT NOT NULL DEFAULT 0,
    content_type    TEXT NOT NULL DEFAULT 'application/octet-stream',
    sha1            TEXT,
    sha256          TEXT,
    sha512          TEXT,
    md5             TEXT,
    uploader_id     UUID,
    last_modified   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_downloaded TIMESTAMPTZ,
    download_count  BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repository_id, path)
);

CREATE INDEX idx_assets_component       ON assets(component_id);
CREATE INDEX idx_assets_repo_path       ON assets(repository_id, path);
CREATE INDEX idx_assets_sha256          ON assets(sha256) WHERE sha256 IS NOT NULL;
CREATE INDEX idx_assets_last_downloaded ON assets(last_downloaded);

-- ── Users ────────────────────────────────────────────────────
CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username      TEXT NOT NULL UNIQUE,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT,
    first_name    TEXT NOT NULL DEFAULT '',
    last_name     TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    source        TEXT NOT NULL DEFAULT 'local' CHECK (source IN ('local','ldap','saml')),
    external_id   TEXT,
    last_login    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Roles ────────────────────────────────────────────────────
CREATE TABLE roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT 'local',
    builtin     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE role_roles (
    parent_role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    child_role_id  UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (parent_role_id, child_role_id),
    CHECK (parent_role_id != child_role_id)
);

-- ── Privileges ───────────────────────────────────────────────
CREATE TABLE privileges (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    type        TEXT NOT NULL CHECK (type IN ('wildcard','repository-view','repository-admin','application','script')),
    attrs       JSONB NOT NULL DEFAULT '{}',
    builtin     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE role_privileges (
    role_id      UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    privilege_id UUID NOT NULL REFERENCES privileges(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, privilege_id)
);

CREATE TABLE user_roles (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

-- ── API Tokens ───────────────────────────────────────────────
CREATE TABLE user_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    token_hash  TEXT NOT NULL UNIQUE,
    scopes      TEXT[] NOT NULL DEFAULT '{}',
    last_used   TIMESTAMPTZ,
    expires_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Cleanup policies ─────────────────────────────────────────
CREATE TABLE cleanup_policies (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    description     TEXT NOT NULL DEFAULT '',
    format          TEXT NOT NULL DEFAULT '*',
    criteria        JSONB NOT NULL DEFAULT '{}',
    schedule_cron   TEXT NOT NULL DEFAULT '0 2 * * *',
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    dry_run         BOOLEAN NOT NULL DEFAULT FALSE,
    last_run_at     TIMESTAMPTZ,
    last_run_freed  BIGINT,
    last_run_count  INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Scheduled tasks ──────────────────────────────────────────
CREATE TABLE scheduled_tasks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    type            TEXT NOT NULL,
    schedule_cron   TEXT NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    params          JSONB NOT NULL DEFAULT '{}',
    last_run_at     TIMESTAMPTZ,
    last_run_status TEXT,
    last_run_msg    TEXT,
    next_run_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Audit log (partitioned by month) ─────────────────────────
CREATE TABLE audit_events (
    id          BIGSERIAL,
    event_time  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    username    TEXT NOT NULL DEFAULT 'anonymous',
    remote_ip   INET,
    user_agent  TEXT,
    domain      TEXT NOT NULL,
    action      TEXT NOT NULL,
    entity_type TEXT,
    entity_id   TEXT,
    entity_name TEXT,
    context     JSONB NOT NULL DEFAULT '{}',
    result      TEXT NOT NULL DEFAULT 'success' CHECK (result IN ('success','failure','denied')),
    PRIMARY KEY (id, event_time)
) PARTITION BY RANGE (event_time);

-- Initial partitions — extend via scheduled task
CREATE TABLE audit_events_2026_04 PARTITION OF audit_events
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE audit_events_2026_05 PARTITION OF audit_events
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE audit_events_2026_06 PARTITION OF audit_events
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

CREATE INDEX idx_audit_time   ON audit_events(event_time DESC);
CREATE INDEX idx_audit_user   ON audit_events(user_id, event_time DESC);
CREATE INDEX idx_audit_domain ON audit_events(domain, action, event_time DESC);

-- ── LDAP servers ─────────────────────────────────────────────
CREATE TABLE ldap_servers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT NOT NULL UNIQUE,
    protocol            TEXT NOT NULL DEFAULT 'ldap' CHECK (protocol IN ('ldap','ldaps')),
    hostname            TEXT NOT NULL,
    port                INT NOT NULL DEFAULT 389,
    search_base         TEXT NOT NULL,
    auth_scheme         TEXT NOT NULL DEFAULT 'simple',
    bind_dn             TEXT,
    bind_password       TEXT,
    user_base_dn        TEXT,
    user_filter         TEXT NOT NULL DEFAULT '(objectClass=inetOrgPerson)',
    user_id_attr        TEXT NOT NULL DEFAULT 'uid',
    user_name_attr      TEXT NOT NULL DEFAULT 'cn',
    user_email_attr     TEXT NOT NULL DEFAULT 'mail',
    group_base_dn       TEXT,
    group_filter        TEXT NOT NULL DEFAULT '(objectClass=groupOfUniqueNames)',
    group_member_attr   TEXT NOT NULL DEFAULT 'uniqueMember',
    group_name_attr     TEXT NOT NULL DEFAULT 'cn',
    connection_timeout_sec INT NOT NULL DEFAULT 30,
    retry_delay_sec     INT NOT NULL DEFAULT 300,
    max_retries         INT NOT NULL DEFAULT 3,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    order_num           INT NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Migration jobs ───────────────────────────────────────────
CREATE TABLE migration_jobs (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_url       TEXT NOT NULL,
    source_user      TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending','running','paused','done','error')),
    migrate_repos    BOOLEAN NOT NULL DEFAULT TRUE,
    migrate_users    BOOLEAN NOT NULL DEFAULT TRUE,
    migrate_blobs    BOOLEAN NOT NULL DEFAULT TRUE,
    migrate_policies BOOLEAN NOT NULL DEFAULT TRUE,
    total_repos      INT NOT NULL DEFAULT 0,
    done_repos       INT NOT NULL DEFAULT 0,
    total_assets     BIGINT NOT NULL DEFAULT 0,
    done_assets      BIGINT NOT NULL DEFAULT 0,
    total_bytes      BIGINT NOT NULL DEFAULT 0,
    done_bytes       BIGINT NOT NULL DEFAULT 0,
    error_count      INT NOT NULL DEFAULT 0,
    last_error       TEXT,
    cursor           JSONB NOT NULL DEFAULT '{}',
    started_at       TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── System config ────────────────────────────────────────────
CREATE TABLE system_config (
    key         TEXT PRIMARY KEY,
    value       JSONB NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by  UUID REFERENCES users(id) ON DELETE SET NULL
);

-- ── Seed data ────────────────────────────────────────────────

-- Default blob store
INSERT INTO blob_stores (name, type, config) VALUES
    ('default', 'local', '{"path": "./data/blobs/default"}'),
    ('docker',  'local', '{"path": "./data/blobs/docker"}');

-- Built-in roles
INSERT INTO roles (name, description, builtin) VALUES
    ('nx-admin',       'Administrator with all privileges', TRUE),
    ('nx-anonymous',   'Anonymous access role',             TRUE),
    ('nx-developer',   'Developer — read/write access',     TRUE),
    ('nx-deployer',    'Deployer — upload artifacts',       TRUE),
    ('nx-replication', 'Replication service account',       TRUE);

-- Admin user (password: admin123 — change on first login)
-- Hash: bcrypt cost 12 of 'admin123'
INSERT INTO users (username, email, password_hash, first_name, status)
VALUES (
    'admin',
    'admin@example.com',
    '$2a$12$LQv3c1yqBWVHxkd0LHAkCOYz6TtxMQJqhN8/LewdBPj/VcSAg/ROS',
    'Admin',
    'active'
);

INSERT INTO users (username, email, first_name, status)
VALUES ('anonymous', 'anonymous@system', 'Anonymous', 'active');

-- Assign admin role to admin user
INSERT INTO user_roles (user_id, role_id)
SELECT u.id, r.id FROM users u, roles r
WHERE u.username = 'admin' AND r.name = 'nx-admin';

INSERT INTO user_roles (user_id, role_id)
SELECT u.id, r.id FROM users u, roles r
WHERE u.username = 'anonymous' AND r.name = 'nx-anonymous';

-- System config defaults
INSERT INTO system_config (key, value, description) VALUES
    ('baseUrl',                '"http://localhost:8081"', 'Base URL for HTTP links'),
    ('anonymousAccessEnabled', 'true',                   'Allow unauthenticated read access'),
    ('activeRealms',           '["local"]',              'Active authentication realms'),
    ('httpRequestTimeout',     '1800',                   'HTTP request timeout seconds');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS system_config;
DROP TABLE IF EXISTS migration_jobs;
DROP TABLE IF EXISTS ldap_servers;
DROP TABLE IF EXISTS audit_events_2026_04;
DROP TABLE IF EXISTS audit_events_2026_05;
DROP TABLE IF EXISTS audit_events_2026_06;
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS scheduled_tasks;
DROP TABLE IF EXISTS cleanup_policies;
DROP TABLE IF EXISTS user_tokens;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS role_privileges;
DROP TABLE IF EXISTS privileges;
DROP TABLE IF EXISTS role_roles;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS assets;
DROP TABLE IF EXISTS components;
DROP TABLE IF EXISTS repositories;
DROP TABLE IF EXISTS routing_rules;
DROP TABLE IF EXISTS blob_stores;
DROP EXTENSION IF EXISTS pg_trgm;
DROP EXTENSION IF EXISTS pgcrypto;

-- +goose StatementEnd
