package oci_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// Delegate every operation except the freshness update to the shared mock.
type manifestRefreshFailAssets struct {
	repository.AssetRepo
	calls atomic.Int32
	err   error
}

func (a *manifestRefreshFailAssets) TouchLastModified(_ context.Context, _ string) error {
	a.calls.Add(1)
	return a.err
}

func TestHandler_ProxyManifest_TouchFailureServesRevalidatedCache(t *testing.T) {
	const body = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, condition := range []string{"none", "matching", "nonmatching"} {
			t.Run(method+"/"+condition, func(t *testing.T) {
				ctx := context.Background()
				stale := time.Now().Add(-2 * time.Minute).Truncate(time.Second)
				var hits atomic.Int32
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if req.URL.Path != "/v2/library/mongo/manifests/8.2.5" || req.Method != http.MethodGet {
						t.Errorf("unexpected upstream request: %s %s", req.Method, req.URL.Path)
						http.NotFound(w, req)
						return
					}
					if req.Header.Get("Accept") != proxyOCIIndexType {
						t.Error("upstream Accept does not match the cached representation")
					}
					if hits.Add(1) > 1 {
						if got := req.Header.Get("If-Modified-Since"); got != stale.UTC().Format(http.TimeFormat) {
							t.Errorf("If-Modified-Since = %q, want unchanged stale timestamp", got)
						}
						w.WriteHeader(http.StatusNotModified)
						return
					}
					w.Header().Set("Content-Type", proxyOCIIndexType)
					fmt.Fprint(w, body)
				}))
				defer up.Close()
				repo := proxyManifestRepo(up.URL)
				repo.ProxyConfig["metadata_max_age"] = 60 // seconds
				r, d := setupWithDeps(repo)
				fill := manifestRequest(r, http.MethodGet, "8.2.5", proxyOCIIndexType)
				require.Equal(t, http.StatusOK, fill.Code, fill.Body.String())
				require.Equal(t, body, fill.Body.String())
				cached, err := d.Assets.GetByPath(ctx, repo.Name, "/manifests/library/mongo/8.2.5")
				require.NoError(t, err)
				// Expire the shared mock's persisted timestamp without a timing-dependent sleep.
				cached.LastModified = stale
				assets := &manifestRefreshFailAssets{AssetRepo: d.Assets, err: errors.New("freshness update unavailable")}
				d.Assets = assets
				r = manifestRouter(d)

				core, logs := observer.New(zap.WarnLevel)
				repoproxy.SetLogger(zap.New(core).Sugar())
				t.Cleanup(func() { repoproxy.SetLogger(nil) })
				span := trace.NewSpanContext(trace.SpanContextConfig{
					TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled,
				})
				for attempt := range 2 {
					req := httptest.NewRequest(method, "/repository/proxy/v2/library/mongo/manifests/8.2.5", nil)
					req = req.WithContext(trace.ContextWithSpanContext(ctx, span))
					req.Header.Set("Accept", proxyOCIIndexType)
					status := http.StatusOK
					switch condition {
					case "matching":
						req.Header.Set("If-None-Match", "W/"+fill.Header().Get("ETag"))
						status = http.StatusNotModified
					case "nonmatching":
						req.Header.Set("If-None-Match", `"other"`)
					}
					got := httptest.NewRecorder()
					r.ServeHTTP(got, req)
					require.Equal(t, status, got.Code, got.Body.String())
					require.Equal(t, digest(body), got.Header().Get("Docker-Content-Digest"))
					require.Equal(t, proxyOCIIndexType, got.Header().Get("Content-Type"))
					require.Equal(t, fill.Header().Get("ETag"), got.Header().Get("ETag"))
					if status == http.StatusNotModified {
						require.Empty(t, got.Header().Get("Content-Length"))
					} else {
						require.Equal(t, fmt.Sprint(len(body)), got.Header().Get("Content-Length"))
					}
					if method == http.MethodGet && status == http.StatusOK {
						require.Equal(t, []byte(body), got.Body.Bytes())
					} else {
						require.Empty(t, got.Body.Bytes())
					}
					stored, err := d.Assets.GetByPath(ctx, repo.Name, cached.Path)
					require.NoError(t, err)
					require.Equal(t, stale, stored.LastModified)
					require.Equal(t, int32(attempt+2), hits.Load(), "each stale request must revalidate")
					require.Equal(t, int32(attempt+1), assets.calls.Load())
				}
				require.Equal(t, 2, logs.Len())
				for _, entry := range logs.All() {
					fields := entry.ContextMap()
					require.Equal(t, span.TraceID().String(), fields["trace_id"])
					require.Equal(t, span.SpanID().String(), fields["span_id"])
					require.Equal(t, assets.err.Error(), fields["error"])
				}
			})
		}
	}
}
