package domain

import (
	"slices"
	"strings"
	"testing"
)

func TestNormalizeScanSeverities(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil stays nil", nil, nil},
		{"empty stays nil", []string{}, nil},
		{"canonical order", []string{"low", "Critical", "malicious"}, []string{"malicious", "critical", "low"}},
		{"trim, lower-case, dedupe", []string{" HIGH ", "high", "Unknown"}, []string{"high", "unknown"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeScanSeverities(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNormalizeScanSeverities_RejectsUnknownValue(t *testing.T) {
	t.Parallel()
	_, err := NormalizeScanSeverities([]string{"high", "moderate"})
	if err == nil {
		t.Fatal("expected an error for an unknown severity")
	}
	if !strings.Contains(err.Error(), `"moderate"`) || !strings.Contains(err.Error(), "allowed:") {
		t.Fatalf("error should name the bad value and the allowed list, got %q", err)
	}
}

func TestEffectiveScanFailSeverities(t *testing.T) {
	t.Parallel()
	if got := (&PromotionRule{}).EffectiveScanFailSeverities(); !slices.Equal(got, DefaultScanFailSeverities) {
		t.Fatalf("empty rule: got %v, want the default %v", got, DefaultScanFailSeverities)
	}
	own := []string{ScanSeverityMedium}
	if got := (&PromotionRule{ScanFailSeverities: own}).EffectiveScanFailSeverities(); !slices.Equal(got, own) {
		t.Fatalf("rule with its own list: got %v, want %v", got, own)
	}
}
