package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNoSpace reports that a write failed because the backing storage ran out of
// space. Callers map it to 507 Insufficient Storage instead of an opaque 500.
var ErrNoSpace = errors.New("no space left on device")

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

// AppendableBlobStore is an optional extension of BlobStore for backends that
// can grow a blob in place, without reading back what is already stored.
// Check with a type assertion: as, ok := store.(storage.AppendableBlobStore)
//
// It exists for chunked uploads (the OCI blob PATCH sequence): with Put/Get
// alone, every chunk has to re-read and rewrite the whole staged blob, so an
// N-chunk push moves O(N²) bytes. Callers must keep a fallback path for stores
// that do not implement it.
//
// Every method is keyed exactly like Put/Get — no in-process handle, channel or
// goroutine — so a staged upload survives being continued by another instance,
// or by a fresh process after a restart.
type AppendableBlobStore interface {
	// AppendBlob appends everything r yields to the blob at key, creating it if
	// absent, and returns the blob's new total size. Cost is O(len(chunk)), not
	// O(current size).
	AppendBlob(ctx context.Context, key string, r io.Reader) (total int64, err error)

	// TruncateBlob shrinks the blob at key back to size. A streaming append
	// cannot measure an incoming chunk before committing it — that is the
	// buffering being avoided — so a caller enforcing a size cap appends first
	// and rolls back here.
	TruncateBlob(ctx context.Context, key string, size int64) error

	// AppendedSize returns the bytes staged so far, including any an in-progress
	// append has not published at key yet; ok is false when nothing is staged.
	// Plain Size cannot answer this for backends where append progress is
	// invisible until FinalizeAppend runs (S3 multipart).
	AppendedSize(ctx context.Context, key string) (size int64, ok bool, err error)

	// FinalizeAppend publishes everything appended so far as the blob at key, so
	// Get/Size see it. Idempotent, and a no-op when no append is in progress.
	FinalizeAppend(ctx context.Context, key string) error

	// AbortAppend discards an unfinished append and releases whatever the
	// backend holds for it (S3 multipart parts, which nothing else reclaims).
	// Idempotent, and a no-op when no append is in progress.
	AbortAppend(ctx context.Context, key string) error
}

// Meta holds blob content metadata returned alongside the data stream.
type Meta struct {
	Key         string
	Size        int64
	ContentType string
	SHA256      string
}
