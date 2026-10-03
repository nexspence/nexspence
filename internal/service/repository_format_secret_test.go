package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func signedAptRepo() *domain.Repository {
	r := testutil.SimpleRepo("apt-signed", "apt")
	r.FormatConfig = map[string]any{
		"signing_key": "KEY", "signing_key_passphrase": "PASS", "distribution": "stable",
	}
	return r
}

// GHSA-pg67-wh39-mx9j: readers now get *_set markers, so an edit that
// round-trips them, or leaves the secrets out, must keep the stored key.
func TestRepositoryService_Update_KeepsSigningKeyWhenOmitted(t *testing.T) {
	svc := newRepoServiceWith(signedAptRepo())
	got, err := svc.Update(context.Background(), "apt-signed", &domain.Repository{
		Online: true,
		FormatConfig: map[string]any{
			"distribution": "bookworm", "signing_key_set": true, "signing_key_passphrase_set": true,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "KEY", got.FormatConfig["signing_key"])
	assert.Equal(t, "PASS", got.FormatConfig["signing_key_passphrase"])
	assert.Equal(t, "bookworm", got.FormatConfig["distribution"])
	assert.NotContains(t, got.FormatConfig, "signing_key_set", "markers are never stored")
}

func TestRepositoryService_Update_ReplacesOrClearsSigningKey(t *testing.T) {
	svc := newRepoServiceWith(signedAptRepo())
	got, err := svc.Update(context.Background(), "apt-signed", &domain.Repository{
		Online:       true,
		FormatConfig: map[string]any{"signing_key": "NEWKEY", "signing_key_passphrase": ""},
	})
	require.NoError(t, err)
	assert.Equal(t, "NEWKEY", got.FormatConfig["signing_key"])
	assert.NotContains(t, got.FormatConfig, "signing_key_passphrase", "an explicit empty value clears it")
}

func newRepoServiceWith(repos ...*domain.Repository) *service.RepositoryService {
	return service.NewRepositoryService(
		testutil.NewRepoRepo(repos...),
		testutil.NewBlobStoreRepo(),
		testutil.NewBlobStore(),
		testutil.NewCleanupPolicyRepo(),
	)
}
