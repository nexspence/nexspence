package raw_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func rawPut(router http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestRawHandler_AllowOnce_RedeployIs400(t *testing.T) {
	repo := testutil.SimpleRepo("raw-once", "raw")
	repo.FormatConfig = map[string]any{domain.WritePolicyKey: "allow_once"}
	router, blobStore := setupRouter(repo)

	require.Equal(t, http.StatusCreated, rawPut(router, "/repository/raw-once/app/1.0/app.zip", "first").Code)

	w := rawPut(router, "/repository/raw-once/app/1.0/app.zip", "second")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Repository does not allow updating assets: raw-once")

	got, err := blobStore.Read(base.BlobKey("raw-once", "/app/1.0/app.zip"))
	require.NoError(t, err)
	assert.Equal(t, "first", got)
}

func TestRawHandler_Deny_Is400(t *testing.T) {
	repo := testutil.SimpleRepo("raw-ro", "raw")
	repo.FormatConfig = map[string]any{domain.WritePolicyKey: "deny"}
	router, blobStore := setupRouter(repo)

	w := rawPut(router, "/repository/raw-ro/file.txt", "x")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Repository is read-only: raw-ro")
	assert.False(t, blobStore.Has(base.BlobKey("raw-ro", "/file.txt")))
}
