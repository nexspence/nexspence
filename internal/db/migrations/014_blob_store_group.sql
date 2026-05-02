-- +goose Up
ALTER TABLE blob_stores DROP CONSTRAINT IF EXISTS blob_stores_type_check;
ALTER TABLE blob_stores ADD CONSTRAINT blob_stores_type_check
    CHECK (type IN ('local', 's3', 'group'));

-- +goose Down
ALTER TABLE blob_stores DROP CONSTRAINT IF EXISTS blob_stores_type_check;
ALTER TABLE blob_stores ADD CONSTRAINT blob_stores_type_check
    CHECK (type IN ('local', 's3'));
