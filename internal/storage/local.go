package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// LocalBlobStore stores blobs as files under a base directory.
// Key "ab/cd/abcdef123..." maps to <basePath>/ab/cd/abcdef123...
type LocalBlobStore struct {
	basePath string
	// syncFile flushes a file (or directory) to durable storage. It is a field so
	// tests can simulate fsync failures, which cannot be provoked otherwise.
	syncFile func(*os.File) error
}

// NewLocalBlobStore creates a LocalBlobStore rooted at basePath, creating the directory if needed.
func NewLocalBlobStore(basePath string) (*LocalBlobStore, error) {
	if err := os.MkdirAll(basePath, 0o750); err != nil {
		return nil, fmt.Errorf("create blob store dir %s: %w", basePath, err)
	}
	return &LocalBlobStore{basePath: basePath, syncFile: (*os.File).Sync}, nil
}

func (s *LocalBlobStore) keyPath(key string) (string, error) {
	var p string
	// Shard by first 4 chars to avoid huge flat directories
	if len(key) >= 4 {
		p = filepath.Join(s.basePath, key[:2], key[2:4], key)
	} else {
		p = filepath.Join(s.basePath, key)
	}
	// Containment guard: filepath.Join cleans the result, so a key carrying
	// "../" segments (e.g. from an attacker-crafted backup/import archive)
	// would resolve outside basePath. Reject any key whose final path escapes
	// the blob store root.
	base := filepath.Clean(s.basePath)
	if p != base && !strings.HasPrefix(p, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid blob key %q: resolves outside blob store", key)
	}
	return p, nil
}

// Put writes the blob for key, staging to a temp file and renaming for atomicity.
func (s *LocalBlobStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	dst, err := s.keyPath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	// Write to a temp file first, then rename (atomic on same filesystem).
	// The temp name must be unique per attempt: a fixed dst+".tmp" gives two
	// concurrent Puts for the same key two descriptors on the *same* inode, and
	// the rename only republishes the directory entry — the loser keeps writing
	// into the blob the winner already made live, corrupting it (issue #196).
	// os.CreateTemp opens with O_EXCL and a random suffix, so every writer owns
	// its own file and cleans up only its own file.
	f, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp.*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return classifyWriteErr(err)
	}
	// Rename is atomic with respect to the name only: without an fsync the bytes
	// may still be in the page cache, so a crash could publish a truncated blob.
	if err := s.syncFile(f); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return classifyWriteErr(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.syncDir(filepath.Dir(dst))
	return nil
}

// AppendBlob appends r to the blob for key and returns its new total size,
// creating the blob when it does not exist yet.
//
// It opens the blob's own path with O_APPEND rather than staging a temp file:
// the point of the whole extension is to never read back — nor rewrite — what is
// already stored, which a copy-then-rename would do on every call.
//
// Deliberately not fsynced. The caller is upload staging, never a finished
// artifact — that still goes through Put, which does fsync — and syncing every
// chunk costs far more than the guarantee is worth here: a crash loses at most
// the tail of an upload that was never complete anyway, and the client resumes
// from the size the next progress GET reports.
func (s *LocalBlobStore) AppendBlob(_ context.Context, key string, r io.Reader) (int64, error) {
	dst, err := s.keyPath(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(dst, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // dst is validated by keyPath to stay within the blob store base dir
	if err != nil {
		return 0, err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return 0, classifyWriteErr(err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// TruncateBlob shrinks the blob for key to size.
func (s *LocalBlobStore) TruncateBlob(_ context.Context, key string, size int64) error {
	p, err := s.keyPath(key)
	if err != nil {
		return err
	}
	return os.Truncate(p, size)
}

// AppendedSize reports the bytes staged for key. An appended file is the live
// blob from the first byte on, so this is just Exists plus Size.
func (s *LocalBlobStore) AppendedSize(ctx context.Context, key string) (int64, bool, error) {
	exists, err := s.Exists(ctx, key)
	if err != nil || !exists {
		return 0, false, err
	}
	n, err := s.Size(ctx, key)
	if err != nil {
		return 0, false, err
	}
	return n, true, nil
}

// FinalizeAppend is a no-op: appended bytes are already readable at key.
func (s *LocalBlobStore) FinalizeAppend(_ context.Context, _ string) error { return nil }

// AbortAppend is a no-op: a local append holds no backend resource beyond the
// file itself, which the caller deletes with Delete.
func (s *LocalBlobStore) AbortAppend(_ context.Context, _ string) error { return nil }

// classifyWriteErr tags a full-disk failure with ErrNoSpace so handlers can
// answer 507 Insufficient Storage; any other error is returned unchanged.
func classifyWriteErr(err error) error {
	if errors.Is(err, syscall.ENOSPC) {
		return fmt.Errorf("%w: %w", ErrNoSpace, err)
	}
	return err
}

// syncDir flushes a directory entry so a completed rename survives a crash.
// Best effort: some filesystems reject fsync on directories, and by this point
// the blob contents are already durable.
func (s *LocalBlobStore) syncDir(dir string) {
	d, err := os.Open(dir) //nolint:gosec // dir is derived from a key validated by keyPath
	if err != nil {
		return
	}
	_ = s.syncFile(d)
	_ = d.Close()
}

// Get opens the blob for key and returns its reader and size.
func (s *LocalBlobStore) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	p, err := s.keyPath(key)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(p) //nolint:gosec // p is validated by keyPath to stay within the blob store base dir
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

// Delete removes the blob for key; a missing blob is not an error.
func (s *LocalBlobStore) Delete(_ context.Context, key string) error {
	p, err := s.keyPath(key)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Exists reports whether a blob is stored for key.
func (s *LocalBlobStore) Exists(_ context.Context, key string) (bool, error) {
	p, err := s.keyPath(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// Size returns the byte size of the blob for key.
func (s *LocalBlobStore) Size(_ context.Context, key string) (int64, error) {
	p, err := s.keyPath(key)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// ListKeys walks the store and returns every blob key, stripping the shard prefix.
func (s *LocalBlobStore) ListKeys(_ context.Context) ([]string, error) {
	var keys []string
	err := filepath.WalkDir(s.basePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		// Strip basePath prefix and the two shard dirs to recover the raw key.
		rel, _ := filepath.Rel(s.basePath, path)
		// rel = "ab/cd/abcdef..." → key = "abcdef..."
		parts := strings.SplitN(filepath.ToSlash(rel), "/", 3)
		if len(parts) == 3 {
			keys = append(keys, parts[2])
		} else {
			keys = append(keys, rel)
		}
		return nil
	})
	return keys, err
}

// ListEntries walks the store and returns each blob's key, size and mtime.
func (s *LocalBlobStore) ListEntries(_ context.Context) ([]BlobEntry, error) {
	var entries []BlobEntry
	err := filepath.WalkDir(s.basePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		rel, _ := filepath.Rel(s.basePath, path)
		parts := strings.SplitN(filepath.ToSlash(rel), "/", 3)
		key := rel
		if len(parts) == 3 {
			key = parts[2]
		}
		entries = append(entries, BlobEntry{Key: key, Size: info.Size(), ModTime: info.ModTime()})
		return nil
	})
	return entries, err
}

// UsedBytes returns the total size of all blobs under the base directory.
func (s *LocalBlobStore) UsedBytes(_ context.Context) (int64, error) {
	var total int64
	err := filepath.WalkDir(s.basePath, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
