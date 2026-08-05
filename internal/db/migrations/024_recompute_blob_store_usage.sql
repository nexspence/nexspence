-- blob_stores.used_bytes is what checkQuota reads to decide whether a write
-- fits, so it has to mean how full the store is. Until this release it was
-- incremented once per registered asset, and several paths register more than
-- one asset against a single stored object: an OCI manifest push registers the
-- tag and the sha256: digest alias, a cross-repository blob mount registers a
-- second asset on a blob already stored, and a proxy cache refresh re-registered
-- a path it had already counted. Every existing deployment therefore carries an
-- inflated counter, and a store that reports itself fuller than it is refuses
-- writes that fit.
--
-- The code fix only governs writes from here on, so the counters have to be
-- restated once. Doing it as a migration rather than an operator action is
-- deliberate: the symptom is a spurious "storage quota exceeded" that gives no
-- hint the counter is wrong, so an install nobody thinks to repair stays broken.
--
-- The honest figure is one size per distinct blob key per store — that is what
-- the store holds. Rows that disagree about a key's size (an alias left behind
-- when the path was overwritten in place) are read at their largest, the only
-- reading that cannot let a store overfill. Bytes on disk that no asset
-- references are not counted here; the blob GC now decrements what it collects,
-- so the counter converges rather than drifting.
--
-- Repeatable through BlobStoreRepo.RecomputeUsedBytes, which runs the same
-- statement for drift found later.

-- +goose Up
UPDATE blob_stores bs
SET used_bytes = COALESCE((
        SELECT SUM(k.size_bytes) FROM (
            SELECT DISTINCT ON (a.blob_key) a.size_bytes
            FROM assets a
            WHERE COALESCE(a.blob_store_id,
                           (SELECT d.id FROM blob_stores d WHERE d.name = 'default')) = bs.id
              AND a.blob_key IS NOT NULL AND TRIM(a.blob_key) <> ''
            ORDER BY a.blob_key, a.size_bytes DESC
        ) k
    ), 0),
    updated_at = NOW();

-- +goose Down
-- Down is a no-op: the previous numbers were wrong, and nothing recorded them.
SELECT 1;
