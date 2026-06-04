package handlers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// mountScan wires the real ScanService (over mocks) as router.go does.
// The OSV client is pointed at a local httptest server so the non-Docker scan path
// is exercised without a live network call. The Trivy binary is forced to a
// nonexistent path so the Docker path resolves to ErrTrivyNotInstalled (503).
func mountScan(t *testing.T) (*gin.Engine, *testutil.ComponentRepo, *testutil.ScanResultRepo, *httptest.Server) {
	t.Helper()
	comps := testutil.NewComponentRepo()
	scanRepo := testutil.NewScanResultRepo()

	// Local OSV stub: returns one HIGH vuln for any query.
	osvSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"vulns":[{"id":"GHSA-xxxx","summary":"test vuln","database_specific":{"severity":"HIGH"}}]}`))
	}))
	t.Cleanup(osvSrv.Close)

	svc := service.NewScanService(comps, "http://localhost").WithScanResults(scanRepo)
	svc.OSVClient = &service.OSVClient{BaseURL: osvSrv.URL, HTTPClient: osvSrv.Client()}
	svc.TrivyBin = "/nonexistent/trivy-binary-xyz" // force ErrTrivyNotInstalled on Docker path
	svc.TrivyTimeout = 5 * time.Second

	h := handlers.NewScanHandler(svc)
	r := gin.New()
	r.POST("/api/v1/components/:id/scan", h.Scan)
	r.GET("/api/v1/components/:id/scan", h.GetScanResult)
	r.GET("/api/v1/security/summary", h.Summary)
	r.GET("/api/v1/security/vulnerabilities", h.Vulnerabilities)
	r.POST("/api/v1/security/scan/bulk", h.BulkScanHandler)
	return r, comps, scanRepo, osvSrv
}

func seedComponent(t *testing.T, comps *testutil.ComponentRepo, format, name, version string) *domain.Component {
	t.Helper()
	c := &domain.Component{Format: format, Name: name, Version: version, Repository: "repo1"}
	require.NoError(t, comps.Create(testContext(), c))
	return c
}

// ── Scan (single) ──────────────────────────────────────────────────────────────

func TestScanHandler_Scan_OSV_Success(t *testing.T) {
	r, comps, scanRepo, _ := mountScan(t)
	c := seedComponent(t, comps, "maven", "log4j-core", "2.14.0")

	rec := do(t, r, http.MethodPost, "/api/v1/components/"+c.ID+"/scan", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var res domain.ScanResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, domain.ScanStatusOK, res.Status)
	assert.Equal(t, 1, res.Summary.High)
	assert.Equal(t, 1, res.Summary.Total)
	require.Len(t, res.Findings, 1)
	assert.Equal(t, "GHSA-xxxx", res.Findings[0].ID)
	// A scan row should have been persisted.
	assert.NotEmpty(t, scanRepo.Rows())
}

func TestScanHandler_Scan_ComponentNotFound_400(t *testing.T) {
	// Scan returns "component not found" error → handler maps non-Trivy errors to 400.
	r, _, _, _ := mountScan(t)
	rec := do(t, r, http.MethodPost, "/api/v1/components/ghost/scan", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestScanHandler_Scan_UnsupportedFormat_400(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	c := seedComponent(t, comps, "raw", "file", "1")
	rec := do(t, r, http.MethodPost, "/api/v1/components/"+c.ID+"/scan", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestScanHandler_Scan_Docker_TrivyMissing_503(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	c := seedComponent(t, comps, "docker", "myimage", "latest")
	rec := do(t, r, http.MethodPost, "/api/v1/components/"+c.ID+"/scan",
		map[string]any{"imageRef": "localhost/myimage:latest"})
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestScanHandler_Scan_RepoError_400(t *testing.T) {
	// Components.Get errors → propagated through Scan → handler maps to 400 (non-Trivy).
	r, comps, _, _ := mountScan(t)
	comps.Err = errors.New("db down")
	rec := do(t, r, http.MethodPost, "/api/v1/components/any/scan", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── GetScanResult ──────────────────────────────────────────────────────────────

func TestScanHandler_GetScanResult_NoCache_204(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	c := seedComponent(t, comps, "docker", "img", "v1")
	rec := do(t, r, http.MethodGet, "/api/v1/components/"+c.ID+"/scan", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestScanHandler_GetScanResult_Cached_200(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	c := seedComponent(t, comps, "maven", "pkg", "1.0")
	// Run an OSV scan first so a result is cached in component.Extra.
	rec := do(t, r, http.MethodPost, "/api/v1/components/"+c.ID+"/scan", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = do(t, r, http.MethodGet, "/api/v1/components/"+c.ID+"/scan", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var res domain.ScanResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, domain.ScanStatusOK, res.Status)
}

func TestScanHandler_GetScanResult_RepoError_500(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	comps.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/api/v1/components/any/scan", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Summary ────────────────────────────────────────────────────────────────────

func TestScanHandler_Summary_OK(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	c := seedComponent(t, comps, "maven", "pkg", "1.0")
	// Produce a persisted scan row via OSV scan (1 HIGH).
	require.Equal(t, http.StatusOK,
		do(t, r, http.MethodPost, "/api/v1/components/"+c.ID+"/scan", nil).Code)

	rec := do(t, r, http.MethodGet, "/api/v1/security/summary", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var s domain.SecuritySummary
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &s))
	assert.Equal(t, 1, s.High)
	assert.Equal(t, 1, s.ScannedTotal)
}

func TestScanHandler_Summary_RepoError_500(t *testing.T) {
	r, _, scanRepo, _ := mountScan(t)
	scanRepo.Err = errors.New("agg down")
	rec := do(t, r, http.MethodGet, "/api/v1/security/summary", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Vulnerabilities ──────────────────────────────────────────────────────────────

func TestScanHandler_Vulnerabilities_Empty(t *testing.T) {
	r, _, _, _ := mountScan(t)
	rec := do(t, r, http.MethodGet, "/api/v1/security/vulnerabilities", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Items []*domain.VulnRow `json:"items"`
		Total int               `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.NotNil(t, body.Items)
	assert.Empty(t, body.Items)
	assert.Equal(t, 0, body.Total)
}

