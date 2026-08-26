package oci_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// An in-flight upload session must be visible to quota at every PATCH, not
// only at the finalizing PUT: a session opened and fed chunks but never
// finalized can otherwise hold an arbitrary amount of data with the quota
// system never seeing it.

func setupQuotaUploadRouter(repoQuota int64) *gin.Engine {
	repo := &domain.Repository{
		ID: "repo-quota-upload", Name: "docker-quota",
		Format: domain.FormatDocker, Type: domain.TypeHosted, Online: true,
		QuotaBytes: &repoQuota,
	}
	d := formats.Deps{
		Repos: testutil.NewRepoRepo(repo),
		Blobs: testutil.NewBlobStoreRepo(&domain.BlobStore{
			ID: "00000000-0000-0000-0000-000000000001", Name: "default", Type: "local",
		}),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := oci.New(d)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := requestctx.WithUser(c.Request.Context(), "test-user-id", "testuser")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func startQuotaUpload(t *testing.T, r *gin.Engine) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/repository/docker-quota/v2/lib/app/blobs/uploads/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	loc := w.Header().Get("Location")
	require.NotEmpty(t, loc)
	return loc
}

func TestPatchUpload_SessionBytesCountAgainstQuota(t *testing.T) {
	r := setupQuotaUploadRouter(5)
	loc := startQuotaUpload(t, r)

	// First chunk: 4 bytes into a 5-byte quota — accepted.
	req := httptest.NewRequest(http.MethodPatch, loc, strings.NewReader("aaaa"))
	req.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())

	// Second chunk: session 4 + declared 4 = 8 > 5 — must be refused before the
	// bytes are staged, not silently accepted until a finalize that never comes.
	req = httptest.NewRequest(http.MethodPatch, loc, strings.NewReader("bbbb"))
	req.Header.Set("Content-Type", "application/octet-stream")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code,
		"an unfinalized session grew past the quota invisibly: %d %s", w.Code, w.Body.String())
}
