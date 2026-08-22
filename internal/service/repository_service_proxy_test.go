package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// seededProxy returns a stored maven proxy repository carrying an upstream proxy password.
func seededProxy() *domain.Repository {
	return &domain.Repository{
		ID: "p1", Name: "maven-proxy", Format: domain.FormatMaven2, Type: domain.TypeProxy,
		ProxyConfig: map[string]any{
			"remote_url":     "https://repo1.maven.org/maven2/",
			"proxy_username": "svc",
			"proxy_password": "s3cret",
		},
	}
}

func TestRepositoryService_Update_KeepsProxyPasswordWhenOmitted(t *testing.T) {
	repos := testutil.NewRepoRepo(seededProxy())
	svc := newRepoSvcFull(repos, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	got, err := svc.Update(context.Background(), "maven-proxy", &domain.Repository{
		ProxyConfig: map[string]any{
			"remote_url":     "https://my.mirror/maven/",
			"proxy_username": "svc",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "https://my.mirror/maven/", got.ProxyConfig["remote_url"])
	assert.Equal(t, "s3cret", got.ProxyConfig["proxy_password"], "omitted password must survive the edit")
}

func TestRepositoryService_Update_ReplacesProxyPasswordWhenProvided(t *testing.T) {
	repos := testutil.NewRepoRepo(seededProxy())
	svc := newRepoSvcFull(repos, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	got, err := svc.Update(context.Background(), "maven-proxy", &domain.Repository{
		ProxyConfig: map[string]any{
			"remote_url":     "https://repo1.maven.org/maven2/",
			"proxy_password": "n3w",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "n3w", got.ProxyConfig["proxy_password"])
}

func TestRepositoryService_Update_ClearsProxyPasswordWhenEmpty(t *testing.T) {
	repos := testutil.NewRepoRepo(seededProxy())
	svc := newRepoSvcFull(repos, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	got, err := svc.Update(context.Background(), "maven-proxy", &domain.Repository{
		ProxyConfig: map[string]any{
			"remote_url":     "https://repo1.maven.org/maven2/",
			"proxy_password": "",
		},
	})

	require.NoError(t, err)
	assert.NotContains(t, got.ProxyConfig, "proxy_password", "an explicit empty password clears the credential")
}

func TestRepositoryService_Update_DropsProxyPasswordSetMarker(t *testing.T) {
	repos := testutil.NewRepoRepo(seededProxy())
	svc := newRepoSvcFull(repos, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	// A client that round-trips a redacted payload sends the marker back.
	got, err := svc.Update(context.Background(), "maven-proxy", &domain.Repository{
		ProxyConfig: map[string]any{
			"remote_url":               "https://repo1.maven.org/maven2/",
			domain.ProxyPasswordSetKey: true,
		},
	})

	require.NoError(t, err)
	assert.NotContains(t, got.ProxyConfig, domain.ProxyPasswordSetKey, "the read-only marker must never be stored")
	assert.Equal(t, "s3cret", got.ProxyConfig["proxy_password"])
}

func TestRepositoryService_Update_RejectsEmptyRemoteURLForProxy(t *testing.T) {
	repos := testutil.NewRepoRepo(seededProxy())
	svc := newRepoSvcFull(repos, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	_, err := svc.Update(context.Background(), "maven-proxy", &domain.Repository{
		ProxyConfig: map[string]any{"remote_url": "   "},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote_url")
}

func TestRepositoryService_Create_RejectsBlankRemoteURL(t *testing.T) {
	repos := testutil.NewRepoRepo()
	svc := newRepoSvcFull(repos, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	err := svc.Create(context.Background(), &domain.Repository{
		Name: "maven-proxy", Format: domain.FormatMaven2, Type: domain.TypeProxy,
		ProxyConfig: map[string]any{"remote_url": "   "},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote_url")
}

func TestRepositoryService_Create_DropsProxyPasswordSetMarker(t *testing.T) {
	repos := testutil.NewRepoRepo()
	svc := newRepoSvcFull(repos, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	err := svc.Create(context.Background(), &domain.Repository{
		Name: "maven-proxy", Format: domain.FormatMaven2, Type: domain.TypeProxy,
		ProxyConfig: map[string]any{
			"remote_url":               "https://repo1.maven.org/maven2/",
			domain.ProxyPasswordSetKey: true,
		},
	})

	require.NoError(t, err)
	stored, err := repos.Get(context.Background(), "maven-proxy")
	require.NoError(t, err)
	assert.NotContains(t, stored.ProxyConfig, domain.ProxyPasswordSetKey)
}

// remote_password (upstream Basic auth, #281) follows the same redact/merge
// contract as proxy_password: omitted means unchanged, empty clears.
func TestRepositoryService_Update_RemotePassword_MergeContract(t *testing.T) {
	seeded := seededProxy()
	seeded.ProxyConfig["remote_username"] = "deploy"
	seeded.ProxyConfig["remote_password"] = "upstream-s3cret"
	repos := testutil.NewRepoRepo(seeded)
	svc := newRepoSvcFull(repos, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	// Omitted → unchanged (and the echoed *_set marker is dropped).
	got, err := svc.Update(context.Background(), "maven-proxy", &domain.Repository{
		ProxyConfig: map[string]any{
			"remote_url":          "https://api.mapbox.com/downloads/v2/releases/maven/",
			"remote_username":     "deploy",
			"remote_password_set": true,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "upstream-s3cret", got.ProxyConfig["remote_password"], "omitted remote_password must survive the edit")
	assert.NotContains(t, got.ProxyConfig, "remote_password_set")

	// Explicit empty → cleared.
	got, err = svc.Update(context.Background(), "maven-proxy", &domain.Repository{
		ProxyConfig: map[string]any{
			"remote_url":      "https://api.mapbox.com/downloads/v2/releases/maven/",
			"remote_password": "",
		},
	})
	require.NoError(t, err)
	assert.NotContains(t, got.ProxyConfig, "remote_password")
}
