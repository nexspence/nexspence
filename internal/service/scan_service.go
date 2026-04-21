package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// ErrTrivyNotInstalled is returned when the trivy executable is missing (not found in PATH or at TrivyBin).
var ErrTrivyNotInstalled = errors.New("trivy not installed; install trivy or use Docker image with trivy pre-bundled")

func trivyExecMissing(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	var pe *os.PathError
	if errors.As(err, &pe) && errors.Is(pe.Err, os.ErrNotExist) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "no such file or directory") ||
		strings.Contains(msg, "cannot find the file")
}

// scanTrivyErrorMessage prefers stderr from trivy (real diagnostics); stdout is empty on many failures.
func scanTrivyErrorMessage(runErr error, stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr != "" {
		return stderr
	}
	return runErr.Error()
}

const scanErrorMaxLen = 8000

func truncateScanError(s string) string {
	if len(s) <= scanErrorMaxLen {
		return s
	}
	return s[:scanErrorMaxLen] + "…"
}

// DockerScanImageRef builds a pull reference for this Nexor instance (Docker API under /v2/repository/<repo>/…).
// Trivy uses it to fetch layers from Nexor instead of interpreting name:tag as Docker Hub.
// Example: base http://localhost:8081, repo my-docker, image da/nginx, tag 1.0 → localhost:8081/repository/my-docker/da/nginx:1.0
func DockerScanImageRef(baseURL, repoName, imageName, version string) string {
	baseURL = strings.TrimSpace(baseURL)
	repoName = strings.TrimSpace(repoName)
	imageName = strings.Trim(strings.TrimSpace(imageName), "/")
	version = strings.TrimSpace(version)
	if baseURL == "" || repoName == "" || imageName == "" {
		return ""
	}
	if version == "" {
		version = "latest"
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return ""
	}
	pathPrefix := strings.TrimSpace(u.Path)
	pathPrefix = strings.TrimSuffix(pathPrefix, "/")
	if pathPrefix == "" || pathPrefix == "/" {
		pathPrefix = ""
	}
	return u.Host + pathPrefix + "/repository/" + repoName + "/" + imageName + ":" + version
}

func httpBaseURLInsecure(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	return err == nil && strings.EqualFold(u.Scheme, "http")
}

// ScanService scans a component for vulnerabilities using Trivy.
type ScanService struct {
	Components   repository.ComponentRepo
	HTTPBaseURL  string        // e.g. http://localhost:8081 — used to build registry pull refs for hosted images
	TrivyBin     string        // path to trivy binary; defaults to "trivy"
	TrivyTimeout time.Duration // per-scan wall-clock limit (0 = no extra timeout); default 10m

	// trivyMu serializes Trivy CLI runs. Trivy's on-disk cache (BoltDB) is not safe for concurrent
	// processes; parallel scans caused "cache may be in use by another process: timeout".
	trivyMu sync.Mutex
}

func NewScanService(components repository.ComponentRepo, httpBaseURL string) *ScanService {
	return &ScanService{
		Components:   components,
		HTTPBaseURL:  strings.TrimSpace(httpBaseURL),
		TrivyBin:     "trivy",
		TrivyTimeout: 10 * time.Minute,
	}
}

