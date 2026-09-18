package raw_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/raw"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func TestRawHandler_AzureTransportErrorDoesNotExposeSAS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	azureStore, err := storage.NewAzureBlobStore(context.Background(), storage.AzureOptions{
		Container:   "nx",
		AccountName: "devstoreaccount1",
		SASToken:    "sv=2024-11-04&sig=handler-secret",
		Endpoint:    srv.URL,
	})
	require.NoError(t, err)
	srv.Close()

	repo := testutil.SimpleRepo("raw-azure-error", "raw")
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  azureStore,
	}
	h := raw.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", h.ServeHTTP)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPut, "/repository/raw-azure-error/artifact.txt", strings.NewReader("payload")).WithContext(ctx)
	req.ContentLength = int64(len("payload"))
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	require.Equal(t, http.StatusInternalServerError, resp.Code)
	require.NotContains(t, resp.Body.String(), "handler-secret")
	require.NotContains(t, resp.Body.String(), "sig=")
}
