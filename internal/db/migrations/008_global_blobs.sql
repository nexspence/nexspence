-- +goose Up
-- Superseded: this table was dropped again in 030. Nothing outside its own
-- repository's tests ever wrote to it, so the guarantee described below was
-- never the one the code provided — deletion has always counted references via
-- the assets table (CountByBlobKey / CountByBlobKeyInStore). Kept as applied
-- history only; see 030_drop_dead_global_blobs.sql (#387).
--
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
