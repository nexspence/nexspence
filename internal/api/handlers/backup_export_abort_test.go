package handlers_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A manual export that fails after the 200 is on the wire must not end like a
// complete download: the connection is closed before the final chunk (#569).

// failingAssetList fails every asset page of every repository.
type failingAssetList struct{ *testutil.AssetRepo }

func (failingAssetList) List(context.Context, string, int, int) (*domain.Page[domain.Asset], error) {
	return nil, errors.New("FATAL: terminating connection due to administrator command (SQLSTATE 57P01)")
}

// exportServer serves both export routes behind gin.Recovery, as router.go does.
func exportServer(t *testing.T, svc *service.BackupService) (*httptest.Server, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	h := handlers.NewBackupHandler(svc).WithLogger(zap.New(core).Sugar())
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/api/v1/backup/export", h.Export)
	r.GET("/api/v1/repositories/:name/export", h.ExportRepo)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, logs
}

func exportSvc(repo *domain.Repository) *service.BackupService {
	return &service.BackupService{
		BlobStores: testutil.NewBlobStoreRepo(),
		Repos:      testutil.NewRepoRepo(repo),
		Users:      testutil.NewUserRepo(),
		Roles:      testutil.NewRoleRepo(),
		Policies:   testutil.NewCleanupPolicyRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
	}
}

// download fetches path and returns the status and the error from reading
// the whole body.
func download(t *testing.T, srv *httptest.Server, path string) (int, []byte, error) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// readArchive fully reads a gzipped tar and returns its entry names.
func readArchive(t *testing.T, body []byte) []string {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(body))
	require.NoError(t, err)
	tr := tar.NewReader(gr)
	var names []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return names
		}
		require.NoError(t, err)
		names = append(names, hdr.Name)
	}
}

func TestBackupHandler_Export_PageError_AbortsDownload(t *testing.T) {
	for _, path := range []string{"/api/v1/backup/export", "/api/v1/repositories/r1/export"} {
		t.Run(path, func(t *testing.T) {
			svc := exportSvc(testutil.SimpleRepo("r1", "raw"))
			svc.Assets = failingAssetList{testutil.NewAssetRepo()}
			srv, logs := exportServer(t, svc)

			status, _, err := download(t, srv, path)
			assert.Equal(t, http.StatusOK, status, "the status was already sent")
			require.Error(t, err, "the body must not read as a complete download")

			entries := logs.FilterMessage("backup export failed, aborting the download").All()
			require.Len(t, entries, 1)
			assert.Contains(t, entries[0].ContextMap()["err"], "list assets of r1")
		})
	}
}

func TestBackupHandler_Export_Success_IsCompleteArchive(t *testing.T) {
	for _, path := range []string{"/api/v1/backup/export", "/api/v1/repositories/r1/export"} {
		t.Run(path, func(t *testing.T) {
			srv, logs := exportServer(t, exportSvc(testutil.SimpleRepo("r1", "raw")))

			status, body, err := download(t, srv, path)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, status)
			assert.Contains(t, readArchive(t, body), "assets.json")
			assert.Zero(t, logs.Len())
		})
	}
}

// Unreadable blobs leave a valid archive that only lacks those blobs — the
// same archive a scheduled run keeps — so it is still served whole, with a
// warning in the log.
func TestBackupHandler_Export_IncompleteBlobs_ServedWithWarning(t *testing.T) {
	repo := testutil.SimpleRepo("r1", "raw")
	svc := exportSvc(repo)
	ctx := context.Background()
	comp := &domain.Component{RepositoryID: repo.ID, Repository: repo.Name, Format: "raw", Name: "/a", Version: "1"}
	require.NoError(t, svc.Components.Create(ctx, comp))
	require.NoError(t, svc.Assets.Create(ctx, &domain.Asset{
		ComponentID: comp.ID, RepositoryID: repo.ID, Repository: repo.Name,
		Path: "/a", BlobKey: "ab/cd/missing", SizeBytes: 1,
	}))
	srv, logs := exportServer(t, svc)

	status, body, err := download(t, srv, "/api/v1/repositories/r1/export")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, readArchive(t, body), "assets.json")
	assert.Equal(t, 1, logs.FilterMessage("backup export is incomplete").Len())
}
