package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
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
