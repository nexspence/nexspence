package domain

const (
	// ProxyPasswordKey is the proxyConfig entry holding the upstream proxy password.
	ProxyPasswordKey = "proxy_password"
	// ProxyPasswordSetKey is a read-only proxyConfig marker the API adds in place of
	// ProxyPasswordKey so clients can tell a password is stored without receiving it.
	ProxyPasswordSetKey = "proxy_password_set"
	// RemotePasswordKey is the proxyConfig entry holding the password sent to the
	// upstream registry itself (HTTP Basic), as opposed to ProxyPasswordKey, which
	// authenticates to an outbound forward proxy on the way there (#281).
	RemotePasswordKey = "remote_password"
	// RemotePasswordSetKey mirrors ProxyPasswordSetKey for RemotePasswordKey.
	RemotePasswordSetKey = "remote_password_set"
	// SecretKeyKey is the blob store config entry holding the S3 secret access key.
	SecretKeyKey = "secret_key"
	// SecretKeySetKey is the read-only blob store config marker the API adds in place
	// of SecretKeyKey, mirroring ProxyPasswordSetKey.
	SecretKeySetKey = "secret_key_set"
)

// redactedConfig copies cfg without secretKey, substituting a `<secretKey>_set: true`
// marker when a non-empty secret is present. A nil cfg is returned unchanged.
func redactedConfig(cfg map[string]any, secretKey, setKey string) map[string]any {
	if cfg == nil {
		return nil
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if k == secretKey || k == setKey {
			continue
		}
		out[k] = v
	}
	if s, ok := cfg[secretKey].(string); ok && s != "" {
		out[setKey] = true
	}
	return out
}

// RedactedBlobStore returns a copy of bs with the S3 secret access key stripped from
// its config, so blob store payloads can be served to any reader. When a secret is
// stored, SecretKeySetKey is set to true in its place. The input is untouched.
func RedactedBlobStore(bs BlobStore) BlobStore {
	bs.Config = redactedConfig(bs.Config, SecretKeyKey, SecretKeySetKey)
	return bs
}

// RedactedBlobStores applies RedactedBlobStore to every entry, leaving the input slice intact.
func RedactedBlobStores(list []BlobStore) []BlobStore {
	if list == nil {
		return nil
	}
	out := make([]BlobStore, len(list))
	for i, bs := range list {
		out[i] = RedactedBlobStore(bs)
	}
	return out
}

// RedactedRepository returns a copy of r with proxy credentials stripped from
// proxyConfig, so repository payloads can be served to any reader. When a password
// is stored, ProxyPasswordSetKey is set to true in its place. The input is untouched.
func RedactedRepository(r Repository) Repository {
	r.ProxyConfig = redactedConfig(r.ProxyConfig, ProxyPasswordKey, ProxyPasswordSetKey)
	r.ProxyConfig = redactedConfig(r.ProxyConfig, RemotePasswordKey, RemotePasswordSetKey)
	return r
}

// RedactedRepositories applies RedactedRepository to every entry, leaving the input slice intact.
func RedactedRepositories(list []Repository) []Repository {
	if list == nil {
		return nil
	}
	out := make([]Repository, len(list))
	for i, r := range list {
		out[i] = RedactedRepository(r)
	}
	return out
}
