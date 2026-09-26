package group_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// TestMain installs an unguarded upstream HTTP client so helm-proxy members in
// group tests can reach loopback httptest remotes. Production UpstreamClient is
// SSRF-guarded and would block those.
func TestMain(m *testing.M) {
	repoproxy.UpstreamClient = &http.Client{}
	os.Exit(m.Run())
}
