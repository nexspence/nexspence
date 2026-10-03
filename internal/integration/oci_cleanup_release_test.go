//go:build integration

package integration

import (
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

func ociDo(t *testing.T, token, method, p, ct, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, server(t).URL+p, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// ociPushBlob uploads content as a blob of image and returns its digest.
func ociPushBlob(t *testing.T, token, repo, image, content string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(content))
	dgst := "sha256:" + hex.EncodeToString(sum[:])
	resp := ociDo(t, token, http.MethodPost, "/repository/"+repo+"/v2/"+image+"/blobs/uploads/", "", "")
	resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	loc := resp.Header.Get("Location")
	resp = ociDo(t, token, http.MethodPut, "/repository/"+repo+strings.TrimPrefix(loc, "/repository/"+repo)+"?digest="+dgst,
		"application/octet-stream", content)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	return dgst
}

func ociManifest(config string, layers ...string) string {
	s := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":1},"layers":[`, config)
	for i, l := range layers {
		if i > 0 {
			s += ","
		}
		s += fmt.Sprintf(`{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":%q,"size":1}`, l)
	}
	return s + "]}"
}

func ociStatus(t *testing.T, token, p string) int {
	t.Helper()
	resp := ociDo(t, token, http.MethodGet, p, "", "")
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// #618: retention expires an old tag; once the run is over, the tag's digest
// alias and the blobs only it used are released, while the layer the
// surviving tag shares stays and the surviving image still pulls.
func TestCleanup_RetentionReleasesExpiredImageLayers_RealShape(t *testing.T) {
	createHostedRepo(t, "docker", "oci-release", `{}`)
	token := login(t, "admin", "admin123")
	repo, image := "oci-release", "app"

	base := ociPushBlob(t, token, repo, image, "base-layer")
	l1 := ociPushBlob(t, token, repo, image, "layer-1")
	c1 := ociPushBlob(t, token, repo, image, "config-1")
	m1 := ociManifest(c1, base, l1)
	resp := ociDo(t, token, http.MethodPut, "/repository/"+repo+"/v2/"+image+"/manifests/v1", "application/vnd.oci.image.manifest.v1+json", m1)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	d1 := resp.Header.Get("Docker-Content-Digest")

	l2 := ociPushBlob(t, token, repo, image, "layer-2")
	c2 := ociPushBlob(t, token, repo, image, "config-2")
	resp = ociDo(t, token, http.MethodPut, "/repository/"+repo+"/v2/"+image+"/manifests/v2", "application/vnd.oci.image.manifest.v1+json", ociManifest(c2, base, l2))
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	body := fmt.Sprintf(`{"name":"oci-release-p","format":"docker","criteria":{},"retainNVersions":1,"scope":{"repositoryName":%q}}`, repo)
	resp = authReq(t, http.MethodPost, "/service/rest/v1/cleanup-policies", strings.NewReader(body), token)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Less(t, resp.StatusCode, 300, "%s", raw)
	var policy struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(raw, &policy))
	t.Cleanup(func() {
		d := authReq(t, http.MethodDelete, "/service/rest/v1/cleanup-policies/"+policy.ID, nil, token)
		d.Body.Close()
	})
	resp = authReq(t, http.MethodPost, "/service/rest/v1/cleanup-policies/"+policy.ID+"/run", nil, token)
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "%s", raw)

	v2 := "/repository/" + repo + "/v2/" + image
	assert.Equal(t, http.StatusNotFound, ociStatus(t, token, v2+"/manifests/v1"), "the expired tag")
	assert.Equal(t, http.StatusNotFound, ociStatus(t, token, v2+"/manifests/"+d1), "its digest alias")
	assert.Equal(t, http.StatusNotFound, ociStatus(t, token, v2+"/blobs/"+l1), "its own layer")
	assert.Equal(t, http.StatusNotFound, ociStatus(t, token, v2+"/blobs/"+c1), "its config")
	assert.Equal(t, http.StatusOK, ociStatus(t, token, v2+"/manifests/v2"))
	for _, b := range []string{base, l2, c2} {
		assert.Equal(t, http.StatusOK, ociStatus(t, token, v2+"/blobs/"+b), b)
	}
}
