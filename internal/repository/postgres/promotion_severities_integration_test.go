//go:build integration

package postgres

import (
	"context"
	"slices"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// scan_fail_severities (migration 037) round-trips through create and update;
// an empty list is stored as NULL and reads back empty (the default), and the
// column's CHECK refuses a value no gate understands (#543).
func TestPromotionRepo_ScanFailSeverities_RoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "sev_rt")
	rule := makePromotionRule("rule_sev_rt", p)
	rule.ScanFailSeverities = []string{"critical", "medium"}
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	got, err := repo.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if !slices.Equal(got.ScanFailSeverities, []string{"critical", "medium"}) {
		t.Errorf("after create: got %v, want [critical medium]", got.ScanFailSeverities)
	}

	rule.ScanFailSeverities = []string{}
	if err := repo.UpdateRule(ctx, rule); err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	var isNull bool
	if err := pool.QueryRow(ctx,
		`SELECT scan_fail_severities IS NULL FROM promotion_rules WHERE id = $1`, rule.ID,
	).Scan(&isNull); err != nil {
		t.Fatalf("select: %v", err)
	}
	if !isNull {
		t.Error("an empty list must be stored as NULL (the default)")
	}
	got, err = repo.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if len(got.ScanFailSeverities) != 0 {
		t.Errorf("after clearing: got %v, want empty", got.ScanFailSeverities)
	}

	rule.ScanFailSeverities = []string{"high", "moderate"}
	if err := repo.UpdateRule(ctx, rule); err == nil {
		t.Error("the CHECK constraint must refuse an unknown severity")
	}
}
