package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// BackupHandler handles export and restore of all repository data.
type BackupHandler struct {
	svc *service.BackupService
}

func NewBackupHandler(svc *service.BackupService) *BackupHandler {
	return &BackupHandler{svc: svc}
}

// Export streams a full backup archive (gzipped tar) to the client.
// GET /api/v1/backup/export
func (h *BackupHandler) Export(c *gin.Context) {
	filename := fmt.Sprintf("nexspence-backup-%s.tar.gz",
		time.Now().UTC().Format("20060102-150405"))

	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Header("Content-Type", "application/x-tar")
	c.Header("Transfer-Encoding", "chunked")
	c.Status(http.StatusOK)

	if err := h.svc.Export(c.Request.Context(), c.Writer); err != nil {
		// Headers already sent; log the error but can't change status code.
		_ = err
	}
}

// Restore accepts a backup archive (multipart field "file" or raw body) and
// re-creates all exported data. Existing records are skipped (non-destructive).
// POST /api/v1/backup/restore
func (h *BackupHandler) Restore(c *gin.Context) {
	var reader = c.Request.Body

	// Support multipart upload (e.g. from a browser form).
	if c.ContentType() == "multipart/form-data" || c.GetHeader("Content-Type") == "" {
		if err := c.Request.ParseMultipartForm(512 << 20); err == nil {
			if f, _, err := c.Request.FormFile("file"); err == nil {
				defer f.Close()
				reader = f
			}
		}
	}

	stats, err := h.svc.Restore(c.Request.Context(), reader)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"restored": stats,
	})
}
