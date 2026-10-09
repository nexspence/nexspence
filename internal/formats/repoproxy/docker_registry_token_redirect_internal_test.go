package repoproxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

type tokenRedirectTransport func(*http.Request) (*http.Response, error)

func (f tokenRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestFetchDockerRegistryToken_Redirect_AuthBoundary(t *testing.T) {
	tests := []struct {
		name          string
		targets       []string
		authenticated []bool
	}{
		{"same HTTPS origin", []string{"https://registry.example/next"}, []bool{true}},
		{"explicit default port", []string{"https://registry.example:443/next"}, []bool{true}},
		{"different port", []string{"https://registry.example:8443/next"}, []bool{false}},
		{"TLS downgrade", []string{"http://registry.example/next"}, []bool{false}},
		{"subdomain", []string{"https://sub.registry.example/next"}, []bool{false}},
		{"foreign host", []string{"https://other.example/next"}, []bool{false}},
		{"return after port change", []string{"https://registry.example:8443/next", "https://registry.example/final"}, []bool{false, false}},
		{"return after downgrade", []string{"http://registry.example/next", "https://registry.example/final"}, []bool{false, false}},
	}
	for _, proxy := range []bool{false, true} {
		name := "direct"
		if proxy {
			name = "outbound proxy"
		}
		t.Run(name, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					client := &http.Client{}
					if proxy {
						var err error
						client, err = buildProxyClient(proxySettings{httpProxy: "http://proxy.example:3128"})
						if err != nil {
							t.Fatal(err)
						}
					} else {
						client.CheckRedirect = UpstreamClient.CheckRedirect
					}
					calls := 0
					client.Transport = tokenRedirectTransport(func(req *http.Request) (*http.Response, error) {
						_, _, hasAuth := req.BasicAuth()
						wantAuth := true
						if calls > 0 {
							wantAuth = tt.authenticated[calls-1]
						}
						if hasAuth != wantAuth {
							t.Errorf("hop %d authenticated=%v, want %v", calls, hasAuth, wantAuth)
						}
						res := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"token":"safe-token"}`)), Request: req}
						if calls < len(tt.targets) {
							res.StatusCode = http.StatusFound
							res.Header.Set("Location", tt.targets[calls])
						}
						calls++
						return res, nil
					})
					repo := &domain.Repository{ProxyConfig: map[string]any{"remote_url": "https://registry.example", "remote_username": "puller", "remote_password": "test-secret"}}
					token, err := fetchDockerRegistryToken(context.Background(), repo, client, "https://registry.example/token", "registry.example", "repository:team/app:pull")
					if err != nil {
						t.Fatal(err)
					}
					if token != "safe-token" || calls != len(tt.targets)+1 {
						t.Fatalf("token=%q calls=%d", token, calls)
					}
				})
			}
		})
	}
}

func TestTokenRedirectClient_Policy_PreservesRejectionsAndLimits(t *testing.T) {
	sentinel := errors.New("custom redirect rejection")
	for _, tt := range []struct {
		name      string
		policy    func(*http.Request, []*http.Request) error
		wantCalls int
		wantError error
	}{
		{"default limit", nil, 10, nil},
		{"configured limit", redirectPolicy, 12, nil},
		{"custom rejection", func(*http.Request, []*http.Request) error { return sentinel }, 1, sentinel},
		{"return last response", func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, 1, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			tr := tokenRedirectTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"/loop"}}, Body: io.NopCloser(strings.NewReader("redirect")), Request: req}, nil
			})
			original := &http.Client{Transport: tr, CheckRedirect: tt.policy}
			cloned := tokenRedirectClient(original)
			res, err := cloned.Get("https://registry.example/token")
			if res != nil {
				res.Body.Close()
			}
			if calls != tt.wantCalls {
				t.Errorf("calls=%d, want %d", calls, tt.wantCalls)
			}
			if tt.wantError != nil && !errors.Is(err, tt.wantError) {
				t.Errorf("error=%v, want %v", err, tt.wantError)
			}
			if tt.wantCalls > 1 && err == nil {
				t.Error("redirect loop did not fail")
			}
			if tt.name == "return last response" && err != nil {
				t.Errorf("ErrUseLastResponse: %v", err)
			}
			if (original.CheckRedirect == nil) != (tt.policy == nil) {
				t.Error("shared client's policy changed")
			}
		})
	}
}

func TestFetchDockerRegistryToken_Redirect_AnonymousRefusalIsNotCredentialsRejected(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: tokenRedirectTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		res := &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unauthorized")), Request: req}
		if calls == 1 {
			res.StatusCode = http.StatusFound
			res.Header.Set("Location", "https://registry.example:8443/token")
		}
		return res, nil
	})}
	repo := &domain.Repository{ProxyConfig: map[string]any{"remote_url": "https://registry.example", "remote_username": "puller", "remote_password": "test-secret"}}
	_, err := fetchDockerRegistryToken(context.Background(), repo, client, "https://registry.example/token", "registry.example", "repository:team/app:pull")
	if err == nil {
		t.Fatal("expected refusal")
	}
	if credentialsRejected(err) {
		t.Error("anonymous redirected request was classified as rejected credentials")
	}
}
