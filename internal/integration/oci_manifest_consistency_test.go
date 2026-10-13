//go:build integration

package integration

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
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

// This helper runs in a separate OS process. Its only durable state is the
// shared Postgres database and the parent's temporary local blob directory.
func TestOCIManifestPersistenceHelper(t *testing.T) {
	raw := os.Getenv("NEXSPENCE_TEST_MANIFEST_PROCESS")
	if raw == "" {
		return
	}
	var settings struct{ DSN, Storage, AliasRepo string }
	require.NoError(t, json.Unmarshal([]byte(raw), &settings))
	pool, err := pgxpool.New(context.Background(), settings.DSN)
	require.NoError(t, err)
	defer pool.Close()
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "integration-test-secret-32bytes!!"
	cfg.Auth.JWTExpiryHours = 1
	cfg.Auth.BcryptCost = 4
	cfg.Auth.AnonymousEnabled = true
	cfg.Storage.Local.BasePath = settings.Storage
	cfg.HTTP.MaxBodyMB = 64
	cfg.Log.Level = "error"
	if settings.AliasRepo != "" {
		cfg.Docker.SubdomainConnector.Enabled = true
		cfg.Docker.SubdomainConnector.Aliases = map[string]string{"manifest.alias.test": settings.AliasRepo}
	}
	router := api.NewRouter(context.Background(), cfg, pool, logger.New("error", "json"), "manifest-persistence-test")
	ts := httptest.NewServer(router)
	defer ts.Close()
	fmt.Println("MANIFEST_TEST_URL=" + ts.URL)
	// The parent closes stdin to stop this process and its router cleanly.
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func startManifestProcess(t *testing.T, storageDir string, aliasRepo ...string) (string, func()) {
	t.Helper()
	alias := ""
	if len(aliasRepo) > 0 {
		alias = aliasRepo[0]
	}
	settings, err := json.Marshal(struct{ DSN, Storage, AliasRepo string }{pgtest.Pool(t).Config().ConnString(), storageDir, alias})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOCIManifestPersistenceHelper$")
	cmd.Env = append(os.Environ(), "NEXSPENCE_TEST_MANIFEST_PROCESS="+string(settings))
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	done := false
	stop := func() {
		if done {
			return
		}
		done = true
		_ = stdin.Close()
		require.NoError(t, cmd.Wait())
		cancel()
	}
	t.Cleanup(stop)
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if url, ok := strings.CutPrefix(scanner.Text(), "MANIFEST_TEST_URL="); ok {
			return url, stop
		}
	}
	cancel()
	t.Fatalf("manifest server process did not start: %v", scanner.Err())
	return "", stop
}

func TestProxyOCI_ManifestPersistence_RealShape(t *testing.T) {
	_ = server(t) // Initialize the real database, admin credentials and migrations.
	for _, format := range []string{"docker", "oci"} {
		t.Run(format, func(t *testing.T) {
			const ct = "application/vnd.oci.image.index.v1+json"
			const body = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`
			sum := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(body)))
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v2/library/mongo/manifests/8.2.5" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", ct)
				w.Header().Set("Docker-Content-Digest", "sha256:wrong")
				fmt.Fprint(w, body)
			}))
			defer up.Close()
			name := "manifest-persistence-" + format
			createProxyRepo(t, format, name, up.URL)
			storageDir := t.TempDir()
			pool := pgtest.Pool(t)
			blobs := postgres.NewBlobStoreRepo(pool)
			bs := &domain.BlobStore{Name: name, Type: "local", Config: map[string]any{"path": storageDir}}
			require.NoError(t, blobs.Create(context.Background(), bs))
			t.Cleanup(func() {
				_ = postgres.NewRepositoryRepo(pool).Delete(context.Background(), name)
				_ = blobs.Delete(context.Background(), bs.Name)
			})
			repos := postgres.NewRepositoryRepo(pool)
			repo, err := repos.Get(context.Background(), name)
			require.NoError(t, err)
			repo.BlobStoreID = &bs.ID
			require.NoError(t, repos.Update(context.Background(), repo))
			baseURL, stop := startManifestProcess(t, storageDir, name)
			token := login(t, "admin", "admin123")
			fetch := func(baseURL, method, path string, host ...string) *http.Response {
				t.Helper()
				req, err := http.NewRequest(method, baseURL+path, nil)
				require.NoError(t, err)
				if len(host) > 0 {
					req.Host = host[0]
				}
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Accept", ct)
				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				return resp
			}
			for _, prefix := range []string{"/v2/" + name + "/library/mongo/manifests/", "/v2/repository/" + name + "/library/mongo/manifests/", "/repository/" + name + "/manifests/library/mongo/"} {
				resp := fetch(baseURL, http.MethodGet, prefix+"8.2.5")
				raw, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				require.NoError(t, err)
				require.Equal(t, 200, resp.StatusCode, string(raw))
				require.Equal(t, body, string(raw))
				require.Equal(t, sum, resp.Header.Get("Docker-Content-Digest"))
			}
			// Host aliases reach the same handler, and new local digest paths remain
			// subject to the repository's anonymous-access policy.
			aliasResponse := fetch(baseURL, http.MethodGet, "/v2/library/mongo/manifests/"+sum, "manifest.alias.test")
			aliasBody, err := io.ReadAll(aliasResponse.Body)
			aliasResponse.Body.Close()
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, aliasResponse.StatusCode)
			require.Equal(t, body, string(aliasBody))
			anonymous := func() *http.Response {
				t.Helper()
				resp, err := http.Get(baseURL + "/v2/" + name + "/library/mongo/manifests/" + sum)
				require.NoError(t, err)
				return resp
			}
			refused := anonymous()
			refused.Body.Close()
			require.NotEqual(t, http.StatusOK, refused.StatusCode)
			repo.AllowAnonymous = true
			require.NoError(t, repos.Update(context.Background(), repo))
			allowed := anonymous()
			allowed.Body.Close()
			require.Equal(t, http.StatusOK, allowed.StatusCode)
			repo.AllowAnonymous = false
			require.NoError(t, repos.Update(context.Background(), repo))

			stop()
			up.Close() // A real restart must serve the alias with no upstream available.
			baseURL, stop = startManifestProcess(t, storageDir, name)
			defer stop()
			for _, ref := range []string{"8.2.5", sum} {
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					resp := fetch(baseURL, method, "/v2/"+name+"/library/mongo/manifests/"+ref)
					raw, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					require.NoError(t, err)
					require.Equal(t, 200, resp.StatusCode, string(raw))
					require.Equal(t, sum, resp.Header.Get("Docker-Content-Digest"))
					require.Equal(t, ct, resp.Header.Get("Content-Type"))
					require.Equal(t, int64(len(body)), resp.ContentLength)
					if method == http.MethodGet {
						require.Equal(t, body, string(raw))
					} else {
						require.Empty(t, raw)
					}
				}
			}
		})
	}
}
