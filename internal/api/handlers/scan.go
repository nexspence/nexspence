package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
)

type ScanHandler struct {
	svc *service.ScanService
}

func NewScanHandler(svc *service.ScanService) *ScanHandler {
	return &ScanHandler{svc: svc}
}

// Scan triggers a Trivy vulnerability scan for a Docker component.
// POST /api/v1/components/:id/scan
// Body (optional): {"imageRef": "registry/image:tag"}
func (h *ScanHandler) Scan(c *gin.Context) {
	id := c.Param("id")

	var body struct {
		ImageRef string `json:"imageRef"`
	}
	_ = c.ShouldBindJSON(&body)

	result, err := h.svc.Scan(c.Request.Context(), id, body.ImageRef)
	if err != nil {
		if errors.Is(err, service.ErrTrivyNotInstalled) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// GetScanResult returns the cached scan result for a component.
// GET /api/v1/components/:id/scan
func (h *ScanHandler) GetScanResult(c *gin.Context) {
	id := c.Param("id")
	result, err := h.svc.GetResult(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if result == nil {
		// No cached scan yet — not an error (avoid 404 in logs / monitoring).
		c.Status(http.StatusNoContent)
		return
	}
	c.JSON(http.StatusOK, result)
}

// Summary returns aggregated vulnerability counts across all scanned components.
// GET /api/v1/security/summary
func (h *ScanHandler) Summary(c *gin.Context) {
	summary, err := h.svc.GetSummary(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, summary)
}

// Vulnerabilities returns a paginated list of vulnerability rows.
// GET /api/v1/security/vulnerabilities?repo=&severity=&format=&limit=&offset=
func (h *ScanHandler) Vulnerabilities(c *gin.Context) {
	limit := 50
	offset := 0
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	f := domain.VulnFilter{
		Repo:     c.Query("repo"),
		Severity: c.Query("severity"),
		Format:   c.Query("format"),
		Limit:    limit,
		Offset:   offset,
	}
	items, total, err := h.svc.ListVulnerabilities(c.Request.Context(), f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []*domain.VulnRow{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

// BulkScanHandler triggers a synchronous bulk scan across all components (or one repo).
// POST /api/v1/security/scan/bulk
// Body (optional): {"repo": "my-repo"}
func (h *ScanHandler) BulkScanHandler(c *gin.Context) {
	var body struct {
		Repo string `json:"repo"`
	}
	_ = c.ShouldBindJSON(&body)

	scanned, failed, err := h.svc.BulkScan(c.Request.Context(), body.Repo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"scanned": scanned, "failed": failed})
}
