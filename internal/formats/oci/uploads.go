package oci

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/nexspence-oss/nexspence/internal/formats"
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

// read returns everything received so far; ok is false when the session is unknown.
func (s uploadStore) read(ctx context.Context, id string) (data []byte, ok bool, err error) {
	if _, ok = s.size(ctx, id); !ok {
		return nil, false, nil
	}
	rc, _, err := s.deps.BlobStore.Get(ctx, s.key(id))
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = rc.Close() }()
	data, err = io.ReadAll(rc)
	return data, true, err
}

// append adds a chunk and returns the new total size. ok is false when the
// session is unknown. The blob store API is whole-object, so the chunk is
// concatenated onto what is already staged.
func (s uploadStore) append(ctx context.Context, id string, r io.Reader) (total int64, ok bool, err error) {
	existing, ok, err := s.read(ctx, id)
	if err != nil || !ok {
		return 0, ok, err
	}
	var buf bytes.Buffer
	buf.Write(existing)
	if _, err := io.Copy(&buf, r); err != nil {
		return 0, true, err
	}
	if err := s.deps.BlobStore.Put(ctx, s.key(id), bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		return 0, true, err
	}
	return int64(buf.Len()), true, nil
}

// remove drops a finished (or abandoned) session.
func (s uploadStore) remove(ctx context.Context, id string) {
	if !validUploadID(id) {
		return
	}
	_ = s.deps.BlobStore.Delete(ctx, s.key(id))
}
