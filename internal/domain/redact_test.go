package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactedRepository_StripsProxyPassword(t *testing.T) {
	t.Parallel()
	r := Repository{
		Name: "maven-proxy",
		ProxyConfig: map[string]any{
			"remote_url":     "https://repo1.maven.org/maven2/",
			"proxy_username": "svc",
			"proxy_password": "s3cret",
		},
	}

	got := RedactedRepository(r)

	_, hasPassword := got.ProxyConfig["proxy_password"]
	require.False(t, hasPassword, "proxy_password must not be exposed")
	require.Equal(t, true, got.ProxyConfig[ProxyPasswordSetKey])
	require.Equal(t, "https://repo1.maven.org/maven2/", got.ProxyConfig["remote_url"])
	require.Equal(t, "svc", got.ProxyConfig["proxy_username"])
}

func TestRedactedRepository_DoesNotMutateInput(t *testing.T) {
	t.Parallel()
	r := Repository{
		ProxyConfig: map[string]any{"proxy_password": "s3cret"},
	}

	_ = RedactedRepository(r)

	require.Equal(t, "s3cret", r.ProxyConfig["proxy_password"], "input must stay intact")
	require.NotContains(t, r.ProxyConfig, ProxyPasswordSetKey)
}

func TestRedactedRepository_NoPasswordLeavesFlagUnset(t *testing.T) {
	t.Parallel()
	r := Repository{
		ProxyConfig: map[string]any{"remote_url": "https://example.test/"},
	}

	got := RedactedRepository(r)

	require.NotContains(t, got.ProxyConfig, ProxyPasswordSetKey)
	require.Equal(t, "https://example.test/", got.ProxyConfig["remote_url"])
}

func TestRedactedRepository_EmptyPasswordLeavesFlagUnset(t *testing.T) {
	t.Parallel()
	r := Repository{
		ProxyConfig: map[string]any{"proxy_password": ""},
	}

	got := RedactedRepository(r)

	require.NotContains(t, got.ProxyConfig, "proxy_password")
	require.NotContains(t, got.ProxyConfig, ProxyPasswordSetKey)
}

func TestRedactedRepository_NilProxyConfig(t *testing.T) {
	t.Parallel()
	r := Repository{Name: "hosted"}

	got := RedactedRepository(r)

	require.Nil(t, got.ProxyConfig)
	require.Equal(t, "hosted", got.Name)
}

func TestRedactedRepositories_RedactsEveryEntry(t *testing.T) {
	t.Parallel()
	list := []Repository{
		{Name: "a", ProxyConfig: map[string]any{"proxy_password": "one"}},
		{Name: "b"},
		{Name: "c", ProxyConfig: map[string]any{"proxy_password": "three"}},
	}

	got := RedactedRepositories(list)

	require.Len(t, got, 3)
	require.NotContains(t, got[0].ProxyConfig, "proxy_password")
	require.NotContains(t, got[2].ProxyConfig, "proxy_password")
	require.Equal(t, "one", list[0].ProxyConfig["proxy_password"], "input must stay intact")
}

func TestRedactedRepositories_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, RedactedRepositories(nil))
}

func TestRedactedBlobStore_StripsSecretKey(t *testing.T) {
	t.Parallel()
	bs := BlobStore{
		Name: "archive", Type: "s3",
		Config: map[string]any{
			"bucket":     "artifacts",
			"region":     "eu-central-1",
			"access_key": "AKIAEXAMPLE",
			"secret_key": "sup3rs3cret",
		},
	}

	got := RedactedBlobStore(bs)

	require.NotContains(t, got.Config, "secret_key")
	require.Equal(t, true, got.Config[SecretKeySetKey])
	require.Equal(t, "artifacts", got.Config["bucket"])
	require.Equal(t, "AKIAEXAMPLE", got.Config["access_key"], "the key id is not a secret and stays visible")
}

func TestRedactedBlobStore_DoesNotMutateInput(t *testing.T) {
	t.Parallel()
	bs := BlobStore{Config: map[string]any{"secret_key": "sup3rs3cret"}}

	_ = RedactedBlobStore(bs)

	require.Equal(t, "sup3rs3cret", bs.Config["secret_key"], "input must stay intact")
	require.NotContains(t, bs.Config, SecretKeySetKey)
}

func TestRedactedBlobStore_EmptySecretLeavesFlagUnset(t *testing.T) {
	t.Parallel()
	bs := BlobStore{Config: map[string]any{"secret_key": "", "bucket": "artifacts"}}

	got := RedactedBlobStore(bs)

	require.NotContains(t, got.Config, "secret_key")
	require.NotContains(t, got.Config, SecretKeySetKey)
	require.Equal(t, "artifacts", got.Config["bucket"])
}

func TestRedactedBlobStore_NilConfig(t *testing.T) {
	t.Parallel()
	bs := BlobStore{Name: "local"}

	got := RedactedBlobStore(bs)

	require.Nil(t, got.Config)
	require.Equal(t, "local", got.Name)
}

func TestRedactedBlobStores_RedactsEveryEntry(t *testing.T) {
	t.Parallel()
	list := []BlobStore{
		{Name: "a", Config: map[string]any{"secret_key": "one"}},
		{Name: "b"},
		{Name: "c", Config: map[string]any{"secret_key": "three"}},
	}

	got := RedactedBlobStores(list)

	require.Len(t, got, 3)
	require.NotContains(t, got[0].Config, "secret_key")
	require.NotContains(t, got[2].Config, "secret_key")
	require.Equal(t, "one", list[0].Config["secret_key"], "input must stay intact")
}

func TestRedactedBlobStores_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, RedactedBlobStores(nil))
}
