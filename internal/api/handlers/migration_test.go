package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
)

// migrationRepoMock is a minimal in-memory repository.MigrationRepo for handler tests.
// No MigrationRepo mock exists in internal/testutil, so we define a local one here.
type migrationRepoMock struct {
	jobs map[string]*domain.MigrationJob
	// listErr/getErr/createErr/updateErr/deleteErr force the corresponding 500 branches.
	listErr   error
	getErr    error
	createErr error
	updateErr error
	deleteErr error
}

func newMigrationRepoMock() *migrationRepoMock {
	return &migrationRepoMock{jobs: make(map[string]*domain.MigrationJob)}
}

func (m *migrationRepoMock) List(_ context.Context) ([]domain.MigrationJob, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	out := make([]domain.MigrationJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, *j)
	}
	return out, nil
}

func (m *migrationRepoMock) Get(_ context.Context, id string) (*domain.MigrationJob, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	j, ok := m.jobs[id]
	if !ok {
		return nil, errors.New("migration job not found")
	}
	return j, nil
}

func (m *migrationRepoMock) Create(_ context.Context, job *domain.MigrationJob) error {
	if m.createErr != nil {
		return m.createErr
	}
	if job.ID == "" {
		job.ID = "job-" + job.SourceURL
	}
	now := time.Now()
	job.CreatedAt = now
	job.UpdatedAt = now
	m.jobs[job.ID] = job
	return nil
}

func (m *migrationRepoMock) UpdateStatus(_ context.Context, id string, status domain.MigrationJobStatus) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	j, ok := m.jobs[id]
	if !ok {
		return errors.New("migration job not found")
	}
	j.Status = status
	return nil
}

func (m *migrationRepoMock) Delete(_ context.Context, id string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if _, ok := m.jobs[id]; !ok {
		return errors.New("migration job not found")
	}
	delete(m.jobs, id)
	return nil
}

func mountMigration(t *testing.T) (*gin.Engine, *migrationRepoMock) {
	t.Helper()
	repo := newMigrationRepoMock()
	h := handlers.NewMigrationHandler(repo)
	r := gin.New()
	r.GET("/api/v1/migration/jobs", h.ListJobs)
	r.GET("/api/v1/migration/jobs/:id", h.GetJob)
	r.POST("/api/v1/migration/jobs", h.CreateJob)
	r.POST("/api/v1/migration/jobs/:id/pause", h.PauseJob)
	r.POST("/api/v1/migration/jobs/:id/resume", h.ResumeJob)
	r.DELETE("/api/v1/migration/jobs/:id", h.DeleteJob)
	return r, repo
}

// ── ListJobs ────────────────────────────────────────────────────────────────

func TestMigration_ListJobs_Empty(t *testing.T) {
	r, _ := mountMigration(t)
	rec := do(t, r, http.MethodGet, "/api/v1/migration/jobs", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got)
}

func TestMigration_ListJobs_WithJob(t *testing.T) {
	r, repo := mountMigration(t)
	require.NoError(t, repo.Create(testContext(), &domain.MigrationJob{
		ID: "j1", SourceURL: "https://nexus.example.com", SourceUser: "admin",
		Status: domain.MigrationPending, MigrateRepos: true,
	}))
	rec := do(t, r, http.MethodGet, "/api/v1/migration/jobs", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "j1", got[0]["id"])
	assert.Equal(t, "https://nexus.example.com", got[0]["sourceUrl"])
	assert.Equal(t, "pending", got[0]["status"])
}

