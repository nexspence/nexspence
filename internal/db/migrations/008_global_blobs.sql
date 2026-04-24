-- +goose Up
-- Global blob reference table for content-addressed deduplication.
-- Each row tracks how many asset records point to a given physical blob.
-- BlobStore.Delete is called only when ref_count reaches 0.
CREATE TABLE global_blobs (
    blob_key   TEXT        PRIMARY KEY,
    size_bytes BIGINT      NOT NULL DEFAULT 0,
    ref_count  INT         NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS global_blobs;
