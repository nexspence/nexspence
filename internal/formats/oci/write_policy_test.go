package oci_test

import (
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

func manifestBody(tag string) string {
	return `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","annotations":{"t":"` + tag + `"}}`
}

func putManifest(r http.Handler, repo, image, ref, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repo+"/v2/"+image+"/manifests/"+ref, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func fetchManifest(r http.Handler, repo, image, ref string) string {
	req := httptest.NewRequest(http.MethodGet, "/repository/"+repo+"/v2/"+image+"/manifests/"+ref, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Body.String()
}

func requireDenied(t *testing.T, w *httptest.ResponseRecorder, msg string) {
	t.Helper()
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var body struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
	require.Len(t, body.Errors, 1)
	assert.Equal(t, "DENIED", body.Errors[0].Code)
	assert.Equal(t, msg, body.Errors[0].Message)
}

func dockerRepo(name string, cfg map[string]any) *domain.Repository {
	repo := testutil.SimpleRepo(name, "docker")
	repo.FormatConfig = cfg
	return repo
}

func TestOCI_AllowOnce_TagRepushIsDenied(t *testing.T) {
	r := setup(dockerRepo("reg-once", map[string]any{domain.WritePolicyKey: "allow_once"}))

	v1 := manifestBody("v1")
	require.Equal(t, http.StatusCreated, putManifest(r, "reg-once", "app", "1.0", v1).Code)

	requireDenied(t, putManifest(r, "reg-once", "app", "1.0", manifestBody("v2")),
		"Repository does not allow updating assets: reg-once")
	assert.Equal(t, v1, fetchManifest(r, "reg-once", "app", "1.0"), "the tag still resolves to the first push")

	// latest is write-once too without the opt-in.
	require.Equal(t, http.StatusCreated, putManifest(r, "reg-once", "app", "latest", v1).Code)
	requireDenied(t, putManifest(r, "reg-once", "app", "latest", manifestBody("v2")),
		"Repository does not allow updating assets: reg-once")

	// A new tag pointing at the same image is a first push of that tag; its
	// digest alias already exists and is content-addressed.
	assert.Equal(t, http.StatusCreated, putManifest(r, "reg-once", "app", "1.0-copy", v1).Code)
	// So is re-pushing the manifest by digest.
	assert.Equal(t, http.StatusCreated, putManifest(r, "reg-once", "app", digest(v1), v1).Code)
	// Blobs are content-addressed: pushing a layer twice is fine.
	pushBlob(t, r, "reg-once", "app", "layer")
	pushBlob(t, r, "reg-once", "app", "layer")
}

func TestOCI_AllowOnce_LatestRedeployWhenAllowed(t *testing.T) {
	r := setup(dockerRepo("reg-latest", map[string]any{
		domain.WritePolicyKey: "allow_once", domain.AllowRedeployLatestKey: true,
	}))

	require.Equal(t, http.StatusCreated, putManifest(r, "reg-latest", "app", "latest", manifestBody("v1")).Code)
	v2 := manifestBody("v2")
	require.Equal(t, http.StatusCreated, putManifest(r, "reg-latest", "app", "latest", v2).Code)
	assert.Equal(t, v2, fetchManifest(r, "reg-latest", "app", "latest"))

	require.Equal(t, http.StatusCreated, putManifest(r, "reg-latest", "app", "2.0", v2).Code)
	requireDenied(t, putManifest(r, "reg-latest", "app", "2.0", manifestBody("v3")),
		"Repository does not allow updating assets: reg-latest")
}

func TestOCI_Deny_RefusesUploadsAndManifests(t *testing.T) {
	r := setup(dockerRepo("reg-ro", map[string]any{domain.WritePolicyKey: "deny"}))

	req := httptest.NewRequest(http.MethodPost, "/repository/reg-ro/v2/app/blobs/uploads/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	requireDenied(t, w, "Repository is read-only: reg-ro")

	requireDenied(t, putManifest(r, "reg-ro", "app", "1.0", manifestBody("v1")), "Repository is read-only: reg-ro")
}
