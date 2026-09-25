-- Automatic promotion on publish (#542).
--
-- A rule with auto_promote set starts by itself when a client publishes a
-- matching component into its from_repo. The publish only records the pair
-- (rule, component) in promotion_auto_queue; a background worker evaluates it
-- once the component has settled (no new asset for the settle window), so a
-- multi-file upload is promoted once, whole, and never from the upload request.
--
-- The queue row is the durable half: it survives a restart, and its unique key
-- keeps one row per (rule, component) however many assets the upload has. A
-- later publish into the same component bumps generation, which is how a worker
-- that was already evaluating the older state knows to leave the row for
-- another pass instead of deleting it. Replicas claim rows with FOR UPDATE SKIP
-- LOCKED and a lease (claimed_until), so each row is worked by one node at a
-- time, and a node that dies mid-row only delays it until the lease runs out.
--
-- A request filed by the worker has no user behind it: requested_by becomes
-- nullable and `automatic` says where it came from. At most one automatic
-- request per (rule, component) can be pending at once — the partial unique
-- index is what makes filing it idempotent across replicas and retries.
-- published_at is the publish an automatic request was last evaluated for:
-- Approve refuses it while no scan of that publish exists.
--
-- Clocks: due_at and claimed_until are scheduling state and use the
-- database's now(), so replicas agree on what is due. last_published_at is
-- compared with a scan's scanned_at, which the scanning process stamps with
-- its own clock, so it keeps the application clock — and only ever moves
-- forward (GREATEST), so a replica whose clock lags cannot pull it back.

-- +goose Up
ALTER TABLE promotion_rules ADD COLUMN IF NOT EXISTS auto_promote BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE promotion_requests ALTER COLUMN requested_by DROP NOT NULL;
ALTER TABLE promotion_requests ADD COLUMN IF NOT EXISTS automatic BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE promotion_requests ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ;
CREATE UNIQUE INDEX IF NOT EXISTS idx_promotion_requests_auto_pending
    ON promotion_requests (rule_id, component_id)
    WHERE automatic AND status = 'pending';

CREATE TABLE IF NOT EXISTS promotion_auto_queue (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id           UUID NOT NULL REFERENCES promotion_rules(id) ON DELETE CASCADE,
    component_id      UUID NOT NULL REFERENCES components(id) ON DELETE CASCADE,
    -- When the component last received an asset: the settle window and the
    -- scan-freshness check both count from here.
    last_published_at TIMESTAMPTZ NOT NULL,
    -- When the row is next worth looking at.
    due_at            TIMESTAMPTZ NOT NULL,
    -- Bumped by every publish; a worker only deletes the row it evaluated.
    generation        BIGINT NOT NULL DEFAULT 1,
    -- Transient failures only; waiting for the settle window or a scan is
    -- not a failure and does not count toward giving up.
    attempts          INT NOT NULL DEFAULT 0,
    -- Whether AUTO_PROMOTE_STARTED was audited for this generation.
    started           BOOLEAN NOT NULL DEFAULT false,
    -- Set while the rule's scan gate waits for a scan of this publish, so a
    -- finished scan can wake the row without skipping the settle window.
    waiting_for_scan  BOOLEAN NOT NULL DEFAULT false,
    reason            TEXT NOT NULL DEFAULT '',
    claimed_until     TIMESTAMPTZ,
    -- Names the current claim: Finish and Retry only act for its holder, so a
    -- worker whose lease ran out cannot overwrite the next claimer's state.
    claim_token       UUID,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (rule_id, component_id)
);

CREATE INDEX IF NOT EXISTS idx_promotion_auto_queue_due ON promotion_auto_queue (due_at);
CREATE INDEX IF NOT EXISTS idx_promotion_auto_queue_component ON promotion_auto_queue (component_id);

-- +goose Down
DROP TABLE IF EXISTS promotion_auto_queue;
DROP INDEX IF EXISTS idx_promotion_requests_auto_pending;
-- Automatic requests have no requester; the NOT NULL cannot come back while
-- they exist.
DELETE FROM promotion_requests WHERE requested_by IS NULL;
ALTER TABLE promotion_requests DROP COLUMN IF EXISTS published_at;
ALTER TABLE promotion_requests DROP COLUMN IF EXISTS automatic;
ALTER TABLE promotion_requests ALTER COLUMN requested_by SET NOT NULL;
ALTER TABLE promotion_rules DROP COLUMN IF EXISTS auto_promote;
