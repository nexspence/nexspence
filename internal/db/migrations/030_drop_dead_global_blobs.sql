-- +goose Up
-- global_blobs (008) described itself as the reference count deletion depends
-- on — "BlobStore.Delete is called only when ref_count reaches 0" — but nothing
-- outside its own tests ever wrote to it. The real mechanism is
-- CountByBlobKey/CountByBlobKeyInStore over the assets table, so every row here
-- is stale from the moment it was written, and the table's comment described a
-- guarantee the code does not provide (#387). Dropping it removes the trap of a
-- future change trusting ref_count, or "fixing" it, against a table that is
-- disconnected from the real delete path.
DROP TABLE IF EXISTS global_blobs;

-- +goose Down
CREATE TABLE IF NOT EXISTS global_blobs (
    blob_key   TEXT        PRIMARY KEY,
    size_bytes BIGINT      NOT NULL DEFAULT 0,
    ref_count  INT         NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