func TestScanHandler_Vulnerabilities_WithRows_AndFilters(t *testing.T) {
	r, _, scanRepo, _ := mountScan(t)
	scanRepo.VulnRows = []*domain.VulnRow{
		{RepoName: "repo1", Format: "maven", Name: "pkg", Version: "1.0", High: 2},
	}
	// Exercise the limit/offset/filter query-param parsing branches.
	rec := do(t, r, http.MethodGet,
		"/api/v1/security/vulnerabilities?repo=repo1&severity=HIGH&format=maven&limit=10&offset=5", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Items []*domain.VulnRow `json:"items"`
		Total int               `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)
	assert.Equal(t, 1, body.Total)
	assert.Equal(t, "pkg", body.Items[0].Name)
}

func TestScanHandler_Vulnerabilities_BadLimitOffset_UsesDefaults(t *testing.T) {
	// Non-numeric limit/offset are ignored (defaults kept) — still 200.
	r, _, _, _ := mountScan(t)
	rec := do(t, r, http.MethodGet, "/api/v1/security/vulnerabilities?limit=abc&offset=-3", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestScanHandler_Vulnerabilities_RepoError_500(t *testing.T) {
	r, _, scanRepo, _ := mountScan(t)
	scanRepo.Err = errors.New("list down")
	rec := do(t, r, http.MethodGet, "/api/v1/security/vulnerabilities", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── BulkScanHandler ──────────────────────────────────────────────────────────────

func TestScanHandler_BulkScan_OK(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	seedComponent(t, comps, "maven", "a", "1.0")
	seedComponent(t, comps, "npm", "b", "2.0")
	// sha256 alias should be skipped by BulkScan.
	seedComponent(t, comps, "docker", "img", "sha256:deadbeef")

	rec := do(t, r, http.MethodPost, "/api/v1/security/scan/bulk", map[string]any{"repo": "repo1"})
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Scanned int `json:"scanned"`
		Failed  int `json:"failed"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	// Two OSV-scannable components succeed; the sha256 docker alias is skipped.
	assert.Equal(t, 2, body.Scanned)
	assert.Equal(t, 0, body.Failed)
}

func TestScanHandler_BulkScan_RepoError_500(t *testing.T) {
	// Components.Search errors → BulkScan returns err → 500.
	r, comps, _, _ := mountScan(t)
	comps.Err = errors.New("search down")
	rec := do(t, r, http.MethodPost, "/api/v1/security/scan/bulk", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
