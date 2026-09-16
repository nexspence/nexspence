package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
)

// MemberInfo carries the blob-store fields needed for fill-policy member selection.
type MemberInfo struct {
	ID         string
	QuotaBytes *int64
	UsedBytes  int64
}

// BlobStoreDescriptor carries the minimal DB data needed to instantiate a physical BlobStore.
type BlobStoreDescriptor struct {
	ID     string
	Type   string // "local" | "s3" | "group" | "azure"
	Config map[string]any
}

// PhysicalStoreIdentity returns a stable identifier for the physical object
// namespace addressed by desc. Credentials and the logical database ID are
// deliberately excluded: two logical Azure stores can share one container,
// and GC must treat a blob referenced by either store as live.
func PhysicalStoreIdentity(desc BlobStoreDescriptor) string {
	switch desc.Type {
	case "azure":
		// Let the SDK resolve connection strings exactly as it does for I/O.
		// Separate account/endpoint fields are ignored in this credential mode.
		endpoint := azureServiceURL(strVal(desc.Config, "endpoint"), strVal(desc.Config, "account_name")) + "/" + strVal(desc.Config, "container")
		if cs := strVal(desc.Config, "connection_string"); cs != "" {
			client, err := container.NewClientFromConnectionString(azureIdentityConnectionString(cs), strVal(desc.Config, "container"), nil)
			if err != nil {
				return "" // An invalid descriptor has no known physical namespace.
			}
			endpoint = client.URL()
		}
		u, err := url.Parse(endpoint)
		if err != nil || u.Host == "" {
			return ""
		}
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		u.User, u.RawQuery, u.Fragment = nil, "", ""
		u.ForceQuery = false
		return "azure|" + u.String()
	case "s3":
		return "s3|" + strings.ToLower(strings.TrimRight(strVal(desc.Config, "endpoint"), "/")) + "|" + strVal(desc.Config, "region") + "|" + strVal(desc.Config, "bucket")
	case "local", "":
		path := strVal(desc.Config, "path")
		if path == "" {
			path = "./data/blobs"
		}
		return "local|" + path
	default:
		// Unknown and group descriptors are not known to share a physical
		// namespace. Keep the logical ID in the fallback key.
		return desc.Type + "|" + desc.ID
	}
}

// azureIdentityConnectionString leaves endpoint resolution to the SDK while
// replacing credentials with syntactically valid placeholders. An invalid or
// expired credential must not hide a live blob's namespace from GC.
func azureIdentityConnectionString(cs string) string {
	parts := strings.Split(cs, ";")
	for i, part := range parts {
		key, _, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch key {
		case "AccountKey":
			parts[i] = "AccountKey=YQ=="
		case "SharedAccessSignature":
			parts[i] = "SharedAccessSignature=sig=identity"
		}
	}
	return strings.Join(parts, ";")
}

// Registry creates and caches physical BlobStore instances keyed by blob store ID.
// Safe for concurrent use. The default store is returned when Get is called with
// an empty/unrecognized descriptor.
type Registry struct {
	mu           sync.RWMutex
	instances    map[string]BlobStore
	defaultStore BlobStore
	rrCounters   sync.Map // groupID → *atomic.Uint64
}

// NewRegistry creates a Registry that returns defaultStore for empty descriptors.
func NewRegistry(defaultStore BlobStore) *Registry {
	return &Registry{
		instances:    make(map[string]BlobStore),
		defaultStore: defaultStore,
	}
}

// Get returns a cached or newly-created BlobStore for desc.
// Falls back to the default store when desc.ID is empty.
func (r *Registry) Get(ctx context.Context, desc BlobStoreDescriptor) (BlobStore, error) {
	if desc.ID == "" {
		return r.defaultStore, nil
	}

	r.mu.RLock()
	if bs, ok := r.instances[desc.ID]; ok {
		r.mu.RUnlock()
		return bs, nil
	}
	r.mu.RUnlock()

	bs, err := newFromDescriptor(ctx, desc)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	// Double-check after acquiring write lock.
	if existing, ok := r.instances[desc.ID]; ok {
		r.mu.Unlock()
		return existing, nil
	}
	r.instances[desc.ID] = bs
	r.mu.Unlock()
	return bs, nil
}

