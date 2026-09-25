//go:build integration

// Automatic promotion on publish (#542), end to end: a client publish through
// the real router lands in the target repository with nobody calling Promote.
// The server runs the auto-promotion worker with a short settle window and
// poll interval (see server()).
package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createAutoRule creates an auto_promote rule and returns its id.
func createAutoRule(t *testing.T, token, name, from, to string) string {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"from_repo":%q,"to_repo":%q,"auto_promote":true}`, name, from, to)
	resp := authReq(t, http.MethodPost, "/api/v1/promotion/rules", strings.NewReader(body), token)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create rule: %s", raw)
	var rule struct {
		ID          string `json:"id"`
		AutoPromote bool   `json:"auto_promote"`
	}
	require.NoError(t, json.Unmarshal(raw, &rule))
	require.True(t, rule.AutoPromote)
	t.Cleanup(func() {
		d := authReq(t, http.MethodDelete, "/api/v1/promotion/rules/"+rule.ID, nil, token)
		d.Body.Close()
	})
	return rule.ID
}

// eventually polls get until it answers 200, and returns the body.
func eventually(t *testing.T, token, p string) []byte {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp := registryReq(t, http.MethodGet, p, "", nil, token)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s: still %d after 20s: %s", p, resp.StatusCode, body)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type listedRequest struct {
	RuleID      string `json:"rule_id"`
	Status      string `json:"status"`
	Automatic   bool   `json:"automatic"`
	RequestedBy string `json:"requested_by"`
	Error       string `json:"error"`
}

func requestsForRule(t *testing.T, token, ruleID string) []listedRequest {
	t.Helper()
	resp := authReq(t, http.MethodGet, "/api/v1/promotion/requests", nil, token)
	defer resp.Body.Close()
	var all []listedRequest
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&all))
	var out []listedRequest
	for _, r := range all {
		if r.RuleID == ruleID {
			out = append(out, r)
		}
	}
	return out
}

func TestAutoPromoteMaven_RealShape(t *testing.T) {
	createHostedRepo(t, "maven2", "auto-mvn-a", `{}`)
	createHostedRepo(t, "maven2", "auto-mvn-b", `{}`)
	token := login(t, "admin", "admin123")
	ruleID := createAutoRule(t, token, "auto-mvn-a-to-b", "auto-mvn-a", "auto-mvn-b")

	base := "/com/example/auto/1.0.0/auto-1.0.0"
	for _, ext := range []string{".jar", ".pom"} {
		code, body := putBody(t, token, "/repository/auto-mvn-a"+base+ext, "content of "+ext)
		require.Equal(t, http.StatusCreated, code, "deploy %s: %s", ext, body)
	}

	assert.Equal(t, "content of .jar", string(eventually(t, token, "/repository/auto-mvn-b"+base+".jar")))
	assert.Equal(t, "content of .pom", string(eventually(t, token, "/repository/auto-mvn-b"+base+".pom")))
	reqs := requestsForRule(t, token, ruleID)
	require.Len(t, reqs, 1, "one promotion for the jar+pom deploy")
	assert.True(t, reqs[0].Automatic)
	assert.Empty(t, reqs[0].RequestedBy)
	assert.Equal(t, "completed", reqs[0].Status, reqs[0].Error)

	// A file deployed later follows by itself.
	code, body := putBody(t, token, "/repository/auto-mvn-a"+base+"-sources.jar", "sources")
	require.Equal(t, http.StatusCreated, code, body)
	assert.Equal(t, "sources", string(eventually(t, token, "/repository/auto-mvn-b"+base+"-sources.jar")))
}

func TestAutoPromoteDocker_RealShape(t *testing.T) {
	token := login(t, "admin", "admin123")
	createDockerHosted(t, "auto-docker-a", token)
	createDockerHosted(t, "auto-docker-b", token)
	ruleID := createAutoRule(t, token, "auto-docker-a-to-b", "auto-docker-a", "auto-docker-b")

	const image = "team/auto"
	cfg := pushBlob(t, "auto-docker-a", image, []byte(`{"architecture":"arm64","os":"linux"}`), token)
	l1 := pushBlob(t, "auto-docker-a", image, []byte("auto layer bytes"), token)
	manifest := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json",`+
		`"config":{"mediaType":"application/vnd.docker.container.image.v1+json","size":37,"digest":%q},`+
		`"layers":[{"mediaType":"application/vnd.docker.image.rootfs.diff.tar.gzip","size":16,"digest":%q}]}`, cfg, l1))
	manifestDigest := sha256Digest(manifest)
	mresp := registryReq(t, http.MethodPut, "/v2/auto-docker-a/"+image+"/manifests/2.0",
		"application/vnd.docker.distribution.manifest.v2+json", manifest, token)
	raw, _ := io.ReadAll(mresp.Body)
	mresp.Body.Close()
	require.Equal(t, http.StatusCreated, mresp.StatusCode, "push manifest: %s", raw)

	// What `docker pull auto-docker-b/team/auto:2.0` does.
	for _, ref := range []string{"2.0", manifestDigest} {
		body := eventually(t, token, "/v2/auto-docker-b/"+image+"/manifests/"+ref)
		assert.Equal(t, manifest, body, "manifest %s", ref)
	}
	for _, d := range []string{cfg, l1} {
		body := eventually(t, token, "/v2/auto-docker-b/"+image+"/blobs/"+d)
		assert.Equal(t, d, sha256Digest(body), "blob %s", d)
	}
	reqs := requestsForRule(t, token, ruleID)
	require.Len(t, reqs, 1, "the tag push, not each blob, started the rule")
	assert.True(t, reqs[0].Automatic)
	assert.Equal(t, "completed", reqs[0].Status, reqs[0].Error)
}
