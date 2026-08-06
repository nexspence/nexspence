package handlers_test

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// The export is the report someone hands to somebody else, so what matters is
// that it arrives as a file, carries every filtered row rather than one page,
// and does not omit the most severe class of finding.

func exportVulnRows() []*domain.VulnRow {
	scanned := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	return []*domain.VulnRow{
		{
			RepoName: "npm-hosted", Format: "npm", ComponentID: "c1", Name: "debug", Version: "4.4.2",
			Malicious: 1, ScannedAt: scanned,
		},
		{
			RepoName: "maven-hosted", Format: "maven2", ComponentID: "c2", Name: "log4j-core", Version: "2.14.1",
			Critical: 2, High: 1, ScannedAt: scanned,
		},
	}
}

func TestScanHandler_Vulnerabilities_ExportCSV(t *testing.T) {
	r, _, scanRepo, _ := mountScan(t)
	scanRepo.VulnRows = exportVulnRows()

	rec := do(t, r, http.MethodGet, "/api/v1/security/vulnerabilities?export=csv", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Contains(t, rec.Header().Get("Content-Type"), "text/csv")
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	assert.Contains(t, rec.Header().Get("Content-Disposition"), ".csv")

	records, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 3, "header + one row per finding")

	header := records[0]
	assert.Equal(t, "repository", header[0])
	assert.Contains(t, header, "malicious", "an export that omits malware is worse than none")

	assert.Equal(t, "npm-hosted", records[1][0])
	assert.Equal(t, "debug", records[1][2])
	malCol := indexOf(header, "malicious")
	require.GreaterOrEqual(t, malCol, 0)
	assert.Equal(t, "1", records[1][malCol])
}

// indexOf returns the position of col in header, or -1.
func indexOf(header []string, col string) int {
	for i, h := range header {
		if h == col {
			return i
		}
	}
	return -1
}

func TestScanHandler_Vulnerabilities_ExportJSON(t *testing.T) {
	r, _, scanRepo, _ := mountScan(t)
	scanRepo.VulnRows = exportVulnRows()

	rec := do(t, r, http.MethodGet, "/api/v1/security/vulnerabilities?export=json", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Disposition"), ".json")

	// A bare array: the same row-oriented data CSV carries, without an envelope
	// a script would have to unwrap.
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows))
	require.Len(t, rows, 2)
	assert.Equal(t, float64(1), rows[0]["malicious"])
}

// An export of one page would be a quietly wrong report.
func TestScanHandler_Vulnerabilities_ExportIgnoresPaging(t *testing.T) {
	r, _, scanRepo, _ := mountScan(t)
	scanRepo.VulnRows = exportVulnRows()

	rec := do(t, r, http.MethodGet, "/api/v1/security/vulnerabilities?export=csv&limit=1&offset=10", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Greater(t, scanRepo.LastFilter.Limit, 1, "export must not request a single page")
	assert.Equal(t, 0, scanRepo.LastFilter.Offset, "export starts at the beginning")
}

// The export shows what the screen shows.
func TestScanHandler_Vulnerabilities_ExportKeepsFilters(t *testing.T) {
	r, _, scanRepo, _ := mountScan(t)
	scanRepo.VulnRows = exportVulnRows()

	rec := do(t, r, http.MethodGet,
		"/api/v1/security/vulnerabilities?export=csv&repo=npm-hosted&severity=MALICIOUS&format=npm", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, "npm-hosted", scanRepo.LastFilter.Repo)
	assert.Equal(t, "MALICIOUS", scanRepo.LastFilter.Severity)
	assert.Equal(t, "npm", scanRepo.LastFilter.Format)
}

func TestScanHandler_Vulnerabilities_ExportUnknownFormat_400(t *testing.T) {
	r, _, scanRepo, _ := mountScan(t)
	scanRepo.VulnRows = exportVulnRows()

	rec := do(t, r, http.MethodGet, "/api/v1/security/vulnerabilities?export=pdf", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// Without export=, the endpoint answers exactly as it did before.
func TestScanHandler_Vulnerabilities_NoExportStillPaginatedJSON(t *testing.T) {
	r, _, scanRepo, _ := mountScan(t)
	scanRepo.VulnRows = exportVulnRows()

	rec := do(t, r, http.MethodGet, "/api/v1/security/vulnerabilities", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Content-Disposition"))

	var body struct {
		Items []domain.VulnRow `json:"items"`
		Total int              `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 2, body.Total)
}

// ── Single component ──────────────────────────────────────────────────────────

func TestScanHandler_GetScanResult_ExportCSV(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	c := seedComponent(t, comps, "npm", "debug", "4.4.2")

	// Produce a cached result via a real scan (the OSV stub returns one HIGH).
	require.Equal(t, http.StatusOK,
		do(t, r, http.MethodPost, "/api/v1/components/"+c.ID+"/scan", nil).Code)

	rec := do(t, r, http.MethodGet, "/api/v1/components/"+c.ID+"/scan?export=csv", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/csv")
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")

	records, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2, "header + one finding")
	assert.Equal(t, "id", records[0][0])
	assert.Equal(t, "GHSA-xxxx", records[1][0])
	assert.Equal(t, "HIGH", records[1][1])
}

func TestScanHandler_GetScanResult_ExportJSON(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	c := seedComponent(t, comps, "npm", "debug", "4.4.2")
	require.Equal(t, http.StatusOK,
		do(t, r, http.MethodPost, "/api/v1/components/"+c.ID+"/scan", nil).Code)

	rec := do(t, r, http.MethodGet, "/api/v1/components/"+c.ID+"/scan?export=json", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Disposition"), ".json")

	var findings []domain.CVEFinding
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &findings))
	require.Len(t, findings, 1)
	assert.Equal(t, "GHSA-xxxx", findings[0].ID)
}

// A component that has never been scanned has nothing to export — and an empty
// file would read as "scanned, nothing found".
func TestScanHandler_GetScanResult_ExportWithoutScan_204(t *testing.T) {
	r, comps, _, _ := mountScan(t)
	c := seedComponent(t, comps, "npm", "never-scanned", "1.0.0")

	rec := do(t, r, http.MethodGet, "/api/v1/components/"+c.ID+"/scan?export=csv", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}
