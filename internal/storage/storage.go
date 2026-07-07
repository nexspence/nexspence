package storage

import (
	"context"
	"io"
	"time"
)

// BlobStore is the interface every storage backend must implement.
// Keys are opaque strings (typically UUID-based paths).
type BlobStore interface {
	// Put streams data from r into the store under key.
	Put(ctx context.Context, key string, r io.Reader, size int64) error

	// Get returns a ReadCloser for the blob. Caller must close it.
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)

	// Delete removes a blob. No error if the key doesn't exist.
	Delete(ctx context.Context, key string) error

	// Exists reports whether a blob exists.
	Exists(ctx context.Context, key string) (bool, error)

	// Size returns the stored byte size of a blob.
	Size(ctx context.Context, key string) (int64, error)

	// UsedBytes returns total bytes stored in this blob store.
	UsedBytes(ctx context.Context) (int64, error)

	// ListKeys returns all blob keys present in the store.
	// Used by GC to find orphaned blobs not referenced by any asset.
	ListKeys(ctx context.Context) ([]string, error)

	// ListEntries returns every blob in the store with its size and last-modified
	// time. Used by GC to age-gate orphan deletion.
	ListEntries(ctx context.Context) ([]BlobEntry, error)
}

// BlobEntry describes one stored blob for GC listing.
type BlobEntry struct {
	Key     string
	Size    int64
	ModTime time.Time
}

// PresignableStore is an optional extension of BlobStore for S3-backed stores.
// Check with a type assertion: ps, ok := store.(storage.PresignableStore)
type PresignableStore interface {
	// PresignGetURL returns a time-limited URL for direct client download.
	PresignGetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	// PresignPutURL returns a time-limited URL for direct client upload.
	PresignPutURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	// ConfigureLifecycle sets a bucket lifecycle expiration rule.
	// Pass 0 to remove all rules.
	ConfigureLifecycle(ctx context.Context, expirationDays int32) error
}

// Meta holds blob content metadata returned alongside the data stream.
type Meta struct {
	Key         string
	Size        int64
	ContentType string
	SHA256      string
}
