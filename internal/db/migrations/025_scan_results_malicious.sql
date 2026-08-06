-- A malicious-package report (OSV.dev `MAL-…`, sourced from OpenSSF's
-- malicious-packages dataset) carries no CVSS score, so it used to land in the
-- `unknown` bucket next to "the scanner could not classify this" — a compromised
-- package counted toward a number nobody reads. It gets its own counter so it
-- can be aggregated, filtered, and shown on its own.
--
-- Existing rows keep 0: they were scanned before the classification existed, and
-- backfilling would mean re-querying OSV.dev for every component. The daily bulk
-- re-scan refills them on its next run.

-- +goose Up
ALTER TABLE scan_results ADD COLUMN IF NOT EXISTS malicious INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE scan_results DROP COLUMN IF EXISTS malicious;
