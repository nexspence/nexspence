package npm_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// publishBodyWithManifest builds a publish payload whose version document is a
// full package.json, the way the npm CLI sends it.
func publishBodyWithManifest(pkgName, version string, manifest map[string]any, tgzContent string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(tgzContent))
	body := map[string]any{
		"name":      pkgName,
		"dist-tags": map[string]string{"latest": version},
		"versions":  map[string]any{version: manifest},
		"_attachments": map[string]any{
			pkgName + "-" + version + ".tgz": map[string]any{
				"data":         encoded,
				"content_type": "application/octet-stream",
				"length":       len(tgzContent),
			},
		},
	}
	b, _ := json.Marshal(body)
	return string(b)
}

func publishRaw(t *testing.T, r http.Handler, repoName, pkgName, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+"/"+pkgName,
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}

func getPackument(t *testing.T, r http.Handler, repoName, pkgName string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/repository/"+repoName+"/"+pkgName, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var doc map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	return doc
}

// versionDoc digs versions[ver] out of a packument.
func versionDoc(t *testing.T, packument map[string]any, ver string) map[string]any {
	t.Helper()
	versions, ok := packument["versions"].(map[string]any)
	require.True(t, ok, "packument has no versions object")
	v, ok := versions[ver].(map[string]any)
	require.True(t, ok, "packument has no version %s", ver)
	return v
}

// TestNPM_Publish_PreservesDependencies covers #131: the packument served for a
// published package must carry the dependency lists from the published
// package.json — pnpm resolves from the registry document, not the tarball, and
// fails the install when they are missing.
func TestNPM_Publish_PreservesDependencies(t *testing.T) {
	repo := testutil.SimpleRepo("npm-hosted", "npm")
	r := setup(repo)

	manifest := map[string]any{
		"name":    "fast-glob",
		"version": "3.3.3",
		"dependencies": map[string]any{
			"glob-parent": "^5.1.2",
			"merge2":      "^1.3.0",
		},
		"peerDependencies":     map[string]any{"typescript": "^5.0.0"},
		"optionalDependencies": map[string]any{"fsevents": "^2.3.2"},
		"engines":              map[string]any{"node": ">=8.6.0"},
	}
	publishRaw(t, r, "npm-hosted", "fast-glob", publishBodyWithManifest("fast-glob", "3.3.3", manifest, "tgz-bytes"))

	v := versionDoc(t, getPackument(t, r, "npm-hosted", "fast-glob"), "3.3.3")

	assert.Equal(t, map[string]any{"glob-parent": "^5.1.2", "merge2": "^1.3.0"}, v["dependencies"])
	assert.Equal(t, map[string]any{"typescript": "^5.0.0"}, v["peerDependencies"])
	assert.Equal(t, map[string]any{"fsevents": "^2.3.2"}, v["optionalDependencies"])
	assert.Equal(t, map[string]any{"node": ">=8.6.0"}, v["engines"])
}

// TestNPM_Publish_FillsDistChecksum covers #131: clients verify the tarball
// against dist.shasum/dist.integrity. A manifest that carries neither (npm only
// adds them from the local tarball it built) must be completed from the stored
// blob, or the install fails on an unverifiable package.
func TestNPM_Publish_FillsDistChecksum(t *testing.T) {
	repo := testutil.SimpleRepo("npm-sums", "npm")
	r := setup(repo)

	const tgz = "tgz-bytes"
	manifest := map[string]any{"name": "nodist", "version": "1.0.0"}
	publishRaw(t, r, "npm-sums", "nodist", publishBodyWithManifest("nodist", "1.0.0", manifest, tgz))

	v := versionDoc(t, getPackument(t, r, "npm-sums", "nodist"), "1.0.0")
	dist, ok := v["dist"].(map[string]any)
	require.True(t, ok, "version doc has no dist object")

	sum := sha1.Sum([]byte(tgz))
	assert.Equal(t, hex.EncodeToString(sum[:]), dist["shasum"])
}

// TestNPM_Publish_KeepsPublishedIntegrity covers #131: when the client did send
// dist.integrity/shasum, they are the client's own hashes of the very bytes we
// stored — they must survive the round trip untouched.
func TestNPM_Publish_KeepsPublishedIntegrity(t *testing.T) {
	repo := testutil.SimpleRepo("npm-integrity", "npm")
	r := setup(repo)

	manifest := map[string]any{
		"name":    "withdist",
		"version": "2.0.0",
		"dist": map[string]any{
			"integrity": "sha512-deadbeef==",
			"shasum":    "0123456789abcdef0123456789abcdef01234567",
			"tarball":   "https://registry.npmjs.org/withdist/-/withdist-2.0.0.tgz",
		},
	}
	publishRaw(t, r, "npm-integrity", "withdist", publishBodyWithManifest("withdist", "2.0.0", manifest, "tgz"))

	v := versionDoc(t, getPackument(t, r, "npm-integrity", "withdist"), "2.0.0")
	dist := v["dist"].(map[string]any)
	assert.Equal(t, "sha512-deadbeef==", dist["integrity"])
	assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", dist["shasum"])
	// The tarball URL is ours, never the upstream one the client sent.
	assert.Equal(t, "http://localhost:8080/repository/npm-integrity/withdist/-/withdist-2.0.0.tgz",
		dist["tarball"])
}

