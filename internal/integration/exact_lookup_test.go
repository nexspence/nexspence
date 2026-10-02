//go:build integration

// Protocol indexes look a package up by its exact coordinates (#586). The
// component search matches substrings and returns one page, which the unit
// test mock does not model: these tests run the real router over PostgreSQL,
// each with a package whose name contains another's and more rows than one
// search page.
package integration

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getBody fetches p as admin and returns status and body.
func getBody(t *testing.T, token, p string) (int, string) {
	t.Helper()
	resp := authReq(t, http.MethodGet, p, nil, token)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func sendRaw(t *testing.T, token, method, p, contentType string, body []byte) {
	t.Helper()
	req, err := http.NewRequest(method, server(t).URL+p, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Less(t, resp.StatusCode, 300, "%s %s: %s", method, p, raw)
}

func multipartBody(t *testing.T, fields map[string]string, fileField, filename string, content []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		require.NoError(t, w.WriteField(k, v))
	}
	fw, err := w.CreateFormFile(fileField, filename)
	require.NoError(t, err)
	_, _ = fw.Write(content)
	require.NoError(t, w.Close())
	return buf.Bytes(), w.FormDataContentType()
}

func TestGoHosted_ModuleLookupIsExact_RealShape(t *testing.T) {
	createHostedRepo(t, "go", "exact-go", `{}`)
	token := login(t, "admin", "admin123")
	put := func(module, version string) {
		sendRaw(t, token, http.MethodPut,
			"/repository/exact-go/"+module+"/@v/"+version+".mod", "text/plain",
			[]byte("module "+module+"\n"))
	}

	own := []string{"v1.0.0", "v1.9.0", "v1.10.0", "v1.11.0-rc.1", "v3.0.0+incompatible"}
	for _, v := range own {
		put("example.com/acme/repo", v)
	}
	put("example.com/acme/repo/v2", "v2.0.0")
	// A module whose path contains repo's, with more versions than one page,
	// sorted before it ("aaa" < "repo").
	for i := 0; i < 201; i++ {
		put("example.com/acme/repo/aaa", fmt.Sprintf("v0.0.%d", i))
	}

	code, body := getBody(t, token, "/repository/exact-go/example.com/acme/repo/@v/list")
	require.Equal(t, http.StatusOK, code)
	got := strings.Fields(body)
	sort.Strings(got)
	want := append([]string(nil), own...)
	sort.Strings(want)
	assert.Equal(t, want, got)

	code, body = getBody(t, token, "/repository/exact-go/example.com/acme/repo/@latest")
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `"Version":"v1.10.0"`, "the highest release, not the last row by string")

	code, _ = getBody(t, token, "/repository/exact-go/example.com/acme/repo/@v/v2.0.0.info")
	assert.Equal(t, http.StatusNotFound, code, "v2.0.0 belongs to repo/v2")

	code, body = getBody(t, token, "/repository/exact-go/example.com/acme/@v/list")
	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, strings.TrimSpace(body), "a parent path the go command probes is not a module")

	code, body = getBody(t, token, "/repository/exact-go/example.com/acme/repo/aaa/@v/list")
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, strings.Fields(body), 201, "every version past one page")
}

// With no release at all, @latest falls back to the highest prerelease.
func TestGoHosted_LatestFallsBackToPrerelease_RealShape(t *testing.T) {
	createHostedRepo(t, "go", "exact-go-pre", `{}`)
	token := login(t, "admin", "admin123")
	for _, v := range []string{"v0.9.0-rc.1", "v0.10.0-rc.1", "v0.0.0-20260101000000-abcdef123456"} {
		sendRaw(t, token, http.MethodPut,
			"/repository/exact-go-pre/example.com/pre/@v/"+v+".mod", "text/plain", []byte("module example.com/pre\n"))
	}
	code, body := getBody(t, token, "/repository/exact-go-pre/example.com/pre/@latest")
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `"Version":"v0.10.0-rc.1"`)
}

func TestNuGetHosted_VersionListsAreExact_RealShape(t *testing.T) {
	createHostedRepo(t, "nuget", "exact-nuget", `{}`)
	token := login(t, "admin", "admin123")
	push := func(filename string) {
		body, ct := multipartBody(t, nil, "package", filename, []byte("not a zip"))
		sendRaw(t, token, http.MethodPut, "/repository/exact-nuget/v2/package", ct, body)
	}
	push("Foo.1.0.0-Beta.nupkg")
	push("Foo.Abstractions.2.0.0.nupkg")

	code, body := getBody(t, token, "/repository/exact-nuget/v3/flatcontainer/foo/index.json")
	require.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `{"versions":["1.0.0-Beta"]}`, body)

	code, body = getBody(t, token, "/repository/exact-nuget/v3/registration/foo/index.json")
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "foo.abstractions")

	code, body = getBody(t, token, "/repository/exact-nuget/FindPackagesById()?id='foo'")
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "foo.abstractions")
}

