package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/service"
)

// osvServer replies to every query with the given raw `vulns` array.
func osvServer(t *testing.T, vulns []map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"vulns": vulns})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// OSV.dev indexes OpenSSF's malicious-packages dataset as `MAL-…` reports. They
// carry no `database_specific.severity` — that field is CVSS-specific and
// malware reports never set it — so a compromised package used to land in the
// same "Unknown" bucket as a package the scanner could not classify.
func TestOSVClient_MaliciousReportIsClassifiedMalicious(t *testing.T) {
	t.Parallel()
	srv := osvServer(t, []map[string]any{
		{
			"id":      "MAL-2025-46974",
			"aliases": []string{},
			"summary": "Malicious code in debug (npm)",
		},
	})

	client := service.NewOSVClient()
	client.BaseURL = srv.URL

	vulns, err := client.Query(context.Background(), "debug", "4.4.2", "npm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vulns) != 1 {
		t.Fatalf("expected 1 vuln, got %d", len(vulns))
	}
	if vulns[0].Severity != "MALICIOUS" {
		t.Errorf("expected MALICIOUS, got %q", vulns[0].Severity)
	}
	if vulns[0].ID != "MAL-2025-46974" {
		t.Errorf("expected the MAL- id to be kept, got %q", vulns[0].ID)
	}
}

// The CVE-alias preference exists to show a familiar identifier. Applying it to
// a malware report would bury the one signal that matters most, so a MAL- id
// wins over any alias.
func TestOSVClient_MaliciousIDWinsOverCVEAlias(t *testing.T) {
	t.Parallel()
	srv := osvServer(t, []map[string]any{
		{
			"id":      "GHSA-aaaa-bbbb-cccc",
			"aliases": []string{"CVE-2025-11111", "MAL-2025-46974"},
			"summary": "Malicious code in chalk (npm)",
		},
	})

	client := service.NewOSVClient()
	client.BaseURL = srv.URL

	vulns, err := client.Query(context.Background(), "chalk", "5.6.1", "npm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vulns) != 1 {
		t.Fatalf("expected 1 vuln, got %d", len(vulns))
	}
	if vulns[0].Severity != "MALICIOUS" {
		t.Errorf("expected MALICIOUS, got %q", vulns[0].Severity)
	}
	if vulns[0].ID != "MAL-2025-46974" {
		t.Errorf("expected the MAL- alias as the displayed id, got %q", vulns[0].ID)
	}
}

// Malware detection outranks a coincidental CVSS score: a MAL- report that does
// carry a severity is still reported as malicious.
func TestOSVClient_MaliciousOverridesReportedSeverity(t *testing.T) {
	t.Parallel()
	srv := osvServer(t, []map[string]any{
		{
			"id":                "MAL-2025-12345",
			"aliases":           []string{},
			"summary":           "Malicious package",
			"database_specific": map[string]any{"severity": "LOW"},
		},
	})

	client := service.NewOSVClient()
	client.BaseURL = srv.URL

	vulns, err := client.Query(context.Background(), "evil", "1.0.0", "npm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vulns[0].Severity != "MALICIOUS" {
		t.Errorf("expected MALICIOUS to override LOW, got %q", vulns[0].Severity)
	}
}

// A plain CVE keeps behaving exactly as before.
func TestOSVClient_NonMaliciousUnaffected(t *testing.T) {
	t.Parallel()
	srv := osvServer(t, []map[string]any{
		{
			"id":                "GHSA-aaaa-bbbb-cccc",
			"aliases":           []string{"CVE-2023-1234"},
			"summary":           "Prototype pollution",
			"database_specific": map[string]any{"severity": "HIGH"},
		},
	})

	client := service.NewOSVClient()
	client.BaseURL = srv.URL

	vulns, err := client.Query(context.Background(), "lodash", "4.17.20", "npm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vulns[0].Severity != "HIGH" || vulns[0].ID != "CVE-2023-1234" {
		t.Errorf("expected the CVE-alias behavior to be unchanged, got %#v", vulns[0])
	}
}
