package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// stubRoundTrip answers every outbound request from the migration runner, so a
// handler test never depends on a reachable Nexus.
type stubRoundTrip func(*http.Request) (*http.Response, error)

func (f stubRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// emptyNexus answers every REST call with an empty list: a job created in a
// handler test runs to completion immediately and touches nothing.
func emptyNexus() stubRoundTrip {
	return func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`[]`)),
		}, nil
	}
}

func mountMigration(t *testing.T) (*gin.Engine, *testutil.MigrationRepo) {
	return mountMigrationWith(t, emptyNexus())
}

func mountMigrationWith(t *testing.T, rt stubRoundTrip) (*gin.Engine, *testutil.MigrationRepo) {
	t.Helper()
	repo := testutil.NewMigrationRepo()

	repoRepo := testutil.NewRepoRepo()
	blobRepo := testutil.NewBlobStoreRepo()
	blobStore := testutil.NewBlobStore()

	svc := service.NewNexusMigrationService(service.NexusMigrationConfig{
		Jobs:         repo,
		Repos:        service.NewRepositoryService(repoRepo, blobRepo, blobStore, testutil.NewCleanupPolicyRepo()),
		Users:        &noopUserStore{},
		Roles:        testutil.NewRoleRepo(),
		Privileges:   testutil.NewPrivilegeRepo(),
		RoutingRules: testutil.NewRoutingRuleRepo(),
		Deps: formats.Deps{
			Repos: repoRepo, Components: testutil.NewComponentRepo(),
			Assets: testutil.NewAssetRepo(), Blobs: blobRepo, BlobStore: blobStore,
		},
		JWTSecret: "handler-test-secret",
		Log:       zap.NewNop().Sugar(),
		HTTPClientFor: func(timeout time.Duration) *http.Client {
			return &http.Client{Timeout: timeout, Transport: rt}
		},
	})

	h := handlers.NewMigrationHandler(repo, svc)
	r := gin.New()
	r.GET("/api/v1/migration/jobs", h.ListJobs)
	r.GET("/api/v1/migration/jobs/:id", h.GetJob)
	r.POST("/api/v1/migration/jobs", h.CreateJob)
	r.POST("/api/v1/migration/jobs/:id/pause", h.PauseJob)
	r.POST("/api/v1/migration/jobs/:id/resume", h.ResumeJob)
	r.DELETE("/api/v1/migration/jobs/:id", h.DeleteJob)
	r.POST("/api/v1/migration/preview", h.Preview)
	return r, repo
}

// noopUserStore stands in for user management the handler tests never exercise.
type noopUserStore struct{}

func (noopUserStore) Get(_ context.Context, _ string) (*domain.User, error) {
	return nil, service.ErrNotFound
}
func (noopUserStore) Create(_ context.Context, _ *domain.User, _ string) error { return nil }

// jobStatus reads the job back, failing the test if it is gone.
func jobStatus(t *testing.T, repo *testutil.MigrationRepo, id string) domain.MigrationJobStatus {
	t.Helper()
	j, err := repo.Get(testContext(), id)
	require.NoError(t, err)
	return j.Status
}

