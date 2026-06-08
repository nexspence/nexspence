//go:build !windows

package handlers

import "syscall"

// diskUsage returns the available and total bytes of the filesystem that
// contains path. ok is false if the figures could not be determined.
func diskUsage(path string) (free, total uint64, ok bool) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, 0, false
	}
	bsize := uint64(fs.Bsize) //nolint:gosec // Bsize is a filesystem block size — always positive
	return fs.Bavail * bsize, fs.Blocks * bsize, true
}
