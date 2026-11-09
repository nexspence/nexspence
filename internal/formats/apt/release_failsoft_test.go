package apt_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/apt"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// setupWithAssets wires the handler around a caller-owned asset repository, so
// a test can make one of its calls fail.
func setupWithAssets(repo *domain.Repository, assets *testutil.AssetRepo) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     assets,
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := apt.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// The Release is the document every apt client fetches first. A failed
// lookup of the last deletion must not turn it into a 500: before the suites
// landed, a failing lookup fell back to the current time and the document
// was still served.
//
// The fallback has to move the date forward rather than freeze it: a date
// that is older than the one a client cached makes apt keep that Release —
// still listing a package that was deleted (#728).
func TestApt_Release_SurvivesFailedDeletionLookup(t *testing.T) {
	repo := testutil.SimpleRepo("debs-failsoft", "apt")
	assets := testutil.NewAssetRepo()
	r := setupWithAssets(repo, assets)

	require.Equal(t, http.StatusCreated,
		putDeb(r, "debs-failsoft", "/pool/main/curl_8.0.0_amd64.deb", "curl-bytes"))

	// The lookup the Release depends on now fails.
	assets.LastDeletedAtErr = errors.New("deletion lookup unavailable")

	req := httptest.NewRequest(http.MethodGet, "/repository/debs-failsoft/dists/focal/Release", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"a failed deletion lookup must not take the Release down")
	body := w.Body.String()
	assert.Contains(t, body, "Date: ", "the document must still carry a date")
	assert.Contains(t, body, strings.TrimSpace("SHA256:"), "the document must still carry its checksums")
}
