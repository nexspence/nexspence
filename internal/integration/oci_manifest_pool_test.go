//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository/postgres"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

func TestProxyOCI_ManifestSmallPool_RealShape(t *testing.T) {
	for _, tc := range []struct {
		name                                           string
		parallel, mixed, shared, quota, blocked, large bool
	}{
		{name: "blocked_upstream", parallel: true, mixed: true, blocked: true},
		{name: "large_quota", parallel: true, quota: true, large: true},
		{name: "single"}, {name: "same", parallel: true}, {name: "different", parallel: true, mixed: true},
		{name: "shared_digest_quota", parallel: true, mixed: true, shared: true, quota: true},
		{name: "same_quota", parallel: true, quota: true}, {name: "different_quota", parallel: true, mixed: true, quota: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := pgtest.Pool(t)
			ctx := context.Background()
			name := "manifest-pool-" + tc.name
			const ct = "application/vnd.oci.image.index.v1+json"
			bodyFor := func(ref string) string {
				padding := ""
				if tc.large {
					padding = strings.Repeat("x", 5<<20)
				}
				return fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"manifests":[],"annotations":{"version":%q},"padding":%q}`, ct, ref, padding)
			}
			initialBody := func(ref string) string {
				if tc.shared && ref == "two" {
					return bodyFor("one")
				}
				return bodyFor(ref)
			}
			var swapped atomic.Bool
			entered := make(chan struct{}, 32)
			release := make(chan struct{})
			var unblock sync.Once
			defer unblock.Do(func() { close(release) })
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ref, ok := strings.CutPrefix(r.URL.Path, "/v2/library/test/manifests/")
				if !ok || strings.Contains(ref, ":") {
					http.NotFound(w, r)
					return
				}
				if tc.blocked && !swapped.Load() {
					entered <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				// Model a slow registry while concurrent pulls start.
				if ref == "one" && swapped.Load() {
					ref = "new"
				}
				time.Sleep(50 * time.Millisecond)
				w.Header().Set("Content-Type", ct)
				_, _ = io.WriteString(w, initialBody(ref))
			}))
			defer up.Close()
			bs := &domain.BlobStore{Name: name, Type: "local", Config: map[string]any{"path": t.TempDir()}}
			repo := &domain.Repository{Name: name, Format: domain.FormatOCI, Type: domain.TypeProxy, Online: true, AllowAnonymous: true, ProxyConfig: map[string]any{"remote_url": up.URL}, FormatConfig: map[string]any{"pull_block_severity": "critical"}}
			if tc.quota {
				q := int64(len(bodyFor("one")) + len(bodyFor("new")))
				if tc.mixed && !tc.shared {
					q += int64(len(bodyFor("two")))
				}
				bs.QuotaBytes = &q
				repo.QuotaBytes = &q
			}
			blobs := postgres.NewBlobStoreRepo(pool)
			repos := postgres.NewRepositoryRepo(pool)
			require.NoError(t, blobs.Create(ctx, bs))
			repo.BlobStoreID = &bs.ID
			require.NoError(t, repos.Create(ctx, repo))
			t.Cleanup(func() { require.NoError(t, repos.Delete(ctx, name)); require.NoError(t, blobs.Delete(ctx, name)) })
			var urls []string
			var pools []*pgxpool.Pool
			// Independent routers and pools share only PostgreSQL and physical storage.
			for range 2 {
				pc := pool.Config().Copy()
				pc.MaxConns = 2
				pc.MinConns = 0
				small, err := pgxpool.NewWithConfig(ctx, pc)
				require.NoError(t, err)
				t.Cleanup(small.Close)
				pools = append(pools, small)
				cfg := &config.Config{}
				cfg.Auth.JWTSecret = "integration-test-secret-32bytes!!"
				cfg.Auth.JWTExpiryHours = 1
				cfg.Auth.BcryptCost = 4
				cfg.Auth.AnonymousEnabled = true
				cfg.Storage.Local.BasePath = t.TempDir()
				cfg.HTTP.MaxBodyMB = 64
				cfg.Log.Level = "error"
				appCtx, cancel := context.WithCancel(ctx)
				t.Cleanup(cancel)
				router := api.NewRouter(appCtx, cfg, small, logger.New("error", "json"), "manifest-pool-test")
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					timeout := 3 * time.Second
					if tc.large {
						timeout = 30 * time.Second
					}
					requestCtx, stop := context.WithTimeout(r.Context(), timeout)
					defer stop()
					router.ServeHTTP(w, r.WithContext(requestCtx))
				}))
				t.Cleanup(ts.Close)
				urls = append(urls, ts.URL)
			}
			clientTimeout := 5 * time.Second
			if tc.large {
				clientTimeout = 45 * time.Second
			}
			client := &http.Client{Timeout: clientTimeout}
			fetch := func(url, ref, want string) error {
				req, err := http.NewRequest(http.MethodGet, url+"/v2/repository/"+name+"/library/test/manifests/"+ref, nil)
				if err != nil {
					return err
				}
				req.Header.Set("Accept", ct)
				resp, err := client.Do(req)
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				raw, err := io.ReadAll(resp.Body)
				if err != nil {
					return err
				}
				if resp.StatusCode != http.StatusOK || string(raw) != want {
					return fmt.Errorf("pull %s: HTTP %d, body %s", ref, resp.StatusCode, raw)
				}
				if got := resp.Header.Get("Docker-Content-Digest"); got != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(want))) {
					return fmt.Errorf("incorrect digest: %s", got)
				}
				return nil
			}
			n := 1
			if tc.parallel {
				n = 16
				if tc.large {
					n = 4
				}
			}
			results := make(chan error, n)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := range n {
				wg.Go(func() {
					<-start
					ref := "one"
					if tc.mixed && i%2 == 1 {
						ref = "two"
					}
					results <- fetch(urls[i%2], ref, initialBody(ref))
				})
			}
			close(start)
			if tc.blocked {
				// Both instances must reach the blocked registry without retaining
				// either connection of their deliberately small PostgreSQL pools.
				for range 4 {
					select {
					case <-entered:
					case <-time.After(2 * time.Second):
						t.Fatal("manifest pulls did not reach upstream")
					}
				}
				for _, small := range pools {
					queryCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
					var one int
					err := small.QueryRow(queryCtx, "SELECT 1").Scan(&one)
					cancel()
					require.NoError(t, err, "blocked upstream must not monopolize the DB pool")
					require.Equal(t, 1, one)
				}
				unblock.Do(func() { close(release) })
			}
			wg.Wait()
			close(results)
			for err := range results {
				require.NoError(t, err)
			}
			expected := int64(len(bodyFor("one")))
			refs := []string{"one"}
			if tc.mixed {
				refs = append(refs, "two")
				if !tc.shared {
					expected += int64(len(bodyFor("two")))
				}
			}
			for _, ref := range refs {
				sum := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(initialBody(ref))))
				require.NoError(t, fetch(urls[1], sum, initialBody(ref)))
			}
			// Make the tag stale, then race its replacement against pulls of
			// the old digest on independent instances. Both representations
			// must remain reachable, including when quota becomes exactly full.
			swapped.Store(true)
			_, err := pool.Exec(ctx, `UPDATE assets SET last_modified = now() - interval '1 day'
                WHERE repository_id = $1 AND path = '/manifests/library/test/one'`, repo.ID)
			require.NoError(t, err)
			oldDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(bodyFor("one"))))
			changed := make(chan error, n)
			for i := range n {
				wg.Go(func() {
					ref, want := "one", bodyFor("new")
					if i%2 == 1 {
						ref, want = oldDigest, bodyFor("one")
					}
					changed <- fetch(urls[i%2], ref, want)
				})
			}
			wg.Wait()
			close(changed)
			for err := range changed {
				require.NoError(t, err)
			}
			newDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(bodyFor("new"))))
			require.NoError(t, fetch(urls[0], newDigest, bodyFor("new")))
			require.NoError(t, fetch(urls[1], oldDigest, bodyFor("one")))
			expected += int64(len(bodyFor("new")))
			if tc.quota {
				req, err := http.NewRequest(http.MethodGet, urls[0]+"/v2/repository/"+name+"/library/test/manifests/overflow", nil)
				require.NoError(t, err)
				req.Header.Set("Accept", ct)
				resp, err := client.Do(req)
				require.NoError(t, err)
				raw, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				require.NoError(t, err)
				require.Equal(t, http.StatusBadGateway, resp.StatusCode, string(raw))
				require.Contains(t, string(raw), "quota")
				require.NoError(t, fetch(urls[1], oldDigest, bodyFor("one")))
				require.NoError(t, fetch(urls[1], newDigest, bodyFor("new")))
			}
			usage, err := postgres.NewAssetRepo(pool).SumSizeByRepo(ctx, name)
			require.NoError(t, err)
			require.Equal(t, expected, usage)
			stored, err := blobs.Get(ctx, name)
			require.NoError(t, err)
			require.Equal(t, expected, stored.UsedBytes)
		})
	}
}
