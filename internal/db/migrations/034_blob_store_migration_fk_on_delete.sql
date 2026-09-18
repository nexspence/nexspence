-- Blob-store migration rows are an audit trail, not a live pointer.
--
-- Migration 015 created blob_store_migrations with FKs to blob_stores and no
-- ON DELETE clause, so Postgres' default NO ACTION applies. A history row then
-- blocks DELETE on blob_stores forever — including after the repository is
-- gone. repository_name is TEXT with no FK to repositories on purpose: the
-- row must outlive the repository so an operator can still see that a
-- migration ran, against which stores, and how far it got. There is no
-- Delete on the repo and no pruning on repository delete; treating leftover
-- rows as a leak would erase that trail.
--
-- ON DELETE SET NULL (and a nullable target_store_id) lets the store go while
-- keeping the row. CASCADE is wrong: deleting a store must not delete the
-- history of how artifacts moved through it. An empty target is only a
-- historical state; Create still refuses to record a new migration without
-- one.

-- +goose Up

ALTER TABLE blob_store_migrations
    ALTER COLUMN target_store_id DROP NOT NULL;

ALTER TABLE blob_store_migrations
    DROP CONSTRAINT IF EXISTS blob_store_migrations_source_store_id_fkey;
ALTER TABLE blob_store_migrations
    DROP CONSTRAINT IF EXISTS blob_store_migrations_target_store_id_fkey;

ALTER TABLE blob_store_migrations
    ADD CONSTRAINT blob_store_migrations_source_store_id_fkey
    FOREIGN KEY (source_store_id) REFERENCES blob_stores(id) ON DELETE SET NULL;
ALTER TABLE blob_store_migrations
    ADD CONSTRAINT blob_store_migrations_target_store_id_fkey
    FOREIGN KEY (target_store_id) REFERENCES blob_stores(id) ON DELETE SET NULL;

-- +goose Down

-- Down is lossy: rows whose target_store_id is already NULL cannot satisfy
-- the restored NOT NULL constraint, so they are deleted first. Up never
-- removes history — only rolling this migration back does.
DELETE FROM blob_store_migrations WHERE target_store_id IS NULL;

ALTER TABLE blob_store_migrations
    DROP CONSTRAINT IF EXISTS blob_store_migrations_source_store_id_fkey;
ALTER TABLE blob_store_migrations
    DROP CONSTRAINT IF EXISTS blob_store_migrations_target_store_id_fkey;

ALTER TABLE blob_store_migrations
    ALTER COLUMN target_store_id SET NOT NULL;

ALTER TABLE blob_store_migrations
    ADD CONSTRAINT blob_store_migrations_source_store_id_fkey
    FOREIGN KEY (source_store_id) REFERENCES blob_stores(id);
ALTER TABLE blob_store_migrations
    ADD CONSTRAINT blob_store_migrations_target_store_id_fkey
    FOREIGN KEY (target_store_id) REFERENCES blob_stores(id);
