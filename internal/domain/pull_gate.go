package domain

import (
	"fmt"
	"slices"
	"strings"
)

// PullBlockSeverityKey is the formatConfig key holding the severity at which a
// docker/oci repository refuses a manifest pull (#641) — Harbor's "Prevent
// vulnerable images from running". Absent or empty means the gate is off.
const PullBlockSeverityKey = "pull_block_severity"

// PullBlockSeverities are the thresholds the pull gate accepts, lowest first.
// The order is the gate's ranking: a threshold blocks its own severity and
// every one after it. Malicious ranks above critical — a malicious-package
// report has no CVSS level, and the promotion gate already treats it as a
// severity of its own — so every threshold stops it. unknown (where a scanner
// files negligible and unclassified findings) is not a threshold and never
// stops a pull, the way Harbor never refuses negligible.
var PullBlockSeverities = []string{
	ScanSeverityLow, ScanSeverityMedium, ScanSeverityHigh, ScanSeverityCritical, ScanSeverityMalicious,
}

// pullSeverityRank is a threshold's position in PullBlockSeverities, counted
// from 1; 0 for anything that is not a threshold.
func pullSeverityRank(sev string) int {
	return slices.Index(PullBlockSeverities, sev) + 1
}

// NormalizePullBlockSeverity validates a pull_block_severity value as the API
// receives it and returns it lower-cased and trimmed. Empty means off.
func NormalizePullBlockSeverity(raw any) (string, error) {
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", PullBlockSeverityKey)
	}
	v := strings.ToLower(strings.TrimSpace(s))
	if v != "" && pullSeverityRank(v) == 0 {
		return "", fmt.Errorf("%s %q is not a severity a pull can be refused at (allowed: %s, or empty for off)",
			PullBlockSeverityKey, s, strings.Join(PullBlockSeverities, ", "))
	}
	return v, nil
}

// RepoPullBlockSeverity returns the threshold of r's pull gate, or "" when the
// gate is off or r is not an OCI registry. A value the API would refuse can
// only come from a row edited behind its back and reads as off.
func RepoPullBlockSeverity(r *Repository) string {
	if r == nil || r.FormatConfig == nil || !r.Format.IsOCIRegistry() {
		return ""
	}
	v, err := NormalizePullBlockSeverity(r.FormatConfig[PullBlockSeverityKey])
	if err != nil {
		return ""
	}
	return v
}

// StricterPullSeverity returns whichever of two thresholds blocks more — the
// lower one — treating "" as off.
func StricterPullSeverity(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	case pullSeverityRank(b) < pullSeverityRank(a):
		return b
	default:
		return a
	}
}

// ScanRawManifestSHA256Key is the scan_results.raw key holding the hex sha256
// of the manifest an image scan was started against (ScanResult.ManifestSHA256).
const ScanRawManifestSHA256Key = "manifest_sha256"

// ScannedManifestSHA256 returns the sha256 of the manifest the scan looked at,
// or "" for a row that does not record one — every row written before #641's
// re-push check, which the pull gate keeps trusting as before.
func (r *ScanResultRow) ScannedManifestSHA256() string {
	v, _ := r.Raw[ScanRawManifestSHA256Key].(string)
	return v
}

// SeverityCount returns how many findings of one ScanSeverities bucket the scan
// counted.
func (r *ScanResultRow) SeverityCount(sev string) int {
	switch sev {
	case ScanSeverityMalicious:
		return r.Malicious
	case ScanSeverityCritical:
		return r.Critical
	case ScanSeverityHigh:
		return r.High
	case ScanSeverityMedium:
		return r.Medium
	case ScanSeverityLow:
		return r.Low
	case ScanSeverityUnknown:
		return r.Unknown
	}
	return 0
}

// PullGateVerdict is the pull gate's answer for one manifest.
type PullGateVerdict struct {
	Blocked bool
	// Unscanned is set when there is no finished scan to judge by: none at
	// all, or only one that failed. A failed scan found nothing because it
	// looked at nothing, so it is not read as clean.
	Unscanned bool
	// Severity and Count name the worst finding at or above the threshold
	// when a finished scan is what blocked.
	Severity string
	Count    int
}

// EvaluatePullGate judges a manifest pull against threshold by the latest scan
// of the image. An empty threshold is the gate switched off and always passes.
// requireScan decides an image with no finished scan: a hosted repository
// refuses it (serving it would read as a clean pull), a proxy or a group lets
// it through (the first pull is what brings the image in to be scanned).
func EvaluatePullGate(threshold string, requireScan bool, scan *ScanResultRow) PullGateVerdict {
	rank := pullSeverityRank(threshold)
	if rank == 0 {
		return PullGateVerdict{}
	}
	if scan == nil || scan.Status != ScanStatusOK {
		return PullGateVerdict{Blocked: requireScan, Unscanned: true}
	}
	// Worst first, so the refusal names the most serious finding.
	for i := len(PullBlockSeverities) - 1; i >= rank-1; i-- {
		sev := PullBlockSeverities[i]
		if n := scan.SeverityCount(sev); n > 0 {
			return PullGateVerdict{Blocked: true, Severity: sev, Count: n}
		}
	}
	return PullGateVerdict{}
}
