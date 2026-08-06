package handlers

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// exportMaxRows caps a single export. An export is one response assembled in
// memory, so it needs a bound; this one is set well past any registry that
// would fit on a dashboard, and a truncated export says so in a header rather
// than quietly handing over a partial report.
const exportMaxRows = 50000

// exportTruncatedHeader marks an export that hit exportMaxRows.
const exportTruncatedHeader = "X-Export-Truncated"

// exportFormat reads the ?export= parameter.
//
// It is a parameter on the existing endpoints rather than a route of its own so
// that an export is authorized exactly as the data it exports already is —
// there is no second path to get the access rules right on.
//
// Returns ("", false) when no export was asked for, and ("", true) for a format
// that does not exist.
func exportFormat(c *gin.Context) (format string, invalid bool) {
	switch v := c.Query("export"); v {
	case "":
		return "", false
	case "csv", "json":
		return v, false
	default:
		return "", true
	}
}

// exportFilename builds a dated, self-describing download name.
func exportFilename(kind, format string) string {
	return fmt.Sprintf("nexspence-%s-%s.%s", kind, time.Now().UTC().Format("2006-01-02"), format)
}

func setExportHeaders(c *gin.Context, kind, format string) {
	contentType := "text/csv; charset=utf-8"
	if format == "json" {
		contentType = "application/json; charset=utf-8"
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", `attachment; filename="`+exportFilename(kind, format)+`"`)
}

// writeCSV streams a header row and the given rows to the response.
func writeCSV(c *gin.Context, header []string, rows [][]string) {
	w := csv.NewWriter(c.Writer)
	if err := w.Write(header); err != nil {
		return
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return
		}
	}
	w.Flush()
}

// vulnExportHeader names the columns of a vulnerability-dashboard export.
// Malicious leads the counts, as it does everywhere else: a report that buries
// the most severe class of finding is the problem the tier was added to fix.
var vulnExportHeader = []string{
	"repository", "format", "component", "version",
	"malicious", "critical", "high", "medium", "low", "unknown",
	"scanned_at",
}

func vulnExportRows(items []*domain.VulnRow) [][]string {
	rows := make([][]string, 0, len(items))
	for _, v := range items {
		rows = append(rows, []string{
			v.RepoName, v.Format, v.Name, v.Version,
			strconv.Itoa(v.Malicious), strconv.Itoa(v.Critical), strconv.Itoa(v.High),
			strconv.Itoa(v.Medium), strconv.Itoa(v.Low), strconv.Itoa(v.Unknown),
			v.ScannedAt.UTC().Format(time.RFC3339),
		})
	}
	return rows
}

// exportVulnerabilities answers GET /api/v1/security/vulnerabilities?export=…
// with the whole filtered set as a downloadable file.
//
// Paging is deliberately ignored: the paged response exists to render a screen,
// while an export of a single page would be a report that is quietly wrong.
func (h *ScanHandler) exportVulnerabilities(c *gin.Context, format string, f domain.VulnFilter) {
	f.Limit = exportMaxRows
	f.Offset = 0

	items, _, err := h.svc.ListVulnerabilities(c.Request.Context(), f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []*domain.VulnRow{}
	}
	if len(items) >= exportMaxRows {
		c.Header(exportTruncatedHeader, strconv.Itoa(exportMaxRows))
	}

	setExportHeaders(c, "vulnerabilities", format)
	if format == "json" {
		// A bare array, not the endpoint's {items,total} envelope: this is the
		// same row-oriented data the CSV carries, and a file a script reads is
		// easier to consume without unwrapping it.
		c.JSON(http.StatusOK, items)
		return
	}
	writeCSV(c, vulnExportHeader, vulnExportRows(items))
}

// findingExportHeader names the columns of a single component's export.
var findingExportHeader = []string{"id", "severity", "package", "installed_version", "fixed_version", "title"}

func findingExportRows(findings []domain.CVEFinding) [][]string {
	rows := make([][]string, 0, len(findings))
	for _, f := range findings {
		rows = append(rows, []string{
			f.ID, f.Severity, f.PkgName, f.InstalledVer, f.FixedVersion, f.Title,
		})
	}
	return rows
}

// exportScanResult answers GET /api/v1/components/:id/scan?export=… with that
// component's findings as a downloadable file.
func (h *ScanHandler) exportScanResult(c *gin.Context, format string, result *domain.ScanResult) {
	setExportHeaders(c, "scan-"+c.Param("id"), format)
	if format == "json" {
		findings := result.Findings
		if findings == nil {
			findings = []domain.CVEFinding{}
		}
		c.JSON(http.StatusOK, findings)
		return
	}
	writeCSV(c, findingExportHeader, findingExportRows(result.Findings))
}
