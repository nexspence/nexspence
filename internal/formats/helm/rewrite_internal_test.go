package helm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRewriteChartURL covers every shape an index.yaml `urls` entry can take.
// An httptest upstream always carries an explicit port and no path prefix, so the
// port-normalization and remote-path-prefix branches are only reachable here.
func TestRewriteChartURL(t *testing.T) {
	const local = "http://nexus.local/repository/charts/"

	tests := []struct {
		name       string
		remoteBase string
		url        string
		want       string
	}{
		// ── Case 1: repository-relative ──────────────────────────────
		{
			name:       "relative nested path keeps its directory",
			remoteBase: "https://charts.example.com",
			url:        "charts/ingress-nginx-4.11.2.tgz",
			want:       local + "charts/ingress-nginx-4.11.2.tgz",
		},
		{
			name:       "relative flat path",
			remoteBase: "https://charts.example.com",
			url:        "ingress-nginx-4.11.2.tgz",
			want:       local + "ingress-nginx-4.11.2.tgz",
		},
		{
			name:       "relative dot-slash prefix is cleaned",
			remoteBase: "https://charts.example.com",
			url:        "./charts/x-1.0.0.tgz",
			want:       local + "charts/x-1.0.0.tgz",
		},

		// ── Case 2: absolute under the remote ────────────────────────
		{
			name:       "absolute under remote root",
			remoteBase: "https://charts.example.com",
			url:        "https://charts.example.com/charts/x-1.0.0.tgz",
			want:       local + "charts/x-1.0.0.tgz",
		},
		{
			name:       "absolute under remote with path prefix",
			remoteBase: "https://charts.example.com/charts-repo",
			url:        "https://charts.example.com/charts-repo/charts/x-1.0.0.tgz",
			want:       local + "charts/x-1.0.0.tgz",
		},
		{
			name:       "scheme mismatch is still the same upstream",
			remoteBase: "https://charts.example.com",
			url:        "http://charts.example.com/x-1.0.0.tgz",
			want:       local + "x-1.0.0.tgz",
		},

		// ── Default-port normalization ───────────────────────────────
		{
			name:       "explicit https default port matches bare remote host",
			remoteBase: "https://charts.example.com",
			url:        "https://charts.example.com:443/x-1.0.0.tgz",
			want:       local + "x-1.0.0.tgz",
		},
		{
			name:       "explicit http default port matches bare remote host",
			remoteBase: "http://charts.example.com",
			url:        "http://charts.example.com:80/x-1.0.0.tgz",
			want:       local + "x-1.0.0.tgz",
		},
		{
			name:       "bare host matches remote carrying the default port",
			remoteBase: "https://charts.example.com:443",
			url:        "https://charts.example.com/x-1.0.0.tgz",
			want:       local + "x-1.0.0.tgz",
		},
		{
			name:       "host comparison is case-insensitive",
			remoteBase: "https://Charts.Example.com",
			url:        "https://charts.example.com/x-1.0.0.tgz",
			want:       local + "x-1.0.0.tgz",
		},
		{
			name:       "a non-default port is a different upstream",
			remoteBase: "https://charts.example.com",
			url:        "https://charts.example.com:8443/x-1.0.0.tgz",
			want:       "https://charts.example.com:8443/x-1.0.0.tgz",
		},

		// ── Case 3: unproxyable, handed back absolute ────────────────
		{
			name:       "absolute URL on another host is left alone",
			remoteBase: "https://charts.example.com",
			url:        "https://github.com/o/r/releases/download/v1/x-1.0.0.tgz",
			want:       "https://github.com/o/r/releases/download/v1/x-1.0.0.tgz",
		},
		{
			name:       "same host but outside the proxied subtree",
			remoteBase: "https://charts.example.com/charts-repo",
			url:        "https://charts.example.com/other-repo/x-1.0.0.tgz",
			want:       "https://charts.example.com/other-repo/x-1.0.0.tgz",
		},
		{
			// The subtree check must run on the cleaned path. Comparing the raw one
			// would see the "/base/" prefix, strip it, and only then collapse the
			// "..", silently proxying a different upstream file.
			name:       "dot-dot escaping the subtree is not proxied",
			remoteBase: "https://charts.example.com/base",
			url:        "https://charts.example.com/base/../secret/x-1.0.0.tgz",
			want:       "https://charts.example.com/base/../secret/x-1.0.0.tgz",
		},

		// ── Case 4: root-relative, resolved against the HOST root ────
		{
			name:       "root-relative under the remote prefix",
			remoteBase: "https://charts.example.com/base",
			url:        "/base/charts/x-1.0.0.tgz",
			want:       local + "charts/x-1.0.0.tgz",
		},
		{
			name:       "root-relative outside the remote prefix resolves upstream",
			remoteBase: "https://charts.example.com/base",
			url:        "/charts/x-1.0.0.tgz",
			want:       "https://charts.example.com/charts/x-1.0.0.tgz",
		},
		{
			name:       "root-relative with no remote prefix is proxied",
			remoteBase: "https://charts.example.com",
			url:        "/charts/x-1.0.0.tgz",
			want:       local + "charts/x-1.0.0.tgz",
		},

		// ── Query strings cannot be forwarded upstream ───────────────
		{
			name:       "absolute signed URL is handed to the client",
			remoteBase: "https://charts.example.com",
			url:        "https://charts.example.com/x-1.0.0.tgz?sig=abc123",
			want:       "https://charts.example.com/x-1.0.0.tgz?sig=abc123",
		},
		{
			name:       "relative signed URL resolves to the upstream original",
			remoteBase: "https://charts.example.com",
			url:        "charts/x-1.0.0.tgz?sig=abc123",
			want:       "https://charts.example.com/charts/x-1.0.0.tgz?sig=abc123",
		},
		{
			name:       "empty but forced query still counts as a query",
			remoteBase: "https://charts.example.com",
			url:        "https://charts.example.com/x-1.0.0.tgz?",
			want:       "https://charts.example.com/x-1.0.0.tgz?",
		},

		// ── Degenerate input ─────────────────────────────────────────
		{
			name:       "oci scheme is not something we can serve over HTTP",
			remoteBase: "https://charts.example.com",
			url:        "oci://registry.example.com/charts/x",
			want:       "oci://registry.example.com/charts/x",
		},
		{
			name:       "unparsable entry is left for the client",
			remoteBase: "https://charts.example.com",
			url:        "%zz-1.0.0.tgz",
			want:       "%zz-1.0.0.tgz",
		},
		{
			name:       "unparsable remote leaves every entry alone",
			remoteBase: "http://[::1",
			url:        "charts/x-1.0.0.tgz",
			want:       "charts/x-1.0.0.tgz",
		},
		{
			// A remote_url saved without a scheme parses as a bare path. Nothing
			// upstream will work, but the rewrite must not panic or leak the
			// pseudo-host into the proxy path.
			name:       "schemeless remote still yields a sane proxy path",
			remoteBase: "charts.example.com",
			url:        "charts/x-1.0.0.tgz",
			want:       local + "charts/x-1.0.0.tgz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, rewriteChartURL(tt.url, tt.remoteBase, local))
		})
	}
}

func TestSplitChartFilename(t *testing.T) {
	tests := []struct {
		filename string
		name     string
		version  string
	}{
		{"ingress-nginx-4.11.2.tgz", "ingress-nginx", "4.11.2"},
		{"nginx-15.0.0.tgz", "nginx", "15.0.0"},
		{"nfs-helm1-0.1.1.tgz", "nfs-helm1", "0.1.1"},
		// SemVer prerelease and build metadata must not be split at their dashes.
		{"cert-manager-v1.13.0-beta.0.tgz", "cert-manager", "v1.13.0-beta.0"},
		{"foo-1.2.3-rc.1.tgz", "foo", "1.2.3-rc.1"},
		{"foo-1.2.3+build.5.tgz", "foo", "1.2.3+build.5"},
		{"my-2.0-chart-1.0.0.tgz", "my-2.0-chart", "1.0.0"},
		// Fallback: nothing SemVer-shaped, split at the last dash as before.
		{"foo-1.0.tgz", "foo", "1.0"},
		{"chart.tgz", "chart", "0.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			gotName, gotVersion := splitChartFilename(tt.filename)
			assert.Equal(t, tt.name, gotName, "chart name")
			assert.Equal(t, tt.version, gotVersion, "version")
		})
	}
}
