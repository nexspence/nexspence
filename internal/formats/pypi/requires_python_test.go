package pypi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Requires-Python on a hosted repository (#665): twine, uv and poetry send it
// as the requires_python form field; the simple page carries it as PEP 503
// data-requires-python so pip skips releases its interpreter cannot run.

func uploadWithRequires(r *gin.Engine, repoName, pkgName, version, filename, requiresPython string) *httptest.ResponseRecorder {
	body, ct := buildUpload(pkgName, version, filename, "bytes of "+filename, "requires_python", requiresPython)
	req := httptest.NewRequest(http.MethodPost, "/repository/"+repoName+"/", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func simplePage(t *testing.T, r *gin.Engine, repoName, pkgName string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/repository/"+repoName+"/simple/"+pkgName+"/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	return w.Body.String()
}

// anchorFor returns the opening <a ...> tag of the link whose text is filename.
func anchorFor(t *testing.T, page, filename string) string {
	t.Helper()
	end := strings.Index(page, ">"+filename+"</a>")
	require.GreaterOrEqual(t, end, 0, "no link for %s in:\n%s", filename, page)
	start := strings.LastIndex(page[:end], "<a ")
	require.GreaterOrEqual(t, start, 0)
	return page[start : end+1]
}

func TestPyPI_RequiresPython_RecordedAndEscaped(t *testing.T) {
	r := setup(testutil.SimpleRepo("pypi-rp", "pypi"))
	whl := "acme_lib-1.0.0-py3-none-any.whl"
	require.Equal(t, http.StatusOK, uploadWithRequires(r, "pypi-rp", "acme-lib", "1.0.0", whl, ">=3.10, <4").Code)

	a := anchorFor(t, simplePage(t, r, "pypi-rp", "acme-lib"), whl)
	assert.Contains(t, a, `data-requires-python="&gt;=3.10, &lt;4"`)
}

func TestPyPI_RequiresPython_OmittedWhenNotSent(t *testing.T) {
	r := setup(testutil.SimpleRepo("pypi-rp", "pypi"))
	whl := "acme_lib-1.0.0-py3-none-any.whl"
	require.Equal(t, http.StatusOK, upload(r, "pypi-rp", "acme-lib", "1.0.0", whl, "wheel"))

	a := anchorFor(t, simplePage(t, r, "pypi-rp", "acme-lib"), whl)
	assert.NotContains(t, a, "data-requires-python",
		"an empty attribute says nothing; PEP 503 leaves it out")
}

// Every file of a version shares one component, and storing a file resets the
// component's extra; the specifier must survive the second upload.
func TestPyPI_RequiresPython_SharedByAllFilesOfARelease(t *testing.T) {
	r := setup(testutil.SimpleRepo("pypi-rp", "pypi"))
	whl := "acme_lib-1.0.0-py3-none-any.whl"
	sdist := "acme_lib-1.0.0.tar.gz"
	require.Equal(t, http.StatusOK, uploadWithRequires(r, "pypi-rp", "acme-lib", "1.0.0", whl, ">=3.10").Code)
	require.Equal(t, http.StatusOK, uploadWithRequires(r, "pypi-rp", "acme-lib", "1.0.0", sdist, ">=3.10").Code)
	require.Equal(t, http.StatusOK, uploadWithRequires(r, "pypi-rp", "acme-lib", "0.9.0", "acme_lib-0.9.0.tar.gz", ">=3.8").Code)

	page := simplePage(t, r, "pypi-rp", "acme-lib")
	assert.Contains(t, anchorFor(t, page, whl), `data-requires-python="&gt;=3.10"`)
	assert.Contains(t, anchorFor(t, page, sdist), `data-requires-python="&gt;=3.10"`)
	assert.Contains(t, anchorFor(t, page, "acme_lib-0.9.0.tar.gz"), `data-requires-python="&gt;=3.8"`,
		"each release keeps its own specifier")
}

// A file uploaded without the field (an older client, curl) keeps the
// release's specifier for every file of that release.
func TestPyPI_RequiresPython_SurvivesUploadWithoutSpecifier(t *testing.T) {
	r := setup(testutil.SimpleRepo("pypi-rp", "pypi"))
	whl := "acme_lib-1.0.0-py3-none-any.whl"
	sdist := "acme_lib-1.0.0.tar.gz"
	require.Equal(t, http.StatusOK, uploadWithRequires(r, "pypi-rp", "acme-lib", "1.0.0", whl, ">=3.10").Code)
	require.Equal(t, http.StatusOK, upload(r, "pypi-rp", "acme-lib", "1.0.0", sdist, "sdist"))

	page := simplePage(t, r, "pypi-rp", "acme-lib")
	assert.Contains(t, anchorFor(t, page, whl), `data-requires-python="&gt;=3.10"`)
	assert.Contains(t, anchorFor(t, page, sdist), `data-requires-python="&gt;=3.10"`)
}

// Files of one release that disagree: the last specifier sent applies to all.
func TestPyPI_RequiresPython_LastSentWins(t *testing.T) {
	r := setup(testutil.SimpleRepo("pypi-rp", "pypi"))
	whl := "acme_lib-1.0.0-py3-none-any.whl"
	sdist := "acme_lib-1.0.0.tar.gz"
	require.Equal(t, http.StatusOK, uploadWithRequires(r, "pypi-rp", "acme-lib", "1.0.0", whl, ">=3.10").Code)
	require.Equal(t, http.StatusOK, uploadWithRequires(r, "pypi-rp", "acme-lib", "1.0.0", sdist, ">=3.11").Code)

	page := simplePage(t, r, "pypi-rp", "acme-lib")
	assert.Contains(t, anchorFor(t, page, whl), `data-requires-python="&gt;=3.11"`)
	assert.Contains(t, anchorFor(t, page, sdist), `data-requires-python="&gt;=3.11"`)
}

func TestPyPI_RequiresPython_CannotBreakOutOfTheAttribute(t *testing.T) {
	r := setup(testutil.SimpleRepo("pypi-rp", "pypi"))
	whl := "acme_lib-1.0.0-py3-none-any.whl"
	require.Equal(t, http.StatusOK,
		uploadWithRequires(r, "pypi-rp", "acme-lib", "1.0.0", whl, `>=3" onclick="x`).Code)

	page := simplePage(t, r, "pypi-rp", "acme-lib")
	assert.NotContains(t, page, `onclick="x"`)
	assert.Contains(t, anchorFor(t, page, whl), `data-requires-python="&gt;=3&#34; onclick=&#34;x"`)
}

func TestPyPI_RequiresPython_InvalidValueRejected(t *testing.T) {
	for name, v := range map[string]string{
		"control character": ">=3.10\n<a href=x>",
		"too long":          ">=" + strings.Repeat("3", 2000),
	} {
		t.Run(name, func(t *testing.T) {
			r := setup(testutil.SimpleRepo("pypi-rp", "pypi"))
			w := uploadWithRequires(r, "pypi-rp", "acme-lib", "1.0.0", "acme_lib-1.0.0-py3-none-any.whl", v)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), "requires_python")

			// Refused before anything was stored.
			req := httptest.NewRequest(http.MethodGet,
				"/repository/pypi-rp/packages/acme-lib/acme_lib-1.0.0-py3-none-any.whl", nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

func TestPyPI_RequiresPython_RecordFailureIs500(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-rp", "pypi")
	r, comps := setupWithComponents(repo)
	comps.UpdateExtraErr = assert.AnError
	w := uploadWithRequires(r, "pypi-rp", "acme-lib", "1.0.0", "acme_lib-1.0.0-py3-none-any.whl", ">=3.10")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
