package huggingface_test

import (
	"net/http"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// useUnguardedUpstream swaps the SSRF-guarded upstream client for a plain one so
// cache-miss fetches can reach the loopback httptest server used as a fake Hub.
func useUnguardedUpstream(t *testing.T) {
	t.Helper()
	orig := repoproxy.UpstreamClient
	repoproxy.UpstreamClient = &http.Client{}
	t.Cleanup(func() { repoproxy.UpstreamClient = orig })
}
