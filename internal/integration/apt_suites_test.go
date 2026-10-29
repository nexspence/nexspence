//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func aptSuitePackages(t *testing.T, token, repoPath, suite, arch string) []string {
	t.Helper()
	code, idx := getBody(t, token, repoPath+"/dists/"+suite+"/main/binary-"+arch+"/Packages")
	require.Equal(t, http.StatusOK, code, idx)
	var out []string
	for _, line := range strings.Split(idx, "\n") {
		if v, ok := strings.CutPrefix(line, "Package: "); ok {
			out = append(out, v)
		}
	}
	return out
}

// #727 through the real router and postgres: suites survive the JSONB round
// trip, a shared file is stored once, a redeploy keeps the suites through the
// component upsert, and a group serves each member's suite.
func TestAptSuites_RealShape(t *testing.T) {
	createHostedRepo(t, "apt", "apt-suites", `{"write_policy":"allow"}`)
	token := login(t, "admin", "admin123")
	base := "/repository/apt-suites"
	ctl := func(pkg, version string) string {
		return fmt.Sprintf("Package: %s\nVersion: %s\nArchitecture: amd64\nMaintainer: Acme <dev@example.com>\nDescription: %s\n", pkg, version, pkg)
	}
	shared := aptDeb(t, ctl("acme-toolchain", "1.0"), "toolchain")

	code, body := putBody(t, token, base+"/pool/main/acme_1.4_amd64.deb?suite=acme-1.4", aptDeb(t, ctl("acme", "1.4"), "a"))
	require.Equal(t, http.StatusCreated, code, body)
	code, body = putBody(t, token, base+"/pool/main/acme_1.5_amd64.deb?suite=acme-1.5", aptDeb(t, ctl("acme", "1.5"), "b"))
	require.Equal(t, http.StatusCreated, code, body)
	code, body = putBody(t, token, base+"/pool/main/acme-toolchain_1.0_amd64.deb?suite=acme-1.4", shared)
	require.Equal(t, http.StatusCreated, code, body)
	code, body = putBody(t, token, base+"/pool/main/acme-toolchain_1.0_amd64.deb?suite=acme-1.5", shared)
	require.Equal(t, http.StatusOK, code, body)

	assert.ElementsMatch(t, []string{"acme", "acme-toolchain"}, aptSuitePackages(t, token, base, "acme-1.4", "amd64"))
	assert.ElementsMatch(t, []string{"acme", "acme-toolchain"}, aptSuitePackages(t, token, base, "acme-1.5", "amd64"))
	_, v14 := getBody(t, token, base+"/dists/acme-1.4/main/binary-amd64/Packages")
	assert.Contains(t, v14, "Version: 1.4\n")
	assert.NotContains(t, v14, "Version: 1.5\n")

	// A redeploy naming no suite keeps both, through the real upsert.
	code, body = putBody(t, token, base+"/pool/main/acme-toolchain_1.0_amd64.deb", aptDeb(t, ctl("acme-toolchain", "1.0"), "rebuilt"))
	require.Equal(t, http.StatusCreated, code, body)
	assert.Contains(t, aptSuitePackages(t, token, base, "acme-1.4", "amd64"), "acme-toolchain")
	assert.Contains(t, aptSuitePackages(t, token, base, "acme-1.5", "amd64"), "acme-toolchain")

	// Leaving a suite keeps the file; deleting it and uploading again starts
	// with no suites recorded.
	del := authReq(t, http.MethodDelete, base+"/pool/main/acme-toolchain_1.0_amd64.deb?suite=acme-1.4", nil, token)
	del.Body.Close()
	require.Equal(t, http.StatusNoContent, del.StatusCode)
	assert.NotContains(t, aptSuitePackages(t, token, base, "acme-1.4", "amd64"), "acme-toolchain")
	assert.Contains(t, aptSuitePackages(t, token, base, "acme-1.5", "amd64"), "acme-toolchain")
	del = authReq(t, http.MethodDelete, base+"/pool/main/acme-toolchain_1.0_amd64.deb", nil, token)
	del.Body.Close()
	require.Less(t, del.StatusCode, 300)
	code, body = putBody(t, token, base+"/pool/main/acme-toolchain_1.0_amd64.deb", shared)
	require.Equal(t, http.StatusCreated, code, body)
	assert.Contains(t, aptSuitePackages(t, token, base, "acme-1.4", "amd64"), "acme-toolchain", "suite-less again: in every suite")

	// A group passes the suite through to its members.
	createHostedRepo(t, "apt", "apt-suites-b", `{}`)
	code, body = putBody(t, token, "/repository/apt-suites-b/pool/main/other_1.0_amd64.deb?suite=acme-1.5", aptDeb(t, ctl("other", "1.0"), "o"))
	require.Equal(t, http.StatusCreated, code, body)
	resp := authReq(t, http.MethodPost, "/service/rest/v1/repositories/apt/group",
		strings.NewReader(`{"name":"apt-suites-group","online":true,"formatConfig":{"member_names":["apt-suites","apt-suites-b"]}}`), token)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	t.Cleanup(func() {
		d := authReq(t, http.MethodDelete, "/service/rest/v1/repositories/apt-suites-group", nil, token)
		d.Body.Close()
	})
	group := "/repository/apt-suites-group"
	assert.ElementsMatch(t, []string{"acme", "acme-toolchain", "other"}, aptSuitePackages(t, token, group, "acme-1.5", "amd64"))
	assert.ElementsMatch(t, []string{"acme", "acme-toolchain"}, aptSuitePackages(t, token, group, "acme-1.4", "amd64"))
	_, release := getBody(t, token, group+"/dists/acme-1.4/Release")
	assert.Contains(t, release, "Suite: acme-1.4\n")
}
