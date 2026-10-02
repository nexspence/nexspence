//go:build integration

package integration

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #587: the dependencies and features of a published crate survive the trip
// through the component's JSONB extra into its sparse-index record.
func TestCargoHosted_IndexCarriesDepsAndFeatures_RealShape(t *testing.T) {
	createHostedRepo(t, "cargo", "cargo-meta", `{}`)
	token := login(t, "admin", "admin123")

	meta := `{"name":"rvc-libb","vers":"0.1.0",
	  "deps":[{"name":"rvc-liba","version_req":"^0.1","features":[],"optional":true,
	    "default_features":true,"target":null,"kind":"normal","registry":null,"explicit_name_in_toml":"foo"}],
	  "features":{"default":[],"extra":[],"withfoo":["dep:foo"]}}`
	crate := "crate-bytes"
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(meta)))
	buf.WriteString(meta)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(crate)))
	buf.WriteString(crate)
	sendRaw(t, token, http.MethodPut, "/repository/cargo-meta/api/v1/crates/new", "application/octet-stream", buf.Bytes())

	code, body := getBody(t, token, "/repository/cargo-meta/index/rv/c-/rvc-libb")
	require.Equal(t, http.StatusOK, code)
	var rec struct {
		Deps      []map[string]any    `json:"deps"`
		Features  map[string][]string `json:"features"`
		Features2 map[string][]string `json:"features2"`
		V         int                 `json:"v"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(body)), &rec), body)
	require.Len(t, rec.Deps, 1)
	assert.Equal(t, "foo", rec.Deps[0]["name"])
	assert.Equal(t, "rvc-liba", rec.Deps[0]["package"])
	assert.Equal(t, "^0.1", rec.Deps[0]["req"])
	assert.Equal(t, true, rec.Deps[0]["optional"])
	assert.Equal(t, map[string][]string{"default": {}, "extra": {}}, rec.Features)
	assert.Equal(t, map[string][]string{"withfoo": {"dep:foo"}}, rec.Features2)
	assert.Equal(t, 2, rec.V)
}
