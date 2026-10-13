//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Privileged DinD clients are opt-in; every case owns a fresh daemon and data
// directory. Run with NEXSPENCE_TEST_DOCKER_MANIFEST_CLIENTS=1. The host daemon
// is only used to create/remove those disposable containers, never reconfigured.
func TestProxyOCI_ManifestDockerClients_RealShape(t *testing.T) {
	if os.Getenv("NEXSPENCE_TEST_DOCKER_MANIFEST_CLIENTS") != "1" {
		t.Skip("set NEXSPENCE_TEST_DOCKER_MANIFEST_CLIENTS=1 to run isolated Docker 29.2.1 clients")
	}
	_ = server(t)
	for _, tc := range []struct {
		name, image, format string
		containerd          bool
		arch                string
	}{
		{"rootless-containerd", "docker:29.2.1-dind-rootless", "oci", true, ""},
		{"rootless-classic-amd64", "docker:29.2.1-dind-rootless", "docker", false, "amd64"},
		{"rootless-classic-arm64", "docker:29.2.1-dind-rootless", "docker", false, "arm64"},
		{"rootful-containerd", "docker:29.2.1-dind", "docker", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := map[string]struct{ ct, body string }{}
			var descriptors []string
			for _, arch := range []string{"amd64", "arm64"} {
				config := fmt.Sprintf(`{"architecture":%q,"os":"linux","rootfs":{"type":"layers","diff_ids":[]},"config":{},"history":[]}`, arch)
				configSum := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(config)))
				objects["/blobs/"+configSum] = struct{ ct, body string }{"application/octet-stream", config}
				ct := "application/vnd.docker.distribution.manifest.v2+json"
				manifest := fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"config":{"mediaType":"application/vnd.docker.container.image.v1+json","digest":%q,"size":%d},"layers":[]}`, ct, configSum, len(config))
				sum := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(manifest)))
				objects["/manifests/"+sum] = struct{ ct, body string }{ct, manifest}
				descriptors = append(descriptors, fmt.Sprintf(`{"mediaType":%q,"digest":%q,"size":%d,"platform":{"architecture":%q,"os":"linux"}}`, ct, sum, len(manifest), arch))
			}
			const indexCT = "application/vnd.docker.distribution.manifest.list.v2+json"
			index := fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"manifests":[%s]}`, indexCT, strings.Join(descriptors, ","))
			indexSum := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(index)))
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				p := strings.TrimPrefix(r.URL.Path, "/v2/library/mongo")
				if p == "/manifests/8.2.5" {
					w.Header().Set("Content-Type", indexCT)
					fmt.Fprint(w, index)
					return
				}
				if obj, ok := objects[p]; ok {
					w.Header().Set("Content-Type", obj.ct)
					fmt.Fprint(w, obj.body)
					return
				}
				// The variant's own digest deliberately does not exist upstream.
				http.NotFound(w, r)
			}))
			defer up.Close()
			name := "manifest-client-" + tc.name
			createProxyRepo(t, tc.format, name, up.URL)
			baseURL, stop := startManifestProcess(t, t.TempDir())
			defer stop()
			u, err := url.Parse(baseURL)
			require.NoError(t, err)
			registry := "host.docker.internal:" + u.Port()
			containerName := fmt.Sprintf("nexspence-manifest-%s-%d", tc.name, time.Now().UnixNano())
			docker := func(args ...string) (string, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
				return string(out), err
			}
			rootless := strings.Contains(tc.image, "rootless")
			args := []string{"run", "-d", "--privileged", "--name", containerName, "-e", "DOCKER_TLS_CERTDIR="}
			if rootless {
				// Use slirp4netns for the isolated test network: VPNKit's default
				// subnet overlaps Docker Desktop's host.docker.internal address.
				args = append(args, "-e", "DOCKERD_ROOTLESS_ROOTLESSKIT_NET=slirp4netns")
			}
			args = append(args, tc.image, "--insecure-registry="+registry, "--insecure-registry=172.16.0.0/12", fmt.Sprintf("--feature=containerd-snapshotter=%t", tc.containerd))
			if !tc.containerd {
				args = append(args, "--storage-driver=vfs")
			}

			out, err := docker(args...)
			require.NoError(t, err, out)
			t.Cleanup(func() {
				if t.Failed() {
					logs, _ := docker("logs", "--tail", "30", containerName)
					t.Log(logs)
				}
				_, _ = docker("rm", "-fv", containerName)
			})
			cli := []string{"exec", containerName, "docker"}
			if strings.Contains(tc.image, "rootless") {
				cli = append(cli, "-H", "unix:///run/user/1000/docker.sock")
			}
			client := func(args ...string) (string, error) { return docker(append(append([]string(nil), cli...), args...)...) }
			var info string
			require.Eventually(t, func() bool {
				var err error
				info, err = client("info", "--format", `{{.ServerVersion}} {{.Driver}} {{json .DriverStatus}} {{json .SecurityOptions}}`)
				return err == nil
			}, 60*time.Second, time.Second)
			t.Log("active Docker store: " + info)
			require.Contains(t, info, "29.2.1")
			if tc.containerd {
				require.Contains(t, info, "io.containerd.snapshotter.v1")
			} else {
				require.Contains(t, info, "vfs")
				require.NotContains(t, info, "io.containerd.snapshotter.v1")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			loginCmd := exec.CommandContext(ctx, "docker", append([]string{"exec", "-i"}, append(cli[1:], "login", registry, "--username", "admin", "--password-stdin")...)...)
			loginCmd.Stdin = strings.NewReader("admin123\n")
			loginOut, err := loginCmd.CombinedOutput()
			cancel()
			require.NoError(t, err, string(loginOut))
			// Seed via HTTP first: the historical failure specifically used a cache hit.
			token := login(t, "admin", "admin123")
			req, err := http.NewRequest(http.MethodGet, baseURL+"/v2/"+name+"/library/mongo/manifests/8.2.5", nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			resp.Body.Close()
			require.Equal(t, 200, resp.StatusCode)
			arches := []string{"amd64", "arm64"}
			// The classic image store pins one platform to a multi-arch digest.
			// Each platform therefore uses its own fresh daemon/data directory.
			if tc.arch != "" {
				arches = []string{tc.arch}
			}
			for _, arch := range arches {
				for _, ref := range []string{":8.2.5", "@" + indexSum} {
					out, err := client("pull", "--platform", "linux/"+arch, registry+"/"+name+"/library/mongo"+ref)
					require.NoError(t, err, out)
				}
			}
		})
	}
}
