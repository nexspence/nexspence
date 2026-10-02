//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func deleteStatus(t *testing.T, token, p string) int {
	t.Helper()
	resp := authReq(t, http.MethodDelete, p, nil, token)
	resp.Body.Close()
	return resp.StatusCode
}

// #589: `dotnet nuget delete Bar 1.0.0` sends DELETE {PackagePublish}/Bar/1.0.0
// with the id as typed. The version must leave the lists along with its file.
func TestNuGetHosted_DeleteRemovesTheVersion_RealShape(t *testing.T) {
	createHostedRepo(t, "nuget", "nuget-del", `{}`)
	token := login(t, "admin", "admin123")
	for _, f := range []string{"Bar.1.0.0.nupkg", "Bar.2.0.0.nupkg"} {
		body, ct := multipartBody(t, nil, "package", f, []byte("not a zip"))
		sendRaw(t, token, http.MethodPut, "/repository/nuget-del/v2/package", ct, body)
	}

	require.Equal(t, http.StatusNoContent, deleteStatus(t, token, "/repository/nuget-del/v2/package/Bar/1.0.0"))
	code, body := getBody(t, token, "/repository/nuget-del/v3/flatcontainer/bar/index.json")
	require.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `{"versions":["2.0.0"]}`, body)
	code, _ = getBody(t, token, "/repository/nuget-del/v3/flatcontainer/bar/1.0.0/bar.1.0.0.nupkg")
	assert.Equal(t, http.StatusNotFound, code)

	assert.Equal(t, http.StatusNotFound, deleteStatus(t, token, "/repository/nuget-del/v2/package/Bar/1.0.0"),
		"a version that is not there")

	// The older route keeps working, and also matches the id regardless of case.
	require.Equal(t, http.StatusNoContent, deleteStatus(t, token, "/repository/nuget-del/v2/packages/BAR/2.0.0"))
	code, body = getBody(t, token, "/repository/nuget-del/v3/flatcontainer/bar/index.json")
	require.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `{"versions":[]}`, body)
}
