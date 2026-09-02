package repoproxy_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// earlyFailStore is a blob store whose Put gives up before draining the reader,
// exactly as a real one does when the destination cannot be created at all
// (permission denied, disk full, a backend timeout on the first write).
type earlyFailStore struct {
	*testutil.BlobStore
	readFirst int
}

func (s *earlyFailStore) Put(_ context.Context, _ string, r io.Reader, _ int64) error {
	if s.readFirst > 0 {
		_, _ = io.ReadFull(r, make([]byte, s.readFirst))
	}
	return errors.New("blob store unavailable")
}

// The cache-fill pipe is written by the goroutine serving the client's own HTTP
// request, so a Put that stops reading early used to block that request on
// pw.Write forever: no response, no error, no timeout, and a connection held
// open for good (#367). The fill must fail fast instead.
func TestServeGET_CacheFill_PutFailsEarly_DoesNotHang(t *testing.T) {
	useUnguardedUpstream(t)

	body := make([]byte, 256*1024) // larger than io.Copy's buffer, so the copy writes more than once
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	for _, tc := range []struct {
		name      string
		readFirst int
	}{
		{"put fails before reading anything", 0},
		{"put fails after a partial read", 1024},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := proxyRepo("hangfill", srv.URL)
			d := formats.Deps{
				Repos:      testutil.NewRepoRepo(repo),
				Blobs:      testutil.NewBlobStoreRepo(),
				Components: testutil.NewComponentRepo(),
				Assets:     testutil.NewAssetRepo(),
				BlobStore:  &earlyFailStore{BlobStore: testutil.NewBlobStore(), readFirst: tc.readFirst},
			}

			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- revalGET(d, repo, "/pkg/1.0/big.bin", 10*time.Minute) }()

			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the request never completed: the cache-fill pipe blocked the client's own goroutine")
			}
		})
	}
}

// A blob store that fails this way must not be mistaken for a successful fill.
func TestServeGET_CacheFill_PutFailsEarly_RegistersNoAsset(t *testing.T) {
	useUnguardedUpstream(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("artifact-bytes"))
	}))
	defer srv.Close()

	const path = "/pkg/1.0/a.txt"
	repo := proxyRepo("hangfill2", srv.URL)
	assets := testutil.NewAssetRepo()
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     assets,
		BlobStore:  &earlyFailStore{BlobStore: testutil.NewBlobStore()},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		revalGET(d, repo, path, 10*time.Minute)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the request never completed")
	}

	a, err := assets.GetByPath(context.Background(), "hangfill2", path)
	if err == nil {
		assert.Nil(t, a, "a failed cache fill must not leave an asset row behind")
	}
}
