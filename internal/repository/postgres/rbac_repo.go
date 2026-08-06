package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/repository"
)

type rbacRepo struct{ db *pgxpool.Pool }

// NewRBACRepo returns a postgres-backed RBACRepo.
func NewRBACRepo(db *pgxpool.Pool) repository.RBACRepo {
	return &rbacRepo{db: db}
}

func (r *rbacRepo) GetUserPrivilegesWithSelectors(ctx context.Context, userID string) ([]repository.PrivilegeWithSelector, error) {
	rows, err := r.db.Query(ctx, `
		SELECT COALESCE(p.attrs->'actions', '[]'::jsonb)::text, cs.expression
		FROM user_roles ur
		JOIN role_privileges rp ON rp.role_id = ur.role_id
		JOIN privileges p ON p.id = rp.privilege_id
		JOIN content_selectors cs ON cs.id = p.content_selector_id
		WHERE ur.user_id = $1 AND p.content_selector_id IS NOT NULL
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []repository.PrivilegeWithSelector
	for rows.Next() {
		var ps repository.PrivilegeWithSelector
		var actionsJSON string
		if err := rows.Scan(&actionsJSON, &ps.Expression); err != nil {
			return nil, err
		}
		// A privilege whose actions fail to decode grants nothing — the one case
		// here where silence is a security problem, so it is logged against the
		// expression that identifies the row (the query selects no id).
		unmarshalJSONB([]byte(actionsJSON), &ps.Actions, "privileges", ps.Expression, "actions")
		result = append(result, ps)
	}
	return result, rows.Err()
}
