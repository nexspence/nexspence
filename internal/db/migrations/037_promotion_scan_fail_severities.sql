-- A promotion rule's require_scan_pass used to fail on a hardcoded list —
-- malicious, critical, high (#543). The list is now per rule. NULL (every
-- existing row) keeps that default; the CHECK keeps the column to the six
-- buckets a scan result is counted into, so a value no gate understands cannot
-- be stored behind the API's back.

-- +goose Up
ALTER TABLE promotion_rules ADD COLUMN IF NOT EXISTS scan_fail_severities TEXT[];
ALTER TABLE promotion_rules ADD CONSTRAINT promotion_rules_scan_fail_severities_check
    CHECK (scan_fail_severities <@ ARRAY['malicious','critical','high','medium','low','unknown']::TEXT[]);

-- +goose Down
ALTER TABLE promotion_rules DROP CONSTRAINT IF EXISTS promotion_rules_scan_fail_severities_check;
ALTER TABLE promotion_rules DROP COLUMN IF EXISTS scan_fail_severities;
