-- +goose Up
-- Enforce that every privilege must have a content selector
-- This prevents the "user has no access" situation caused by privileges with NULL selector

-- Delete any privileges with NULL content_selector_id (they don't work anyway)
DELETE FROM privileges WHERE content_selector_id IS NULL AND type = 'repository-content-selector';

-- Make content_selector_id NOT NULL for repository-content-selector privileges
ALTER TABLE privileges
ADD CONSTRAINT content_selector_required
CHECK (type != 'repository-content-selector' OR content_selector_id IS NOT NULL);

-- +goose Down
ALTER TABLE privileges DROP CONSTRAINT content_selector_required;
