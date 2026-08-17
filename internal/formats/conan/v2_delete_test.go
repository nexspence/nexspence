package conan_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func v2Delete(r http.Handler, url string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestConan_V2Delete_OneRevisionLeavesTheOther(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-del-rev", "conan")
	r := setup(repo)
	ref := "/repository/cv2-del-rev/v2/conans/boost/1.83.0/_/_"

	require.Equal(t, http.StatusCreated, v2Put(r, ref+"/revisions/r1/files/conanfile.py", "one"))
	require.Equal(t, http.StatusCreated, v2Put(r, ref+"/revisions/r1/packages/pkgA/revisions/p1/files/conan_package.tgz", "bin"))
	require.Equal(t, http.StatusCreated, v2Put(r, ref+"/revisions/r2/files/conanfile.py", "two"))

	assert.Equal(t, http.StatusOK, v2Delete(r, ref+"/revisions/r1").Code)

	// r1 is gone, binaries included; r2 survives.
	assert.Equal(t, http.StatusNotFound, v2Get(r, ref+"/revisions/r1/files").Code)
	assert.Equal(t, http.StatusNotFound, v2Get(r, ref+"/revisions/r1/packages/pkgA/revisions/p1/files/conan_package.tgz").Code)
	w := v2Get(r, ref+"/revisions")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "r2")
	assert.NotContains(t, w.Body.String(), "r1")

	// The client raises RecipeNotFound on 404 — a second delete is exactly
	// that, not a silent 200.
	assert.Equal(t, http.StatusNotFound, v2Delete(r, ref+"/revisions/r1").Code)
}

func TestConan_V2Delete_WholeReference(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-del-ref", "conan")
	r := setup(repo)
	ref := "/repository/cv2-del-ref/v2/conans/zlib/1.3/team/stable"

	require.Equal(t, http.StatusCreated, v2Put(r, ref+"/revisions/r1/files/conanfile.py", "one"))
	require.Equal(t, http.StatusCreated, v2Put(r, ref+"/revisions/r2/files/conanfile.py", "two"))

	assert.Equal(t, http.StatusOK, v2Delete(r, ref).Code)
	assert.Equal(t, http.StatusNotFound, v2Get(r, ref+"/revisions").Code)
	assert.Equal(t, http.StatusNotFound, v2Delete(r, ref).Code)

	// The deleted reference no longer surfaces in search.
	assert.Empty(t, searchResults(t, r, "/repository/cv2-del-ref/v2/conans/search"))
}

func TestConan_V2Delete_PackageGranularStays405(t *testing.T) {
	// Package-level deletion is out of #247's scope — refuse rather than
	// half-guess, exactly as before.
	repo := testutil.SimpleRepo("cv2-del-405", "conan")
	r := setup(repo)
	ref := "/repository/cv2-del-405/v2/conans/boost/1.83.0/_/_"
	require.Equal(t, http.StatusCreated, v2Put(r, ref+"/revisions/r1/packages/pkgA/revisions/p1/files/conaninfo.txt", "x"))

	assert.Equal(t, http.StatusMethodNotAllowed, v2Delete(r, ref+"/revisions/r1/packages/pkgA").Code)
	assert.Equal(t, http.StatusMethodNotAllowed, v2Delete(r, ref+"/revisions/r1/packages/pkgA/revisions/p1").Code)
}

func TestConan_V2Delete_ProxyRepoRefuses(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-del-proxy", "conan")
	repo.Type = domain.TypeProxy
	r := setup(repo)

	w := v2Delete(r, "/repository/cv2-del-proxy/v2/conans/boost/1.83.0/_/_")
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
