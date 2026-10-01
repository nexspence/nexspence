package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// BackupHandler handles export and restore of all repository data.
type BackupHandler struct {
	svc *service.BackupService
	log logger.Logger
}

// NewBackupHandler constructs a BackupHandler backed by the given backup service.
func NewBackupHandler(svc *service.BackupService) *BackupHandler {
	return &BackupHandler{svc: svc}
}

// WithLogger wires a logger for export failures, which can no longer be
// reported in the response status; returns the handler for chaining.
func (h *BackupHandler) WithLogger(log logger.Logger) *BackupHandler {
	h.log = log
	return h
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
		h.failExport(c, err)
	}
}

// failExport handles an export error after the 200 is on the wire. Blobs that
// could not be read leave a valid archive that only lacks them — the same one
// a scheduled run keeps — so it is served and the gap logged. Anything else
// (a lost component or asset page, a write error) means the archive is not a
// backup: the connection is closed before the final chunk, so the client sees
// a broken transfer instead of a complete download (#569). gin.Recovery turns
// a panic(http.ErrAbortHandler) into a normal return and gin refuses to
// hijack once body bytes are written, hence the net/http writer underneath.
// An HTTP/2 connection cannot be hijacked; there the error is only logged.
func (h *BackupHandler) failExport(c *gin.Context, err error) {
	_ = c.Error(err)
	var incomplete *service.IncompleteBackupError
	if errors.As(err, &incomplete) {
		if h.log != nil {
			h.log.Warnw("backup export is incomplete", "path", c.Request.URL.Path, "err", err)
		}
		return
	}
	if h.log != nil {
		h.log.Errorw("backup export failed, aborting the download", "path", c.Request.URL.Path, "err", err)
	}
	var rw http.ResponseWriter = c.Writer
	for {
		u, ok := rw.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		rw = u.Unwrap()
	}
	if conn, _, herr := http.NewResponseController(rw).Hijack(); herr == nil {
		_ = conn.Close()
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
		h.failExport(c, err)
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
	// Validate everything before Upsert: once saved, ReloadSchedule drops the
	// current cron entry, so a bad value would silently stop a schedule that
	// was working — and stay persisted across restarts.
	if msg, status := h.validateBackupSettings(c.Request.Context(), &s); msg != "" {
		c.JSON(status, gin.H{"error": msg})
		return
	}
	if err := h.svc.Settings.Upsert(c.Request.Context(), &s); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save scheduled backup settings"})
		return
	}
	if err := h.svc.ReloadSchedule(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "saved, but the schedule could not be reloaded: " + err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// validateBackupSettings returns an error message and status for an invalid
// PUT body, or "" when it is valid.
func (h *BackupHandler) validateBackupSettings(ctx context.Context, s *domain.BackupSettings) (string, int) {
	if s.RetentionCount < 0 {
		return "retentionCount must be >= 0", http.StatusBadRequest
	}
	s.ScheduleCron = strings.TrimSpace(s.ScheduleCron)
	if s.ScheduleCron == "" {
		if s.Enabled {
			return "scheduleCron is required when scheduling is enabled", http.StatusBadRequest
		}
	} else if err := service.ValidateSchedule(s.ScheduleCron); err != nil {
		return fmt.Sprintf("invalid scheduleCron %q: %v", s.ScheduleCron, err), http.StatusBadRequest
	}
	if s.BlobStoreID == "" {
		if s.Enabled {
			return "blobStoreId is required when scheduling is enabled", http.StatusBadRequest
		}
		return "", 0
	}
	if _, err := uuid.Parse(s.BlobStoreID); err != nil {
		return fmt.Sprintf("blob store %q not found", s.BlobStoreID), http.StatusBadRequest
	}
	bs, err := h.svc.BlobStores.GetByID(ctx, s.BlobStoreID)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && bs == nil) {
		return fmt.Sprintf("blob store %q not found", s.BlobStoreID), http.StatusBadRequest
	}
	if err != nil {
		return "failed to look up the destination blob store", http.StatusInternalServerError
	}
	if bs.Type == "group" {
		// A group only picks a member per artifact write; a backup is not
		// one, and the registry cannot open a group as a physical store.
		return fmt.Sprintf("blob store %q is a group store: choose one of its member stores instead", bs.Name), http.StatusBadRequest
	}
	return "", 0
}
