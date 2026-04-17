package storage

import (
	"context"
	"io"
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
}

// Meta holds blob content metadata returned alongside the data stream.
type Meta struct {
	Key         string
	Size        int64
	ContentType string
	SHA256      string
}
