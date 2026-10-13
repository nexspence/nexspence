package oci_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func proxyManifestRepo(remote string) *domain.Repository {
	return &domain.Repository{ID: "proxy", Name: "proxy", Format: domain.FormatOCI, Type: domain.TypeProxy, ProxyConfig: map[string]any{"remote_url": remote}}
}

func TestHandler_ProxyManifest_HeadRevalidationAndFallback(t *testing.T) {
	var mode atomic.Int32
	var hits atomic.Int32
	const first = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`
	const second = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[],"annotations":{"updated":"true"}}`
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodGet {
			t.Error("cache fill must GET upstream")
		}
		switch mode.Load() {
		case 1:
			if r.Header.Get("If-Modified-Since") == "" {
				t.Error("missing conditional revalidation")
			}
			w.WriteHeader(http.StatusNotModified)
			return
		case 2:
			w.Header().Set("Content-Type", proxyOCIIndexType)
			fmt.Fprint(w, second)
			return
		case 3:
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", proxyOCIIndexType)
		fmt.Fprint(w, first)
	}))
	defer up.Close()
	repo := proxyManifestRepo(up.URL)
	repo.ProxyConfig["metadata_max_age"] = 0.000000001
	r, d := setupWithDeps(repo)
	head := manifestRequest(r, http.MethodHead, "8.2.5", proxyOCIIndexType)
	require.Equal(t, 200, head.Code)
	require.Equal(t, digest(first), head.Header().Get("Docker-Content-Digest"))
	require.Empty(t, head.Body.String())
	mode.Store(1)
	tag := manifestRequest(r, http.MethodGet, "8.2.5", proxyOCIIndexType)
	require.Equal(t, first, tag.Body.String())
	req := httptest.NewRequest(http.MethodGet, "/repository/proxy/v2/library/mongo/manifests/8.2.5", nil)
	req.Header.Set("If-None-Match", "W/"+tag.Header().Get("ETag"))
	conditional := httptest.NewRecorder()
	r.ServeHTTP(conditional, req)
	require.Equal(t, 304, conditional.Code)
	require.Empty(t, conditional.Body.String())
	mode.Store(2)
	require.Equal(t, second, manifestRequest(r, http.MethodGet, "8.2.5", proxyOCIIndexType).Body.String())
	mode.Store(3)
	require.Equal(t, fmt.Sprint(len(second)), manifestRequest(r, http.MethodHead, "8.2.5", proxyOCIIndexType).Header().Get("Content-Length"))
	require.Equal(t, first, manifestRequest(manifestRouter(d), http.MethodGet, digest(first), proxyOCIIndexType).Body.String())
	up.Close()
	require.Equal(t, second, manifestRequest(r, http.MethodGet, "8.2.5", proxyOCIIndexType).Body.String())
	require.Equal(t, 502, manifestRequest(r, http.MethodGet, "8.2.5", dockerIndexType).Code, "never serve an incompatible stale cache")
	require.Positive(t, hits.Load())
}

func TestHandler_ProxyManifest_RejectsWrongDigestAndRepresentation(t *testing.T) {
	const body = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", proxyOCIIndexType)
		fmt.Fprint(w, body)
	}))
	defer up.Close()
	r, d := setupWithDeps(proxyManifestRepo(up.URL))
	require.Equal(t, 400, manifestRequest(r, http.MethodGet, "sha256:bad", "").Code)
	require.Equal(t, 502, manifestRequest(r, http.MethodGet, "sha256:"+strings.Repeat("a", 64), "").Code)
	require.Equal(t, 406, manifestRequest(r, http.MethodGet, "8.2.5", dockerIndexType).Code)
	require.Equal(t, 200, manifestRequest(r, http.MethodGet, digest(body), "").Code)
	require.Equal(t, 406, manifestRequest(r, http.MethodGet, digest(body), dockerIndexType).Code)
	keys, err := d.BlobStore.ListKeys(context.Background())
	require.NoError(t, err)
	require.Len(t, keys, 1)
}

