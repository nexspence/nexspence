//go:build integration

package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sha256Digest(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// registryReq issues a /v2/ request with the admin JWT and returns the
// response; the caller owns the body.
func registryReq(t *testing.T, method, path, contentType string, body []byte, token string) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, server(t).URL+path, r)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// pushBlob runs the two-step upload a docker client does: POST a session,
// PUT the bytes to its Location with the digest.
func pushBlob(t *testing.T, repo, image string, content []byte, token string) string {
	t.Helper()
	start := registryReq(t, http.MethodPost, "/v2/"+repo+"/"+image+"/blobs/uploads/", "", nil, token)
	start.Body.Close()
	require.Equal(t, http.StatusAccepted, start.StatusCode)
	loc := start.Header.Get("Location")
	require.NotEmpty(t, loc)
	digest := sha256Digest(content)
	put := registryReq(t, http.MethodPut, loc+"?digest="+digest, "application/octet-stream", content, token)
	raw, _ := io.ReadAll(put.Body)
	put.Body.Close()
	require.Equal(t, http.StatusCreated, put.StatusCode, "finalize blob: %s", raw)
	return digest
}

func createDockerHosted(t *testing.T, name, token string) {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"online":true,"storage":{"blobStoreName":"default","strictContentTypeValidation":false}}`, name)
	resp := authReq(t, http.MethodPost, "/service/rest/v1/repositories/docker/hosted", strings.NewReader(body), token)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create %s: %s", name, raw)
	t.Cleanup(func() {
		d := authReq(t, http.MethodDelete, "/service/rest/v1/repositories/"+name, nil, token)
		d.Body.Close()
	})
}

// #541, real shape: an image pushed through the registry API into docker-a
// and promoted by its tag alone is pullable from docker-b — the tag, the
// manifest by digest, the config and every layer answer 200 with the bytes
// their digests name. The service test covers the expansion logic; this one
// pins that its idea of the storage layout matches the OCI handler's.
func TestPromoteDockerTag_RealShape(t *testing.T) {
	token := login(t, "admin", "admin123")
	createDockerHosted(t, "promo-docker-a", token)
	createDockerHosted(t, "promo-docker-b", token)

	const image = "team/app"
	cfg := pushBlob(t, "promo-docker-a", image, []byte(`{"architecture":"amd64","os":"linux"}`), token)
	l1 := pushBlob(t, "promo-docker-a", image, []byte("layer one bytes"), token)
	l2 := pushBlob(t, "promo-docker-a", image, []byte("layer two bytes"), token)
	manifest := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json",`+
		`"config":{"mediaType":"application/vnd.docker.container.image.v1+json","size":37,"digest":%q},`+
		`"layers":[{"mediaType":"application/vnd.docker.image.rootfs.diff.tar.gzip","size":15,"digest":%q},`+
		`{"mediaType":"application/vnd.docker.image.rootfs.diff.tar.gzip","size":15,"digest":%q}]}`, cfg, l1, l2))
	manifestDigest := sha256Digest(manifest)
	mresp := registryReq(t, http.MethodPut, "/v2/promo-docker-a/"+image+"/manifests/1.2.3",
		"application/vnd.docker.distribution.manifest.v2+json", manifest, token)
	raw, _ := io.ReadAll(mresp.Body)
	mresp.Body.Close()
	require.Equal(t, http.StatusCreated, mresp.StatusCode, "push manifest: %s", raw)

	// The tag component, as Browse → Tags would hand it to Promote.
	cresp := authReq(t, http.MethodGet, "/service/rest/v1/search?repository=promo-docker-a&version=1.2.3", nil, token)
	var page struct {
		Items []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(cresp.Body).Decode(&page))
	cresp.Body.Close()
	tagID := ""
	for _, it := range page.Items {
		if it.Version == "1.2.3" {
			tagID = it.ID
		}
	}
	require.NotEmpty(t, tagID, "tag component not found: %+v", page.Items)

	rresp := authReq(t, http.MethodPost, "/api/v1/promotion/rules",
		strings.NewReader(`{"name":"promo-docker-a-to-b","from_repo":"promo-docker-a","to_repo":"promo-docker-b"}`), token)
	raw, _ = io.ReadAll(rresp.Body)
	rresp.Body.Close()
	require.Equal(t, http.StatusCreated, rresp.StatusCode, "create rule: %s", raw)
	var rule struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(raw, &rule))
	t.Cleanup(func() {
		d := authReq(t, http.MethodDelete, "/api/v1/promotion/rules/"+rule.ID, nil, token)
		d.Body.Close()
	})

	presp := authReq(t, http.MethodPost, "/api/v1/promotion/promote",
		strings.NewReader(fmt.Sprintf(`{"rule_id":%q,"component_ids":[%q]}`, rule.ID, tagID)), token)
	raw, _ = io.ReadAll(presp.Body)
	presp.Body.Close()
	require.Equal(t, http.StatusOK, presp.StatusCode, "promote: %s", raw)
	var promoted struct {
		Requests []struct {
			Status             string `json:"status"`
			Error              string `json:"error"`
			IncludedComponents int    `json:"included_components"`
		} `json:"requests"`
	}
	require.NoError(t, json.Unmarshal(raw, &promoted))
	require.Len(t, promoted.Requests, 1)
	require.Equal(t, "completed", promoted.Requests[0].Status, "promotion error: %s", promoted.Requests[0].Error)
	assert.Equal(t, 4, promoted.Requests[0].IncludedComponents, "digest alias + config + 2 layers")

	// What `docker pull promo-docker-b/team/app:1.2.3` does: the tag, the
	// manifest by digest, then every blob it names.
	for _, ref := range []string{"1.2.3", manifestDigest} {
		resp := registryReq(t, http.MethodGet, "/v2/promo-docker-b/"+image+"/manifests/"+ref, "", nil, token)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "manifest %s: %s", ref, body)
		assert.Equal(t, manifestDigest, resp.Header.Get("Docker-Content-Digest"), "manifest %s", ref)
		assert.Equal(t, manifest, body, "manifest %s", ref)
	}
	for _, d := range []string{cfg, l1, l2} {
		resp := registryReq(t, http.MethodGet, "/v2/promo-docker-b/"+image+"/blobs/"+d, "", nil, token)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "blob %s", d)
		assert.Equal(t, d, sha256Digest(body), "blob %s bytes", d)
	}

	// Promoting again is a no-op for the content-addressed parts, not a failure.
	again := authReq(t, http.MethodPost, "/api/v1/promotion/promote",
		strings.NewReader(fmt.Sprintf(`{"rule_id":%q,"component_ids":[%q]}`, rule.ID, tagID)), token)
	raw, _ = io.ReadAll(again.Body)
	again.Body.Close()
	require.Equal(t, http.StatusOK, again.StatusCode, "re-promote: %s", raw)
	assert.Contains(t, string(raw), `"status":"completed"`)
}