// waitForJob polls until pred holds, which is how a test observes a runner
// goroutine without guessing at timings.
func waitForJob(t *testing.T, repo *testutil.MigrationRepo, id string, pred func(domain.MigrationJob) bool) domain.MigrationJob {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, err := repo.Get(testContext(), id)
		require.NoError(t, err)
		if pred(*j) {
			return *j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s never reached the expected state", id)
	return domain.MigrationJob{}
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

func TestMigration_ListJobs_NeverLeaksTheSourcePassword(t *testing.T) {
	r, repo := mountMigration(t)
	require.NoError(t, repo.Create(testContext(), &domain.MigrationJob{
		ID: "j1", SourceURL: "https://nexus.example.com", SourceUser: "admin",
		SourcePassword: "sealed-secret-value", Status: domain.MigrationPending,
	}))
	rec := do(t, r, http.MethodGet, "/api/v1/migration/jobs", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "sealed-secret-value")
	assert.NotContains(t, strings.ToLower(rec.Body.String()), "password")
}

func TestMigration_ListJobs_RepoError_500(t *testing.T) {
	r, repo := mountMigration(t)
	repo.ListErr = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/api/v1/migration/jobs", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── GetJob ──────────────────────────────────────────────────────────────────

func TestMigration_GetJob_OK(t *testing.T) {
	r, repo := mountMigration(t)
	started := time.Now().UTC()
	require.NoError(t, repo.Create(testContext(), &domain.MigrationJob{
		ID: "g1", SourceURL: "https://nexus.example.com", Status: domain.MigrationRunning,
		TotalRepos: 4, DoneRepos: 2, TotalAssets: 100, DoneAssets: 40, StartedAt: &started,
	}))
	rec := do(t, r, http.MethodGet, "/api/v1/migration/jobs/g1", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "running", got["status"])
	assert.InDelta(t, 4, got["repositoriesTotal"], 0.001)
	assert.InDelta(t, 40, got["assetsDone"], 0.001)
	assert.NotEmpty(t, got["startedAt"])
}

func TestMigration_GetJob_NotFound_404(t *testing.T) {
	r, _ := mountMigration(t)
	rec := do(t, r, http.MethodGet, "/api/v1/migration/jobs/ghost", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMigration_GetJob_RepoError_500(t *testing.T) {
	r, repo := mountMigration(t)
	repo.GetErr = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/api/v1/migration/jobs/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── CreateJob ───────────────────────────────────────────────────────────────

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
	// scope omitted → every stage is on
	for _, key := range []string{
		"migrateRepos", "migrateUsers", "migrateBlobs", "migratePolicies",
		"migratePrivileges", "migrateRoles", "migrateRoutingRules",
	} {
		assert.Equal(t, true, got[key], key)
	}
}

func TestMigration_CreateJob_OK_ExplicitScope(t *testing.T) {
	r, _ := mountMigration(t)
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs", map[string]any{
		"sourceUrl": "https://nexus.example.com",
		"scope": map[string]any{
			"migrateRepos":        false,
			"migrateUsers":        true,
			"migrateBlobs":        false,
			"migratePolicies":     false,
			"migrateRoutingRules": true,
		},
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, false, got["migrateRepos"])
	assert.Equal(t, true, got["migrateUsers"])
	assert.Equal(t, false, got["migrateBlobs"])
	assert.Equal(t, false, got["migratePolicies"])
	// unnamed security scopes follow migratePolicies; a named one wins
	assert.Equal(t, false, got["migratePrivileges"])
	assert.Equal(t, false, got["migrateRoles"])
	assert.Equal(t, true, got["migrateRoutingRules"])
}

// This is the reported bug: a job could be created and then nothing ever ran it.
func TestMigration_CreateJob_StartsTheRun(t *testing.T) {
	r, repo := mountMigration(t)
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs", map[string]any{
		"sourceUrl":   "https://nexus.example.com",
		"credentials": map[string]any{"username": "admin", "password": "secret"},
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

	final := waitForJob(t, repo, got["id"].(string), func(j domain.MigrationJob) bool {
		return j.Status == domain.MigrationDone
	})
	assert.NotNil(t, final.StartedAt, "the run stamps started_at instead of sitting pending")
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

func TestMigration_CreateJob_MalformedSourceURL_400(t *testing.T) {
	r, repo := mountMigration(t)
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs", map[string]any{
		"sourceUrl": "nexus.example.com",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	jobs, err := repo.List(testContext())
	require.NoError(t, err)
	assert.Empty(t, jobs, "a rejected job leaves no row behind")
}

func TestMigration_CreateJob_RepoError_500(t *testing.T) {
	r, repo := mountMigration(t)
	repo.CreateErr = errors.New("db down")
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs", map[string]any{
		"sourceUrl": "https://nexus.example.com",
	})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── PauseJob / ResumeJob ────────────────────────────────────────────────────

func TestMigration_PauseJob_OK(t *testing.T) {
	r, repo := mountMigration(t)
	require.NoError(t, repo.Create(testContext(), &domain.MigrationJob{
		ID: "p1", SourceURL: "https://nexus.example.com", Status: domain.MigrationRunning,
	}))
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs/p1/pause", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, domain.MigrationPaused, jobStatus(t, repo, "p1"))
}

func TestMigration_PauseJob_AlreadyFinished_400(t *testing.T) {
	r, repo := mountMigration(t)
	require.NoError(t, repo.Create(testContext(), &domain.MigrationJob{
		ID: "p2", SourceURL: "https://nexus.example.com", Status: domain.MigrationDone,
	}))
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs/p2/pause", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMigration_ResumeJob_OK(t *testing.T) {
	r, repo := mountMigration(t)
	require.NoError(t, repo.Create(testContext(), &domain.MigrationJob{
		ID: "r1", SourceURL: "https://nexus.example.com", Status: domain.MigrationPaused,
	}))
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs/r1/resume", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	waitForJob(t, repo, "r1", func(j domain.MigrationJob) bool {
		return j.Status != domain.MigrationPaused
	})
}

func TestMigration_PauseJob_NotFound_404(t *testing.T) {
	r, _ := mountMigration(t)
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs/ghost/pause", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMigration_ResumeJob_NotFound_404(t *testing.T) {
	r, _ := mountMigration(t)
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs/ghost/resume", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMigration_ResumeJob_RepoError_500(t *testing.T) {
	r, repo := mountMigration(t)
	repo.GetErr = errors.New("db down")
	rec := do(t, r, http.MethodPost, "/api/v1/migration/jobs/any/resume", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── DeleteJob ───────────────────────────────────────────────────────────────

func TestMigration_DeleteJob_OK(t *testing.T) {
	r, repo := mountMigration(t)
	require.NoError(t, repo.Create(testContext(), &domain.MigrationJob{ID: "d1", SourceURL: "s"}))
	rec := do(t, r, http.MethodDelete, "/api/v1/migration/jobs/d1", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	_, err := repo.Get(testContext(), "d1")
	assert.Error(t, err)
}

func TestMigration_DeleteJob_NotFound_404(t *testing.T) {
	r, _ := mountMigration(t)
	rec := do(t, r, http.MethodDelete, "/api/v1/migration/jobs/ghost", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMigration_DeleteJob_RepoError_500(t *testing.T) {
	r, repo := mountMigration(t)
	require.NoError(t, repo.Create(testContext(), &domain.MigrationJob{ID: "any", SourceURL: "s"}))
	repo.DeleteErr = errors.New("db down")
	rec := do(t, r, http.MethodDelete, "/api/v1/migration/jobs/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Preview ─────────────────────────────────────────────────────────────────

func TestMigration_Preview_OK(t *testing.T) {
	rt := stubRoundTrip(func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, "/service/rest/v1/repositories", req.URL.Path)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`[{"name":"raw-hosted","format":"raw","type":"hosted"}]`)),
		}, nil
	})
	r, repo := mountMigrationWith(t, rt)

	rec := do(t, r, http.MethodPost, "/api/v1/migration/preview", map[string]any{
		"sourceUrl": "https://nexus.example.com", "username": "admin", "password": "s3cret",
	})
	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, true, got["reachable"])
	assert.InDelta(t, 1, got["repoCount"], 0.001)

	jobs, err := repo.List(testContext())
	require.NoError(t, err)
	assert.Empty(t, jobs, "a preview creates no job")
}

func TestMigration_Preview_MissingURL_422(t *testing.T) {
	r, _ := mountMigration(t)
	rec := do(t, r, http.MethodPost, "/api/v1/migration/preview", map[string]any{
		"username": "admin",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, false, got["reachable"])
}

func TestMigration_Preview_Unreachable_502(t *testing.T) {
	rt := stubRoundTrip(func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused")
	})
	r, _ := mountMigrationWith(t, rt)

	rec := do(t, r, http.MethodPost, "/api/v1/migration/preview", map[string]any{
		"sourceUrl": "https://this-nexus-does-not-exist.invalid",
	})
	require.Equal(t, http.StatusBadGateway, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, false, got["reachable"])
	assert.Contains(t, got["error"], "connection refused")
}

func TestMigration_Preview_BadCredentials_502(t *testing.T) {
	rt := stubRoundTrip(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	})
	r, _ := mountMigrationWith(t, rt)

	rec := do(t, r, http.MethodPost, "/api/v1/migration/preview", map[string]any{
		"sourceUrl": "https://nexus.example.com", "username": "admin", "password": "wrong",
	})
	require.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "401")
}

func TestMigration_Preview_BadJSON_400(t *testing.T) {
	r, _ := mountMigration(t)
	rec := doRaw(t, r, http.MethodPost, "/api/v1/migration/preview", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
