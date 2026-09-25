package domain

import (
	"fmt"
	"slices"
	"strings"
)

// Scan severities a promotion rule can fail on. They are the buckets a scan
// result is counted into (ScanSummary), lower-cased: malicious for OSV.dev
// malicious-package reports, the four CVSS tiers, and unknown for findings the
// scanner could not classify.
const (
	ScanSeverityMalicious = "malicious"
	ScanSeverityCritical  = "critical"
	ScanSeverityHigh      = "high"
	ScanSeverityMedium    = "medium"
	ScanSeverityLow       = "low"
	ScanSeverityUnknown   = "unknown"
)

// ScanSeverities is every severity a rule may list, in display order.
var ScanSeverities = []string{
	ScanSeverityMalicious, ScanSeverityCritical, ScanSeverityHigh,
	ScanSeverityMedium, ScanSeverityLow, ScanSeverityUnknown,
}

// DefaultScanFailSeverities is what require_scan_pass fails on when a rule
// lists no severities of its own.
var DefaultScanFailSeverities = []string{ScanSeverityMalicious, ScanSeverityCritical, ScanSeverityHigh}

// NormalizeScanSeverities lower-cases, trims and deduplicates severities and
// returns them in ScanSeverities order, or an error naming the first value that
// is not a known severity. An empty input yields nil (the default list).
func NormalizeScanSeverities(in []string) ([]string, error) {
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		v := strings.ToLower(strings.TrimSpace(raw))
		if !slices.Contains(ScanSeverities, v) {
			return nil, fmt.Errorf("unknown scan severity %q (allowed: %s)", raw, strings.Join(ScanSeverities, ", "))
		}
		seen[v] = true
	}
	var out []string
	for _, v := range ScanSeverities {
		if seen[v] {
			out = append(out, v)
		}
	}
	return out, nil
}

// EffectiveScanFailSeverities returns the severities that fail this rule's
// require_scan_pass gate: its own list, or the default when it has none.
func (r *PromotionRule) EffectiveScanFailSeverities() []string {
	if len(r.ScanFailSeverities) == 0 {
		return DefaultScanFailSeverities
	}
	return r.ScanFailSeverities
}
