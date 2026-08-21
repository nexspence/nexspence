package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// One run per rule per process: detached from the request context (#254),
// repeated POSTs to .../run would otherwise pile up concurrent runs of the
// same rule pushing the same assets.
func TestReplicationService_RunRule_RefusesConcurrentRun(t *testing.T) {
	repRepo := testutil.NewReplicationRepo()
	svc := NewReplicationService(repRepo, testutil.NewAssetRepo(), testutil.NewBlobStore(), "test-secret", nil, zap.NewNop().Sugar())

	rule := &domain.ReplicationRule{Name: "solo", SourceRepo: "src", TargetURL: "http://127.0.0.1:1/", TargetRepo: "dst"}
	require.NoError(t, repRepo.CreateRule(context.Background(), rule))

	// Occupy the guard the way an in-flight run does.
	svc.running.Store(rule.ID, struct{}{})
	defer svc.running.Delete(rule.ID)

	err := svc.RunRule(context.Background(), rule.ID)
	require.ErrorContains(t, err, "already running")
}

// idResolver hands out a different physical store per blob-store id, the way a
// real registry does once an operator adds a second store.
type idResolver map[string]storage.BlobStore

func (r idResolver) Get(_ context.Context, d storage.BlobStoreDescriptor) (storage.BlobStore, error) {
	s, ok := r[d.ID]
	if !ok {
		return nil, errors.New("no store for " + d.ID)
	}
	return s, nil
}

// An asset living outside the default store must be read from its own store:
// replication used to always read the injected default one, so every asset on
// a second store failed with "blob not found" — or pushed whatever unrelated
// bytes happened to share the key in the default store (#299).
func TestReplicationService_PushAsset_ReadsAssetsOwnStore(t *testing.T) {
	defaultStore := testutil.NewBlobStore()
	s3Store := testutil.NewBlobStore()
	require.NoError(t, defaultStore.Put(context.Background(), "k", testutil.MakeReader("stale-default-bytes"), 19))
	require.NoError(t, s3Store.Put(context.Background(), "k", testutil.MakeReader("real-bytes"), 10))

	s3 := &domain.BlobStore{ID: "store-s3", Name: "s3", Type: "s3"}
	blobs := testutil.NewBlobStoreRepo(s3)

	svc := NewReplicationService(testutil.NewReplicationRepo(), testutil.NewAssetRepo(), defaultStore,
		"test-secret", nil, zap.NewNop().Sugar())
	svc.WithResolver(blobs, idResolver{"store-s3": s3Store})

	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	rule := &domain.ReplicationRule{TargetURL: srv.URL, TargetRepo: "dst"}
	asset := domain.Asset{Path: "/a.jar", BlobKey: "k", BlobStoreID: "store-s3", SizeBytes: 10}

	pushed, n, err := svc.pushAsset(context.Background(), srv.Client(), rule, "", asset)
	require.NoError(t, err)
	require.True(t, pushed)
	require.Equal(t, int64(10), n)
	require.Equal(t, "real-bytes", string(got))
}

// No resolver configured (or an asset with no store id) keeps the old
// behaviour: read from the injected default store.
func TestReplicationService_PushAsset_FallsBackToDefaultStore(t *testing.T) {
	defaultStore := testutil.NewBlobStore()
	require.NoError(t, defaultStore.Put(context.Background(), "k", testutil.MakeReader("default-bytes"), 13))

	svc := NewReplicationService(testutil.NewReplicationRepo(), testutil.NewAssetRepo(), defaultStore,
		"test-secret", nil, zap.NewNop().Sugar())

	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	rule := &domain.ReplicationRule{TargetURL: srv.URL, TargetRepo: "dst"}
	asset := domain.Asset{Path: "/a.jar", BlobKey: "k", SizeBytes: 13}

	pushed, _, err := svc.pushAsset(context.Background(), srv.Client(), rule, "", asset)
	require.NoError(t, err)
	require.True(t, pushed)
	require.Equal(t, "default-bytes", string(got))
}
