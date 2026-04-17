package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexspence-oss/nexspence/internal/domain"
)

type AuditRepo struct{ pool *pgxpool.Pool }

func NewAuditRepo(pool *pgxpool.Pool) *AuditRepo {
	return &AuditRepo{pool: pool}
}

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

func (r *AuditRepo) List(ctx context.Context, domainFilter, actionFilter string, limit, offset int) ([]domain.AuditEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, event_time,
		       user_id::text, username,
		       COALESCE(remote_ip::text,''), COALESCE(user_agent,''),
		       domain, action,
		       COALESCE(entity_type,''), COALESCE(entity_id,''), COALESCE(entity_name,''),
		       context, result
		FROM audit_events
		WHERE ($1 = '' OR domain = $1)
		  AND ($2 = '' OR action = $2)
		ORDER BY event_time DESC
		LIMIT $3 OFFSET $4`,
		domainFilter, actionFilter, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.AuditEvent
	for rows.Next() {
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
			return nil, err
		}
		e.UserID = userID
		_ = json.Unmarshal(ctxJSON, &e.Context)
		out = append(out, e)
	}
	return out, rows.Err()
}

// stringOrNil unwraps a *string pointer to a string, or "" if nil.
func stringOrNil(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
