package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

type AuditRepo struct{ pool *pgxpool.Pool }

func NewAuditRepo(pool *pgxpool.Pool) *AuditRepo {
	return &AuditRepo{pool: pool}
}

// streamRowCap is the hard safety limit on a single Stream call.
const streamRowCap = 100_000

// ErrStreamCapExceeded is returned by Stream when the underlying query
// would have produced more than streamRowCap rows.
var ErrStreamCapExceeded = errors.New("audit stream exceeds row cap; narrow the time range")

func (r *AuditRepo) Write(ctx context.Context, e *domain.AuditEvent) error {
	ctxJSON, _ := json.Marshal(e.Context)
	_, err := r.pool.Exec(ctx, `
		INSERT INTO audit_events
		    (user_id, username, remote_ip, user_agent, domain, action,
		     entity_type, entity_id, entity_name, context, result)
		VALUES
		    ($1, $2, $3::inet, $4, $5, $6,
		     $7, $8, $9, $10, $11)`,
		nullStr(stringOrNil(e.UserID)),
		e.Username,
		nullStr(e.RemoteIP),
		nullStr(e.UserAgent),
		e.Domain,
		e.Action,
		nullStr(e.EntityType),
		nullStr(e.EntityID),
		nullStr(e.EntityName),
		ctxJSON,
		e.Result,
	)
	return err
}

// buildWhere returns the WHERE clause and ordered args for both List and Stream.
// $1..$N are reserved for the WHERE; LIMIT/OFFSET (if added) use $N+1, $N+2.
func buildWhere(q repository.AuditQuery) (string, []any) {
	parts := []string{}
	args := []any{}
	add := func(cond string, val any) {
		args = append(args, val)
		parts = append(parts, fmt.Sprintf(cond, len(args)))
	}
	if q.Domain != "" {
		add("domain = $%d", q.Domain)
	}
	if q.Action != "" {
		add("action = $%d", q.Action)
	}
	if q.Username != "" {
		add("username = $%d", q.Username)
	}
	if q.From != nil {
		add("event_time >= $%d", *q.From)
	}
	if q.To != nil {
		add("event_time < $%d", *q.To)
	}
	if len(parts) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(parts, " AND "), args
}

func (r *AuditRepo) List(ctx context.Context, q repository.AuditQuery) ([]domain.AuditEvent, int, error) {
	if q.Limit <= 0 {
		q.Limit = 100
	}
	if q.Limit > 1000 {
		q.Limit = 1000
	}
	where, args := buildWhere(q)

	var total int
	if err := r.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM audit_events "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, q.Limit, q.Offset)
	sql := fmt.Sprintf(`
		SELECT id, event_time,
		       user_id::text, username,
		       COALESCE(remote_ip::text,''), COALESCE(user_agent,''),
		       domain, action,
		       COALESCE(entity_type,''), COALESCE(entity_id,''), COALESCE(entity_name,''),
		       context, result
		FROM audit_events
		%s
		ORDER BY event_time DESC
		LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args))

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []domain.AuditEvent
	for rows.Next() {
		e, err := scanAuditRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

func (r *AuditRepo) Stream(ctx context.Context, q repository.AuditQuery, fn func(domain.AuditEvent) error) error {
	where, args := buildWhere(q)
	sql := `
		SELECT id, event_time,
		       user_id::text, username,
		       COALESCE(remote_ip::text,''), COALESCE(user_agent,''),
		       domain, action,
		       COALESCE(entity_type,''), COALESCE(entity_id,''), COALESCE(entity_name,''),
		       context, result
		FROM audit_events ` + where + `
		ORDER BY event_time DESC`
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	n := 0
	for rows.Next() {
		n++
		if n > streamRowCap {
			return ErrStreamCapExceeded
		}
		e, err := scanAuditRow(rows)
		if err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}

// scanAuditRow scans one row from the audit_events SELECT used in List/Stream.
func scanAuditRow(rows interface {
	Scan(...any) error
}) (domain.AuditEvent, error) {
	var e domain.AuditEvent
	var userID *string
	var ctxJSON []byte
	if err := rows.Scan(
		&e.ID, &e.EventTime,
		&userID, &e.Username,
		&e.RemoteIP, &e.UserAgent,
		&e.Domain, &e.Action,
		&e.EntityType, &e.EntityID, &e.EntityName,
		&ctxJSON, &e.Result,
	); err != nil {
		return e, err
	}
	e.UserID = userID
	_ = json.Unmarshal(ctxJSON, &e.Context)
	return e, nil
}

// stringOrNil unwraps a *string pointer to a string, or "" if nil.
func stringOrNil(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