func TestHandler_ProxyManifest_QuotaAndRegistrationFailures(t *testing.T) {
	const body = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`
	for _, kind := range []string{"quota", "registration", "quota check"} {
		for _, digestAvailable := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/available=%v", kind, digestAvailable), func(t *testing.T) {
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.Contains(r.URL.Path, "sha256:") && !digestAvailable {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Type", proxyOCIIndexType)
					fmt.Fprint(w, body)
				}))
				defer up.Close()
				repo := proxyManifestRepo(up.URL)
				quota := int64(0)
				if kind != "registration" {
					repo.QuotaBytes = &quota
				}
				r, d := setupWithDeps(repo)
				assets := d.Assets.(*testutil.AssetRepo)
				if kind == "registration" {
					assets.CreateErr = errors.New("database unavailable")
				}
				if kind == "quota check" {
					assets.Err = errors.New("quota unavailable")
				}
				got := manifestRequest(r, http.MethodGet, "8.2.5", "")
				if digestAvailable {
					require.Equal(t, 200, got.Code, got.Body.String())
					require.Equal(t, body, got.Body.String())
					require.Equal(t, digest(body), got.Header().Get("Docker-Content-Digest"))
				} else {
					require.Equal(t, 502, got.Code)
				}
				keys, err := d.BlobStore.ListKeys(context.Background())
				require.NoError(t, err)
				require.Empty(t, keys)
			})
		}
	}
}

func TestHandler_ProxyManifest_RepairsLegacyCacheAndMissingAlias(t *testing.T) {
	const body = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`
	r, d := setupWithDeps(proxyManifestRepo("http://127.0.0.1:1"))
	repo, _ := d.Repos.Get(context.Background(), "proxy")
	pin, err := base.StorePinnedCopy(context.Background(), d, repo, "/manifests/library/mongo/8.2.5", proxyOCIIndexType, base.Coords{Name: "library/mongo", Version: "8.2.5"}, []byte(body), &domain.Asset{})
	require.NoError(t, err)
	tag := manifestRequest(r, http.MethodGet, "8.2.5", "")
	require.Equal(t, 200, tag.Code, tag.Body.String())
	alias, err := d.Assets.GetByPath(context.Background(), "proxy", "/manifests/library/mongo/"+digest(body))
	require.NoError(t, err)
	require.NotEqual(t, pin.BlobKey, alias.BlobKey, "legacy mutable object must be detached")
	require.NoError(t, d.BlobStore.Delete(context.Background(), alias.BlobKey))
	require.Equal(t, 200, manifestRequest(r, http.MethodHead, "8.2.5", "").Code)
	require.Equal(t, body, manifestRequest(manifestRouter(d), http.MethodGet, digest(body), "").Body.String())
}

func TestHandler_ProxyManifest_ParallelFillIsSerialized(t *testing.T) {
	const body = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", proxyOCIIndexType)
		fmt.Fprint(w, body)
	}))
	defer up.Close()
	r, d := setupWithDeps(proxyManifestRepo(up.URL))
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			got := manifestRequest(r, http.MethodGet, "8.2.5", "")
			if got.Code != 200 || got.Body.String() != body {
				t.Errorf("bad concurrent response: %d %s", got.Code, got.Body.String())
			}
		})
	}
	wg.Wait()
	require.Positive(t, hits.Load())
	usage, err := d.Assets.SumSizeByRepo(context.Background(), "proxy")
	require.NoError(t, err)
	require.Equal(t, int64(len(body)), usage)
}

func TestHandler_ProxyManifest_MultipleAcceptHeaderLines(t *testing.T) {
	const body = `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.list.v2+json","manifests":[]}`
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept"), dockerIndexType) {
			t.Error("upstream lost an Accept header line")
		}
		w.Header().Set("Content-Type", dockerIndexType)
		fmt.Fprint(w, body)
	}))
	defer up.Close()
	r, _ := setupWithDeps(proxyManifestRepo(up.URL))
	req := httptest.NewRequest(http.MethodGet, "/repository/proxy/v2/library/mongo/manifests/8.2.5", nil)
	req.Header.Add("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	req.Header.Add("Accept", dockerIndexType)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, body, w.Body.String())
}

func TestHandler_ProxyManifest_RepairsCorruptDigestObject(t *testing.T) {
	const body = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "sha256:") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", proxyOCIIndexType)
		fmt.Fprint(w, body)
	}))
	defer up.Close()
	repo := proxyManifestRepo(up.URL)
	quota := int64(len(body))
	repo.QuotaBytes = &quota
	r, d := setupWithDeps(repo)
	require.Equal(t, http.StatusOK, manifestRequest(r, http.MethodGet, "8.2.5", "").Code)
	alias, err := d.Assets.GetByPath(context.Background(), repo.Name, "/manifests/library/mongo/"+digest(body))
	require.NoError(t, err)
	require.NoError(t, d.BlobStore.Put(context.Background(), alias.BlobKey, strings.NewReader(`{"corrupt":true}`), 16))
	require.Equal(t, http.StatusOK, manifestRequest(r, http.MethodGet, "8.2.5", "").Code)
	up.Close()
	require.Equal(t, body, manifestRequest(r, http.MethodGet, digest(body), "").Body.String())
	used, err := d.Assets.SumSizeByRepo(context.Background(), repo.Name)
	require.NoError(t, err)
	require.Equal(t, int64(len(body)), used, "repair at full quota must not double charge")
}

func TestHandler_ProxyManifest_RejectsUnverifiableUpstreamBodies(t *testing.T) {
	const body = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`
	for _, condition := range []string{"invalid JSON", "truncated", "wrong media type"} {
		t.Run(condition, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", proxyOCIIndexType)
				switch condition {
				case "invalid JSON":
					fmt.Fprint(w, `{"schemaVersion":`)
					return
				case "truncated":
					w.Header().Set("Content-Length", fmt.Sprint(len(body)+10))
				case "wrong media type":
					w.Header().Set("Content-Type", dockerIndexType)
				}
				fmt.Fprint(w, body)
			}))
			defer up.Close()
			r, d := setupWithDeps(proxyManifestRepo(up.URL))
			got := manifestRequest(r, http.MethodGet, "8.2.5", "")
			require.Equal(t, http.StatusBadGateway, got.Code)
			require.Empty(t, got.Header().Get("Docker-Content-Digest"))
			keys, err := d.BlobStore.ListKeys(context.Background())
			require.NoError(t, err)
			require.Empty(t, keys)
		})
	}
}
