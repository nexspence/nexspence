-- +goose Up
ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_source_check,
    ADD CONSTRAINT users_source_check CHECK (source IN ('local', 'ldap', 'saml', 'oidc'));

-- +goose Down
-- Revert: remove rows with source='oidc' first if any exist, then restore old constraint
DELETE FROM users WHERE source = 'oidc';
ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_source_check,
    ADD CONSTRAINT users_source_check CHECK (source IN ('local', 'ldap', 'saml'));
