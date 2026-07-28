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

// publish PUTs a package version and asserts it was stored.
func publish(t *testing.T, r http.Handler, repoName, pkg, version string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+"/"+pkg,
		strings.NewReader(publishBody(pkg, version, "tgz-"+version)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
}

// getJSON performs a GET and decodes the JSON body.
func getJSON(t *testing.T, r http.Handler, url string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func status(t *testing.T, r http.Handler, method, url string, body string) int {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, url, nil)
	} else {
		req = httptest.NewRequest(method, url, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// ─── dist-tags (#101) ───────────────────────────────────────────

// `npm dist-tag add pkg@1.0.0 beta` PUTs a bare JSON string to
// /-/package/:pkg/dist-tags/:tag. That used to hit the publish handler and 400.
func TestNPM_DistTagAdd_SetsTag(t *testing.T) {
	repo := testutil.SimpleRepo("npm-dt", "npm")
	r := setup(repo)
	publish(t, r, "npm-dt", "mylib", "1.0.0")
	publish(t, r, "npm-dt", "mylib", "2.0.0")

	code := status(t, r, http.MethodPut, "/repository/npm-dt/-/package/mylib/dist-tags/beta", `"1.0.0"`)
	require.Equal(t, http.StatusCreated, code)

	code, tags := getJSON(t, r, "/repository/npm-dt/-/package/mylib/dist-tags")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "1.0.0", tags["beta"])
	assert.Equal(t, "2.0.0", tags["latest"], "latest is the highest published version")

	// The packument must advertise the same tags.
	code, meta := getJSON(t, r, "/repository/npm-dt/mylib")
	require.Equal(t, http.StatusOK, code)
	dt, _ := meta["dist-tags"].(map[string]any)
	require.NotNil(t, dt)
	assert.Equal(t, "1.0.0", dt["beta"])
	assert.Equal(t, "2.0.0", dt["latest"])
}

// Moving a tag to another version must not leave it pointing at both.
func TestNPM_DistTagAdd_MovesTagBetweenVersions(t *testing.T) {
	repo := testutil.SimpleRepo("npm-dt-move", "npm")
	r := setup(repo)
	publish(t, r, "npm-dt-move", "mylib", "1.0.0")
	publish(t, r, "npm-dt-move", "mylib", "2.0.0")

	require.Equal(t, http.StatusCreated,
		status(t, r, http.MethodPut, "/repository/npm-dt-move/-/package/mylib/dist-tags/beta", `"1.0.0"`))
	require.Equal(t, http.StatusCreated,
		status(t, r, http.MethodPut, "/repository/npm-dt-move/-/package/mylib/dist-tags/beta", `"2.0.0"`))

	_, tags := getJSON(t, r, "/repository/npm-dt-move/-/package/mylib/dist-tags")
	assert.Equal(t, "2.0.0", tags["beta"])
}

func TestNPM_DistTagAdd_UnknownVersion_404(t *testing.T) {
	repo := testutil.SimpleRepo("npm-dt-404", "npm")
	r := setup(repo)
	publish(t, r, "npm-dt-404", "mylib", "1.0.0")

	assert.Equal(t, http.StatusNotFound,
		status(t, r, http.MethodPut, "/repository/npm-dt-404/-/package/mylib/dist-tags/beta", `"9.9.9"`))
}

func TestNPM_DistTagRm_RemovesTag(t *testing.T) {
	repo := testutil.SimpleRepo("npm-dt-rm", "npm")
	r := setup(repo)
	publish(t, r, "npm-dt-rm", "mylib", "1.0.0")
	require.Equal(t, http.StatusCreated,
		status(t, r, http.MethodPut, "/repository/npm-dt-rm/-/package/mylib/dist-tags/beta", `"1.0.0"`))

	require.Equal(t, http.StatusOK,
		status(t, r, http.MethodDelete, "/repository/npm-dt-rm/-/package/mylib/dist-tags/beta", ""))

	_, tags := getJSON(t, r, "/repository/npm-dt-rm/-/package/mylib/dist-tags")
	_, ok := tags["beta"]
	assert.False(t, ok, "tag should be gone, got %v", tags)

	// Removing it again reports that there is nothing to remove.
	assert.Equal(t, http.StatusNotFound,
		status(t, r, http.MethodDelete, "/repository/npm-dt-rm/-/package/mylib/dist-tags/beta", ""))
}

func TestNPM_DistTags_ScopedPackage(t *testing.T) {
	repo := testutil.SimpleRepo("npm-dt-scope", "npm")
	r := setup(repo)
	publish(t, r, "npm-dt-scope", "@acme/lib", "1.0.0")

	require.Equal(t, http.StatusCreated,
		status(t, r, http.MethodPut, "/repository/npm-dt-scope/-/package/@acme/lib/dist-tags/next", `"1.0.0"`))

	_, tags := getJSON(t, r, "/repository/npm-dt-scope/-/package/@acme/lib/dist-tags")
	assert.Equal(t, "1.0.0", tags["next"])
}

func TestNPM_DistTags_ProxyRejectsMutation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	r, _ := proxySetup(upstream)
	assert.Equal(t, http.StatusMethodNotAllowed,
		status(t, r, http.MethodPut, "/repository/npm-proxy/-/package/pkg/dist-tags/beta", `"1.0.0"`))
}

// ─── unpublish (#101) ───────────────────────────────────────────

// `npm unpublish pkg@1.0.0` finishes by DELETEing the tarball with a -rev
// suffix. That used to return 200 while deleting nothing.
func TestNPM_Unpublish_Version_DeletesTarball(t *testing.T) {
	repo := testutil.SimpleRepo("npm-unp-v", "npm")
	r := setup(repo)
	publish(t, r, "npm-unp-v", "mylib", "1.0.0")
	publish(t, r, "npm-unp-v", "mylib", "2.0.0")

	require.Equal(t, http.StatusOK,
		status(t, r, http.MethodDelete, "/repository/npm-unp-v/mylib/-/mylib-1.0.0.tgz/-rev/3-abc", ""))

	assert.Equal(t, http.StatusNotFound,
		status(t, r, http.MethodGet, "/repository/npm-unp-v/mylib/-/mylib-1.0.0.tgz", ""))
	assert.Equal(t, http.StatusOK,
		status(t, r, http.MethodGet, "/repository/npm-unp-v/mylib/-/mylib-2.0.0.tgz", ""),
		"the other version must survive")
}

// `npm unpublish pkg --force` DELETEs the whole package by revision.
func TestNPM_Unpublish_Package_DeletesEveryVersion(t *testing.T) {
	repo := testutil.SimpleRepo("npm-unp-p", "npm")
	r := setup(repo)
	publish(t, r, "npm-unp-p", "mylib", "1.0.0")
	publish(t, r, "npm-unp-p", "mylib", "2.0.0")

	require.Equal(t, http.StatusOK,
		status(t, r, http.MethodDelete, "/repository/npm-unp-p/mylib/-rev/3-abc", ""))

	assert.Equal(t, http.StatusNotFound,
		status(t, r, http.MethodGet, "/repository/npm-unp-p/mylib", ""))
	assert.Equal(t, http.StatusNotFound,
		status(t, r, http.MethodGet, "/repository/npm-unp-p/mylib/-/mylib-1.0.0.tgz", ""))
	assert.Equal(t, http.StatusNotFound,
		status(t, r, http.MethodGet, "/repository/npm-unp-p/mylib/-/mylib-2.0.0.tgz", ""))
}

// Deleting what is not there must say so instead of reporting success.
func TestNPM_Unpublish_Unknown_404(t *testing.T) {
	repo := testutil.SimpleRepo("npm-unp-404", "npm")
	r := setup(repo)

	assert.Equal(t, http.StatusNotFound,
		status(t, r, http.MethodDelete, "/repository/npm-unp-404/ghost/-rev/1-abc", ""))
	assert.Equal(t, http.StatusNotFound,
		status(t, r, http.MethodDelete, "/repository/npm-unp-404/ghost/-/ghost-1.0.0.tgz", ""))
}

// `npm unpublish pkg@1.0.0` first PUTs the packument without that version.
// Versions missing from the body are unpublished.
func TestNPM_Unpublish_RevPut_DropsMissingVersions(t *testing.T) {
	repo := testutil.SimpleRepo("npm-unp-rev", "npm")
	r := setup(repo)
	publish(t, r, "npm-unp-rev", "mylib", "1.0.0")
	publish(t, r, "npm-unp-rev", "mylib", "2.0.0")

	body := `{"name":"mylib","versions":{"2.0.0":{"name":"mylib","version":"2.0.0"}}}`
	require.Equal(t, http.StatusOK,
		status(t, r, http.MethodPut, "/repository/npm-unp-rev/mylib/-rev/3-abc", body))

	code, meta := getJSON(t, r, "/repository/npm-unp-rev/mylib")
	require.Equal(t, http.StatusOK, code)
	versions, _ := meta["versions"].(map[string]any)
	require.NotNil(t, versions)
	_, has1 := versions["1.0.0"]
	_, has2 := versions["2.0.0"]
	assert.False(t, has1, "1.0.0 should be unpublished")
	assert.True(t, has2, "2.0.0 should remain")

	assert.Equal(t, http.StatusNotFound,
		status(t, r, http.MethodGet, "/repository/npm-unp-rev/mylib/-/mylib-1.0.0.tgz", ""))
}

// The component row must go with its assets, not linger as an empty package.
func TestNPM_Unpublish_Package_RemovesComponents(t *testing.T) {
	repo := testutil.SimpleRepo("npm-unp-comp", "npm")
	r, comps := setupWithComponents(repo)
	publish(t, r, "npm-unp-comp", "mylib", "1.0.0")

	require.Equal(t, http.StatusOK,
		status(t, r, http.MethodDelete, "/repository/npm-unp-comp/mylib/-rev/3-abc", ""))

	page, err := comps.Search(t.Context(), domain.SearchParams{Repository: "npm-unp-comp", Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, page.Items, "component rows should be gone")
}