// Invalidate removes a cached instance so the next Get recreates it.
// Call after updating a blob store's config.
func (r *Registry) Invalidate(id string) {
	r.mu.Lock()
	delete(r.instances, id)
	r.mu.Unlock()
}

// hasCapacity reports whether m can still take a write. A member with no quota
// is bounded only by its filesystem.
func hasCapacity(m MemberInfo) bool {
	return m.QuotaBytes == nil || m.UsedBytes < *m.QuotaBytes
}

// PickMember selects a member blob store ID according to the fill policy.
// Returns "" when members is empty or every member is at capacity — the caller
// turns that into a quota rejection, which is the only quota gate a group store
// has: checkQuota deliberately skips the group's own aggregate.
//
// Every policy skips full members. Round-robin used to rotate blindly, so a
// member backed by a small volume kept accepting writes past its quota until
// the filesystem refused, failing an upload mid-flight instead of rejecting it
// before a byte was written.
func (r *Registry) PickMember(groupID, policy string, members []MemberInfo) string {
	if len(members) == 0 {
		return ""
	}
	switch policy {
	case "round_robin":
		v, _ := r.rrCounters.LoadOrStore(groupID, new(atomic.Uint64))
		ctr := v.(*atomic.Uint64)
		start := ctr.Add(1) - 1
		n := uint64(len(members))
		for i := uint64(0); i < n; i++ {
			if m := members[(start+i)%n]; hasCapacity(m) {
				return m.ID
			}
		}
		return ""
	default:
		// write_to_first_fill, and anything unrecognized: fill members in order,
		// moving on only once one is full.
		for _, m := range members {
			if hasCapacity(m) {
				return m.ID
			}
		}
		return ""
	}
}

// NewFromConfig creates a BlobStore directly from type + config map (no caching).
// Used by the test-connection endpoint.
func NewFromConfig(ctx context.Context, bsType string, cfg map[string]any) (BlobStore, error) {
	return newFromDescriptor(ctx, BlobStoreDescriptor{Type: bsType, Config: cfg})
}

func newFromDescriptor(ctx context.Context, desc BlobStoreDescriptor) (BlobStore, error) {
	switch desc.Type {
	case "s3":
		opts := S3Options{
			Bucket:          strVal(desc.Config, "bucket"),
			Region:          strVal(desc.Config, "region"),
			Endpoint:        strVal(desc.Config, "endpoint"),
			AccessKeyID:     strVal(desc.Config, "access_key"),
			SecretAccessKey: strVal(desc.Config, "secret_key"),
		}
		// Force path style when a custom endpoint is provided (standard for MinIO/Ceph).
		if opts.Endpoint != "" {
			opts.ForcePathStyle = true
		}
		if bv, ok := desc.Config["force_path_style"].(bool); ok {
			opts.ForcePathStyle = bv
		}
		opts.SkipTLSVerify = boolVal(desc.Config, "skip_tls_verify")
		if opts.Bucket == "" {
			return nil, fmt.Errorf("s3 blob store: bucket is required")
		}
		return NewS3BlobStore(ctx, opts)
	case "azure":
		opts := AzureOptions{
			Container:        strVal(desc.Config, "container"),
			AccountName:      strVal(desc.Config, "account_name"),
			AccountKey:       strVal(desc.Config, "account_key"),
			ConnectionString: strVal(desc.Config, "connection_string"),
			SASToken:         strVal(desc.Config, "sas_token"),
			Endpoint:         strVal(desc.Config, "endpoint"),
			SkipTLSVerify:    boolVal(desc.Config, "skip_tls_verify"),
		}
		if opts.Container == "" {
			return nil, fmt.Errorf("azure blob store: container is required")
		}
		return NewAzureBlobStore(ctx, opts)
	case "local", "":
		path := strVal(desc.Config, "path")
		if path == "" {
			path = "./data/blobs"
		}
		return NewLocalBlobStore(path)
	default:
		return nil, fmt.Errorf("unknown blob store type %q", desc.Type)
	}
}

func strVal(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

// boolVal reads a boolean config value. A store config round-trips through
// JSONB, so a value the frontend sent as true can come back as the string
// "true"; both spellings mean the same thing here.
func boolVal(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	default:
		return false
	}
}
