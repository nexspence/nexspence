//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func armoredSigningKey(t *testing.T) string {
	t.Helper()
	entity, err := openpgp.NewEntity("Nexspence Test", "apt signing", "apt@example.test", nil)
	require.NoError(t, err)
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	require.NoError(t, err)
	require.NoError(t, entity.SerializePrivateWithoutSigning(w, nil))
	require.NoError(t, w.Close())
	return buf.String()
}

// GHSA-pg67-wh39-mx9j: the signing key of a public apt repository is never
// served — not to anonymous readers, not to authenticated ones — and an edit
// that round-trips the redacted config keeps it.
func TestAptSigningKey_NeverServedAndKeptOnEdit_RealShape(t *testing.T) {
	token := login(t, "admin", "admin123")
	key, pass := armoredSigningKey(t), "PASSPHRASE-THAT-MUST-NOT-LEAK"
	// A line of the key body: the JSON response would escape the newlines of
	// the whole armored block, so look for a fragment.
	keyLine := strings.Split(key, "\n")[3]
	cfg, _ := json.Marshal(map[string]any{
		"name": "apt-signed", "online": true, "allowAnonymous": true,
		"formatConfig": map[string]any{"signing_key": key, "signing_key_passphrase": pass, "distribution": "stable"},
	})
	body := string(cfg)
	resp := authReq(t, http.MethodPost, "/service/rest/v1/repositories/apt/hosted", strings.NewReader(body), token)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode, "%s", raw)
	assert.NotContains(t, string(raw), keyLine, "create response")
	t.Cleanup(func() {
		d := authReq(t, http.MethodDelete, "/service/rest/v1/repositories/apt-signed", nil, token)
		d.Body.Close()
	})

	read := func(p string, tok string) string {
		var resp *http.Response
		if tok == "" {
			resp = anonReq(t, p)
		} else {
			resp = authReq(t, http.MethodGet, p, nil, tok)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	for _, p := range []string{"/service/rest/v1/repositories/apt-signed", "/service/rest/v1/repositories", "/api/v1/repositories"} {
		for _, tok := range []string{"", token} {
			got := read(p, tok)
			assert.NotContains(t, got, keyLine, "%s (authenticated=%v)", p, tok != "")
			assert.NotContains(t, got, pass, "%s (authenticated=%v)", p, tok != "")
		}
	}
	assert.Contains(t, read("/service/rest/v1/repositories/apt-signed", token), `"signing_key_set":true`)

	// The UI and the Terraform provider send back what they read.
	upd := `{"name":"apt-signed","online":true,"allowAnonymous":true,"formatConfig":{"signing_key_set":true,"signing_key_passphrase_set":true,"distribution":"bookworm"}}`
	resp = authReq(t, http.MethodPut, "/service/rest/v1/repositories/apt/hosted/apt-signed", strings.NewReader(upd), token)
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Less(t, resp.StatusCode, 300, "%s", raw)

	// Still signed with the stored key: the public key is derived from it.
	pub := authReq(t, http.MethodGet, "/repository/apt-signed/public.gpg", nil, token)
	pub.Body.Close()
	assert.Equal(t, http.StatusOK, pub.StatusCode, "the repository is still signed")
	assert.Contains(t, read("/service/rest/v1/repositories/apt-signed", token), `"signing_key_set":true`, "the key survived the edit")
	assert.Contains(t, read("/service/rest/v1/repositories/apt-signed", token), `bookworm`)
}
