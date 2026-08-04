//go:build integration

package postgres

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// An index the planner declines to use is not an index. The first form of
// migration 023 was partial on "extra ? 'oci_subject'", which Postgres cannot
// discharge from the equality this query applies, so the query stayed on a
// sequential scan of the whole components table with the index in place. This
// asserts the plan, not just the index's existence.
func TestComponentRepo_ListOCIReferrers_UsesSubjectIndex(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "explain_scratch")

	// On OCI every blob upload creates a component, and they all sit under the
	// image they belong to, so the image name is NOT selective: one busy image
	// owns most of the rows. Only the subject digest is.
	if _, err := pool.Exec(ctx, `
		INSERT INTO components (repository_id, format, name, version, extra)
		SELECT $1, 'oci',
		       CASE WHEN i % 10 = 0 THEN 'charts/other' || (i % 200) ELSE 'charts/nginx' END,
		       'sha256:' || lpad(i::text, 64, '0'),
		       CASE WHEN i % 20 = 0
		            THEN jsonb_build_object('oci_subject', 'sha256:' || lpad((i/20)::text, 64, 'a'))
		            ELSE '{}'::jsonb END
		FROM generate_series(1, 60000) i`, p.RepositoryID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE components`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	var idxdef string
	if err := pool.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes WHERE indexname = 'idx_components_oci_subject'`).Scan(&idxdef); err != nil {
		t.Fatalf("migration 023 did not create the index: %v", err)
	}
	t.Log("INDEX: " + idxdef)

	// The exact query ListOCIReferrers builds for a non-empty imageName.
	q := `EXPLAIN (ANALYZE, BUFFERS)
		SELECT c.id, c.repository_id, rep.name, c.format,
		       c.group_id, c.name, c.version, c.tags,
		       c.extra, c.last_downloaded, c.download_count, c.created_at
		FROM components c
		JOIN repositories rep ON rep.id = c.repository_id
		WHERE rep.name IN ($1) AND c.extra->>'oci_subject' = $2 AND c.name = $3
		ORDER BY c.name, c.version`

	rows, err := pool.Query(ctx, q, p.RepoName,
		"sha256:"+fmt.Sprintf("%064s", "aaa"), "charts/nginx")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan: %v", err)
		}
		plan = append(plan, line)
		t.Log(line)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "idx_components_oci_subject") {
		t.Errorf("the referrers query does not use idx_components_oci_subject:\n%s", joined)
	}
	if strings.Contains(joined, "Seq Scan on components") {
		t.Errorf("the referrers query still sequentially scans components:\n%s", joined)
	}
}