// Scan runs trivy against imageRef, persists the result in component.Extra["scan_result"],
// and returns it. Only docker-format components are supported; others get a clear error.
func (s *ScanService) Scan(ctx context.Context, componentID, imageRef string) (*domain.ScanResult, error) {
	comp, err := s.Components.Get(ctx, componentID)
	if err != nil {
		return nil, err
	}
	if comp == nil {
		return nil, fmt.Errorf("component %s not found", componentID)
	}
	if !strings.EqualFold(comp.Format, "docker") {
		return nil, fmt.Errorf("vulnerability scanning is only supported for docker format (got %s)", comp.Format)
	}

	ref := strings.TrimSpace(imageRef)
	if ref == "" {
		if full := DockerScanImageRef(s.HTTPBaseURL, comp.Repository, comp.Name, comp.Version); full != "" {
			ref = full
		} else {
			ref = comp.Name
			if comp.Version != "" {
				ref += ":" + comp.Version
			}
		}
	}

	result := &domain.ScanResult{
		ScannedAt: time.Now().UTC(),
		ImageRef:  ref,
	}

	bin := s.TrivyBin
	if bin == "" {
		bin = "trivy"
	}

	// #nosec G204 — ref is built from DB / authenticated override; argv is not user-controlled shell.
	args := []string{
		"image",
		"--format", "json",
		"--exit-code", "0", // do not use non-zero exit when CVEs exist; we rely on JSON
		"--quiet",
		"--no-progress",
	}
	if httpBaseURLInsecure(s.HTTPBaseURL) {
		args = append(args, "--insecure")
	}
	args = append(args, ref)

	// Apply per-scan timeout to guard against trivy hanging (e.g. on first DB download).
	scanCtx := ctx
	if s.TrivyTimeout > 0 {
		var cancel context.CancelFunc
		scanCtx, cancel = context.WithTimeout(ctx, s.TrivyTimeout)
		defer cancel()
	}

	var stderrBuf bytes.Buffer
	var out []byte
	var runErr error
	func() {
		s.trivyMu.Lock()
		defer s.trivyMu.Unlock()
		cmd := exec.CommandContext(scanCtx, bin, args...)
		cmd.Stderr = &stderrBuf
		out, runErr = cmd.Output()
	}()

	if runErr != nil {
		if len(out) == 0 && trivyExecMissing(runErr) {
			return nil, ErrTrivyNotInstalled
		}
		// Trivy may exit non-zero when vulnerabilities are found — still parse stdout if present.
		// Empty stdout + error usually means a real failure; details are on stderr.
		if len(out) == 0 {
			msg := scanTrivyErrorMessage(runErr, stderrBuf.String())
			log.Printf("nexor: trivy scan failed component=%s imageRef=%q: %s", componentID, ref, msg)
			result.Status = domain.ScanStatusFailed
			result.Error = truncateScanError(msg)
			_ = s.persistResult(ctx, comp, result)
			return result, nil
		}
	}

	result.Findings, result.Summary = parseTrivyJSON(out)
	result.Status = domain.ScanStatusOK

	_ = s.persistResult(ctx, comp, result)
	return result, nil
}

// GetResult returns the cached scan result stored in component.Extra["scan_result"], or nil.
func (s *ScanService) GetResult(ctx context.Context, componentID string) (*domain.ScanResult, error) {
	comp, err := s.Components.Get(ctx, componentID)
	if err != nil {
		return nil, err
	}
	if comp == nil {
		return nil, fmt.Errorf("component %s not found", componentID)
	}
	raw, ok := comp.Extra["scan_result"]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var sr domain.ScanResult
	if err := json.Unmarshal(b, &sr); err != nil {
		return nil, err
	}
	return &sr, nil
}

func (s *ScanService) persistResult(ctx context.Context, comp *domain.Component, result *domain.ScanResult) error {
	b, _ := json.Marshal(result)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	return s.Components.UpdateExtra(ctx, comp.ID, map[string]any{"scan_result": raw})
}

// ── Trivy JSON parsing ────────────────────────────────────────

type trivyReport struct {
	Results []trivyResult `json:"Results"`
}

type trivyResult struct {
	Vulnerabilities []trivyVuln `json:"Vulnerabilities"`
}

type trivyVuln struct {
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
	Severity         string `json:"Severity"`
	Title            string `json:"Title"`
}

// ParseTrivyJSONForTest exposes parseTrivyJSON for package-level tests.
func ParseTrivyJSONForTest(data []byte) ([]domain.CVEFinding, domain.ScanSummary) {
	return parseTrivyJSON(data)
}

func parseTrivyJSON(data []byte) ([]domain.CVEFinding, domain.ScanSummary) {
	var report trivyReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, domain.ScanSummary{}
	}

	var findings []domain.CVEFinding
	var summary domain.ScanSummary
	seen := map[string]struct{}{}

	for _, res := range report.Results {
		for _, v := range res.Vulnerabilities {
			key := v.VulnerabilityID + "/" + v.PkgName
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}

			findings = append(findings, domain.CVEFinding{
				ID:           v.VulnerabilityID,
				Severity:     v.Severity,
				PkgName:      v.PkgName,
				InstalledVer: v.InstalledVersion,
				FixedVersion: v.FixedVersion,
				Title:        v.Title,
			})

			switch strings.ToUpper(v.Severity) {
			case "CRITICAL":
				summary.Critical++
			case "HIGH":
				summary.High++
			case "MEDIUM":
				summary.Medium++
			case "LOW":
				summary.Low++
			default:
				summary.Unknown++
			}
			summary.Total++
		}
	}
	return findings, summary
}
