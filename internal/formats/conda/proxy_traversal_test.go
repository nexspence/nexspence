package conda_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

// "%252e%252e" arrives as the platform "%2e%2e", which path.Clean leaves alone;
// pasted onto the channel URL it is decoded back to ".." by the upstream, which
// then serves another channel's index with this proxy's credentials attached.
func TestConda_Proxy_EncodedTraversal_NeverReachesUpstream(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"info":{},"packages":{}}`))
	}))
	defer upstream.Close()

	r, _ := subpathProxy(t, "conda-trav", upstream.URL+"/conda-forge")
	for _, target := range []string{
		"/repository/conda-trav/%252e%252e/repodata.json",
		"/repository/conda-trav/linux-64/%252e%252e/repodata.json",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		assert.NotEqual(t, http.StatusOK, w.Code, target)
	}
	assert.Zero(t, hits.Load(), "the upstream must not be asked for an encoded traversal")
}
