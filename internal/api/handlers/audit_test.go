package handlers_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func init() { gin.SetMode(gin.TestMode) }

func mountAudit(t *testing.T) (*gin.Engine, *testutil.AuditRepo) {
	t.Helper()
	repo := testutil.NewAuditRepo()
	h := handlers.NewAuditHandler(repo)
	r := gin.New()
	r.GET("/service/rest/v1/audit", h.List)
	return r, repo
}

func seed(t *testing.T, repo *testutil.AuditRepo, events []domain.AuditEvent) {
	t.Helper()
	for i := range events {
		require.NoError(t, repo.Write(context.Background(), &events[i]))
	}
}

func TestAuditList_FromTo_Filtering(t *testing.T) {
	r, repo := mountAudit(t)
	seed(t, repo, []domain.AuditEvent{
		{EventTime: time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC), Username: "a", Domain: "REPOSITORY", Action: "CREATE", Result: "success"},
		{EventTime: time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC), Username: "b", Domain: "REPOSITORY", Action: "DELETE", Result: "success"},
		{EventTime: time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC), Username: "c", Domain: "REPOSITORY", Action: "CREATE", Result: "success"},
	})

	req := httptest.NewRequest(http.MethodGet,
		"/service/rest/v1/audit?from=2026-04-05&to=2026-04-15", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Items []domain.AuditEvent `json:"items"`
		Total int                 `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 1, body.Total)
	require.Len(t, body.Items, 1)
	assert.Equal(t, "b", body.Items[0].Username)
}

func TestAuditList_BareTo_IsInclusiveOfThatDay(t *testing.T) {
	r, repo := mountAudit(t)
	seed(t, repo, []domain.AuditEvent{
		{EventTime: time.Date(2026, 4, 24, 0, 0, 1, 0, time.UTC), Username: "early", Domain: "X", Action: "CREATE", Result: "success"},
		{EventTime: time.Date(2026, 4, 24, 23, 59, 59, 0, time.UTC), Username: "late", Domain: "X", Action: "CREATE", Result: "success"},
		{EventTime: time.Date(2026, 4, 25, 0, 0, 1, 0, time.UTC), Username: "next", Domain: "X", Action: "CREATE", Result: "success"},
	})

	req := httptest.NewRequest(http.MethodGet,
		"/service/rest/v1/audit?to=2026-04-24", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var body struct {
		Items []domain.AuditEvent `json:"items"`
		Total int                 `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 2, body.Total, "to=2026-04-24 must include events anywhere on April 24")
	gotUsers := []string{body.Items[0].Username, body.Items[1].Username}
	assert.Contains(t, gotUsers, "early")
	assert.Contains(t, gotUsers, "late")
	assert.NotContains(t, gotUsers, "next")
}

func TestAuditList_RFC3339To_StaysExclusive(t *testing.T) {
	r, repo := mountAudit(t)
	seed(t, repo, []domain.AuditEvent{
		{EventTime: time.Date(2026, 4, 24, 11, 59, 59, 0, time.UTC), Username: "before", Domain: "X", Action: "CREATE", Result: "success"},
		{EventTime: time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC), Username: "at", Domain: "X", Action: "CREATE", Result: "success"},
	})

	req := httptest.NewRequest(http.MethodGet,
		"/service/rest/v1/audit?to=2026-04-24T12:00:00Z", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var body struct {
		Items []domain.AuditEvent `json:"items"`
		Total int                 `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 1, body.Total, "RFC3339 to= remains strictly exclusive")
	assert.Equal(t, "before", body.Items[0].Username)
}

func TestAuditList_UsernameFilter(t *testing.T) {
	r, repo := mountAudit(t)
	seed(t, repo, []domain.AuditEvent{
		{EventTime: time.Now(), Username: "alice", Domain: "X", Action: "CREATE", Result: "success"},
		{EventTime: time.Now(), Username: "bob", Domain: "X", Action: "CREATE", Result: "success"},
	})

	req := httptest.NewRequest(http.MethodGet,
		"/service/rest/v1/audit?username=alice", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var body struct {
		Items []domain.AuditEvent `json:"items"`
		Total int                 `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 1, body.Total)
	assert.Equal(t, "alice", body.Items[0].Username)
}

func TestAuditList_TotalReflectsAllMatches_NotPage(t *testing.T) {
	r, repo := mountAudit(t)
	var events []domain.AuditEvent
	for i := 0; i < 5; i++ {
		events = append(events, domain.AuditEvent{
			EventTime: time.Now(), Username: "u", Domain: "D", Action: "CREATE", Result: "success",
		})
	}
	seed(t, repo, events)

	req := httptest.NewRequest(http.MethodGet,
		"/service/rest/v1/audit?limit=2&offset=0", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var body struct {
		Items []domain.AuditEvent `json:"items"`
		Total int                 `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 5, body.Total, "total reflects all matches")
	assert.Len(t, body.Items, 2, "page is limited")
}

func TestAuditList_NDJSON_Export(t *testing.T) {
	r, repo := mountAudit(t)
	seed(t, repo, []domain.AuditEvent{
		{EventTime: time.Now(), Username: "a", Domain: "REPOSITORY", Action: "CREATE", Result: "success"},
		{EventTime: time.Now(), Username: "b", Domain: "REPOSITORY", Action: "DELETE", Result: "success"},
	})

	req := httptest.NewRequest(http.MethodGet,
		"/service/rest/v1/audit?format=ndjson", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "audit-")
	assert.Contains(t, rec.Header().Get("Content-Disposition"), ".ndjson")

	sc := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	n := 0
	for sc.Scan() {
		var e domain.AuditEvent
		require.NoError(t, json.Unmarshal(sc.Bytes(), &e), "each line must be JSON")
		n++
	}
	assert.Equal(t, 2, n)
}

func TestAuditList_BadFromValue_400(t *testing.T) {
	r, _ := mountAudit(t)

	req := httptest.NewRequest(http.MethodGet,
		"/service/rest/v1/audit?from=not-a-date", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
