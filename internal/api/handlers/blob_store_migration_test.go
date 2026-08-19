package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// The struct is serialized directly, so its tags ARE the API contract: the
// frontend reads camelCase keys, and untagged PascalCase left the migration
// progress UI reading undefined everywhere (#253).
func TestBlobStoreMigration_SerializesTheFrontendContract(t *testing.T) {
	m := domain.BlobStoreMigration{ID: "m1", RepositoryName: "repo", SourceStoreID: "s1", TargetStoreID: "s2", Status: "running", TotalAssets: 10, DoneAssets: 3}
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	for _, key := range []string{
		`"id"`, `"repositoryName"`, `"sourceStoreId"`, `"targetStoreId"`, `"status"`,
		`"totalAssets"`, `"doneAssets"`, `"totalBytes"`, `"doneBytes"`,
		// Nullable fields serialize as null, not disappear: the frontend
		// types them `| null` and a missing key reads as undefined.
		`"errorMessage":null`, `"startedAt":null`, `"finishedAt":null`,
	} {
		require.Contains(t, string(raw), key)
	}
}
