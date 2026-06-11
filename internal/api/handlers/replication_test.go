package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// mountReplication wires the real ReplicationService (over mocks) onto a gin engine,
// mirroring router.go. The cron scheduler is nil, so the go-routine ReloadRule calls
// spawned by Create/Update are harmless no-ops.
func mountReplication(t *testing.T) *gin.Engine {
	t.Helper()
	repRepo := testutil.NewReplicationRepo()
	svc := service.NewReplicationService(repRepo, testutil.NewAssetRepo(), testutil.NewBlobStore(), "test-secret", nil, cleanupNopLog())
	h := handlers.NewReplicationHandler(svc)

	r := gin.New()
	r.GET("/api/v1/replication/rules", h.List)
	r.POST("/api/v1/replication/rules", h.Create)
	r.PUT("/api/v1/replication/rules/:id", h.Update)
	r.DELETE("/api/v1/replication/rules/:id", h.Delete)
	r.POST("/api/v1/replication/rules/:id/run", h.ManualRun)
	r.POST("/api/v1/replication/rules/:id/test", h.TestConnection)
	r.GET("/api/v1/replication/rules/:id/history", h.ListHistory)
	return r
}

// replicationCreate posts a valid rule and returns its server-assigned ID.
func replicationCreate(t *testing.T, r *gin.Engine, name string) string {
	t.Helper()
	rec := do(t, r, http.MethodPost, "/api/v1/replication/rules", map[string]any{
		"name":        name,
		"source_repo": "src",
		"target_url":  "http://127.0.0.1:1/",
		"target_repo": "dst",
	})
	require.Equal(t, http.StatusCreated, rec.Code, "body=%s", rec.Body.String())
	var rule domain.ReplicationRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rule))
	require.NotEmpty(t, rule.ID)
	return rule.ID
}

// ── List ────────────────────────────────────────────────────────────────────

func TestReplicationHandler_List_Empty(t *testing.T) {
	r := mountReplication(t)
	rec := do(t, r, http.MethodGet, "/api/v1/replication/rules", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []domain.ReplicationRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got)
}

// ── Create ──────────────────────────────────────────────────────────────────

func TestReplicationHandler_Create_OK(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "rule-a")
	assert.NotEmpty(t, id)
}

func TestReplicationHandler_Create_DefaultsCron(t *testing.T) {
	r := mountReplication(t)
	rec := do(t, r, http.MethodPost, "/api/v1/replication/rules", map[string]any{
		"name":        "rule-defaults",
		"source_repo": "src",
		"target_url":  "http://127.0.0.1:1/",
		"target_repo": "dst",
	})
	require.Equal(t, http.StatusCreated, rec.Code)
	var rule domain.ReplicationRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rule))
	assert.Equal(t, "0 2 * * *", rule.CronExpr) // defaulted when blank
}

func TestReplicationHandler_Create_BadJSON_400(t *testing.T) {
	r := mountReplication(t)
	rec := doRaw(t, r, http.MethodPost, "/api/v1/replication/rules", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplicationHandler_Create_MissingFields_400(t *testing.T) {
	r := mountReplication(t)
	// each row drops exactly one required field
	cases := []map[string]any{
		{"source_repo": "src", "target_url": "http://x/", "target_repo": "dst"}, // no name
		{"name": "n", "target_url": "http://x/", "target_repo": "dst"},          // no source_repo
		{"name": "n", "source_repo": "src", "target_repo": "dst"},               // no target_url
		{"name": "n", "source_repo": "src", "target_url": "http://x/"},          // no target_repo
	}
	for i, body := range cases {
		rec := do(t, r, http.MethodPost, "/api/v1/replication/rules", body)
		assert.Equalf(t, http.StatusBadRequest, rec.Code, "case %d body=%s", i, rec.Body.String())
	}
}

// ── Update ──────────────────────────────────────────────────────────────────

func TestReplicationHandler_Update_OK(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "before")
	rec := do(t, r, http.MethodPut, "/api/v1/replication/rules/"+id, map[string]any{
		"name":        "after",
		"source_repo": "src",
		"target_url":  "http://127.0.0.1:1/",
		"target_repo": "dst",
		"cron_expr":   "0 5 * * *",
	})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var rule domain.ReplicationRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rule))
	assert.Equal(t, id, rule.ID)
	assert.Equal(t, "after", rule.Name)
}

func TestReplicationHandler_Update_BadJSON_400(t *testing.T) {
	r := mountReplication(t)
	rec := doRaw(t, r, http.MethodPut, "/api/v1/replication/rules/any", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplicationHandler_Update_UnknownID_400(t *testing.T) {
	r := mountReplication(t)
	// mock UpdateRule returns an error for an unknown id → handler maps to 400.
	rec := do(t, r, http.MethodPut, "/api/v1/replication/rules/ghost", map[string]any{
		"name":        "x",
		"source_repo": "src",
		"target_url":  "http://127.0.0.1:1/",
		"target_repo": "dst",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── Delete ──────────────────────────────────────────────────────────────────

func TestReplicationHandler_Delete_204(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "to-delete")
	rec := do(t, r, http.MethodDelete, "/api/v1/replication/rules/"+id, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

// ── ManualRun ─────────────────────────────────────────────────────────────────

func TestReplicationHandler_ManualRun_NotFound_404(t *testing.T) {
	r := mountReplication(t)
	rec := do(t, r, http.MethodPost, "/api/v1/replication/rules/ghost/run", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestReplicationHandler_ManualRun_202(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "run-me")
	rec := do(t, r, http.MethodPost, "/api/v1/replication/rules/"+id+"/run", nil)
	require.Equal(t, http.StatusAccepted, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "replication started", body["message"])
}

// ── TestConnection ────────────────────────────────────────────────────────────

func TestReplicationHandler_TestConnection_NotFound_404(t *testing.T) {
	r := mountReplication(t)
	rec := do(t, r, http.MethodPost, "/api/v1/replication/rules/ghost/test", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestReplicationHandler_TestConnection_Unreachable_502(t *testing.T) {
	r := mountReplication(t)
	// target_url points at a closed port so client.Do fails fast → 502.
	id := replicationCreate(t, r, "unreachable")
	rec := do(t, r, http.MethodPost, "/api/v1/replication/rules/"+id+"/test", nil)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

// ── ListHistory ───────────────────────────────────────────────────────────────

func TestReplicationHandler_ListHistory_OK(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "with-history")
	rec := do(t, r, http.MethodGet, "/api/v1/replication/rules/"+id+"/history", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var hist []domain.ReplicationHistory
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &hist))
	assert.Empty(t, hist)
}

func TestReplicationHandler_ListHistory_WithLimit_OK(t *testing.T) {
	r := mountReplication(t)
	id := replicationCreate(t, r, "limit-history")
	rec := do(t, r, http.MethodGet, "/api/v1/replication/rules/"+id+"/history?limit=5", nil)
	require.Equal(t, http.StatusOK, rec.Code)
}
