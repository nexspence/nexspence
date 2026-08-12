package repoproxy_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats/base"
)

// Cleanup after a failed cache fill (#198). The blob key is derived from repo
// name + path, so every request filling the same artifact writes to the same
// key: a fill that cleans up by deleting that key destroys whatever else is
// there — the copy already cached at that path, or the one a concurrent fill
// just published and registered. Neither belongs to the failing request.

// flakyUpstream serves a body, and can be switched to abort mid-response:
// it announces a Content-Length it never delivers and drops the connection,
// which surfaces as an unexpected EOF while the proxy is streaming the body.
type flakyUpstream struct {
	mu       sync.Mutex
	body     string
	truncate bool
}

func newFlakyUpstream(body string) (*flakyUpstream, *httptest.Server) {
	fu := &flakyUpstream{body: body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fu.mu.Lock()
		payload, truncate := fu.body, fu.truncate
		fu.mu.Unlock()

		if !truncate {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(payload))
			return
		}
		// Hand-write a response promising more than we send, then hang up.
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s",
			len(payload)+64, payload)
		_ = buf.Flush()
		_ = conn.Close()
	}))
	return fu, srv
}

func (fu *flakyUpstream) set(body string, truncate bool) {
	fu.mu.Lock()
	defer fu.mu.Unlock()
	fu.body, fu.truncate = body, truncate
}

// A refill that dies mid-stream must leave the previously cached copy alone.
// The failing request published nothing of its own (Put stages at its own temp
// path and drops it on error), so deleting the shared key can only destroy
// someone else's blob — leaving the asset row that points at it with no bytes.
func TestServeGET_Revalidation_FillFails_KeepsCachedBlob(t *testing.T) {
	useUnguardedUpstream(t)
	fu, srv := newFlakyUpstream("InRelease-v1")
	defer srv.Close()

	const path = "/dists/trixie/InRelease"
	repo := proxyRepo("failfill", srv.URL)
	d, assets, blobStore := newRevalDeps(repo)

	require.Equal(t, http.StatusOK, revalGET(d, repo, path, 10*time.Minute).Code)
	require.Equal(t, "InRelease-v1", blobStoreBody(t, blobStore, "failfill", path))

	// Stale cache + an upstream that now aborts mid-body → the refill fails.
	makeStale(t, assets, "failfill", path)
	fu.set("InRelease-v2-trunc", true)
	revalGET(d, repo, path, 10*time.Minute)

	assert.Empty(t, blobStore.Deleted, "a failed fill must not delete the shared blob key")
	assert.Equal(t, "InRelease-v1", blobStoreBody(t, blobStore, "failfill", path),
		"the previously cached copy must survive a failed refill")
	_, err := assets.GetByPath(nil, "failfill", path) //nolint:staticcheck // mock ignores ctx
	require.NoError(t, err, "the asset row must not be left pointing at a deleted blob")
}

// The post-stream quota gate drops an over-quota fill that could not be checked
// up front (no Content-Length). It must not drop the blob when an asset row
// already points at that key — those bytes are not this request's to delete.
func TestServeGET_Revalidation_OverQuotaChunkedRefill_KeepsReferencedBlob(t *testing.T) {
	useUnguardedUpstream(t)
	const path = "/pkg/1.0/a.jar"

	var mu sync.Mutex
	body := "v1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		payload := body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		// Flush between writes so the response is chunked (no Content-Length),
		// which defers the quota check until after the bytes are streamed.
		fl := w.(http.Flusher)
		_, _ = w.Write([]byte(payload[:1]))
		fl.Flush()
		_, _ = w.Write([]byte(payload[1:]))
	}))
	defer srv.Close()

	repo := proxyRepo("quotarefill", srv.URL)
	quota := int64(100)
	repo.QuotaBytes = &quota
	d, assets, blobStore := quotaDeps(repo)

	require.Equal(t, http.StatusOK, revalGET(d, repo, path, 10*time.Minute).Code)
	require.Equal(t, "v1", blobStoreBody(t, blobStore, "quotarefill", path))

	// Stale cache + a refill that busts the quota only once fully streamed.
	makeStale(t, assets, "quotarefill", path)
	mu.Lock()
	body = overQuotaBody
	mu.Unlock()
	quota = 10

	w := revalGET(d, repo, path, 10*time.Minute)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, overQuotaBody, w.Body.String(), "the client still receives the artifact")

	assert.Empty(t, blobStore.Deleted, "an over-quota fill must not delete a blob an asset row points at")
	exists, err := blobStore.Exists(nil, base.BlobKey("quotarefill", path)) //nolint:staticcheck // mock ignores ctx
	require.NoError(t, err)
	assert.True(t, exists, "the cached asset must keep its bytes")
}
