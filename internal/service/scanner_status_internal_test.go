package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestScanner_ReprobesAfterTTL pins the refresh branch: once the TTL has
// passed, Scanner must run a fresh probe and see the world as it now is.
// It lives in package service because the nowFn seam is private.
func TestScanner_ReprobesAfterTTL(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "trivy")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'Version: 0.70.0'\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	svc := NewScanService(nil, "http://localhost:8081").
		WithTrivy(TrivyOptions{Enabled: true, Bin: bin})

	now := time.Now()
	svc.nowFn = func() time.Time { return now }

	if st := svc.Scanner(context.Background()); st.State != ScannerReady {
		t.Fatalf("first probe: %q", st.State)
	}
	firstAt := svc.statusAt

	if err := os.Remove(bin); err != nil {
		t.Fatalf("remove: %v", err)
	}
	now = now.Add(scannerStatusTTL + time.Second)

	if st := svc.Scanner(context.Background()); st.State != ScannerMissing {
		t.Fatalf("post-TTL probe = %q, want %q", st.State, ScannerMissing)
	}
	if !svc.statusAt.After(firstAt) {
		t.Error("statusAt was not refreshed by the post-TTL probe")
	}
}
