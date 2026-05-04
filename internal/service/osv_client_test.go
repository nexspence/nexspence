package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/service"
)

func TestOSVClient_Query_ReturnsCVEs(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/v1/query") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"vulns": []map[string]any{
				{
					"id":      "GHSA-abcd-1234-5678",
					"aliases": []string{"CVE-2023-1234"},
					"summary": "Remote code execution",
					"database_specific": map[string]any{"severity": "CRITICAL"},
				},
				{
					"id":      "GHSA-ffff-9999-0000",
					"aliases": []string{},
					"summary": "DoS vulnerability",
					"database_specific": map[string]any{"severity": "HIGH"},
				},
			},
		})
	}))
	defer srv.Close()

	client := service.NewOSVClient()
	client.BaseURL = srv.URL

	vulns, err := client.Query(context.Background(), "log4j-core", "2.14.1", "Maven")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vulns) != 2 {
		t.Fatalf("expected 2 vulns, got %d", len(vulns))
	}
	// First vuln: prefer CVE alias
	if vulns[0].ID != "CVE-2023-1234" {
		t.Errorf("expected CVE alias as ID, got %q", vulns[0].ID)
	}
	if vulns[0].Severity != "CRITICAL" {
		t.Errorf("expected CRITICAL, got %q", vulns[0].Severity)
	}
	// Second vuln: no CVE alias, use OSV ID
	if vulns[1].ID != "GHSA-ffff-9999-0000" {
		t.Errorf("expected OSV ID, got %q", vulns[1].ID)
	}
	if vulns[1].Severity != "HIGH" {
		t.Errorf("expected HIGH, got %q", vulns[1].Severity)
	}
}

func TestOSVClient_Query_Empty(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"vulns": []any{}})
	}))
	defer srv.Close()

	client := service.NewOSVClient()
	client.BaseURL = srv.URL

	vulns, err := client.Query(context.Background(), "safe-pkg", "1.0.0", "npm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vulns) != 0 {
		t.Fatalf("expected 0 vulns, got %d", len(vulns))
	}
}
