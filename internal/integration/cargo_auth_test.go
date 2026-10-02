//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #588: a cargo repository that refuses anonymous reads tells cargo to send
// its token, or cargo downloads crates without one and gets 401.
func TestCargoHosted_ConfigAuthRequired_RealShape(t *testing.T) {
	token := login(t, "admin", "admin123")
	for _, tc := range []struct {
		name      string
		anonymous bool
	}{{"cargo-auth-priv", false}, {"cargo-auth-pub", true}} {
		body := fmt.Sprintf(`{"name":%q,"online":true,"allowAnonymous":%t}`, tc.name, tc.anonymous)
		resp := authReq(t, http.MethodPost, "/service/rest/v1/repositories/cargo/hosted", strings.NewReader(body), token)
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Equal(t, http.StatusCreated, resp.StatusCode, "%s", raw)
		name := tc.name
		t.Cleanup(func() {
			del := authReq(t, http.MethodDelete, "/service/rest/v1/repositories/"+name, nil, token)
			del.Body.Close()
		})

		cfgResp := authReq(t, http.MethodGet, "/repository/"+tc.name+"/index/config.json", nil, token)
		cfgBody, _ := io.ReadAll(cfgResp.Body)
		cfgResp.Body.Close()
		require.Equal(t, http.StatusOK, cfgResp.StatusCode)
		var cfg map[string]any
		require.NoError(t, json.Unmarshal(cfgBody, &cfg))
		assert.Equal(t, !tc.anonymous, cfg["auth-required"], "%s: %s", tc.name, cfgBody)
	}
}
