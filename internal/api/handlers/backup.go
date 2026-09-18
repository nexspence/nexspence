package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// BackupHandler handles export and restore of all repository data.
type BackupHandler struct {
	svc *service.BackupService
}

// NewBackupHandler constructs a BackupHandler backed by the given backup service.
func NewBackupHandler(svc *service.BackupService) *BackupHandler {
	return &BackupHandler{svc: svc}
}

// Export streams a full backup archive (gzipped tar) to the client.
// GET /api/v1/backup/export
func (h *BackupHandler) Export(c *gin.Context) {
	filename := fmt.Sprintf("nexspence-backup-%s.tar.gz",
		time.Now().UTC().Format("20060102-150405"))

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
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
				defer func() { _ = f.Close() }()
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

// ExportRepo streams a per-repository backup archive (gzipped tar) to the client.
// GET /api/v1/repositories/:name/export
func (h *BackupHandler) ExportRepo(c *gin.Context) {
	name := c.Param("name")
	ctx := c.Request.Context()

	// Pre-check existence before committing to streaming headers.
	repo, _ := h.svc.Repos.Get(ctx, name)
	if repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found: " + name})
		return
	}

	ts := time.Now().UTC().Format("20060102-150405")
	filename := fmt.Sprintf("nexspence-repo-%s-%s.tar.gz", name, ts)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Content-Type", "application/x-tar")
	c.Header("Transfer-Encoding", "chunked")
	c.Status(http.StatusOK)

	if err := h.svc.ExportRepo(ctx, name, c.Writer); err != nil {
		_ = err // headers already sent; cannot change status code
	}
}

// ImportRepo accepts a per-repository backup archive (multipart field "file")
// and re-creates the repository, components, assets, and blobs.
// POST /api/v1/repositories/import
func (h *BackupHandler) ImportRepo(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing file field: " + err.Error()})
		return
	}
	f, err := fh.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot open uploaded file: " + err.Error()})
		return
	}
	defer func() { _ = f.Close() }()

	targetName := c.PostForm("targetName")
	conflictMode := c.DefaultPostForm("conflictMode", "skip")

	stats, err := h.svc.ImportRepo(c.Request.Context(), f, targetName, conflictMode)
	if err != nil {
		if errors.Is(err, service.ErrRepoConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"imported": stats})
}

// Settings serves GET /api/v1/backup/settings — the scheduled-backup config.
func (h *BackupHandler) Settings(c *gin.Context) {
	if h.svc.Settings == nil {
		c.JSON(http.StatusOK, domain.BackupSettings{ScheduleCron: "0 3 * * *", RetentionCount: 7})
		return
	}
	s, err := h.svc.Settings.Get(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s)
}

// UpdateSettings serves PUT /api/v1/backup/settings. Saving reloads the cron
// entry immediately — no restart needed for a schedule/destination change.
func (h *BackupHandler) UpdateSettings(c *gin.Context) {
	if h.svc.Settings == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "scheduled backup is not configured on this instance"})
		return
	}
	var s domain.BackupSettings
	if err := c.ShouldBindJSON(&s); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if s.RetentionCount < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "retentionCount must be >= 0"})
		return
	}
	if err := h.svc.Settings.Upsert(c.Request.Context(), &s); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.ReloadSchedule(c.Request.Context()); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "saved, but schedule is invalid: " + err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