func TestPyPIHosted_SimplePageIsExactAndComplete_RealShape(t *testing.T) {
	createHostedRepo(t, "pypi", "exact-pypi", `{}`)
	token := login(t, "admin", "admin123")
	upload := func(name, version string) {
		filename := strings.ReplaceAll(name, "-", "_") + "-" + version + "-py3-none-any.whl"
		body, ct := multipartBody(t, map[string]string{
			":action": "file_upload", "name": name, "version": version,
		}, "content", filename, []byte(name+version))
		sendRaw(t, token, http.MethodPost, "/repository/exact-pypi/", ct, body)
	}
	for i := 0; i < 200; i++ {
		upload("acme-new", fmt.Sprintf("1.0.%d", i))
	}
	upload("acme-new", "2.0.0")
	upload("acme-new-extra", "9.9.9")

	code, body := getBody(t, token, "/repository/exact-pypi/simple/acme-new/")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, 201, strings.Count(body, "<a "), "every file of the project, past one page")
	assert.Contains(t, body, "acme_new-2.0.0-py3-none-any.whl")
	assert.NotContains(t, body, "acme_new_extra")
}

func TestTerraformHosted_VersionListsAreExact_RealShape(t *testing.T) {
	createHostedRepo(t, "terraform", "exact-tf", `{}`)
	token := login(t, "admin", "admin123")
	for _, m := range []string{"acme/vpc/aws/1.0.0", "acme/vpc/aws/1.9.0", "xacme/vpc/aws/4.0.0", "hashicorp/consul/aws/7.0.0"} {
		sendRaw(t, token, http.MethodPut, "/repository/exact-tf/v1/modules/"+m, "application/x-tar", []byte("tgz"))
	}
	sendRaw(t, token, http.MethodPut, "/repository/exact-tf/v1/providers/hashicorp/aws/5.0.0/upload/linux/amd64",
		"application/zip", []byte("zip"))

	code, body := getBody(t, token, "/repository/exact-tf/v1/modules/acme/vpc/aws/versions")
	require.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `{"modules":[{"versions":[{"version":"1.0.0"},{"version":"1.9.0"}]}]}`, body)

	code, body = getBody(t, token, "/repository/exact-tf/v1/providers/hashicorp/aws/versions")
	require.Equal(t, http.StatusOK, code)
	var providers struct {
		Versions []struct {
			Version string `json:"version"`
		} `json:"versions"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &providers))
	require.Len(t, providers.Versions, 1, "the module hashicorp/consul/aws is not a version of the provider: %s", body)
	assert.Equal(t, "5.0.0", providers.Versions[0].Version)
}

func TestCondaHosted_RepodataIsPerSubdirAndComplete_RealShape(t *testing.T) {
	createHostedRepo(t, "conda", "exact-conda", `{}`)
	token := login(t, "admin", "admin123")
	put := func(subdir, filename string) {
		sendRaw(t, token, http.MethodPut, "/repository/exact-conda/"+subdir+"/"+filename,
			"application/x-tar", []byte(filename))
	}
	put("linux-ppc64le", "lepkg-1.0-1.tar.bz2")
	put("linux-ppc64", "bepkg-1.0-1.tar.bz2")
	for i := 0; i < 501; i++ {
		put("linux-64", fmt.Sprintf("pkg%03d-1.0-0.tar.bz2", i))
	}

	var doc struct {
		Packages map[string]json.RawMessage `json:"packages"`
	}
	code, body := getBody(t, token, "/repository/exact-conda/linux-ppc64/repodata.json")
	require.Equal(t, http.StatusOK, code)
	require.NoError(t, json.Unmarshal([]byte(body), &doc))
	keys := make([]string, 0, len(doc.Packages))
	for k := range doc.Packages {
		keys = append(keys, k)
	}
	assert.Equal(t, []string{"bepkg-1.0-1.tar.bz2"}, keys)

	doc.Packages = nil
	code, body = getBody(t, token, "/repository/exact-conda/linux-64/repodata.json")
	require.Equal(t, http.StatusOK, code)
	require.NoError(t, json.Unmarshal([]byte(body), &doc))
	assert.Len(t, doc.Packages, 501, "every package of the subdir, past one page")
}

func TestCargoHosted_IndexEntryIsExact_RealShape(t *testing.T) {
	createHostedRepo(t, "cargo", "exact-cargo", `{}`)
	token := login(t, "admin", "admin123")
	publish := func(name, version string) {
		meta, _ := json.Marshal(map[string]any{"name": name, "vers": version, "deps": []any{}, "features": map[string]any{}})
		crate := []byte(name + version)
		var buf bytes.Buffer
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(meta)))
		buf.Write(meta)
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(crate)))
		buf.Write(crate)
		sendRaw(t, token, http.MethodPut, "/repository/exact-cargo/api/v1/crates/new", "application/octet-stream", buf.Bytes())
	}
	publish("rvc-serde", "0.1.0")
	publish("rvc-serde_json", "0.1.5")
	publish("rvc-serde-json", "0.1.7")
	for i := 0; i < 201; i++ {
		publish("a-rvc-serde", fmt.Sprintf("0.0.%d", i))
	}

	names := func(body string) map[string]int {
		out := map[string]int{}
		for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
			var rec struct {
				Name string `json:"name"`
			}
			if json.Unmarshal([]byte(line), &rec) == nil {
				out[rec.Name]++
			}
		}
		return out
	}
	code, body := getBody(t, token, "/repository/exact-cargo/index/rv/c-/rvc-serde")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, map[string]int{"rvc-serde": 1}, names(body))

	code, body = getBody(t, token, "/repository/exact-cargo/index/rv/c-/rvc-serde_json")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, map[string]int{"rvc-serde_json": 1}, names(body), "_ is not a wildcard")

	code, body = getBody(t, token, "/repository/exact-cargo/index/a-/rv/a-rvc-serde")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, map[string]int{"a-rvc-serde": 201}, names(body))
}
