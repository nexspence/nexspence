-- +goose Up
-- Azure Blob Storage adapter (architecture.md storage section): allow blob
-- stores of type 'azure' alongside local/s3/group. Replaces the constraint
-- last rewritten by 014; every widening has to be its own migration because
-- an already-shipped Up is never rewritten.
ALTER TABLE blob_stores DROP CONSTRAINT IF EXISTS blob_stores_type_check;
ALTER TABLE blob_stores ADD CONSTRAINT blob_stores_type_check
    CHECK (type IN ('local', 's3', 'group', 'azure'));

-- +goose Down
-- NOT VALID so the rollback neither fails on nor rewrites blob stores that are
-- already azure: validating would abort the migration on the first such row,
-- and flipping one to 'local' would silently point a store holding real blobs
-- at a filesystem path it never had. Existing rows stay as they are; new azure
-- rows are rejected from here on, which is what rolling back is for.
ALTER TABLE blob_stores DROP CONSTRAINT IF EXISTS blob_stores_type_check;
ALTER TABLE blob_stores ADD CONSTRAINT blob_stores_type_check
    CHECK (type IN ('local', 's3', 'group')) NOT VALID;
