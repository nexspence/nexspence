package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// MigrationHandler serves the Nexus-migration job REST endpoints. Reads come
// straight from the job repository; anything that starts, stops or inspects a
// live migration goes through the runner.
type MigrationHandler struct {
	repo repository.MigrationRepo
	svc  *service.NexusMigrationService
}

// NewMigrationHandler constructs a MigrationHandler over the job repository and
// the migration runner.
func NewMigrationHandler(repo repository.MigrationRepo, svc *service.NexusMigrationService) *MigrationHandler {
	return &MigrationHandler{repo: repo, svc: svc}
}

type migrationJobResp struct {
	ID                  string   `json:"id"`
	SourceURL           string   `json:"sourceUrl"`
	SourceUser          string   `json:"sourceUser"`
	Status              string   `json:"status"`
	MigrateRepos        bool     `json:"migrateRepos"`
	MigrateUsers        bool     `json:"migrateUsers"`
	MigrateBlobs        bool     `json:"migrateBlobs"`
	MigratePolicies     bool     `json:"migratePolicies"`
	MigratePrivileges   bool     `json:"migratePrivileges"`
	MigrateRoles        bool     `json:"migrateRoles"`
	MigrateRoutingRules bool     `json:"migrateRoutingRules"`
	UserRealms          []string `json:"userRealms,omitempty"`
	RepositoriesTotal   int      `json:"repositoriesTotal"`
	RepositoriesDone    int      `json:"repositoriesDone"`
	AssetsTotal         int64    `json:"assetsTotal"`
	AssetsDone          int64    `json:"assetsDone"`
	ErrorCount          int      `json:"errorCount"`
	LastError           *string  `json:"lastError,omitempty"`
	StartedAt           *string  `json:"startedAt,omitempty"`
	FinishedAt          *string  `json:"finishedAt,omitempty"`
	CreatedAt           string   `json:"createdAt"`
	UpdatedAt           string   `json:"updatedAt"`
}

// toJobResp maps a job onto its API shape. SourcePassword is deliberately
// absent: it is stored sealed and never leaves the process.
func toJobResp(j domain.MigrationJob) migrationJobResp {
	r := migrationJobResp{
		ID:                  j.ID,
		SourceURL:           j.SourceURL,
		SourceUser:          j.SourceUser,
		Status:              string(j.Status),
		MigrateRepos:        j.MigrateRepos,
		MigrateUsers:        j.MigrateUsers,
		MigrateBlobs:        j.MigrateBlobs,
		MigratePolicies:     j.MigratePolicies,
		MigratePrivileges:   j.MigratePrivileges,
		MigrateRoles:        j.MigrateRoles,
		MigrateRoutingRules: j.MigrateRoutingRules,
		UserRealms:          j.UserRealms,
		RepositoriesTotal:   j.TotalRepos,
		RepositoriesDone:    j.DoneRepos,
		AssetsTotal:         j.TotalAssets,
		AssetsDone:          j.DoneAssets,
		ErrorCount:          j.ErrorCount,
		LastError:           j.LastError,
		CreatedAt:           j.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:           j.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
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
		respondMigrationErr(c, err)
		return
	}
	c.JSON(http.StatusOK, toJobResp(*job))
}

