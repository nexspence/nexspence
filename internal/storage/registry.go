package storage

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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
	Type   string // "local" | "s3"
	Config map[string]any
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

// PickMember selects a member blob store ID according to the fill policy.
// Returns "" if members is empty or all members are at capacity (write_to_first_fill).
func (r *Registry) PickMember(groupID, policy string, members []MemberInfo) string {
	if len(members) == 0 {
		return ""
	}
	switch policy {
	case "round_robin":
		v, _ := r.rrCounters.LoadOrStore(groupID, new(atomic.Uint64))
		ctr := v.(*atomic.Uint64)
		idx := ctr.Add(1) - 1
		return members[idx%uint64(len(members))].ID
	case "write_to_first_fill":
		for _, m := range members {
			if m.QuotaBytes == nil || m.UsedBytes < *m.QuotaBytes {
				return m.ID
			}
		}
		return ""
	default:
		return members[0].ID
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
		if opts.Bucket == "" {
			return nil, fmt.Errorf("s3 blob store: bucket is required")
		}
		return NewS3BlobStore(ctx, opts)
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
