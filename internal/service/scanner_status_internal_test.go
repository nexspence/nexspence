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

// The probe result is a shared cache: a caller that hangs up while the probe
// runs must not get its cancellation recorded as ScannerBroken for everyone
// else in the TTL window.
func TestProbeScanner_CallerCancellationDoesNotPoisonTheCache(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("skip fake-trivy shell script test in CI (no /bin/sh guarantee)")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "trivy")
	script := "#!/bin/sh\necho 'Version: 0.70.0'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake trivy: %v", err)
	}

	s := &ScanService{trivy: TrivyOptions{Enabled: true, Bin: bin}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the caller is already gone when the probe starts

	st := s.probeScanner(ctx)
	if st.State != ScannerReady {
		t.Fatalf("State = %q (message %q), want %q: one caller's cancellation poisoned the shared probe result", st.State, st.Message, ScannerReady)
	}
	if st.Version != "0.70.0" {
		t.Errorf("Version = %q, want 0.70.0", st.Version)
	}
}
