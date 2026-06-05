package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// MigrationHandler serves the Nexus-migration job REST endpoints.
type MigrationHandler struct {
	repo repository.MigrationRepo
}

// NewMigrationHandler constructs a MigrationHandler backed by the given migration repository.
func NewMigrationHandler(repo repository.MigrationRepo) *MigrationHandler {
	return &MigrationHandler{repo: repo}
}

type migrationJobResp struct {
	ID                string  `json:"id"`
	SourceURL         string  `json:"sourceUrl"`
	SourceUser        string  `json:"sourceUser"`
	Status            string  `json:"status"`
	MigrateRepos      bool    `json:"migrateRepos"`
	MigrateUsers      bool    `json:"migrateUsers"`
	MigrateBlobs      bool    `json:"migrateBlobs"`
	MigratePolicies   bool    `json:"migratePolicies"`
	RepositoriesTotal int     `json:"repositoriesTotal"`
	RepositoriesDone  int     `json:"repositoriesDone"`
	AssetsTotal       int64   `json:"assetsTotal"`
	AssetsDone        int64   `json:"assetsDone"`
	ErrorCount        int     `json:"errorCount"`
	LastError         *string `json:"lastError,omitempty"`
	StartedAt         *string `json:"startedAt,omitempty"`
	FinishedAt        *string `json:"finishedAt,omitempty"`
	CreatedAt         string  `json:"createdAt"`
	UpdatedAt         string  `json:"updatedAt"`
}

func toJobResp(j domain.MigrationJob) migrationJobResp {
	r := migrationJobResp{
		ID:                j.ID,
		SourceURL:         j.SourceURL,
		SourceUser:        j.SourceUser,
		Status:            string(j.Status),
		MigrateRepos:      j.MigrateRepos,
		MigrateUsers:      j.MigrateUsers,
		MigrateBlobs:      j.MigrateBlobs,
		MigratePolicies:   j.MigratePolicies,
		RepositoriesTotal: j.TotalRepos,
		RepositoriesDone:  j.DoneRepos,
		AssetsTotal:       j.TotalAssets,
		AssetsDone:        j.DoneAssets,
		ErrorCount:        j.ErrorCount,
		LastError:         j.LastError,
		CreatedAt:         j.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:         j.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if j.StartedAt != nil {
		s := j.StartedAt.Format("2006-01-02T15:04:05Z07:00")
		r.StartedAt = &s
	}
	if j.FinishedAt != nil {
		s := j.FinishedAt.Format("2006-01-02T15:04:05Z07:00")
		r.FinishedAt = &s
	}
	return r
}

// ListJobs handles GET /api/v1/migration/jobs — returns all migration jobs.
func (h *MigrationHandler) ListJobs(c *gin.Context) {
	jobs, err := h.repo.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]migrationJobResp, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, toJobResp(j))
	}
	c.JSON(http.StatusOK, out)
}

// GetJob handles GET /api/v1/migration/jobs/:id — returns a single migration job.
func (h *MigrationHandler) GetJob(c *gin.Context) {
	job, err := h.repo.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, toJobResp(*job))
}

type createJobReq struct {
	SourceURL   string `json:"sourceUrl" binding:"required"`
	Credentials struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"credentials"`
	Options struct {
		Concurrency int `json:"concurrency"`
	} `json:"options"`
	Scope struct {
		MigrateRepos    *bool `json:"migrateRepos"`
		MigrateUsers    *bool `json:"migrateUsers"`
		MigrateBlobs    *bool `json:"migrateBlobs"`
		MigratePolicies *bool `json:"migratePolicies"`
	} `json:"scope"`
}

func boolDefault(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

// CreateJob handles POST /api/v1/migration/jobs — creates a pending migration job.
func (h *MigrationHandler) CreateJob(c *gin.Context) {
	var req createJobReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	job := &domain.MigrationJob{
		SourceURL:       req.SourceURL,
		SourceUser:      req.Credentials.Username,
		Status:          domain.MigrationPending,
		MigrateRepos:    boolDefault(req.Scope.MigrateRepos, true),
		MigrateUsers:    boolDefault(req.Scope.MigrateUsers, true),
		MigrateBlobs:    boolDefault(req.Scope.MigrateBlobs, true),
		MigratePolicies: boolDefault(req.Scope.MigratePolicies, true),
	}

	if err := h.repo.Create(c.Request.Context(), job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, toJobResp(*job))
}

// PauseJob handles the pause action, setting the job status to paused.
func (h *MigrationHandler) PauseJob(c *gin.Context) {
	h.setStatus(c, domain.MigrationPaused)
}

// ResumeJob handles the resume action, setting the job status back to running.
func (h *MigrationHandler) ResumeJob(c *gin.Context) {
	h.setStatus(c, domain.MigrationRunning)
}

// DeleteJob handles DELETE /api/v1/migration/jobs/:id — removes a migration job.
func (h *MigrationHandler) DeleteJob(c *gin.Context) {
	if err := h.repo.Delete(c.Request.Context(), c.Param("id")); err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *MigrationHandler) setStatus(c *gin.Context, status domain.MigrationJobStatus) {
	if err := h.repo.UpdateStatus(c.Request.Context(), c.Param("id"), status); err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
