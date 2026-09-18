package storage

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ProbeLocalWritable reports whether path can be used as a local blob store
// root. A store directory that does not exist yet is the normal case at
// create time, so this creates it and then writes and removes a temporary
// file. Exists() on the resulting store would succeed on a read-only volume
// (the Helm image's root filesystem, a PVC mounted elsewhere) and the
// operator would only find out on the first upload.
func ProbeLocalWritable(path string) error {
	if path == "" {
		path = DefaultLocalBasePath
	}
	if err := os.MkdirAll(path, 0o750); err != nil {
		return classifyLocalPathErr(path, err)
	}
	f, err := os.CreateTemp(path, ".nexspence-write-probe-*")
	if err != nil {
		return classifyLocalPathErr(path, err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return classifyLocalPathErr(path, err)
	}
	if err := os.Remove(name); err != nil {
		return classifyLocalPathErr(path, err)
	}
	return nil
}

// classifyLocalPathErr names EROFS and missing write permission explicitly:
// the kernel's "read-only file system" does not tell an admin that the path
// sits outside the container's writable volume.
func classifyLocalPathErr(path string, err error) error {
	switch {
	case errors.Is(err, syscall.EROFS):
		return fmt.Errorf("path %q is on a read-only filesystem (EROFS); additional local blob stores must live under the writable blob volume", path)
	case errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM):
		return fmt.Errorf("path %q is not writable: permission denied", path)
	default:
		return fmt.Errorf("path %q is not writable: %w", path, err)
	}
}
