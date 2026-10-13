package oci_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
)

func largeProxyManifest(version string) string {
	return fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"manifests":[],"annotations":{"version":%q},"padding":%q}`, proxyOCIIndexType, version, strings.Repeat("x", 5<<20))
}

func TestHandler_ProxyManifest_LargeTagAndDigest(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(fmt.Sprintf("legacy=%v/%s", legacy, method), func(t *testing.T) {
				ctx := context.Background()
				first, second := largeProxyManifest("one"), largeProxyManifest("two")
				var changed atomic.Bool
				var hits atomic.Int32
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					if r.URL.Path != "/v2/library/mongo/manifests/8.2.5" {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Type", proxyOCIIndexType)
					body := first
					if changed.Load() {
						body = second
					}
					_, _ = io.WriteString(w, body)
				}))
				defer up.Close()
				repo := proxyManifestRepo(up.URL)
				// Legacy tag bytes and their detached digest copy are charged separately.
				quota := int64(len(first) + len(second))
				if legacy {
					quota += int64(len(first))
				}
				repo.QuotaBytes = &quota
				r, d := setupWithDeps(repo)
				if legacy {
					key := base.BlobKey(repo.Name, "/manifests/library/mongo/8.2.5")
					require.NoError(t, d.BlobStore.Put(ctx, key, strings.NewReader(first), int64(len(first))))
					_, err := base.RegisterStoredBlob(ctx, d, repo, "/manifests/library/mongo/8.2.5", proxyOCIIndexType, base.Coords{Name: "library/mongo", Version: "8.2.5"}, key, strings.TrimPrefix(digest(first), "sha256:"), "", "", int64(len(first)), "", "")
					require.NoError(t, err)
				}
				initial := manifestRequest(r, method, "8.2.5", proxyOCIIndexType)
				require.Equal(t, http.StatusOK, initial.Code)
				require.Equal(t, digest(first), initial.Header().Get("Docker-Content-Digest"))
				require.Equal(t, fmt.Sprint(len(first)), initial.Header().Get("Content-Length"))
				if method == http.MethodGet {
					require.Equal(t, first, initial.Body.String())
				} else {
					require.Empty(t, initial.Body.String())
				}
				if legacy {
					require.Zero(t, hits.Load(), "large cached tag must not be treated as a miss")
				}
				for _, m := range []string{http.MethodGet, http.MethodHead} {
					pinned := manifestRequest(manifestRouter(d), m, digest(first), proxyOCIIndexType)
					require.Equal(t, http.StatusOK, pinned.Code)
					require.Equal(t, digest(first), pinned.Header().Get("Docker-Content-Digest"))
					require.Equal(t, fmt.Sprint(len(first)), pinned.Header().Get("Content-Length"))
					if m == http.MethodGet {
						require.Equal(t, first, pinned.Body.String())
					} else {
						require.Empty(t, pinned.Body.String())
					}
				}
				cached, err := d.Assets.GetByPath(ctx, repo.Name, "/manifests/library/mongo/8.2.5")
				require.NoError(t, err)
				cached.LastModified = time.Now().Add(-24 * time.Hour)
				changed.Store(true)
				next := manifestRequest(r, http.MethodGet, "8.2.5", proxyOCIIndexType)
				require.Equal(t, http.StatusOK, next.Code)
				require.Equal(t, second, next.Body.String())
				require.Equal(t, digest(second), next.Header().Get("Docker-Content-Digest"))
				up.Close()
				for _, body := range []string{first, second} {
					pinned := manifestRequest(manifestRouter(d), http.MethodGet, digest(body), proxyOCIIndexType)
					require.Equal(t, http.StatusOK, pinned.Code)
					require.Equal(t, body, pinned.Body.String())
				}
				used, err := d.Assets.SumSizeByRepo(ctx, repo.Name)
				require.NoError(t, err)
				require.Equal(t, int64(len(first)+len(second)), used)
			})
		}
	}
}

func TestHandler_ProxyManifest_LargeQuotaFallbackAndDigestValidation(t *testing.T) {
	body := largeProxyManifest("one")
	for _, available := range []bool{false, true} {
		t.Run(fmt.Sprint(available), func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "sha256:") && !available {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", proxyOCIIndexType)
				_, _ = io.WriteString(w, body)
			}))
			defer up.Close()
			repo := proxyManifestRepo(up.URL)
			quota := int64(0)
			repo.QuotaBytes = &quota
			r, d := setupWithDeps(repo)
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				got := manifestRequest(r, method, "8.2.5", proxyOCIIndexType)
				if available {
					require.Equal(t, http.StatusOK, got.Code)
					require.Equal(t, digest(body), got.Header().Get("Docker-Content-Digest"))
					require.Equal(t, fmt.Sprint(len(body)), got.Header().Get("Content-Length"))
					pinned := manifestRequest(r, http.MethodGet, digest(body), proxyOCIIndexType)
					require.Equal(t, http.StatusOK, pinned.Code)
					require.Equal(t, body, pinned.Body.String())
				} else {
					require.Equal(t, http.StatusBadGateway, got.Code)
				}
			}
			if available {
				wrong := manifestRequest(r, http.MethodGet, "sha256:"+strings.Repeat("a", 64), proxyOCIIndexType)
				require.Equal(t, http.StatusBadGateway, wrong.Code)
				require.Empty(t, wrong.Header().Get("Docker-Content-Digest"))
			}
			keys, err := d.BlobStore.ListKeys(context.Background())
			require.NoError(t, err)
			require.Empty(t, keys)
		})
	}
}

func TestHandler_ProxyManifest_LargePullGatesRemainSeparate(t *testing.T) {
	body := largeProxyManifest("one")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", proxyOCIIndexType)
		_, _ = io.WriteString(w, body)
	}))
	defer up.Close()
	repo := gatedRepo("proxy", domain.TypeProxy, "critical")
	repo.ProxyConfig = map[string]any{"remote_url": up.URL}
	env := setupGate(repo)
	require.Equal(t, http.StatusOK, env.pull(http.MethodGet, "proxy", "library/mongo", "8.2.5").Code)
	env.scan(t, "proxy", "library/mongo", "8.2.5", domain.ScanStatusOK, domain.ScanSummary{Critical: 1}, 0)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, ref := range []string{"8.2.5", digest(body)} {
			require.Equal(t, http.StatusForbidden, env.pull(method, "proxy", "library/mongo", ref).Code)
		}
	}
}
