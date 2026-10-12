//go:build integration

package integration

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #665 over PostgreSQL, whose component upsert resets extra on every stored
// file: the release's Requires-Python must survive a sibling uploaded with and
// without the field.
var requiresPythonAnchor = regexp.MustCompile(`<a [^>]*data-requires-python="([^"]*)"[^>]*>([^<]+)</a>`)

func TestPyPIHosted_RequiresPythonSurvivesSiblingUploads(t *testing.T) {
	createHostedRepo(t, "pypi", "rp-pypi", `{}`)
	token := login(t, "admin", "admin123")
	upload := func(version, filename, requiresPython string) {
		fields := map[string]string{":action": "file_upload", "name": "acme-lib", "version": version}
		if requiresPython != "" {
			fields["requires_python"] = requiresPython
		}
		body, ct := multipartBody(t, fields, "content", filename, []byte(filename))
		sendRaw(t, token, http.MethodPost, "/repository/rp-pypi/", ct, body)
	}
	upload("1.0.0", "acme_lib-1.0.0-py3-none-any.whl", ">=3.10")
	upload("1.0.0", "acme_lib-1.0.0.tar.gz", "")
	upload("0.9.0", "acme_lib-0.9.0.tar.gz", ">=3.8")

	code, body := getBody(t, token, "/repository/rp-pypi/simple/acme-lib/")
	require.Equal(t, http.StatusOK, code)
	attrs := map[string]string{}
	for _, m := range requiresPythonAnchor.FindAllStringSubmatch(body, -1) {
		attrs[m[2]] = m[1]
	}
	assert.Equal(t, map[string]string{
		"acme_lib-1.0.0-py3-none-any.whl": "&gt;=3.10",
		"acme_lib-1.0.0.tar.gz":           "&gt;=3.10",
		"acme_lib-0.9.0.tar.gz":           "&gt;=3.8",
	}, attrs, body)
}
