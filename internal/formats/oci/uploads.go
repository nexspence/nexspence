package oci

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// uploadKeyPrefix namespaces in-progress blob uploads inside the blob store.
// Staging there rather than in process memory keeps a push alive when its
// POST/PATCH/PUT chain is spread over several instances, or when the process
// restarts mid-push (#104). Abandoned sessions are plain unreferenced blobs, so
// the blob GC reclaims them once they pass its minimum-age gate.
const uploadKeyPrefix = "_uploads/docker/"

// uploadStore keeps in-progress blob uploads in the default blob store.
//
// The Docker protocol drives one upload strictly sequentially (POST, then
// PATCH per chunk, then PUT), so appends need no cross-request locking; two
// clients racing on the same upload id is already undefined by the spec.
type uploadStore struct{ deps formats.Deps }

// newUploadID returns an unguessable upload id. It must not be derived from
// process-local state: two instances would otherwise hand out the same id.
func newUploadID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("upload id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// validUploadID guards the blob key against path traversal via the URL: ids we
// mint are hex, so anything else cannot name an existing session anyway.
func validUploadID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func (s uploadStore) key(id string) string { return uploadKeyPrefix + id }

// appendable returns the blob store's incremental-append extension when it has
// one. Without it a chunk can only be staged by rewriting the whole session, so
// every method below keeps a fallback branch that does exactly that (#214).
func (s uploadStore) appendable() (storage.AppendableBlobStore, bool) {
	as, ok := s.deps.BlobStore.(storage.AppendableBlobStore)
	return as, ok
}

// create starts an empty session and returns its id.
func (s uploadStore) create(ctx context.Context) (string, error) {
	id, err := newUploadID()
	if err != nil {
		return "", err
	}
	if err := s.deps.BlobStore.Put(ctx, s.key(id), bytes.NewReader(nil), 0); err != nil {
		return "", err
	}
	return id, nil
}

// size returns the bytes received so far; ok is false when the session is unknown.
func (s uploadStore) size(ctx context.Context, id string) (n int64, ok bool) {
	if !validUploadID(id) {
		return 0, false
	}
	if as, isAppendable := s.appendable(); isAppendable {
		// Bytes handed to AppendBlob may not be visible at the key yet, so the
		// store — not a stat of the key — is what knows how far the session got.
		n, ok, err := as.AppendedSize(ctx, s.key(id))
		if err != nil {
			return 0, false
		}
		return n, ok
	}
	exists, err := s.deps.BlobStore.Exists(ctx, s.key(id))
	if err != nil || !exists {
		return 0, false
	}
	n, err = s.deps.BlobStore.Size(ctx, s.key(id))
	if err != nil {
		return 0, false
	}
	return n, true
}

// open returns a reader over everything received so far, with the session's
// size; ok is false when the session is unknown. The caller closes the reader.
//
// This is read's streaming counterpart. read has to materialize the session to
// concatenate onto it, which is inherent to the whole-object fallback append
// path — but finalizing a push does not, and buffering there put a whole layer
// (up to MaxUploadBytes, 10 GiB by default) in RAM at the moment of the commit
// (#385).
func (s uploadStore) open(ctx context.Context, id string) (rc io.ReadCloser, size int64, ok bool, err error) {
	if _, ok = s.size(ctx, id); !ok {
		return nil, 0, false, nil
	}
	if as, isAppendable := s.appendable(); isAppendable {
		// Publish the appended bytes at the key before reading them back: on a
		// backend that stages them out of band (S3 multipart) a Get would
		// otherwise return whatever the key held before the session started.
		// Idempotent, so a client re-sending the finalizing PUT is fine.
		if err := as.FinalizeAppend(ctx, s.key(id)); err != nil {
			return nil, 0, true, err
		}
	}
	rc, size, err = s.deps.BlobStore.Get(ctx, s.key(id))
	if err != nil {
		return nil, 0, true, err
	}
	return rc, size, true, nil
}

// read returns everything received so far; ok is false when the session is unknown.
func (s uploadStore) read(ctx context.Context, id string) (data []byte, ok bool, err error) {
	rc, _, ok, err := s.open(ctx, id)
	if err != nil || !ok {
		return nil, ok, err
	}
	defer func() { _ = rc.Close() }()
	data, err = io.ReadAll(rc)
	return data, true, err
}

// errUploadTooLarge reports a session that would grow past deps.MaxUploadBytes.
// Handlers map it to 413 rather than a generic 500.
var errUploadTooLarge = errors.New("upload exceeds the configured maximum size")

// append adds a chunk and returns the new total size. ok is false when the
// session is unknown.
//
// On a store with the append extension the chunk streams straight onto the
// staged blob, so a push costs O(bytes pushed) in total. Otherwise the blob
// store API is whole-object and the chunk is concatenated onto what is already
// staged, which makes an N-chunk push move O(N²) bytes (#214).
//
// With a cap configured, the session size is checked with a stat-only size()
// call and the chunk is read through a LimitReader before anything already
// staged is loaded — so an over-cap push never re-buffers the session's bytes,
// which is what made the read-modify-write cycle a memory-exhaustion vector.
func (s uploadStore) append(ctx context.Context, id string, r io.Reader) (total int64, ok bool, err error) {
	stored, ok := s.size(ctx, id)
	if !ok {
		return 0, false, nil
	}

	limit := s.deps.MaxUploadBytes
	if limit > 0 {
		if stored >= limit {
			return 0, true, errUploadTooLarge
		}
		// One byte past the remaining budget is enough to detect the overflow
		// without buffering more than the cap allows.
		r = io.LimitReader(r, limit-stored+1)
	}

	if as, isAppendable := s.appendable(); isAppendable {
		return s.appendStreaming(ctx, as, id, r, stored, limit)
	}

	var chunk bytes.Buffer
	if _, err := io.Copy(&chunk, r); err != nil {
		return 0, true, err
	}
	if limit > 0 && stored+int64(chunk.Len()) > limit {
		return 0, true, errUploadTooLarge
	}

	existing, ok, err := s.read(ctx, id)
	if err != nil || !ok {
		return 0, ok, err
	}
	var buf bytes.Buffer
	buf.Grow(len(existing) + chunk.Len())
	buf.Write(existing)
	buf.Write(chunk.Bytes())
	if err := s.deps.BlobStore.Put(ctx, s.key(id), bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		return 0, true, err
	}
	return int64(buf.Len()), true, nil
}

// appendStreaming stages the chunk through the append extension: nothing
// already staged is read, and nothing but the chunk itself is written.
//
// A streaming append cannot know a chunk's length before committing it —
// measuring it up front means buffering it, the very thing being avoided — so a
// chunk that crosses the cap is written and then rolled back to where the
// session was. The session is left holding exactly the bytes it held before, so
// the rejected chunk consumes none of the remaining budget.
func (s uploadStore) appendStreaming(ctx context.Context, as storage.AppendableBlobStore,
	id string, r io.Reader, stored, limit int64,
) (total int64, ok bool, err error) {
	total, err = as.AppendBlob(ctx, s.key(id), r)
	if err != nil {
		return 0, true, err
	}
	if limit > 0 && total > limit {
		if terr := as.TruncateBlob(ctx, s.key(id), stored); terr != nil {
			return 0, true, fmt.Errorf("%w (rolling the session back to %d bytes failed: %w)",
				errUploadTooLarge, stored, terr)
		}
		return 0, true, errUploadTooLarge
	}
	return total, true, nil
}

// remove drops a finished (or abandoned) session.
func (s uploadStore) remove(ctx context.Context, id string) {
	if !validUploadID(id) {
		return
	}
	if as, isAppendable := s.appendable(); isAppendable {
		// Deleting the key alone leaves whatever an unfinished append holds
		// behind the scenes (S3 multipart parts), which no GC sweep can see.
		_ = as.AbortAppend(ctx, s.key(id))
	}
	_ = s.deps.BlobStore.Delete(ctx, s.key(id))
}
