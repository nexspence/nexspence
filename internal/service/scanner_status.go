package service

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// ScannerState is what nexspence can honestly say about image scanning right now.
type ScannerState string

const (
	// ScannerDisabled — the operator has not turned the capability on.
	ScannerDisabled ScannerState = "disabled"
	// ScannerMissing — turned on, but no binary at the configured location.
	ScannerMissing ScannerState = "missing"
	// ScannerBroken — a binary is there and will not run: wrong architecture,
	// truncated download, no execute permission. A different problem from
	// ScannerMissing, and pointing an operator at the wrong one costs an evening.
	ScannerBroken ScannerState = "broken"
	// ScannerReady — it runs, and reported its version.
	ScannerReady ScannerState = "ready"
)

// ScannerStatus is the answer to "can we scan, and what do we tell the user".
// Message is a finished sentence: the frontend renders it rather than
// reassembling one from State, so the wording lives in one place.
type ScannerStatus struct {
	State   ScannerState `json:"state"`
	Version string       `json:"version,omitempty"`
	Path    string       `json:"path,omitempty"`
	Message string       `json:"message"`
}

// Ready reports whether a scan can be attempted.
func (s ScannerStatus) Ready() bool { return s.State == ScannerReady }

// ScannerUnavailableError is returned by Scan when the capability is not ready.
// It carries the status so the API can answer with the same sentence the status
// endpoint would have given.
type ScannerUnavailableError struct{ Status ScannerStatus }

func (e *ScannerUnavailableError) Error() string { return e.Status.Message }

// scannerStatusTTL bounds how stale the probe may be. It exists because a
// binary can appear on a running system — an operator installs a package on the
// host — and demanding a restart for that would be rude; and because
// `trivy --version` costs ~200ms, which is too much per page render.
const scannerStatusTTL = 60 * time.Second

// trivyVersionRe matches the first line of `trivy --version`: "Version: 0.70.0".
var trivyVersionRe = regexp.MustCompile(`(?m)^Version:\s*(\S+)`)

// Scanner returns the current capability, probing at most once per TTL.
// statusMu is held across the probe on purpose: concurrent callers wait for
// one probe rather than each spawning their own; worst case is one 30s stall
// per TTL window.
func (s *ScanService) Scanner(ctx context.Context) ScannerStatus {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()

	if !s.statusAt.IsZero() && s.now().Sub(s.statusAt) < scannerStatusTTL {
		return s.status
	}
	s.status = s.probeScanner(ctx)
	s.statusAt = s.now()
	return s.status
}

// probeScanner runs `<bin> --version`, which answers three questions at once:
// is the file there, will it execute here, and which version is it.
func (s *ScanService) probeScanner(ctx context.Context) ScannerStatus {
	if !s.trivy.Enabled {
		return ScannerStatus{
			State:   ScannerDisabled,
			Message: "Image scanning is disabled by the administrator",
		}
	}

	bin := s.trivy.BinOrDefault()
	looked := bin
	if !strings.ContainsRune(bin, '/') {
		looked = fmt.Sprintf("%q on PATH", bin)
	}

	resolved, err := exec.LookPath(bin)
	if err != nil {
		return ScannerStatus{
			State:   ScannerMissing,
			Message: "Trivy not found: looked for " + looked,
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, resolved, "--version").CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return ScannerStatus{
			State:   ScannerBroken,
			Message: "Trivy found at " + resolved + " but will not run: " + truncateScanError(detail),
		}
	}

	version := ""
	if m := trivyVersionRe.FindStringSubmatch(string(out)); len(m) == 2 {
		version = m[1]
	}
	name := "Trivy"
	if version != "" {
		name += " " + version
	}
	return ScannerStatus{
		State:   ScannerReady,
		Version: version,
		Path:    resolved,
		Message: name + " — " + resolved,
	}
}
