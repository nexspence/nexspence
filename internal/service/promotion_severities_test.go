package service_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// promoteWithScan creates a rule with the given severities over a fresh
// component whose latest scan is scan, and returns the Promote error.
func promoteWithScan(t *testing.T, severities []string, scan domain.ScanResultRow) error {
	t.Helper()
	svc, _, compRepo, _, _, repoRepo, _, scanRepo := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := testutil.SimpleRepo("sev-src", "raw")
	toRepo := testutil.SimpleRepo("sev-dst", "raw")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	comp := &domain.Component{
		ID: "comp-sev", Repository: fromRepo.Name, Format: "raw",
		Group: "g", Name: "s", Version: "1",
	}
	compRepo.AddComponent(comp)

	scan.ComponentID = comp.ID
	scan.Scanner = "osv"
	scan.Status = domain.ScanStatusOK
	scan.ScannedAt = time.Now()
	if err := scanRepo.Insert(ctx, &scan); err != nil {
		t.Fatalf("Insert scan row: %v", err)
	}

	rule := &domain.PromotionRule{
		Name: "sev-gate", FromRepo: fromRepo.Name, ToRepo: toRepo.Name,
		RequireScanPass: true, RequireManualApproval: true,
		ScanFailSeverities: severities,
	}
	if err := svc.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	_, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user")
	return err
}

func TestScanGate_DefaultSeverities(t *testing.T) {
	cases := []struct {
		name     string
		scan     domain.ScanResultRow
		wantFail string // "" = promotion allowed
	}{
		{"malicious fails", domain.ScanResultRow{Malicious: 1}, "1 malicious"},
		{"critical fails", domain.ScanResultRow{Critical: 2}, "2 critical"},
		{"high fails", domain.ScanResultRow{High: 3}, "3 high"},
		{"medium passes", domain.ScanResultRow{Medium: 5}, ""},
		{"low passes", domain.ScanResultRow{Low: 5}, ""},
		{"unknown passes", domain.ScanResultRow{Unknown: 5}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := promoteWithScan(t, nil, tc.scan)
			if tc.wantFail == "" {
				if err != nil {
					t.Fatalf("expected the promotion to pass, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantFail) {
				t.Fatalf("expected a refusal naming %q, got %v", tc.wantFail, err)
			}
			if !strings.Contains(err.Error(), "fails on malicious, critical, high") {
				t.Errorf("the refusal must name the rule's severities, got %v", err)
			}
		})
	}
}

// A custom list replaces the default: severities left out no longer fail.
func TestScanGate_CustomSeverities(t *testing.T) {
	err := promoteWithScan(t, []string{"medium"}, domain.ScanResultRow{High: 4, Critical: 1})
	if err != nil {
		t.Fatalf("high/critical are not in the rule's list, expected a pass, got %v", err)
	}

	err = promoteWithScan(t, []string{"medium"}, domain.ScanResultRow{Medium: 2, High: 4})
	if err == nil || !strings.Contains(err.Error(), "2 medium findings") {
		t.Fatalf("expected a refusal naming the medium findings, got %v", err)
	}
	if strings.Contains(err.Error(), "4 high") {
		t.Errorf("high is not in the rule's list and must not be reported as failing: %v", err)
	}

	err = promoteWithScan(t, []string{"LOW", "unknown"}, domain.ScanResultRow{Low: 1, Unknown: 7})
	if err == nil || !strings.Contains(err.Error(), "1 low, 7 unknown") {
		t.Fatalf("expected a refusal naming both failing severities, got %v", err)
	}
}

// Malicious stays controllable through the same list: kept, it fails; left
// out, a malicious report alone does not block.
func TestScanGate_MaliciousInList(t *testing.T) {
	if err := promoteWithScan(t, []string{"critical"}, domain.ScanResultRow{Malicious: 1}); err != nil {
		t.Fatalf("malicious is not in the rule's list, expected a pass, got %v", err)
	}
	err := promoteWithScan(t, []string{"malicious"}, domain.ScanResultRow{Malicious: 1, Critical: 9})
	if err == nil || !strings.Contains(err.Error(), "1 malicious findings") {
		t.Fatalf("expected a refusal naming the malicious finding, got %v", err)
	}
}

func TestPromotionRule_SeverityValidation(t *testing.T) {
	svc, _, _, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	bad := &domain.PromotionRule{Name: "r", FromRepo: "a", ToRepo: "b", ScanFailSeverities: []string{"high", "severe"}}
	if err := svc.CreateRule(ctx, bad); err == nil || !strings.Contains(err.Error(), `"severe"`) {
		t.Fatalf("expected an error naming the unknown severity, got %v", err)
	}

	rule := &domain.PromotionRule{
		Name: "r", FromRepo: "a", ToRepo: "b", RequireScanPass: true,
		ScanFailSeverities: []string{" High ", "MALICIOUS", "high", "low"},
	}
	if err := svc.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if want := []string{"malicious", "high", "low"}; !slices.Equal(rule.ScanFailSeverities, want) {
		t.Errorf("severities = %v, want normalized %v", rule.ScanFailSeverities, want)
	}

	rule.ScanFailSeverities = []string{"medium", "nope"}
	if err := svc.UpdateRule(ctx, rule); err == nil || !strings.Contains(err.Error(), "scan_fail_severities") {
		t.Fatalf("expected UpdateRule to reject an unknown severity, got %v", err)
	}

	// An empty list is the default, stored as nil.
	rule.ScanFailSeverities = []string{}
	if err := svc.UpdateRule(ctx, rule); err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	if rule.ScanFailSeverities != nil {
		t.Errorf("an empty list must normalize to nil (default), got %#v", rule.ScanFailSeverities)
	}
	if got := rule.EffectiveScanFailSeverities(); !slices.Equal(got, domain.DefaultScanFailSeverities) {
		t.Errorf("EffectiveScanFailSeverities = %v, want default", got)
	}
}
