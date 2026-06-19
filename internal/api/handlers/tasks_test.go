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

// fakeTaskCleanup implements handlers.taskCleanup (via the exported NewTasksHandler signature).
type fakeTaskCleanup struct {
	policies []domain.CleanupPolicy
	listErr  error
	runErr   error
	ranID    string
	ranCount int
}

func (f *fakeTaskCleanup) List(_ context.Context) ([]domain.CleanupPolicy, error) {
	return f.policies, f.listErr
}

func (f *fakeTaskCleanup) RunPolicy(_ context.Context, id string) error {
	f.ranID = id
	f.ranCount++
	return f.runErr
}

// fakeTaskReplication implements handlers.taskReplication.
type fakeTaskReplication struct {
	rules    []domain.ReplicationRule
	listErr  error
	runErr   error
	ranID    string
	ranCount int
}

func (f *fakeTaskReplication) ListRules(_ context.Context) ([]domain.ReplicationRule, error) {
	return f.rules, f.listErr
}

func (f *fakeTaskReplication) RunRule(_ context.Context, id string) error {
	f.ranID = id
	f.ranCount++
	return f.runErr
}

func mountTasks(t *testing.T, cl *fakeTaskCleanup, rp *fakeTaskReplication) *gin.Engine {
	t.Helper()
	h := handlers.NewTasksHandler(cl, rp)
	r := gin.New()
	r.GET("/service/rest/v1/tasks", h.List)
	r.POST("/service/rest/v1/tasks/:id/run", h.Run)
	return r
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestTasksHandler_List_Merges(t *testing.T) {
	cleanupRun := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	replRun := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	cl := &fakeTaskCleanup{policies: []domain.CleanupPolicy{
		{ID: "c1", Name: "cleanup-one", LastRunAt: &cleanupRun},
	}}
	rp := &fakeTaskReplication{rules: []domain.ReplicationRule{
		{ID: "r1", Name: "repl-one", LastRunAt: &replRun, LastRunStatus: "ok"},
	}}
	r := mountTasks(t, cl, rp)

	rec := do(t, r, http.MethodGet, "/service/rest/v1/tasks", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Items             []map[string]any `json:"items"`
		ContinuationToken any              `json:"continuationToken"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Nil(t, body.ContinuationToken)
	require.Len(t, body.Items, 2)

	// cleanup first
	c := body.Items[0]
	assert.Equal(t, "cleanup:c1", c["id"])
	assert.Equal(t, "cleanup-one", c["name"])
	assert.Equal(t, "repository.cleanup", c["type"])
	assert.Equal(t, "WAITING", c["currentState"])
	assert.Equal(t, "OK", c["lastRunResult"])
	assert.Equal(t, cleanupRun.Format(time.RFC3339), c["lastRun"])
	assert.Nil(t, c["nextRun"])

	// replication second
	rr := body.Items[1]
	assert.Equal(t, "replication:r1", rr["id"])
	assert.Equal(t, "repl-one", rr["name"])
	assert.Equal(t, "replication", rr["type"])
	assert.Equal(t, "WAITING", rr["currentState"])
	assert.Equal(t, "OK", rr["lastRunResult"])
	assert.Equal(t, replRun.Format(time.RFC3339), rr["lastRun"])
}

func TestTasksHandler_List_ReplicationRunningAndStatusMapping(t *testing.T) {
	rp := &fakeTaskReplication{rules: []domain.ReplicationRule{
		{ID: "run", Name: "running", LastRunStatus: "running"},
		{ID: "err", Name: "errored", LastRunStatus: "error"},
		{ID: "non", Name: "never", LastRunStatus: ""},
	}}
	r := mountTasks(t, &fakeTaskCleanup{}, rp)

	rec := do(t, r, http.MethodGet, "/service/rest/v1/tasks", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Items, 3)

	assert.Equal(t, "RUNNING", body.Items[0]["currentState"])
	assert.Equal(t, "", body.Items[0]["lastRunResult"]) // no LastRunAt
	assert.Nil(t, body.Items[0]["lastRun"])

	assert.Equal(t, "WAITING", body.Items[1]["currentState"])
	assert.Equal(t, "ERROR", body.Items[1]["lastRunResult"])

	assert.Equal(t, "WAITING", body.Items[2]["currentState"])
	assert.Equal(t, "", body.Items[2]["lastRunResult"])
}

func TestTasksHandler_List_CleanupNeverRun_EmptyResult(t *testing.T) {
	cl := &fakeTaskCleanup{policies: []domain.CleanupPolicy{
		{ID: "c1", Name: "never-run"}, // LastRunAt nil
	}}
	r := mountTasks(t, cl, &fakeTaskReplication{})
	rec := do(t, r, http.MethodGet, "/service/rest/v1/tasks", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)
	assert.Equal(t, "", body.Items[0]["lastRunResult"])
	assert.Nil(t, body.Items[0]["lastRun"])
}

func TestTasksHandler_List_Empty(t *testing.T) {
	r := mountTasks(t, &fakeTaskCleanup{}, &fakeTaskReplication{})
	rec := do(t, r, http.MethodGet, "/service/rest/v1/tasks", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	// items must be [] (empty array), not null.
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	assert.Equal(t, "[]", string(raw["items"]))
	assert.Equal(t, "null", string(raw["continuationToken"]))

	var body struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Len(t, body.Items, 0)
}

func TestTasksHandler_List_CleanupError_500(t *testing.T) {
	cl := &fakeTaskCleanup{listErr: errors.New("db down")}
	r := mountTasks(t, cl, &fakeTaskReplication{})
	rec := do(t, r, http.MethodGet, "/service/rest/v1/tasks", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestTasksHandler_List_ReplicationError_500(t *testing.T) {
	rp := &fakeTaskReplication{listErr: errors.New("db down")}
	r := mountTasks(t, &fakeTaskCleanup{}, rp)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/tasks", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Run ───────────────────────────────────────────────────────────────────────

func TestTasksHandler_Run_Cleanup_204(t *testing.T) {
	cl := &fakeTaskCleanup{}
	r := mountTasks(t, cl, &fakeTaskReplication{})
	rec := do(t, r, http.MethodPost, "/service/rest/v1/tasks/cleanup:abc-123/run", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "abc-123", cl.ranID)
	assert.Equal(t, 1, cl.ranCount)
}

func TestTasksHandler_Run_Replication_204(t *testing.T) {
	rp := &fakeTaskReplication{}
	r := mountTasks(t, &fakeTaskCleanup{}, rp)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/tasks/replication:xyz-789/run", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "xyz-789", rp.ranID)
	assert.Equal(t, 1, rp.ranCount)
}

func TestTasksHandler_Run_UnknownPrefix_404(t *testing.T) {
	r := mountTasks(t, &fakeTaskCleanup{}, &fakeTaskReplication{})
	rec := do(t, r, http.MethodPost, "/service/rest/v1/tasks/bogus:id/run", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTasksHandler_Run_NoPrefix_404(t *testing.T) {
	r := mountTasks(t, &fakeTaskCleanup{}, &fakeTaskReplication{})
	rec := do(t, r, http.MethodPost, "/service/rest/v1/tasks/noprefix/run", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTasksHandler_Run_CleanupError_500(t *testing.T) {
	cl := &fakeTaskCleanup{runErr: errors.New("boom")}
	r := mountTasks(t, cl, &fakeTaskReplication{})
	rec := do(t, r, http.MethodPost, "/service/rest/v1/tasks/cleanup:id/run", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestTasksHandler_Run_ReplicationError_500(t *testing.T) {
	rp := &fakeTaskReplication{runErr: errors.New("boom")}
	r := mountTasks(t, &fakeTaskCleanup{}, rp)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/tasks/replication:id/run", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
