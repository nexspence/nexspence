package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// makeTarGz builds an in-memory .tar.gz with a single entry of the given
// decompressed size (filled with zero bytes, which gzip compresses heavily —
// a small archive that expands far past the cap, i.e. a gzip bomb).
func makeTarGz(t *testing.T, entryName string, size int) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{
		Name: entryName,
		Mode: 0o600,
		Size: int64(size),
	}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write(make([]byte, size)); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func TestReadBackupArchiveLimited_RejectsOversizeEntry(t *testing.T) {
	// 4 MiB entry, 1 MiB cap → must error.
	archive := makeTarGz(t, "components.json", 4<<20)
	_, err := readBackupArchiveLimited(bytes.NewReader(archive), 1<<20)
	if err == nil {
		t.Fatalf("expected error for oversize archive, got nil")
	}
	if !strings.Contains(err.Error(), "decompression limit") {
		t.Errorf("expected decompression-limit error, got: %v", err)
	}
}

func TestReadBackupArchiveLimited_AllowsWithinLimit(t *testing.T) {
	archive := makeTarGz(t, "repository.json", 1024)
	a, err := readBackupArchiveLimited(bytes.NewReader(archive), 1<<20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(a.entries["repository.json"]); got != 1024 {
		t.Errorf("expected 1024-byte entry, got %d", got)
	}
}
