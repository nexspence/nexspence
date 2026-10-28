package domain

import "testing"

func okScan(s ScanSummary) *ScanResultRow {
	return &ScanResultRow{
		Status: ScanStatusOK, Malicious: s.Malicious, Critical: s.Critical, High: s.High,
		Medium: s.Medium, Low: s.Low, Unknown: s.Unknown,
	}
}

func TestEvaluatePullGate(t *testing.T) {
	cases := []struct {
		name        string
		threshold   string
		requireScan bool
		scan        *ScanResultRow
		want        PullGateVerdict
	}{
		{"off passes a dirty scan", "", true, okScan(ScanSummary{Critical: 9}), PullGateVerdict{}},
		{"off passes no scan on hosted", "", true, nil, PullGateVerdict{}},
		{"clean scan passes", ScanSeverityLow, true, okScan(ScanSummary{}), PullGateVerdict{}},
		{"below threshold passes", ScanSeverityHigh, true, okScan(ScanSummary{Medium: 4, Low: 2}), PullGateVerdict{}},
		{"at threshold blocks", ScanSeverityHigh, true, okScan(ScanSummary{High: 2}),
			PullGateVerdict{Blocked: true, Severity: ScanSeverityHigh, Count: 2}},
		{"above threshold blocks and names the worst", ScanSeverityMedium, false, okScan(ScanSummary{Critical: 1, High: 3, Medium: 5}),
			PullGateVerdict{Blocked: true, Severity: ScanSeverityCritical, Count: 1}},
		{"unknown (negligible) never blocks", ScanSeverityLow, true, okScan(ScanSummary{Unknown: 40}), PullGateVerdict{}},
		{"malicious blocks at critical", ScanSeverityCritical, false, okScan(ScanSummary{Malicious: 1}),
			PullGateVerdict{Blocked: true, Severity: ScanSeverityMalicious, Count: 1}},
		{"malicious threshold ignores CVEs", ScanSeverityMalicious, true, okScan(ScanSummary{Critical: 7}), PullGateVerdict{}},
		{"malicious threshold blocks malicious", ScanSeverityMalicious, true, okScan(ScanSummary{Malicious: 2}),
			PullGateVerdict{Blocked: true, Severity: ScanSeverityMalicious, Count: 2}},
		{"hosted refuses no scan", ScanSeverityHigh, true, nil, PullGateVerdict{Blocked: true, Unscanned: true}},
		{"proxy passes no scan", ScanSeverityHigh, false, nil, PullGateVerdict{Unscanned: true}},
		{"hosted refuses a failed scan", ScanSeverityHigh, true, &ScanResultRow{Status: ScanStatusFailed},
			PullGateVerdict{Blocked: true, Unscanned: true}},
		{"proxy passes a failed scan", ScanSeverityHigh, false, &ScanResultRow{Status: ScanStatusFailed},
			PullGateVerdict{Unscanned: true}},
		{"proxy refuses a finished dirty scan", ScanSeverityHigh, false, okScan(ScanSummary{High: 1}),
			PullGateVerdict{Blocked: true, Severity: ScanSeverityHigh, Count: 1}},
		{"an invalid threshold is off", "negligible", true, nil, PullGateVerdict{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EvaluatePullGate(tc.threshold, tc.requireScan, tc.scan); got != tc.want {
				t.Errorf("EvaluatePullGate = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestNormalizePullBlockSeverity(t *testing.T) {
	for _, c := range [][2]string{{"", ""}, {" High ", "high"}, {"MALICIOUS", "malicious"}, {"low", "low"}} {
		got, err := NormalizePullBlockSeverity(c[0])
		if err != nil || got != c[1] {
			t.Errorf("NormalizePullBlockSeverity(%q) = %q, %v; want %q", c[0], got, err, c[1])
		}
	}
	for _, in := range []any{"negligible", "unknown", "severe", 3, true, nil} {
		if _, err := NormalizePullBlockSeverity(in); err == nil {
			t.Errorf("NormalizePullBlockSeverity(%v) should fail", in)
		}
	}
}

func TestRepoPullBlockSeverity(t *testing.T) {
	repo := func(f RepoFormat, v any) *Repository {
		return &Repository{Format: f, Type: TypeProxy, FormatConfig: map[string]any{PullBlockSeverityKey: v}}
	}
	cases := []struct {
		name string
		repo *Repository
		want string
	}{
		{"nil", nil, ""},
		{"no config", &Repository{Format: FormatDocker}, ""},
		{"docker", repo(FormatDocker, "High"), "high"},
		{"oci", repo(FormatOCI, "critical"), "critical"},
		{"not a registry", repo(FormatRaw, "high"), ""},
		{"invalid value", repo(FormatDocker, "negligible"), ""},
		{"wrong type", repo(FormatDocker, 1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RepoPullBlockSeverity(tc.repo); got != tc.want {
				t.Errorf("RepoPullBlockSeverity = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStricterPullSeverity(t *testing.T) {
	cases := [][3]string{
		{"", "", ""},
		{"high", "", "high"},
		{"", "low", "low"},
		{"high", "medium", "medium"},
		{"low", "critical", "low"},
		{"malicious", "critical", "critical"},
	}
	for _, c := range cases {
		if got := StricterPullSeverity(c[0], c[1]); got != c[2] {
			t.Errorf("StricterPullSeverity(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestScanResultRowSeverityCount(t *testing.T) {
	row := okScan(ScanSummary{Malicious: 1, Critical: 2, High: 3, Medium: 4, Low: 5, Unknown: 6})
	for i, sev := range ScanSeverities {
		if got := row.SeverityCount(sev); got != i+1 {
			t.Errorf("SeverityCount(%q) = %d, want %d", sev, got, i+1)
		}
	}
	if got := row.SeverityCount("negligible"); got != 0 {
		t.Errorf("SeverityCount(negligible) = %d, want 0", got)
	}
}

func TestScanResultRowScannedManifestSHA256(t *testing.T) {
	cases := []struct {
		raw  map[string]any
		want string
	}{
		{nil, ""},
		{map[string]any{"other": "x"}, ""},
		{map[string]any{ScanRawManifestSHA256Key: 7}, ""},
		{map[string]any{ScanRawManifestSHA256Key: "abc"}, "abc"},
	}
	for _, c := range cases {
		if got := (&ScanResultRow{Raw: c.raw}).ScannedManifestSHA256(); got != c.want {
			t.Errorf("ScannedManifestSHA256(%v) = %q, want %q", c.raw, got, c.want)
		}
	}
}
