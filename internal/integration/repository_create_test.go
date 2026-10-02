//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func createRepoStatus(t *testing.T, token, format, typ, body string) (int, string) {
	t.Helper()
	resp := authReq(t, http.MethodPost, "/service/rest/v1/repositories/"+format+"/"+typ, strings.NewReader(body), token)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// #593: an unknown format or type is a 400, without the database's
// constraint names in the answer.
func TestRepositoryCreate_UnknownFormatOrType_400(t *testing.T) {
	token := login(t, "admin", "admin123")
	for _, c := range []struct{ format, typ string }{{"apk", "hosted"}, {"foo", "proxy"}, {"raw", "virtual"}} {
		code, body := createRepoStatus(t, token, c.format, c.typ, `{"name":"x-unknown","online":true}`)
		assert.Equal(t, http.StatusBadRequest, code, "%s/%s: %s", c.format, c.typ, body)
		assert.NotContains(t, body, "constraint")
		assert.NotContains(t, body, "SQLSTATE")
	}
}

// #592: names a client cannot address as one URL path segment are refused.
func TestRepositoryCreate_RejectsUnaddressableNames(t *testing.T) {
	token := login(t, "admin", "admin123")
	for _, name := range []string{"demo#2", "p40 space", "p40/slash", "p40%2", "p40-q?x"} {
		code, body := createRepoStatus(t, token, "raw", "hosted", fmt.Sprintf(`{"name":%q,"online":true}`, name))
		assert.Equal(t, http.StatusBadRequest, code, "%q: %s", name, body)
	}
	code, body := createRepoStatus(t, token, "raw", "hosted", `{"name":"ok.name-1","online":true}`)
	assert.Equal(t, http.StatusCreated, code, body)
	del := authReq(t, http.MethodDelete, "/service/rest/v1/repositories/ok.name-1", nil, token)
	del.Body.Close()
}
