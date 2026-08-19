package service_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/service"
)

// fakeTrivy writes an executable shell script that prints the given version.
func fakeTrivy(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "trivy")
	script := "#!/bin/sh\necho 'Version: " + version + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("fakeTrivy: %v", err)
	}
	return bin
}

func TestScanner_Disabled(t *testing.T) {
	svc := service.NewScanService(nil, "http://localhost:8081").
		WithTrivy(service.TrivyOptions{Enabled: false, Bin: fakeTrivy(t, "0.70.0")})

	st := svc.Scanner(context.Background())
	if st.State != service.ScannerDisabled {
		t.Fatalf("State = %q, want %q", st.State, service.ScannerDisabled)
	}
	if st.Version != "" || st.Path != "" {
		t.Error("a disabled scanner must not report a version or a path — it was never probed")
	}
	if !strings.Contains(st.Message, "disabled") {
		t.Errorf("Message = %q, want it to say the capability is disabled", st.Message)
	}
}

func TestScanner_Missing(t *testing.T) {
	svc := service.NewScanService(nil, "http://localhost:8081").
		WithTrivy(service.TrivyOptions{Enabled: true, Bin: "/nonexistent/trivy"})

	st := svc.Scanner(context.Background())
	if st.State != service.ScannerMissing {
		t.Fatalf("State = %q, want %q", st.State, service.ScannerMissing)
	}
	if !strings.Contains(st.Message, "/nonexistent/trivy") {
		t.Errorf("Message = %q, want it to name the path that was looked for", st.Message)
	}
}

func TestScanner_Broken(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "trivy")
	// Present and executable, but exits non-zero — the shape of a wrong-arch
	// or truncated binary as far as the caller can tell.
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'exec format error' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	svc := service.NewScanService(nil, "http://localhost:8081").
		WithTrivy(service.TrivyOptions{Enabled: true, Bin: bin})

	st := svc.Scanner(context.Background())
	if st.State != service.ScannerBroken {
		t.Fatalf("State = %q, want %q", st.State, service.ScannerBroken)
	}
	if !strings.Contains(st.Message, "exec format error") {
		t.Errorf("Message = %q, want it to carry the binary's own error output", st.Message)
	}
	if !strings.Contains(st.Message, bin) {
		t.Errorf("Message = %q, want it to name the resolved path %q", st.Message, bin)
	}
}

func TestScanner_ReadyWithoutVersionLine(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "trivy")
	// Runs fine but prints no "Version:" line — the Message must still be a
	// clean sentence, not "Trivy  — /path" with a doubled space.
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'no version here'\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	svc := service.NewScanService(nil, "http://localhost:8081").
		WithTrivy(service.TrivyOptions{Enabled: true, Bin: bin})

	st := svc.Scanner(context.Background())
	if st.State != service.ScannerReady {
		t.Fatalf("State = %q, want %q (message: %s)", st.State, service.ScannerReady, st.Message)
	}
	if st.Version != "" {
		t.Errorf("Version = %q, want empty", st.Version)
	}
	if want := "Trivy — " + bin; st.Message != want {
		t.Errorf("Message = %q, want %q", st.Message, want)
	}
}

func TestScanner_Ready(t *testing.T) {
	bin := fakeTrivy(t, "0.70.0")
	svc := service.NewScanService(nil, "http://localhost:8081").
		WithTrivy(service.TrivyOptions{Enabled: true, Bin: bin})

	st := svc.Scanner(context.Background())
	if st.State != service.ScannerReady {
		t.Fatalf("State = %q, want %q (message: %s)", st.State, service.ScannerReady, st.Message)
	}
	if st.Version != "0.70.0" {
		t.Errorf("Version = %q, want 0.70.0", st.Version)
	}
	if st.Path != bin {
		t.Errorf("Path = %q, want %q", st.Path, bin)
	}
}

func TestScanner_CachesWithinTTL(t *testing.T) {
	bin := fakeTrivy(t, "0.70.0")
	svc := service.NewScanService(nil, "http://localhost:8081").
		WithTrivy(service.TrivyOptions{Enabled: true, Bin: bin})

	if st := svc.Scanner(context.Background()); st.State != service.ScannerReady {
		t.Fatalf("first probe: %q", st.State)
	}
	// Remove the binary. Within the TTL the answer must not change: re-probing
	// on every call would spawn a process per page render.
	if err := os.Remove(bin); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if st := svc.Scanner(context.Background()); st.State != service.ScannerReady {
		t.Errorf("second probe within TTL = %q, want the cached %q", st.State, service.ScannerReady)
	}
}
