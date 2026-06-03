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

// scanTrivyErrorMessage returns a concise error string from a failed Trivy run.
// It detects well-known OCI registry errors and returns a human-readable message
// instead of the full verbose Trivy output.
func scanTrivyErrorMessage(runErr error, stderr string) string {
	stderr = strings.TrimSpace(stderr)
	msg := stderr
	if msg == "" {
		msg = runErr.Error()
	}
	switch {
	case strings.Contains(msg, "MANIFEST_UNKNOWN"):
		return "image manifest not found in registry — re-push the image to make it scannable"
	case strings.Contains(msg, "UNAUTHORIZED"):
		return "registry authentication failed — check scan credentials in config"
	case strings.Contains(msg, "MANIFEST_INVALID"):
		return "image manifest is invalid or corrupted"
	case strings.Contains(msg, "unable to find the specified image"):
		return "image not found in registry — re-push the image to make it scannable"
	case strings.Contains(msg, "no such file or directory") && strings.Contains(msg, "docker.sock"):
		return "Docker socket not available — ensure --image-src remote is set (internal error)"
	}
	return msg
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
	HTTPBaseURL  string                    // e.g. http://localhost:8081 — used to build registry pull refs for hosted images
	TrivyBin     string                    // path to trivy binary; defaults to "trivy"
	TrivyTimeout time.Duration             // per-scan wall-clock limit (0 = no extra timeout); default 10m
	ScanResults  repository.ScanResultRepo // may be nil; if set, each scan is persisted here
	OSVClient    *OSVClient                // used for non-Docker formats
	scanUsername string                    // registry credentials passed to trivy --username
	scanPassword string

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
		OSVClient:    NewOSVClient(),
	}
}

func (s *ScanService) WithScanResults(repo repository.ScanResultRepo) *ScanService {
	s.ScanResults = repo
	return s
}

func (s *ScanService) WithCredentials(username, password string) *ScanService {
	s.scanUsername = username
	s.scanPassword = password
	return s
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
	switch strings.ToLower(comp.Format) {
	case "docker":
		// falls through to Trivy path below
	case "maven", "npm", "pypi", "cargo":
		return s.scanOSV(ctx, comp)
	default:
		return nil, fmt.Errorf("vulnerability scanning is not supported for format %q", comp.Format)
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
		"--image-src", "remote", // no Docker/containerd socket inside the container
	}
	if httpBaseURLInsecure(s.HTTPBaseURL) {
		args = append(args, "--insecure")
	}
	if s.scanUsername != "" {
		args = append(args, "--username", s.scanUsername, "--password", s.scanPassword)
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
			s.persistScanRow(ctx, comp, result, "trivy")
			return result, nil
		}
	}

	result.Findings, result.Summary = parseTrivyJSON(out)
	result.Status = domain.ScanStatusOK

	_ = s.persistResult(ctx, comp, result)
	s.persistScanRow(ctx, comp, result, "trivy")
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

func (s *ScanService) scanOSV(ctx context.Context, comp *domain.Component) (*domain.ScanResult, error) {
	ecosystem := FormatToEcosystem(comp.Format)
	if ecosystem == "" {
		return nil, fmt.Errorf("format %q not supported for scanning", comp.Format)
	}

	result := &domain.ScanResult{
		ScannedAt: time.Now().UTC(),
		Status:    domain.ScanStatusOK,
	}

	vulns, err := s.OSVClient.Query(ctx, comp.Name, comp.Version, ecosystem)
	if err != nil {
		result.Status = domain.ScanStatusFailed
		result.Error = err.Error()
		_ = s.persistResult(ctx, comp, result)
		s.persistScanRow(ctx, comp, result, "osv")
		return result, nil //nolint:nilerr // best-effort scan: OSV query failure is recorded in result.Error and returned as a failed-status result; propagating the error would break the UI scan response
	}

	var findings []domain.CVEFinding
	var summary domain.ScanSummary
	for _, v := range vulns {
		findings = append(findings, domain.CVEFinding{
			ID:       v.ID,
			Severity: v.Severity,
			Title:    v.Summary,
		})
		switch v.Severity {
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
	result.Findings = findings
	result.Summary = summary

	_ = s.persistResult(ctx, comp, result)
	s.persistScanRow(ctx, comp, result, "osv")
	return result, nil
}

func (s *ScanService) persistScanRow(ctx context.Context, comp *domain.Component, result *domain.ScanResult, scanner string) {
	if s.ScanResults == nil {
		return
	}
	row := &domain.ScanResultRow{
		ComponentID: comp.ID,
		Scanner:     scanner,
		Status:      result.Status,
		Critical:    result.Summary.Critical,
		High:        result.Summary.High,
		Medium:      result.Summary.Medium,
		Low:         result.Summary.Low,
		Unknown:     result.Summary.Unknown,
		Total:       result.Summary.Total,
		ScannedAt:   result.ScannedAt,
		Error:       result.Error,
	}
	_ = s.ScanResults.Insert(ctx, row)
}

func (s *ScanService) BulkScan(ctx context.Context, repoName string) (scanned int, failed int, err error) {
	page, err := s.Components.Search(ctx, domain.SearchParams{Repository: repoName, Limit: 10000})
	if err != nil {
		return 0, 0, err
	}
	for _, comp := range page.Items {
		// Skip SHA digest aliases — they duplicate the tagged image and clutter the dashboard.
		if strings.HasPrefix(comp.Version, "sha256:") {
			continue
		}
		_, scanErr := s.Scan(ctx, comp.ID, "")
		if scanErr != nil {
			failed++
		} else {
			scanned++
		}
	}
	return scanned, failed, nil
}

func (s *ScanService) GetSummary(ctx context.Context) (*domain.SecuritySummary, error) {
	if s.ScanResults == nil {
		return &domain.SecuritySummary{}, nil
	}
	return s.ScanResults.Aggregate(ctx)
}

func (s *ScanService) ListVulnerabilities(ctx context.Context, f domain.VulnFilter) ([]*domain.VulnRow, int, error) {
	if s.ScanResults == nil {
		return nil, 0, nil
	}
	return s.ScanResults.List(ctx, f)
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
