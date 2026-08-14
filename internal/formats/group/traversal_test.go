package group_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A routing rule is the only path-level policy a group has — RBAC grants a
// repository, not a path inside it. The rule is matched against the request
// path, so a path that reaches the artifact by another spelling must not reach
// it at all: otherwise the string that was checked is not the string that is
// served.
func TestGroupHandler_RoutingRule_TraversalDoesNotBypassBlock(t *testing.T) {
	rule := makeBlockRule("rule-traversal", `^/private/`)

	member := testutil.SimpleRepo("secrets", "raw")
	ruleID := "rule-traversal"
	grp := &domain.Repository{
		ID: "repo-grp-t", Name: "tgroup", Format: "raw",
		Type: domain.TypeGroup, Online: true,
		RoutingRuleID: &ruleID,
		FormatConfig:  map[string]any{"member_names": []any{"secrets"}},
	}

	r := buildEngineWithRule(rule, member, grp)
	require.Equal(t, http.StatusCreated, put(r, "secrets", "/private/secret.txt", "TOPSECRET"))

	// The blocked path itself, for the baseline the rule already delivered.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/repository/tgroup/private/secret.txt", nil))
	require.Equal(t, http.StatusNotFound, rec.Code, "baseline: the rule blocks its own path")

	for _, url := range []string{
		"/repository/tgroup/public/../private/secret.txt",
		"/repository/tgroup/%2e%2e/private/secret.txt",
		"/repository/tgroup/a/b/../../private/secret.txt",
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		assert.NotEqual(t, http.StatusOK, rec.Code, "%s must not reach the blocked artifact", url)
		assert.NotContains(t, rec.Body.String(), "TOPSECRET", "%s leaked the blocked content", url)
	}
}

// A traversal segment is refused outright rather than quietly rewritten, so no
// legitimate path changes meaning.
func TestGroupHandler_TraversalSegmentRejected(t *testing.T) {
	member := testutil.SimpleRepo("plain", "raw")
	grp := &domain.Repository{
		ID: "repo-grp-p", Name: "pgroup", Format: "raw",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []any{"plain"}},
	}

	r := buildEngineWithRule(nil, member, grp)
	require.Equal(t, http.StatusCreated, put(r, "plain", "/ok/file.txt", "data"))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/repository/pgroup/x/../ok/file.txt", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// A path that merely contains ".." inside a name is not a traversal.
	require.Equal(t, http.StatusCreated, put(r, "plain", "/ok/we..ird.txt", "fine"))
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/repository/pgroup/ok/we..ird.txt", nil))
	assert.Equal(t, http.StatusOK, rec2.Code)
	assert.Equal(t, "fine", rec2.Body.String())
}
