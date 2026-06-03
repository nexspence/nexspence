package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func buildScanRouter(svc *service.ScanService) *gin.Engine {
	r := gin.New()
	h := handlers.NewScanHandler(svc)
	r.POST("/api/v1/components/:id/scan", h.Scan)
	r.GET("/api/v1/components/:id/scan", h.GetScanResult)
	r.GET("/api/v1/security/summary", h.Summary)
	r.GET("/api/v1/security/vulnerabilities", h.Vulnerabilities)
	r.POST("/api/v1/security/scan/bulk", h.BulkScanHandler)
	return r
}

func newDockerComponent(id string) *domain.Component {
	return &domain.Component{ID: id, Format: "docker", Name: "nginx", Version: "latest", Repository: "docker-hosted"}
}

// fakeTrivyBin creates a shell script in a temp dir that outputs the given JSON and exits 0.
func fakeTrivyBin(t *testing.T, stdout string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "trivy")
	script := "#!/bin/sh\nprintf '%s' '" + strings.ReplaceAll(stdout, "'", "'\\''") + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatalf("fakeTrivyBin: %v", err)
	}
	return bin
}

const minimalTrivyJSON = `{"SchemaVersion":2,"Results":[{"Target":"nginx:latest","Vulnerabilities":[` +
	`{"VulnerabilityID":"CVE-2022-1234","PkgName":"busybox","InstalledVersion":"1.34.0","FixedVersion":"1.34.1","Severity":"HIGH","Title":"rce in sh"}` +
	`]}]}`

// TestScanHandler_TrivyNotInstalled verifies 503 + error body when trivy binary is missing.
func TestScanHandler_TrivyNotInstalled(t *testing.T) {
	comps := testutil.NewComponentRepo()
	comp := newDockerComponent("")
	comps.Create(context.Background(), comp) // Create assigns comp.ID

	svc := service.NewScanService(comps, "")
	svc.TrivyBin = "/no/such/trivy-binary"

	r := buildScanRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/components/"+comp.ID+"/scan", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.Contains(body["error"], "trivy") {
		t.Fatalf("want trivy error message, got %q", body["error"])
	}
}

// TestScanHandler_ComponentNotFound verifies 400 for a missing component.
func TestScanHandler_ComponentNotFound(t *testing.T) {
	svc := service.NewScanService(testutil.NewComponentRepo(), "")
	svc.TrivyBin = "/no/such/binary"

	r := buildScanRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/components/no-such-id/scan", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", w.Code, w.Body.String())
	}
}

// TestScanHandler_NonDockerComponent verifies 400 for a non-docker component.
func TestScanHandler_NonDockerComponent(t *testing.T) {
	comp := &domain.Component{ID: "maven-1", Format: "maven2", Name: "spring", Version: "5.3.0", Repository: "maven-releases"}
	comps := testutil.NewComponentRepo()
	comps.Create(context.Background(), comp)

	svc := service.NewScanService(comps, "")
	svc.TrivyBin = "/no/such/binary"

	r := buildScanRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/components/maven-1/scan", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", w.Code, w.Body.String())
	}
}

// TestScanHandler_SuccessfulScan verifies 200 + ScanResult body for a successful scan.
func TestScanHandler_SuccessfulScan(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("skip fake-trivy shell script test in CI (no /bin/sh guarantee)")
	}
	comps := testutil.NewComponentRepo()
	comp := newDockerComponent("")
	comps.Create(context.Background(), comp)

	svc := service.NewScanService(comps, "")
	svc.TrivyBin = fakeTrivyBin(t, minimalTrivyJSON)

	r := buildScanRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/components/"+comp.ID+"/scan",
		strings.NewReader(`{"imageRef":"nginx:latest"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}
	var result domain.ScanResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Status != domain.ScanStatusOK {
		t.Fatalf("want status=ok got %q", result.Status)
	}
	if result.Summary.High != 1 || result.Summary.Total != 1 {
		t.Fatalf("want High=1 Total=1 got %+v", result.Summary)
	}
	if len(result.Findings) != 1 || result.Findings[0].ID != "CVE-2022-1234" {
		t.Fatalf("unexpected findings: %+v", result.Findings)
	}
}

// TestScanHandler_GetResult_NoScan verifies 204 when no scan has been run yet.
func TestScanHandler_GetResult_NoScan(t *testing.T) {
	comps := testutil.NewComponentRepo()
	comp := newDockerComponent("")
	comps.Create(context.Background(), comp)

	svc := service.NewScanService(comps, "")
	r := buildScanRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/components/"+comp.ID+"/scan", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204 got %d body=%s", w.Code, w.Body.String())
	}
}

// TestScanHandler_Summary verifies GET /api/v1/security/summary returns 200 with aggregated counts.
func TestScanHandler_Summary(t *testing.T) {
	t.Parallel()
	comps := testutil.NewComponentRepo()
	scanResults := testutil.NewScanResultRepo()

	// Seed one row so Aggregate returns something non-zero.
	_ = scanResults.Insert(context.Background(), &domain.ScanResultRow{
		ComponentID: "comp-1", Scanner: "osv", Status: domain.ScanStatusOK,
		Critical: 2, High: 1, Total: 3,
	})

	svc := service.NewScanService(comps, "").WithScanResults(scanResults)
	r := buildScanRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/security/summary", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}
	var s domain.SecuritySummary
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if s.ScannedTotal != 1 {
		t.Errorf("want scanned_total=1 got %d", s.ScannedTotal)
	}
	if s.Critical != 2 || s.High != 1 {
		t.Errorf("unexpected severity counts: %+v", s)
	}
}

// TestScanHandler_Vulnerabilities verifies GET /api/v1/security/vulnerabilities returns 200 with items+total.
func TestScanHandler_Vulnerabilities(t *testing.T) {
	t.Parallel()
	comps := testutil.NewComponentRepo()
	scanResults := testutil.NewScanResultRepo()

	_ = scanResults.Insert(context.Background(), &domain.ScanResultRow{
		ComponentID: "comp-2", Scanner: "trivy", Status: domain.ScanStatusOK,
		High: 3, Total: 3,
	})

	svc := service.NewScanService(comps, "").WithScanResults(scanResults)
	r := buildScanRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/security/vulnerabilities?limit=10&offset=0", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Items []*domain.VulnRow `json:"items"`
		Total int               `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// items is always a non-nil JSON array (never null)
	if body.Items == nil {
		t.Error("want items array, got null")
	}
}

// TestScanHandler_BulkScan verifies POST /api/v1/security/scan/bulk returns 200 with scanned/failed counts.
func TestScanHandler_BulkScan(t *testing.T) {
	t.Parallel()
	comps := testutil.NewComponentRepo()
	// One maven component — scanOSV will be called but OSVClient is real; it will fail on network.
	// We just want to confirm the handler parses the response shape correctly even with all failed.
	comp := &domain.Component{Format: "maven", Name: "junit", Version: "4.13.2", Repository: "maven-releases"}
	comps.Create(context.Background(), comp)

	svc := service.NewScanService(comps, "")
	// Point OSVClient at a non-existent host so the scan fails fast.
	svc.OSVClient.BaseURL = "http://127.0.0.1:0"
	r := buildScanRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/security/scan/bulk",
		strings.NewReader(`{"repo":"maven-releases"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Scanned int `json:"scanned"`
		Failed  int `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Network call fails → scanned=0, failed=1
	if body.Scanned+body.Failed != 1 {
		t.Errorf("want scanned+failed=1 got scanned=%d failed=%d", body.Scanned, body.Failed)
	}
}
