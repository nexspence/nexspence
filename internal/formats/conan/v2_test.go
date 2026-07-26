package conan_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func v2Put(r http.Handler, url, content string) int {
	req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(content))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = int64(len(content))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func v2Get(r http.Handler, url string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestConan_PingAdvertisesRevisions(t *testing.T) {
	// Conan 2.x refuses to talk to servers that do not advertise the
	// "revisions" capability (#95).
	repo := testutil.SimpleRepo("cv2-ping", "conan")
	r := setup(repo)

	w := v2Get(r, "/repository/cv2-ping/v1/ping")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("X-Conan-Server-Capabilities"), "revisions")
}

func TestConan_V2_UploadAndDownload_RecipeFile(t *testing.T) {
	// Regression for #95: Conan 2.x uploads via the v2 revisions API fell
	// through to the default 405 branch and were silently dropped.
	repo := testutil.SimpleRepo("cv2-recipe", "conan")
	r := setup(repo)
	base := "/repository/cv2-recipe/v2/conans/boost/1.83.0/_/_/revisions/abc123/files"

	require.Equal(t, http.StatusCreated, v2Put(r, base+"/conanfile.py", "recipe-src"))

	w := v2Get(r, base+"/conanfile.py")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "recipe-src", w.Body.String())
}

func TestConan_V2_UploadAndDownload_PackageFile(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-pkg", "conan")
	r := setup(repo)
	base := "/repository/cv2-pkg/v2/conans/boost/1.83.0/_/_/revisions/abc123/packages/pkgid1/revisions/prev1/files"

	require.Equal(t, http.StatusCreated, v2Put(r, base+"/conan_package.tgz", "pkg-bytes"))

	w := v2Get(r, base+"/conan_package.tgz")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "pkg-bytes", w.Body.String())
}

func TestConan_V2_ListFiles(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-list", "conan")
	r := setup(repo)
	base := "/repository/cv2-list/v2/conans/boost/1.83.0/_/_/revisions/abc123/files"

	require.Equal(t, http.StatusCreated, v2Put(r, base+"/conanfile.py", "a"))
	require.Equal(t, http.StatusCreated, v2Put(r, base+"/conanmanifest.txt", "b"))

	w := v2Get(r, base)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "conanfile.py")
	assert.Contains(t, w.Body.String(), "conanmanifest.txt")
}

func TestConan_V2_LatestRevision(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-latest", "conan")
	r := setup(repo)
	files := "/repository/cv2-latest/v2/conans/boost/1.83.0/_/_/revisions/abc123/files"

	require.Equal(t, http.StatusCreated, v2Put(r, files+"/conanfile.py", "x"))

	w := v2Get(r, "/repository/cv2-latest/v2/conans/boost/1.83.0/_/_/latest")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "abc123")
	assert.Contains(t, w.Body.String(), "time")
}

func TestConan_V2_ListRevisions(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-revs", "conan")
	r := setup(repo)
	files := "/repository/cv2-revs/v2/conans/boost/1.83.0/_/_/revisions/abc123/files"

	require.Equal(t, http.StatusCreated, v2Put(r, files+"/conanfile.py", "x"))

	w := v2Get(r, "/repository/cv2-revs/v2/conans/boost/1.83.0/_/_/revisions")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "abc123")
}

func TestConan_V2_Latest_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-404", "conan")
	r := setup(repo)

	w := v2Get(r, "/repository/cv2-404/v2/conans/nope/1.0/_/_/latest")
	assert.Equal(t, http.StatusNotFound, w.Code)
}
