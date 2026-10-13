package oci_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
)

const dockerIndexType = "application/vnd.docker.distribution.manifest.list.v2+json"
const proxyOCIIndexType = "application/vnd.oci.image.index.v1+json"

func manifestRouter(d formats.Deps) *gin.Engine {
	r := gin.New()
	h := oci.New(d)
	r.Any("/repository/:repoName/*path", h.ServeHTTP)
	return r
}

func manifestRequest(r *gin.Engine, method, ref, accept string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/repository/proxy/v2/library/mongo/manifests/"+ref, nil)
	req.Header.Set("Accept", accept)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestHandler_ProxyManifest_TagDigestConsistency(t *testing.T) {
	for _, format := range []domain.RepoFormat{domain.FormatDocker, domain.FormatOCI} {
		for _, first := range []string{dockerIndexType, proxyOCIIndexType} {
			t.Run(string(format)+"/"+first, func(t *testing.T) {
				var hits atomic.Int32
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					hits.Add(1)
					if req.URL.Path != "/v2/library/mongo/manifests/8.2.5" {
						http.NotFound(w, req)
						return
					}
					ct := req.Header.Get("Accept")
					w.Header().Set("Content-Type", ct)
					// A registry variant unavailable by its computed digest upstream.
					w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("f", 64))
					fmt.Fprintf(w, `{"schemaVersion":2,"mediaType":%q,"manifests":[]}`, ct)
				}))
				defer up.Close()
				repo := &domain.Repository{ID: "proxy", Name: "proxy", Type: domain.TypeProxy, Format: format, ProxyConfig: map[string]any{"remote_url": up.URL}}
				r, d := setupWithDeps(repo)
				other := proxyOCIIndexType
				if first == proxyOCIIndexType {
					other = dockerIndexType
				}
				for _, ct := range []string{first, other} {
					tag := manifestRequest(r, http.MethodGet, "8.2.5", ct)
					require.Equal(t, 200, tag.Code, tag.Body.String())
					require.Equal(t, ct, tag.Header().Get("Content-Type"))
					sum := digest(tag.Body.String())
					require.Equal(t, sum, tag.Header().Get("Docker-Content-Digest"))
					require.Equal(t, "Accept", tag.Header().Get("Vary"))
					before := hits.Load()
					// A fresh handler shares only persistent dependencies, never local aliases.
					pinned := manifestRequest(manifestRouter(d), http.MethodGet, sum, ct)
					require.Equal(t, 200, pinned.Code, pinned.Body.String())
					require.Equal(t, tag.Body.String(), pinned.Body.String())
					require.Equal(t, before, hits.Load(), "digest must resolve locally")
					head := manifestRequest(r, http.MethodHead, "8.2.5", ct)
					require.Equal(t, 200, head.Code)
					require.Equal(t, sum, head.Header().Get("Docker-Content-Digest"))
					require.Equal(t, fmt.Sprint(tag.Body.Len()), head.Header().Get("Content-Length"))
					require.Empty(t, head.Body.String())
				}
				// Neither representation is destroyed by replacing the negotiated tag.
				for _, ct := range []string{first, other} {
					body := fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"manifests":[]}`, ct)
					require.Equal(t, body, manifestRequest(r, http.MethodGet, digest(body), ct).Body.String())
				}
				used, err := d.Assets.SumSizeByRepo(context.Background(), "proxy")
				require.NoError(t, err)
				keys, err := d.BlobStore.ListKeys(context.Background())
				require.NoError(t, err)
				require.Len(t, keys, 2, "tag and digest share immutable objects")
				require.Positive(t, used)
			})
		}
	}
}
