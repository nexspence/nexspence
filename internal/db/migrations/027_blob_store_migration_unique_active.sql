-- One active blob-store migration per repository, enforced by the database.
--
-- The service checked for an existing active migration with a SELECT and then
-- INSERTed, with nothing between the two. The distributed lock that was meant
-- to cover that window is a NoopLocker whenever Redis is not configured — the
-- documented default — so two genuinely simultaneous requests both passed the
-- check and both ran, each copying the whole dataset to its own destination and
-- leaving the loser's copy orphaned but fully accounted for in used_bytes.
--
-- A partial unique index makes the second INSERT fail instead, which the
-- repository translates into the same "already running" conflict the pre-check
-- already returns. Finished rows are unconstrained: a repository can be
-- migrated any number of times, one after another.

-- +goose Up

-- Any duplicates already in the table would block the index. They are the very
-- rows this index exists to prevent, so all but the newest per repository are
-- retired — the newest is the one whose destination the repository was left
-- pointing at.
UPDATE blob_store_migrations m
SET status        = 'failed',
    error_message = COALESCE(error_message, 'superseded by a concurrent migration of the same repository'),
    finished_at   = COALESCE(finished_at, now()),
    updated_at    = now()
WHERE m.status IN ('pending', 'running')
  AND m.id <> (
      SELECT x.id
      FROM blob_store_migrations x
      WHERE x.repository_name = m.repository_name
        AND x.status IN ('pending', 'running')
      ORDER BY x.created_at DESC, x.id DESC
      LIMIT 1
  );

CREATE UNIQUE INDEX IF NOT EXISTS blob_store_migrations_one_active_per_repo
    ON blob_store_migrations (repository_name)
    WHERE status IN ('pending', 'running');

-- +goose Down
DROP INDEX IF EXISTS blob_store_migrations_one_active_per_repo;