// createJobReq is the body of POST /api/v1/migration/jobs.
//
// It carries no concurrency knob: migrateAssets transfers one asset at a time,
// and the safe unit of parallelism would be a whole repository rather than
// individual assets within one (an OCI manifest must never transfer before the
// blobs it references). The field this struct used to bind, options.concurrency,
// was read nowhere else in the codebase, so a caller asking for 10 got the same
// single-threaded transfer with no error or warning that the setting was
// ignored. An options object on an older client's request is still accepted and
// ignored, as any unknown field is.
type createJobReq struct {
	SourceURL   string `json:"sourceUrl" binding:"required"`
	Credentials struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"credentials"`
	Scope struct {
		MigrateRepos        *bool `json:"migrateRepos"`
		MigrateUsers        *bool `json:"migrateUsers"`
		MigrateBlobs        *bool `json:"migrateBlobs"`
		MigratePolicies     *bool `json:"migratePolicies"`
		MigratePrivileges   *bool `json:"migratePrivileges"`
		MigrateRoles        *bool `json:"migrateRoles"`
		MigrateRoutingRules *bool `json:"migrateRoutingRules"`
		// UserRealms names the source realms user migration pulls accounts
		// from ("default" = local). Empty means local-only (#342).
		UserRealms []string `json:"userRealms"`
	} `json:"scope"`
}

func boolDefault(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

// CreateJob handles POST /api/v1/migration/jobs — creates the job and starts it.
func (h *MigrationHandler) CreateJob(c *gin.Context) {
	var req createJobReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// migratePolicies used to be the single switch for the security model. It
	// stays the default the finer-grained scopes fall back to, so a client
	// written against the older shape still gets what it asked for.
	policies := boolDefault(req.Scope.MigratePolicies, true)

	job := &domain.MigrationJob{
		SourceURL:           req.SourceURL,
		SourceUser:          req.Credentials.Username,
		MigrateRepos:        boolDefault(req.Scope.MigrateRepos, true),
		MigrateUsers:        boolDefault(req.Scope.MigrateUsers, true),
		MigrateBlobs:        boolDefault(req.Scope.MigrateBlobs, true),
		MigratePolicies:     policies,
		MigratePrivileges:   boolDefault(req.Scope.MigratePrivileges, policies),
		MigrateRoles:        boolDefault(req.Scope.MigrateRoles, policies),
		MigrateRoutingRules: boolDefault(req.Scope.MigrateRoutingRules, policies),
		UserRealms:          req.Scope.UserRealms,
	}

	if err := h.svc.Create(c.Request.Context(), job, req.Credentials.Password); err != nil {
		respondMigrationErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, toJobResp(*job))
}

// PauseJob handles POST /api/v1/migration/jobs/:id/pause — stops the run and
// parks the job where it stands.
func (h *MigrationHandler) PauseJob(c *gin.Context) {
	if err := h.svc.Pause(c.Request.Context(), c.Param("id")); err != nil {
		respondMigrationErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ResumeJob handles POST /api/v1/migration/jobs/:id/resume — starts a fresh
// pass that skips everything already migrated.
func (h *MigrationHandler) ResumeJob(c *gin.Context) {
	if err := h.svc.Resume(c.Request.Context(), c.Param("id")); err != nil {
		respondMigrationErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteJob handles DELETE /api/v1/migration/jobs/:id — removes a migration job.
func (h *MigrationHandler) DeleteJob(c *gin.Context) {
	id := c.Param("id")
	// Stop the runner first: deleting the row under a live goroutine would
	// leave it writing progress to a job that no longer exists.
	_ = h.svc.Pause(c.Request.Context(), id)
	if err := h.repo.Delete(c.Request.Context(), id); err != nil {
		respondMigrationErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type previewReq struct {
	SourceURL string `json:"sourceUrl"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

// Preview handles POST /api/v1/migration/preview — a read-only reachability
// check against a Nexus instance. It creates nothing, so it is safe to call as
// often as an operator edits the form.
func (h *MigrationHandler) Preview(c *gin.Context) {
	var req previewReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	res, err := h.svc.Preview(c.Request.Context(), req.SourceURL, req.Username, req.Password)
	if errors.Is(err, service.ErrInvalidInput) {
		// The request was understood and rejected on its content, not its shape.
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error(), "reachable": false})
		return
	}
	if err != nil {
		// The failure is upstream: the operator needs the underlying reason,
		// not a generic 500 that reads like a bug here.
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "reachable": false})
		return
	}
	c.JSON(http.StatusOK, res)
}

// respondMigrationErr maps a service or repository error onto a status code.
func respondMigrationErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, repository.ErrNotFound), strings.Contains(err.Error(), "not found"):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
