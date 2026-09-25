package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// AutoPromotionQueueRepo is the postgres-backed repository.AutoPromotionQueueRepo.
type AutoPromotionQueueRepo struct {
	db *pgxpool.Pool
}

// NewAutoPromotionQueueRepo returns a postgres-backed AutoPromotionQueueRepo.
func NewAutoPromotionQueueRepo(db *pgxpool.Pool) *AutoPromotionQueueRepo {
	return &AutoPromotionQueueRepo{db: db}
}

const autoQueueFields = `id, rule_id, component_id, last_published_at, due_at,
	generation, attempts, waiting_for_scan, reason`

func scanAutoEntry(row pgx.Row) (domain.AutoPromotionEntry, error) {
	var e domain.AutoPromotionEntry
	err := row.Scan(&e.ID, &e.RuleID, &e.ComponentID, &e.LastPublishedAt, &e.DueAt,
		&e.Generation, &e.Attempts, &e.WaitingForScan, &e.Reason)
	return e, err
}

// EnqueuePublish is one statement: the rules come from promotion_rules in the
// same INSERT ... SELECT, so a repository without an auto_promote rule costs a
// single index lookup and writes nothing. A publish into a component already
// queued pushes its due time out (the settle window restarts), resets the
// attempts and bumps the generation.
func (r *AutoPromotionQueueRepo) EnqueuePublish(ctx context.Context, fromRepo, componentID string, publishedAt, dueAt time.Time) (int, error) {
	tag, err := r.db.Exec(ctx,
		`INSERT INTO promotion_auto_queue (rule_id, component_id, last_published_at, due_at)
		 SELECT id, $2, $3, $4 FROM promotion_rules WHERE from_repo = $1 AND auto_promote
		 ON CONFLICT (rule_id, component_id) DO UPDATE
		 SET last_published_at = EXCLUDED.last_published_at,
		     due_at            = EXCLUDED.due_at,
		     generation        = promotion_auto_queue.generation + 1,
		     attempts          = 0,
		     waiting_for_scan  = false,
		     reason            = ''`,
		fromRepo, componentID, publishedAt, dueAt)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// Claim takes due, unclaimed (or lease-expired) rows with FOR UPDATE SKIP
// LOCKED, so two replicas claiming at the same moment get disjoint sets.
func (r *AutoPromotionQueueRepo) Claim(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]domain.AutoPromotionEntry, error) {
	rows, err := r.db.Query(ctx,
		`UPDATE promotion_auto_queue SET claimed_until = $2
		 WHERE id IN (
		     SELECT id FROM promotion_auto_queue
		     WHERE due_at <= $1 AND (claimed_until IS NULL OR claimed_until <= $1)
		     ORDER BY due_at
		     LIMIT $3
		     FOR UPDATE SKIP LOCKED)
		 RETURNING `+autoQueueFields,
		now, now.Add(lease), limit)
	if err != nil {
		return nil, err
	}
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

// Finish deletes the row it evaluated; a row a publish refreshed since the
// claim survives, unclaimed, for its next pass.
func (r *AutoPromotionQueueRepo) Finish(ctx context.Context, id string, generation int64) error {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM promotion_auto_queue WHERE id = $1 AND generation = $2`, id, generation)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	_, err = r.db.Exec(ctx, `UPDATE promotion_auto_queue SET claimed_until = NULL WHERE id = $1`, id)
	return err
}

// Retry reschedules the generation it evaluated; a refreshed row keeps the
// due time the publish gave it.
func (r *AutoPromotionQueueRepo) Retry(ctx context.Context, id string, generation int64, dueAt time.Time, waitingForScan bool, reason string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE promotion_auto_queue
		 SET claimed_until    = NULL,
		     due_at           = CASE WHEN generation = $2 THEN $3 ELSE due_at END,
		     attempts         = CASE WHEN generation = $2 THEN attempts + 1 ELSE attempts END,
		     waiting_for_scan = CASE WHEN generation = $2 THEN $4 ELSE waiting_for_scan END,
		     reason           = CASE WHEN generation = $2 THEN $5 ELSE reason END
		 WHERE id = $1`,
		id, generation, dueAt, waitingForScan, reason)
	return err
}

// WakeForScan brings forward only rows already waiting for a scan: those are
// past their settle window, so a scan finishing mid-upload cannot cut it short.
func (r *AutoPromotionQueueRepo) WakeForScan(ctx context.Context, componentID string, now time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE promotion_auto_queue SET due_at = $2
		 WHERE component_id = $1 AND waiting_for_scan AND due_at > $2`,
		componentID, now)
	return err
}

// List returns every queued row, oldest due first.
func (r *AutoPromotionQueueRepo) List(ctx context.Context) ([]domain.AutoPromotionEntry, error) {
	rows, err := r.db.Query(ctx, `SELECT `+autoQueueFields+` FROM promotion_auto_queue ORDER BY due_at`)
	if err != nil {
		return nil, err
	}
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