// TestNPM_Metadata_HidesCachedPackumentSentinel covers #131: a proxy caches the
// upstream packument as the component version "metadata". When such a row ends
// up in a hosted repository (import, replication) it must not be advertised as
// a package version — "metadata" is not a version and resolvers choke on it.
func TestNPM_Metadata_HidesCachedPackumentSentinel(t *testing.T) {
	repo := testutil.SimpleRepo("npm-sentinel", "npm")
	r, comps := setupWithComponents(repo)

	publishRaw(t, r, "npm-sentinel", "fast-glob",
		publishBodyWithManifest("fast-glob", "3.3.3",
			map[string]any{"name": "fast-glob", "version": "3.3.3"}, "tgz"))
	require.NoError(t, comps.Create(context.Background(), &domain.Component{
		Repository: "npm-sentinel", Format: "npm", Name: "fast-glob", Version: "metadata",
	}))

	packument := getPackument(t, r, "npm-sentinel", "fast-glob")
	versions := packument["versions"].(map[string]any)

	assert.Contains(t, versions, "3.3.3")
	assert.NotContains(t, versions, "metadata")
	assert.Equal(t, "3.3.3", packument["dist-tags"].(map[string]any)["latest"])
}

// npmTarball builds a package tarball with the given package.json, the layout
// npm produces: every entry under a "package/" prefix.
func npmTarball(t *testing.T, manifest map[string]any) string {
	t.Helper()
	pkgJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: "package/package.json", Mode: 0o644, Size: int64(len(pkgJSON)),
	}))
	_, err = tw.Write(pkgJSON)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.String()
}

// publishBodyWithoutManifest mimics a publish that carried no version document,
// which is what every package stored before #131 was fixed looks like.
func publishBodyWithoutManifest(pkgName, version, tgzContent string) string {
	body := map[string]any{
		"name":      pkgName,
		"dist-tags": map[string]string{"latest": version},
		"_attachments": map[string]any{
			pkgName + "-" + version + ".tgz": map[string]any{
				"data":         base64.StdEncoding.EncodeToString([]byte(tgzContent)),
				"content_type": "application/octet-stream",
				"length":       len(tgzContent),
			},
		},
	}
	b, _ := json.Marshal(body)
	return string(b)
}

// TestNPM_Metadata_BackfillsManifestFromTarball covers #131: versions published
// before the manifest was persisted must not stay broken until someone
// republishes them — the package.json inside the stored tarball is the same
// document, so the packument is completed from there.
func TestNPM_Metadata_BackfillsManifestFromTarball(t *testing.T) {
	repo := testutil.SimpleRepo("npm-legacy", "npm")
	r := setup(repo)

	tgz := npmTarball(t, map[string]any{
		"name":         "legacy",
		"version":      "1.0.0",
		"dependencies": map[string]any{"glob-parent": "^5.1.2"},
		"engines":      map[string]any{"node": ">=8.6.0"},
	})
	publishRaw(t, r, "npm-legacy", "legacy", publishBodyWithoutManifest("legacy", "1.0.0", tgz))

	v := versionDoc(t, getPackument(t, r, "npm-legacy", "legacy"), "1.0.0")

	assert.Equal(t, map[string]any{"glob-parent": "^5.1.2"}, v["dependencies"])
	assert.Equal(t, map[string]any{"node": ">=8.6.0"}, v["engines"])
}

// TestNPM_Metadata_BackfillIsCached covers #131: the extracted manifest is
// written back to the component, so the tarball is opened once and not on every
// packument request.
func TestNPM_Metadata_BackfillIsCached(t *testing.T) {
	repo := testutil.SimpleRepo("npm-legacy-cache", "npm")
	r, comps := setupWithComponents(repo)

	tgz := npmTarball(t, map[string]any{
		"name": "legacy", "version": "1.0.0",
		"dependencies": map[string]any{"glob-parent": "^5.1.2"},
	})
	publishRaw(t, r, "npm-legacy-cache", "legacy", publishBodyWithoutManifest("legacy", "1.0.0", tgz))
	getPackument(t, r, "npm-legacy-cache", "legacy")

	page, err := comps.Search(context.Background(), domain.SearchParams{
		Repository: "npm-legacy-cache", Name: "legacy", Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	manifest, ok := page.Items[0].Extra["npm_manifest"].(map[string]any)
	require.True(t, ok, "manifest was not cached on the component: %v", page.Items[0].Extra)
	assert.Equal(t, map[string]any{"glob-parent": "^5.1.2"}, manifest["dependencies"])
}
