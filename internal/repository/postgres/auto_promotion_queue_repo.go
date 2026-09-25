package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// AutoPromotionQueueRepo is the postgres-backed repository.AutoPromotionQueueRepo.
type AutoPromotionQueueRepo struct {
	db *pgxpool.Pool
}

var _ repository.AutoPromotionQueueRepo = (*AutoPromotionQueueRepo)(nil)

// NewAutoPromotionQueueRepo returns a postgres-backed AutoPromotionQueueRepo.
func NewAutoPromotionQueueRepo(db *pgxpool.Pool) *AutoPromotionQueueRepo {
	return &AutoPromotionQueueRepo{db: db}
}

const autoQueueFields = `id, rule_id, component_id, last_published_at, due_at,
	generation, attempts, waiting_for_scan, started, reason, COALESCE(claim_token::text, '')`

func scanAutoEntry(row pgx.Row) (domain.AutoPromotionEntry, error) {
	var e domain.AutoPromotionEntry
	err := row.Scan(&e.ID, &e.RuleID, &e.ComponentID, &e.LastPublishedAt, &e.DueAt,
		&e.Generation, &e.Attempts, &e.WaitingForScan, &e.Started, &e.Reason, &e.ClaimToken)
	return e, err
}

func collectAutoEntries(rows pgx.Rows) ([]domain.AutoPromotionEntry, error) {
	defer rows.Close()
	var out []domain.AutoPromotionEntry
	for rows.Next() {
		e, err := scanAutoEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EnqueuePublish is one statement: the rules come from promotion_rules in the
// same INSERT ... SELECT, so a repository without an auto_promote rule costs a
// single index lookup and writes nothing. A publish into a component already
// queued restarts the settle window on the database clock, resets the row's
// per-generation state and bumps its generation; last_published_at only moves
// forward, so a replica with a lagging clock cannot pull it back.
func (r *AutoPromotionQueueRepo) EnqueuePublish(ctx context.Context, fromRepo, componentID string, publishedAt time.Time, settle time.Duration) (int, error) {
	tag, err := r.db.Exec(ctx,
		`INSERT INTO promotion_auto_queue (rule_id, component_id, last_published_at, due_at)
		 SELECT id, $2, $3, now() + $4 * interval '1 microsecond'
		 FROM promotion_rules WHERE from_repo = $1 AND auto_promote
		 ON CONFLICT (rule_id, component_id) DO UPDATE
		 SET last_published_at = GREATEST(promotion_auto_queue.last_published_at, EXCLUDED.last_published_at),
		     due_at            = EXCLUDED.due_at,
		     generation        = promotion_auto_queue.generation + 1,
		     attempts          = 0,
		     waiting_for_scan  = false,
		     started           = false,
		     reason            = ''`,
		fromRepo, componentID, publishedAt, settle.Microseconds())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// Claim takes due, unclaimed (or lease-expired) rows with FOR UPDATE SKIP
// LOCKED, so two replicas claiming at the same moment get disjoint sets, and
// stamps each with a fresh claim token.
func (r *AutoPromotionQueueRepo) Claim(ctx context.Context, lease time.Duration, limit int) ([]domain.AutoPromotionEntry, error) {
	rows, err := r.db.Query(ctx,
		`UPDATE promotion_auto_queue
		 SET claimed_until = now() + $1 * interval '1 microsecond', claim_token = gen_random_uuid()
		 WHERE id IN (
		     SELECT id FROM promotion_auto_queue
		     WHERE due_at <= now() AND (claimed_until IS NULL OR claimed_until <= now())
		     ORDER BY due_at
		     LIMIT $2
		     FOR UPDATE SKIP LOCKED)
		 RETURNING `+autoQueueFields,
		lease.Microseconds(), limit)
	if err != nil {
		return nil, err
	}
	return collectAutoEntries(rows)
}

// Finish deletes the row it evaluated; a row a publish refreshed since the
// claim survives, unclaimed, for its next pass. Both only for the holder of
// the claim.
func (r *AutoPromotionQueueRepo) Finish(ctx context.Context, e domain.AutoPromotionEntry) error {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM promotion_auto_queue WHERE id = $1 AND generation = $2 AND claim_token = $3::uuid`,
		e.ID, e.Generation, e.ClaimToken)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	_, err = r.db.Exec(ctx,
		`UPDATE promotion_auto_queue SET claimed_until = NULL, claim_token = NULL
		 WHERE id = $1 AND claim_token = $2::uuid`, e.ID, e.ClaimToken)
	return err
}

// Retry reschedules the generation it evaluated; a refreshed row keeps the
// due time and state the publish gave it. Only for the holder of the claim.
func (r *AutoPromotionQueueRepo) Retry(ctx context.Context, e domain.AutoPromotionEntry, rt repository.AutoPromotionRetry) error {
	inc := 0
	if rt.CountAttempt {
		inc = 1
	}
	_, err := r.db.Exec(ctx,
		`UPDATE promotion_auto_queue
		 SET claimed_until    = NULL,
		     claim_token      = NULL,
		     due_at           = CASE WHEN generation = $2 THEN now() + $3 * interval '1 microsecond' ELSE due_at END,
		     attempts         = CASE WHEN generation = $2 THEN attempts + $4 ELSE attempts END,
		     waiting_for_scan = CASE WHEN generation = $2 THEN $5 ELSE waiting_for_scan END,
		     started          = CASE WHEN generation = $2 THEN $6 ELSE started END,
		     reason           = CASE WHEN generation = $2 THEN $7 ELSE reason END
		 WHERE id = $1 AND claim_token = $8::uuid`,
		e.ID, e.Generation, rt.DueIn.Microseconds(), inc, rt.WaitingForScan, rt.Started, rt.Reason, e.ClaimToken)
	return err
}

// WakeForScan brings forward only rows already waiting for a scan: those are
// past their settle window, so a scan finishing mid-upload cannot cut it short.
func (r *AutoPromotionQueueRepo) WakeForScan(ctx context.Context, componentID string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE promotion_auto_queue SET due_at = now()
		 WHERE component_id = $1 AND waiting_for_scan AND due_at > now()`,
		componentID)
	return err
}

// Queued reports whether (ruleID, componentID) has a row waiting.
func (r *AutoPromotionQueueRepo) Queued(ctx context.Context, ruleID, componentID string) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM promotion_auto_queue WHERE rule_id = $1 AND component_id = $2)`,
		ruleID, componentID).Scan(&ok)
	return ok, err
}

// List returns every queued row, oldest due first.
func (r *AutoPromotionQueueRepo) List(ctx context.Context) ([]domain.AutoPromotionEntry, error) {
	rows, err := r.db.Query(ctx, `SELECT `+autoQueueFields+` FROM promotion_auto_queue ORDER BY due_at`)
	if err != nil {
		return nil, err
	}
	return collectAutoEntries(rows)
}
