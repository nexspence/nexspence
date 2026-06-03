package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/service"
)

// BlobStoreMigrationHandler handles the 3 blob store migration endpoints.
type BlobStoreMigrationHandler struct {
	svc *service.BlobStoreMigrationService
}

func NewBlobStoreMigrationHandler(svc *service.BlobStoreMigrationService) *BlobStoreMigrationHandler {
	return &BlobStoreMigrationHandler{svc: svc}
}

// Start handles POST /api/v1/repositories/:name/migrate-blob-store
func (h *BlobStoreMigrationHandler) Start(c *gin.Context) {
	repoName := c.Param("name")
	var req struct {
		TargetStoreID string `json:"targetStoreId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	m, err := h.svc.Start(c.Request.Context(), repoName, req.TargetStoreID)
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "already running"):
			c.JSON(http.StatusConflict, gin.H{"error": msg})
		case strings.Contains(msg, "not found"), strings.Contains(msg, "same as"):
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": msg})
		}
		return
	}
	c.JSON(http.StatusCreated, m)
}

// GetLatest handles GET /api/v1/repositories/:name/blob-store-migration
func (h *BlobStoreMigrationHandler) GetLatest(c *gin.Context) {
	repoName := c.Param("name")
	m, err := h.svc.GetLatestByRepo(c.Request.Context(), repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if m == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no migration found for this repository"})
		return
	}
	c.JSON(http.StatusOK, m)
}

// Cancel handles DELETE /api/v1/repositories/:name/blob-store-migration
func (h *BlobStoreMigrationHandler) Cancel(c *gin.Context) {
	repoName := c.Param("name")

	active, err := h.svc.GetLatestByRepo(c.Request.Context(), repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if active == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no migration found for this repository"})
		return
	}
	if active.Status != "running" && active.Status != "pending" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "migration is not active"})
		return
	}

	if err := h.svc.Cancel(c.Request.Context(), active.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"cancelled": true}) //nolint:misspell // API response key, stable contract
}
