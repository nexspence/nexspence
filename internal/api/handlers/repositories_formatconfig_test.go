package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// A formatConfig setting sent at the TOP level of a repository payload is
// dropped by the decoder, and a setting that is silently dropped is a setting
// the caller believes is in force and is not: a write policy that does not
// deny writes, a pull gate that never blocks, a group that writes to a member
// nobody named.
//
// So the payload is refused with a message that names the right place.
func TestRepositoryHandler_Create_MisplacedFormatConfigKey_400(t *testing.T) {
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/yum/hosted",
		map[string]any{"name": "yum-once", "write_policy": "deny"})
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var got map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Contains(t, got["error"], "write_policy belongs in formatConfig")
	assert.Contains(t, got["error"], `"formatConfig"`)
}

// The same setting inside formatConfig is what the API expects — the payload
// is accepted and the policy is in force.
func TestRepositoryHandler_Create_FormatConfigWritePolicy_201(t *testing.T) {
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/yum/hosted",
		map[string]any{
			"name":         "yum-once",
			"formatConfig": map[string]any{"write_policy": "deny"},
		})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var got domain.Repository
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "deny", got.FormatConfig["write_policy"])
}

// Every formatConfig key is refused at the top level, not just the write
// policy.
func TestRepositoryHandler_Create_MisplacedFormatConfigKeys_400(t *testing.T) {
	for _, key := range []string{
		"write_policy",
		"allow_redeploy_latest",
		"pull_block_severity",
		"member_names",
		"writable_member",
	} {
		t.Run(key, func(t *testing.T) {
			r, _, _, _ := mountRepos(t)
			rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/yum/hosted",
				map[string]any{"name": "misplaced", key: "whatever"})
			require.Equal(t, http.StatusBadRequest, rec.Code)

			var got map[string]string
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			assert.Contains(t, got["error"], key+" belongs in formatConfig")
		})
	}
}

// The same trap on an update: formatConfig is replaced as a whole, so a
// top-level key would be dropped there too.
func TestRepositoryHandler_Update_MisplacedFormatConfigKey_400(t *testing.T) {
	r, repos, _, _ := mountRepos(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "yum-hosted", Format: domain.FormatYum, Type: domain.TypeHosted})

	rec := do(t, r, http.MethodPut, "/service/rest/v1/repositories/yum/hosted/yum-hosted",
		map[string]any{"name": "yum-hosted", "write_policy": "deny"})
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var got map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Contains(t, got["error"], "write_policy belongs in formatConfig")
}

// A payload without those keys is untouched by the check.
func TestRepositoryHandler_Create_WithoutFormatConfigKeys_201(t *testing.T) {
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/yum/hosted",
		map[string]any{"name": "yum-plain", "description": "no format settings"})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}
