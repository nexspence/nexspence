-- The referrers API filters components on extra->>'oci_subject', and on OCI
-- every blob upload creates a component, so the table is large. cosign issues
-- one referrers call per verify, which without this index is a sequential scan
-- of the whole table.
--
-- The index is partial: only manifests that name a subject are worth indexing,
-- which is a small fraction of the rows.
--
-- The predicate is deliberately "(extra->>'oci_subject') IS NOT NULL" and not
-- the shorter "extra ? 'oci_subject'". The planner only uses a partial index
-- when it can prove the query implies the predicate, and it has no rule that
-- derives the jsonb "?" operator from an equality on "->>": measured against a
-- 60k-row components table, the "?" form left the query on a sequential scan
-- (60000 rows removed by filter) while this form is chosen as a bitmap index
-- scan. The two select the same rows for this query — "->>" yields NULL exactly
-- when the key is absent or its value is JSON null, and neither can equal the
-- digest the query looks for.

-- +goose Up
CREATE INDEX IF NOT EXISTS idx_components_oci_subject
    ON components ((extra->>'oci_subject'))
    WHERE (extra->>'oci_subject') IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_components_oci_subject;
