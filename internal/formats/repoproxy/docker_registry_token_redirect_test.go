package repoproxy_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

func TestServeGET_OCITokenRedirect_DowngradeDoesNotLeakBasic(t *testing.T) {
	var leaked bool
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, leaked = r.BasicAuth()
		fmt.Fprint(w, `{"token":"public-token"}`)
	}))
	defer sink.Close()
	var upstream *httptest.Server
	upstream = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			http.Redirect(w, r, sink.URL+"/token", http.StatusFound)
			return
		}
		if r.Header.Get("Authorization") == "Bearer public-token" {
			fmt.Fprint(w, `{"schemaVersion":2}`)
			return
		}
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",scope="repository:team/app:pull"`, upstream.URL))
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "unauthorized")
	}))
	defer upstream.Close()
	original := repoproxy.UpstreamClient
	client := upstream.Client()
	client.CheckRedirect = original.CheckRedirect
	repoproxy.UpstreamClient = client
	t.Cleanup(func() { repoproxy.UpstreamClient = original })
	repo := proxyRepo("review-redirect", upstream.URL)
	repo.Format = domain.FormatOCI
	repo.ProxyConfig["remote_username"] = "puller"
	repo.ProxyConfig["remote_password"] = "test-secret"
	result := acrPull(t, repo)
	if result.Code != http.StatusOK {
		t.Fatalf("pull HTTP %d", result.Code)
	}
	if leaked {
		t.Error("Basic credentials leaked from HTTPS token realm to HTTP on a different port")
	}
}
