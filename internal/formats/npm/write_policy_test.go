package npm_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func npmPublish(r http.Handler, repo, pkg, version, content string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repo+"/"+pkg,
		strings.NewReader(publishBody(pkg, version, content)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// npm reads the "error" field of a failed publish; the redeploy refusal has
// to arrive there, as a 400, not as a 500 the client reports as a server fault.
func TestNPM_AllowOnce_RepublishSameVersionIs400(t *testing.T) {
	repo := testutil.SimpleRepo("npm-once", "npm")
	repo.FormatConfig = map[string]any{domain.WritePolicyKey: "allow_once"}
	r := setup(repo)

	require.Equal(t, http.StatusCreated, npmPublish(r, "npm-once", "mylib", "1.0.0", "v1").Code)

	w := npmPublish(r, "npm-once", "mylib", "1.0.0", "v1-again")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "Repository does not allow updating assets: npm-once", body["error"])

	// The tarball is still the first publish's.
	req := httptest.NewRequest(http.MethodGet, "/repository/npm-once/mylib/-/mylib-1.0.0.tgz", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req)
	assert.Equal(t, "v1", w2.Body.String())

	// A new version is a first deploy.
	assert.Equal(t, http.StatusCreated, npmPublish(r, "npm-once", "mylib", "1.0.1", "v2").Code)
}

func TestNPM_Deny_PublishIs400(t *testing.T) {
	repo := testutil.SimpleRepo("npm-ro", "npm")
	repo.FormatConfig = map[string]any{domain.WritePolicyKey: "deny"}
	r := setup(repo)

	w := npmPublish(r, "npm-ro", "mylib", "1.0.0", "v1")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Repository is read-only: npm-ro")
}
