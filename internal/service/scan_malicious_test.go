package service_test

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// The visibility gap this closes: the finding was always there, it was just
// counted in a bucket nobody reads.
func TestScanService_MaliciousFindingGetsItsOwnCounter(t *testing.T) {
	comp := &domain.Component{Repository: "npmhosted", Format: "npm", Name: "debug", Version: "4.4.2"}
	comps := testutil.NewComponentRepo()
	comps.Create(context.Background(), comp)

	srv := osvServer(t, []map[string]any{
		{"id": "MAL-2025-46974", "aliases": []string{}, "summary": "Malicious code in debug (npm)"},
		{"id": "GHSA-x", "aliases": []string{"CVE-2025-2"}, "summary": "RCE",
			"database_specific": map[string]any{"severity": "HIGH"}},
	})

	scanRepo := testutil.NewScanResultRepo()
	svc := service.NewScanService(comps, "").WithScanResults(scanRepo)
	svc.OSVClient.BaseURL = srv.URL

	result, err := svc.Scan(context.Background(), comp.ID, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Summary.Malicious != 1 {
		t.Errorf("expected Malicious=1, got %d", result.Summary.Malicious)
	}
	if result.Summary.Unknown != 0 {
		t.Errorf("a malicious report must leave the Unknown bucket, got Unknown=%d", result.Summary.Unknown)
	}
	if result.Summary.High != 1 {
		t.Errorf("expected High=1, got %d", result.Summary.High)
	}
	if result.Summary.Total != 2 {
		t.Errorf("expected Total=2, got %d", result.Summary.Total)
	}

	rows := scanRepo.Rows()
	if len(rows) != 1 {
		t.Fatalf("expected 1 scan_results row, got %d", len(rows))
	}
	if rows[0].Malicious != 1 {
		t.Errorf("expected the count to reach the history row, got Malicious=%d", rows[0].Malicious)
	}
}
