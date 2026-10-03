package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api"
)

// echoUploadHandler answers like the OCI upload handlers: Location and Link are
// built from the path the handler received.
func echoUploadHandler(seen *[]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.URL.Path)
		w.Header().Set("Location", r.URL.Path+"/next")
		w.Header().Set("Link", "<"+r.URL.Path+"?last=a&n=1>; rel=\"next\"")
		w.WriteHeader(http.StatusAccepted)
	})
}

// #607: a client on a subdomain host follows Location back through the
// rewriter. The repository must be injected once, not once per hop.
func TestSubdomainRewriter_LocationStaysInClientView(t *testing.T) {
	for _, tc := range []struct {
		name, host, base string
		aliases          map[string]string
	}{
		{"base domain", "dk-h.registry.test:8081", "registry.test", nil},
		{"alias", "docker.example.org", "", map[string]string{"docker.example.org": "dk-h"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen []string
			rw := api.NewSubdomainRewriter(echoUploadHandler(&seen), tc.base, tc.aliases)
			path := "/v2/alpine/blobs/uploads"
			for hop := 0; hop < 3; hop++ {
				req := httptest.NewRequest(http.MethodPost, path, nil)
				req.Host = tc.host
				rec := httptest.NewRecorder()
				rw.ServeHTTP(rec, req)
				require.Equal(t, http.StatusAccepted, rec.Code)
				loc := rec.Header().Get("Location")
				assert.Equal(t, path+"/next", loc, "hop %d: Location in the client's view", hop)
				assert.Equal(t, "<"+path+"?last=a&n=1>; rel=\"next\"", rec.Header().Get("Link"))
				path = loc
			}
			assert.Equal(t, []string{
				"/v2/dk-h/alpine/blobs/uploads",
				"/v2/dk-h/alpine/blobs/uploads/next",
				"/v2/dk-h/alpine/blobs/uploads/next/next",
			}, seen, "the handler sees the repository exactly once")
		})
	}
}

// The headers are fixed whichever call sends them first: a body write or a
// Flush with no explicit WriteHeader.
func TestSubdomainRewriter_LocationFixedOnImplicitHeaderAndFlush(t *testing.T) {
	rw := api.NewSubdomainRewriter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", r.URL.Path)
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("x"))
	}), "registry.test", nil)
	req := httptest.NewRequest(http.MethodGet, "/v2/alpine/tags/list", nil)
	req.Host = "dk-h.registry.test"
	rec := httptest.NewRecorder()
	rw.ServeHTTP(rec, req)
	assert.True(t, rec.Flushed, "Flush reaches the underlying writer")
	assert.Equal(t, "/v2/alpine/tags/list", rec.Header().Get("Location"))
}

// Requests the rewriter did not touch, and absolute URLs, keep their headers.
func TestSubdomainRewriter_LocationUntouchedOtherwise(t *testing.T) {
	rw := api.NewSubdomainRewriter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/dk-h/alpine/blobs/sha256:x" {
			w.Header().Set("Location", "https://bucket.example/v2/dk-h/alpine/blob")
		} else {
			w.Header().Set("Location", r.URL.Path)
		}
		w.WriteHeader(http.StatusTemporaryRedirect)
	}), "registry.test", nil)

	req := httptest.NewRequest(http.MethodGet, "/v2/dk-h/alpine/manifests/1", nil)
	req.Host = "registry.test"
	rec := httptest.NewRecorder()
	rw.ServeHTTP(rec, req)
	assert.Equal(t, "/v2/dk-h/alpine/manifests/1", rec.Header().Get("Location"), "main host: no rewrite either way")

	req = httptest.NewRequest(http.MethodGet, "/v2/alpine/blobs/sha256:x", nil)
	req.Host = "dk-h.registry.test"
	rec = httptest.NewRecorder()
	rw.ServeHTTP(rec, req)
	assert.Equal(t, "https://bucket.example/v2/dk-h/alpine/blob", rec.Header().Get("Location"))
}
