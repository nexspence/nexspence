//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// The pull gate (#641) reads the scanned manifest's sha256 back out of
// scan_results.raw; a row written without one reads back without one.
func TestScanResultRepo_ManifestSHA256_RoundTrips(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	compID, _, _ := makeScanComponent(t, ctx, "sha_roundtrip")
	now := time.Now()
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: compID, Scanner: "trivy", Status: domain.ScanStatusOK, ScannedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Insert legacy: %v", err)
	}
	got, err := repo.GetLatestByComponent(ctx, compID)
	if err != nil {
		t.Fatalf("GetLatestByComponent: %v", err)
	}
	if sha := got.ScannedManifestSHA256(); sha != "" {
		t.Errorf("legacy row sha = %q, want empty", sha)
	}

	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: compID, Scanner: "trivy", Status: domain.ScanStatusOK, ScannedAt: now,
		Raw: map[string]any{domain.ScanRawManifestSHA256Key: "abc123"},
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err = repo.GetLatestByComponent(ctx, compID)
	if err != nil {
		t.Fatalf("GetLatestByComponent: %v", err)
	}
	if sha := got.ScannedManifestSHA256(); sha != "abc123" {
		t.Errorf("sha = %q, want abc123 (raw %v)", sha, got.Raw)
	}
}
