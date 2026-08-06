//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

func TestScanResultRepo_Malicious_RoundTrips(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	compID, _, _ := makeScanComponent(t, ctx, "mal_roundtrip")

	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: compID, Scanner: "osv", Status: domain.ScanStatusOK,
		Malicious: 2, Critical: 1, Total: 3, ScannedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.GetLatestByComponent(ctx, compID)
	if err != nil {
		t.Fatalf("GetLatestByComponent: %v", err)
	}
	if got.Malicious != 2 {
		t.Errorf("Malicious: got %d, want 2", got.Malicious)
	}
}

func TestScanResultRepo_Aggregate_SumsMalicious(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	comp1, _, _ := makeScanComponent(t, ctx, "mal_agg1")
	comp2, _, _ := makeScanComponent(t, ctx, "mal_agg2")
	now := time.Now().Truncate(time.Second)

	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: comp1, Scanner: "osv", Status: domain.ScanStatusOK,
		Malicious: 1, Total: 1, ScannedAt: now,
	}); err != nil {
		t.Fatalf("Insert comp1: %v", err)
	}
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: comp2, Scanner: "osv", Status: domain.ScanStatusOK,
		Malicious: 2, Critical: 3, Total: 5, ScannedAt: now,
	}); err != nil {
		t.Fatalf("Insert comp2: %v", err)
	}

	sum, err := repo.Aggregate(ctx)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if sum.Malicious != 3 {
		t.Errorf("Malicious: got %d, want 3", sum.Malicious)
	}
	if sum.Critical != 3 {
		t.Errorf("Critical: got %d, want 3", sum.Critical)
	}
}

func TestScanResultRepo_List_ReturnsMaliciousCount(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	compID, _, _ := makeScanComponent(t, ctx, "mal_list")
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: compID, Scanner: "osv", Status: domain.ScanStatusOK,
		Malicious: 1, Total: 1, ScannedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rows, total, err := repo.List(ctx, domain.VulnFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("rows: got %d (total %d), want 1", len(rows), total)
	}
	if rows[0].Malicious != 1 {
		t.Errorf("Malicious: got %d, want 1", rows[0].Malicious)
	}
}

// A compromised package has no CVE and therefore no CVSS level, but it is not
// less urgent than one. Every minimum-severity tier has to match it — filtering
// to CRITICAL and losing the malware is the blind spot this feature exists to
// close.
func TestScanResultRepo_List_MaliciousMatchesEverySeverityFilter(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	compID, _, _ := makeScanComponent(t, ctx, "mal_filter")
	// Malicious only: no CVE counters at all.
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: compID, Scanner: "osv", Status: domain.ScanStatusOK,
		Malicious: 1, Total: 1, ScannedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	for _, sev := range []string{"MALICIOUS", "CRITICAL", "HIGH", "MEDIUM", "LOW"} {
		rows, total, err := repo.List(ctx, domain.VulnFilter{Severity: sev})
		if err != nil {
			t.Fatalf("List(%s): %v", sev, err)
		}
		if total != 1 || len(rows) != 1 {
			t.Errorf("List(%s): got %d rows (total %d), want 1", sev, len(rows), total)
		}
	}
}

// The MALICIOUS tier is a filter of its own: asking for it must not sweep in
// rows that only have CVEs.
func TestScanResultRepo_List_MaliciousFilterExcludesCVEOnlyRows(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	cveOnly, _, _ := makeScanComponent(t, ctx, "mal_excl_cve")
	malicious, _, _ := makeScanComponent(t, ctx, "mal_excl_mal")
	now := time.Now()

	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: cveOnly, Scanner: "trivy", Status: domain.ScanStatusOK,
		Critical: 5, Total: 5, ScannedAt: now,
	}); err != nil {
		t.Fatalf("Insert cve-only: %v", err)
	}
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: malicious, Scanner: "osv", Status: domain.ScanStatusOK,
		Malicious: 1, Total: 1, ScannedAt: now,
	}); err != nil {
		t.Fatalf("Insert malicious: %v", err)
	}

	rows, total, err := repo.List(ctx, domain.VulnFilter{Severity: "MALICIOUS"})
	if err != nil {
		t.Fatalf("List(MALICIOUS): %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("got %d rows (total %d), want only the malicious one", len(rows), total)
	}
	if rows[0].ComponentID != malicious {
		t.Errorf("got component %q, want %q", rows[0].ComponentID, malicious)
	}
}

// Malicious leads the ordering: a compromised package must not be pushed below
// a component that merely has more CVEs.
func TestScanResultRepo_List_MaliciousSortsFirst(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	manyCVEs, _, _ := makeScanComponent(t, ctx, "mal_sort_cve")
	malicious, _, _ := makeScanComponent(t, ctx, "mal_sort_mal")
	now := time.Now()

	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: manyCVEs, Scanner: "trivy", Status: domain.ScanStatusOK,
		Critical: 99, High: 99, Total: 198, ScannedAt: now,
	}); err != nil {
		t.Fatalf("Insert cve-heavy: %v", err)
	}
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: malicious, Scanner: "osv", Status: domain.ScanStatusOK,
		Malicious: 1, Total: 1, ScannedAt: now,
	}); err != nil {
		t.Fatalf("Insert malicious: %v", err)
	}

	rows, _, err := repo.List(ctx, domain.VulnFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows: got %d, want 2", len(rows))
	}
	if rows[0].ComponentID != malicious {
		t.Errorf("first row: got %q, want the malicious component %q", rows[0].ComponentID, malicious)
	}
}
