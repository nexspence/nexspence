-- The Nexus migration jobs table was written for a runner that did not exist:
-- a job could be created, but nothing ever read it back to do the work. Giving
-- it a runner needs three things the row could not hold.
--
-- 1. The source credential. A migration is a long transfer that has to survive
--    a restart, so the runner re-attaches to jobs left running — which means it
--    needs the Nexus password again, after the request that supplied it is long
--    gone. It is sealed with the same AES-256-GCM key replication targets use,
--    never stored in the clear, and never leaves the process in an API response.
--
-- 2. Per-scope flags for the security data. `migrate_policies` was one coarse
--    switch; privileges, roles and routing rules are three independent stages
--    and get a flag each. The old column stays as the default those three fall
--    back to, so a job created against the previous API keeps its meaning.
--
-- 3. `users.must_reset_password`. A migrated local user cannot bring its Nexus
--    password hash along, so it gets a random temporary one. The account logs in
--    normally — blocking it would strand every migrated user at once — and the
--    flag is what asks for a change afterwards.

-- +goose Up
ALTER TABLE migration_jobs ADD COLUMN IF NOT EXISTS source_password TEXT NOT NULL DEFAULT '';
ALTER TABLE migration_jobs ADD COLUMN IF NOT EXISTS migrate_privileges BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE migration_jobs ADD COLUMN IF NOT EXISTS migrate_roles BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE migration_jobs ADD COLUMN IF NOT EXISTS migrate_routing_rules BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE users ADD COLUMN IF NOT EXISTS must_reset_password BOOLEAN NOT NULL DEFAULT FALSE;

-- ListActive is polled on every startup and reads only the unfinished jobs.
CREATE INDEX IF NOT EXISTS idx_migration_jobs_status ON migration_jobs (status);

-- +goose Down
DROP INDEX IF EXISTS idx_migration_jobs_status;
ALTER TABLE users DROP COLUMN IF EXISTS must_reset_password;
ALTER TABLE migration_jobs DROP COLUMN IF EXISTS migrate_routing_rules;
ALTER TABLE migration_jobs DROP COLUMN IF EXISTS migrate_roles;
ALTER TABLE migration_jobs DROP COLUMN IF EXISTS migrate_privileges;
ALTER TABLE migration_jobs DROP COLUMN IF EXISTS source_password;
