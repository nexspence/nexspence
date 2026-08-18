package service_test

import (
	"testing"

	"github.com/nexspence-oss/nexspence/internal/service"
)

func TestWithTrivy_ReplacesTheDefaults(t *testing.T) {
	svc := service.NewScanService(nil, "http://localhost:8081").
		WithTrivy(service.TrivyOptions{Enabled: true, Bin: "/opt/trivy/trivy"})

	got := svc.TrivyOptions()
	if !got.Enabled {
		t.Error("Enabled was not carried through")
	}
	if got.Bin != "/opt/trivy/trivy" {
		t.Errorf("Bin = %q, want /opt/trivy/trivy", got.Bin)
	}
}

func TestNewScanService_DefaultBinIsTrivyOnPath(t *testing.T) {
	svc := service.NewScanService(nil, "http://localhost:8081")
	if got := svc.TrivyOptions().Bin; got != "trivy" {
		t.Errorf("default Bin = %q, want \"trivy\"", got)
	}
	if svc.TrivyOptions().Enabled {
		t.Error("scanning must be off until an operator turns it on")
	}
}
