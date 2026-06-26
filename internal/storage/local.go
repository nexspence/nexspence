package storage

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// LocalBlobStore stores blobs as files under a base directory.
// Key "ab/cd/abcdef123..." maps to <basePath>/ab/cd/abcdef123...
type LocalBlobStore struct {
	basePath string
}

// NewLocalBlobStore creates a LocalBlobStore rooted at basePath, creating the directory if needed.
func NewLocalBlobStore(basePath string) (*LocalBlobStore, error) {
	if err := os.MkdirAll(basePath, 0o750); err != nil {
		return nil, fmt.Errorf("create blob store dir %s: %w", basePath, err)
	}
	return &LocalBlobStore{basePath: basePath}, nil
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
	// Write to a temp file first, then rename (atomic on same filesystem)
	tmp := dst + ".tmp"
	f, err := os.Create(tmp) //nolint:gosec // dst is validated by keyPath to stay within the blob store base dir
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
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
