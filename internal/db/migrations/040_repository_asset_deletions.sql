-- +goose Up
-- When an asset was last deleted from each repository. Index documents whose
-- date follows their newest entry (apt's Release, #728) take it into account,
-- so a deletion never makes them look older than the copy a client cached.
--
-- A trigger records it because assets are deleted along many paths (format
-- handlers, cleanup, browse, the components API, cascades), once per
-- statement rather than per row. It writes a table of its own, with no
-- foreign key: updating the repositories row instead would lock it after the
-- asset rows, while deleting a repository locks it before them — the two
-- would deadlock. A deleted repository's row is left behind; ids are never
-- reused, so it describes nothing.
CREATE TABLE repository_asset_deletions (
    repository_id UUID PRIMARY KEY,
    deleted_at    TIMESTAMPTZ NOT NULL
);

-- +goose StatementBegin
CREATE FUNCTION record_asset_deletions() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- Ordered, so two statements touching several repositories take the
    -- row locks in the same order.
    INSERT INTO repository_asset_deletions (repository_id, deleted_at)
    SELECT DISTINCT repository_id, now() FROM deleted_assets ORDER BY 1
    ON CONFLICT (repository_id) DO UPDATE
       SET deleted_at = GREATEST(repository_asset_deletions.deleted_at, EXCLUDED.deleted_at);
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER record_asset_deletions
    AFTER DELETE ON assets
    REFERENCING OLD TABLE AS deleted_assets
    FOR EACH STATEMENT EXECUTE FUNCTION record_asset_deletions();

-- +goose Down
DROP TRIGGER IF EXISTS record_asset_deletions ON assets;
DROP FUNCTION IF EXISTS record_asset_deletions();
DROP TABLE IF EXISTS repository_asset_deletions;
