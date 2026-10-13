//go:build integration

package storage_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Reuse the real MinIO and Azurite fixtures from the storage protocol tests.
// Fresh registry/handler instances must read the immutable objects written by
// the previous instances, including when a group selects a different member.
func TestOCIProxyManifest_PhysicalStoresAndPinning(t *testing.T) {
	oldClient := repoproxy.UpstreamClient
	repoproxy.UpstreamClient = &http.Client{}
	defer func() { repoproxy.UpstreamClient = oldClient }()
	_ = minioPool(t)
	_ = azuriteStore(t)
	for _, kind := range []string{"local", "s3", "azure", "group"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			local := &domain.BlobStore{ID: "local", Name: "local", Type: "local", Config: map[string]any{"path": t.TempDir()}}
			s3 := &domain.BlobStore{ID: "s3", Name: "s3", Type: "s3", Config: map[string]any{"bucket": minioBucket, "endpoint": minioEndpoint, "region": "us-east-1", "access_key": minioUser, "secret_key": minioPass, "force_path_style": true}}
			azure := &domain.BlobStore{ID: "azure", Name: "azure", Type: "azure", Config: map[string]any{"container": azuriteContainer, "account_name": azuriteAccount, "account_key": azuriteKey, "endpoint": azuriteSvcURL}}
			group := &domain.BlobStore{ID: "group", Name: "group", Type: "group", Config: map[string]any{"member_ids": []string{"s3", "azure"}, "fill_policy": "round_robin"}}
			blobs := testutil.NewBlobStoreRepo(local, s3, azure, group)
			assets := testutil.NewAssetRepo()
			comps := testutil.NewComponentRepo()
			comps.HasAssets = assets.HasComponent
			fallback, err := storage.NewLocalBlobStore(t.TempDir())
			require.NoError(t, err)
			var second atomic.Bool
			const ct = "application/vnd.oci.image.index.v1+json"
			const first = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`
			const next = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[],"annotations":{"next":"true"}}`
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v2/library/mongo/manifests/8.2.5" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", ct)
				if second.Load() {
					fmt.Fprint(w, next)
				} else {
					fmt.Fprint(w, first)
				}
			}))
			defer up.Close()
			repo := &domain.Repository{ID: "repo", Name: "oci-persist-" + kind, Type: domain.TypeProxy, Format: domain.FormatOCI, BlobStoreID: &kind, ProxyConfig: map[string]any{"remote_url": up.URL, "metadata_max_age": 0.000000001}}
			d := formats.Deps{Repos: testutil.NewRepoRepo(repo), Blobs: blobs, Assets: assets, Components: comps, BlobStore: fallback, Registry: storage.NewRegistry(fallback)}
			pull := func(ref string) *httptest.ResponseRecorder {
				t.Helper()
				h := oci.New(d)
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = req
				c.Params = gin.Params{{Key: "repoName", Value: repo.Name}, {Key: "path", Value: "/v2/library/mongo/manifests/" + ref}}
				h.ServeHTTP(c)
				require.Equal(t, 200, w.Code, w.Body.String())
				return w
			}
			a := pull("8.2.5")
			sum := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(first)))
			require.Equal(t, sum, a.Header().Get("Docker-Content-Digest"))
			// Group round-robin must not independently assign the tag's alias.
			tag, err := assets.GetByPath(ctx, repo.Name, "/manifests/library/mongo/8.2.5")
			require.NoError(t, err)
			alias, err := assets.GetByPath(ctx, repo.Name, "/manifests/library/mongo/"+sum)
			require.NoError(t, err)
			require.Equal(t, tag.BlobStoreID, alias.BlobStoreID)
			require.Equal(t, tag.BlobKey, alias.BlobKey)
			second.Store(true)
			require.Equal(t, next, pull("8.2.5").Body.String())
			up.Close()
			d.Registry = storage.NewRegistry(fallback)
			require.Equal(t, first, pull(sum).Body.String())
			require.Equal(t, next, pull(fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(next)))).Body.String())
			usage, err := assets.SumSizeByRepo(ctx, repo.Name)
			require.NoError(t, err)
			require.Equal(t, int64(len(first)+len(next)), usage)
			stores, err := blobs.List(ctx)
			require.NoError(t, err)
			var counted int64
			for _, bs := range stores {
				counted += bs.UsedBytes
			}
			require.Equal(t, usage, counted, "aliases must not double physical usage")
			// Teardown also proves deleting the tag never removes a referenced digest.
			require.NoError(t, base.DeleteArtifact(ctx, d, repo.Name, "/manifests/library/mongo/8.2.5"))
			require.Equal(t, first, pull(sum).Body.String())
			for _, ref := range []string{sum, fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(next)))} {
				require.NoError(t, base.DeleteArtifact(ctx, d, repo.Name, "/manifests/library/mongo/"+ref))
			}
		})
	}
}
