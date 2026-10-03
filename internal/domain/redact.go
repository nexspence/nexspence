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

// SigningKeyKey and SigningKeyPassphraseKey are the formatConfig entries
// holding an apt repository's private signing key and its passphrase.
const (
	SigningKeyKey           = "signing_key"
	SigningKeyPassphraseKey = "signing_key_passphrase"
)

// formatConfigSecretKeys are every formatConfig entry that carries a secret.
// Like proxy passwords they are replaced by a "<key>_set" marker in every
// response, and an update that omits one keeps the stored value
// (GHSA-pg67-wh39-mx9j).
var formatConfigSecretKeys = []string{SigningKeyKey, SigningKeyPassphraseKey}

// FormatConfigSecretKeys returns the formatConfig keys treated as secrets.
func FormatConfigSecretKeys() []string {
	out := make([]string, len(formatConfigSecretKeys))
	copy(out, formatConfigSecretKeys)
	return out
}

// blobStoreSecretKeys are every blob store config entry that carries a
// credential. All of them are stripped from API responses and replaced by a
// "<key>_set" marker; an update that omits one keeps the stored value (see
// handlers.mergeBlobStoreConfig). Azure adds three beyond the S3 secret_key:
// an account key, a connection string (which embeds one), and a SAS token
// that is itself a bearer credential.
var blobStoreSecretKeys = []string{SecretKeyKey, "account_key", "connection_string", "sas_token"}

// BlobStoreSecretKeys returns the blob store config keys treated as secrets.
func BlobStoreSecretKeys() []string {
	out := make([]string, len(blobStoreSecretKeys))
	copy(out, blobStoreSecretKeys)
	return out
}

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

// RedactedBlobStore returns a copy of bs with every credential stripped from
// its config, so blob store payloads can be served to any reader. When a
// secret is stored, "<key>_set" is set to true in its place (secret_key
// mirrors the established SecretKeySetKey marker). The input is untouched.
func RedactedBlobStore(bs BlobStore) BlobStore {
	for _, k := range blobStoreSecretKeys {
		setKey := k + "_set"
		if k == SecretKeyKey {
			setKey = SecretKeySetKey
		}
		bs.Config = redactedConfig(bs.Config, k, setKey)
	}
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
// proxyConfig and signing secrets from formatConfig, so repository payloads
// can be served to any reader. When a password
// is stored, ProxyPasswordSetKey is set to true in its place. The input is untouched.
func RedactedRepository(r Repository) Repository {
	r.ProxyConfig = redactedConfig(r.ProxyConfig, ProxyPasswordKey, ProxyPasswordSetKey)
	r.ProxyConfig = redactedConfig(r.ProxyConfig, RemotePasswordKey, RemotePasswordSetKey)
	for _, k := range formatConfigSecretKeys {
		r.FormatConfig = redactedConfig(r.FormatConfig, k, k+"_set")
	}
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