func TestMigration_ListJobs_RepoError_500(t *testing.T) {
	r, repo := mountMigration(t)
	repo.listErr = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/api/v1/migration/jobs", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── GetJob ──────────────────────────────────────────────────────────────────

func TestMigration_GetJob_OK(t *testing.T) {
	r, repo := mountMigration(t)
	started := time.Now()
	finished := started.Add(time.Hour)
	errMsg := "boom"
	require.NoError(t, repo.Create(testContext(), &domain.MigrationJob{
		ID: "g1", SourceURL: "https://src", Status: domain.MigrationDone,
		StartedAt: &started, FinishedAt: &finished, LastError: &errMsg, ErrorCount: 2,
	}))
	rec := do(t, r, http.MethodGet, "/api/v1/migration/jobs/g1", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "g1", got["id"])
	assert.Equal(t, "done", got["status"])
	assert.NotEmpty(t, got["startedAt"])
	assert.NotEmpty(t, got["finishedAt"])
	assert.Equal(t, "boom", got["lastError"])
}

func TestMigration_GetJob_NotFound_404(t *testing.T) {
	r, _ := mountMigration(t)
	rec := do(t, r, http.MethodGet, "/api/v1/migration/jobs/ghost", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMigration_GetJob_RepoError_500(t *testing.T) {
	r, repo := mountMigration(t)
	repo.getErr = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/api/v1/migration/jobs/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── CreateJob ─────────────────────────────────────────────────────────────────
// CreateJob only persists the job record and returns it; the handler does not
// start any background migration worker, so the full create path is reachable.

func TestMigration_CreateJob_OK_DefaultScope(t *testing.T) {
	r, _ := mountMigration(t)
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs", map[string]any{
		"sourceUrl":   "https://nexus.example.com",
		"credentials": map[string]any{"username": "admin", "password": "secret"},
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "https://nexus.example.com", got["sourceUrl"])
	assert.Equal(t, "admin", got["sourceUser"])
	assert.Equal(t, "pending", got["status"])
	// scope omitted → all four flags default to true
	assert.Equal(t, true, got["migrateRepos"])
	assert.Equal(t, true, got["migrateUsers"])
	assert.Equal(t, true, got["migrateBlobs"])
	assert.Equal(t, true, got["migratePolicies"])
}

func TestMigration_CreateJob_OK_ExplicitScope(t *testing.T) {
	r, _ := mountMigration(t)
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs", map[string]any{
		"sourceUrl": "https://nexus.example.com",
		"scope": map[string]any{
			"migrateRepos":    false,
			"migrateUsers":    true,
			"migrateBlobs":    false,
			"migratePolicies": false,
		},
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, false, got["migrateRepos"])
	assert.Equal(t, true, got["migrateUsers"])
	assert.Equal(t, false, got["migrateBlobs"])
	assert.Equal(t, false, got["migratePolicies"])
}

func TestMigration_CreateJob_BadJSON_400(t *testing.T) {
	r, _ := mountMigration(t)
	rec := doRaw(t, r, http.MethodPost, "/api/v1/migration/jobs", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMigration_CreateJob_MissingSourceURL_400(t *testing.T) {
	r, _ := mountMigration(t)
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs", map[string]any{
		"credentials": map[string]any{"username": "admin"},
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMigration_CreateJob_RepoError_500(t *testing.T) {
	r, repo := mountMigration(t)
	repo.createErr = errors.New("db down")
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs", map[string]any{
		"sourceUrl": "https://nexus.example.com",
	})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── PauseJob / ResumeJob (setStatus) ──────────────────────────────────────────

func TestMigration_PauseJob_OK(t *testing.T) {
	r, repo := mountMigration(t)
	require.NoError(t, repo.Create(testContext(), &domain.MigrationJob{ID: "p1", SourceURL: "s", Status: domain.MigrationRunning}))
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs/p1/pause", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, domain.MigrationPaused, repo.jobs["p1"].Status)
}

func TestMigration_ResumeJob_OK(t *testing.T) {
	r, repo := mountMigration(t)
	require.NoError(t, repo.Create(testContext(), &domain.MigrationJob{ID: "r1", SourceURL: "s", Status: domain.MigrationPaused}))
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs/r1/resume", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, domain.MigrationRunning, repo.jobs["r1"].Status)
}

func TestMigration_PauseJob_NotFound_404(t *testing.T) {
	r, _ := mountMigration(t)
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs/ghost/pause", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMigration_ResumeJob_RepoError_500(t *testing.T) {
	r, repo := mountMigration(t)
	repo.updateErr = errors.New("db down")
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs/any/resume", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── DeleteJob ─────────────────────────────────────────────────────────────────

func TestMigration_DeleteJob_OK(t *testing.T) {
	r, repo := mountMigration(t)
	require.NoError(t, repo.Create(testContext(), &domain.MigrationJob{ID: "d1", SourceURL: "s"}))
	rec := do(t, r, http.MethodDelete, "/api/v1/migration/jobs/d1", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	_, ok := repo.jobs["d1"]
	assert.False(t, ok)
}

func TestMigration_DeleteJob_NotFound_404(t *testing.T) {
	r, _ := mountMigration(t)
	rec := do(t, r, http.MethodDelete, "/api/v1/migration/jobs/ghost", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMigration_DeleteJob_RepoError_500(t *testing.T) {
	r, repo := mountMigration(t)
	repo.deleteErr = errors.New("db down")
	rec := do(t, r, http.MethodDelete, "/api/v1/migration/jobs/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
