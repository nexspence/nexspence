package repoproxy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// The cache fill runs on its own goroutine, where gin's recovery middleware
// does not reach. A panic in a storage driver must not take the process down —
// and the waiters must be released, not left waiting on a fill that will never
// publish an outcome.

// panickingStore is a storage driver that panics when its size is asked for.
type panickingStore struct {
	*testutil.BlobStore
}

func (s panickingStore) Size(context.Context, string) (int64, error) {
	panic("storage driver panic")
}

func TestServeGET_StoragePanicDoesNotTakeTheProcessDown(t *testing.T) {
	useUnguardedUpstream(t)
	// An empty upstream body makes the fill ask the store for the size it
	// just wrote — the call the panicking driver fails on.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	repo := proxyRepo("ppanic", upstream.URL)
	d := makeDeps(repo)
	d.BlobStore = panickingStore{BlobStore: testutil.NewBlobStore()}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/file.bin", nil)

	done := make(chan error, 1)
	go func() {
		done <- repoproxy.ServeGET(c, d, repo, "/file.bin", "", base.Coords{}, "application/octet-stream", 0)
	}()

	select {
	case err := <-done:
		// The fill failed; the request ends with that failure instead of
		// taking the process with it.
		assert.Error(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the request did not finish: the fill published no outcome")
	}
}

func TestServeGET_StoragePanicReleasesWaiters(t *testing.T) {
	useUnguardedUpstream(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	repo := proxyRepo("ppanicwait", upstream.URL)
	d := makeDeps(repo)
	d.BlobStore = panickingStore{BlobStore: testutil.NewBlobStore()}

	// A second request for the same path must not wait on the fill the first
	// one left behind.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/file.bin", nil)

	done := make(chan error, 1)
	go func() {
		done <- repoproxy.ServeGET(c, d, repo, "/file.bin", "", base.Coords{}, "application/octet-stream", 0)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the first request never finished")
	}

	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/file.bin", nil)
	done2 := make(chan error, 1)
	go func() {
		done2 <- repoproxy.ServeGET(c2, d, repo, "/file.bin", "", base.Coords{}, "application/octet-stream", 0)
	}()
	select {
	case <-done2:
	case <-time.After(10 * time.Second):
		t.Fatal("the second request waited on a fill that was never released")
	}
	require.NotNil(t, w2)
}
