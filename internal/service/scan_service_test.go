package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func newDockerComp(repo, name, version string) *domain.Component {
	return &domain.Component{
		ID:         "comp-scan-1",
		Repository: repo,
		Format:     "docker",
		Name:       name,
		Version:    version,
	}
}

func TestDockerScanImageRef(t *testing.T) {
	t.Parallel()
	cases := []struct {
		base, repo, name, ver, want string
	}{
		{
			"http://localhost:8081",
			"docker-hosted",
			"da/devops/nginx",
			"1.27.0",
			"localhost:8081/repository/docker-hosted/da/devops/nginx:1.27.0",
		},
		{
			"http://example.com/nexus",
			"r1",
			"library/alpine",
			"latest",
			"example.com/nexus/repository/r1/library/alpine:latest",
		},
	}
	for _, tc := range cases {
		got := service.DockerScanImageRef(tc.base, tc.repo, tc.name, tc.ver)
		if got != tc.want {
			t.Errorf("DockerScanImageRef(%q,%q,%q,%q) = %q, want %q", tc.base, tc.repo, tc.name, tc.ver, got, tc.want)
		}
	}
}

func TestScanService_NonDocker(t *testing.T) {
	comp := &domain.Component{ID: "x", Format: "maven2", Name: "spring-core", Version: "5.3.0"}
	comps := testutil.NewComponentRepo()
	comps.Create(context.Background(), comp)

	svc := service.NewScanService(comps, "")
	_, err := svc.Scan(context.Background(), comp.ID, "")
	if err == nil {
		t.Fatal("expected error for non-docker format")
	}
}

func TestScanService_ComponentNotFound(t *testing.T) {
	svc := service.NewScanService(testutil.NewComponentRepo(), "")
	_, err := svc.Scan(context.Background(), "no-such-id", "alpine:latest")
	if err == nil {
		t.Fatal("expected error for missing component")
	}
}

func TestScanService_TrivyNotInstalled(t *testing.T) {
	comp := newDockerComp("dockerhosted", "alpine", "latest")
	comps := testutil.NewComponentRepo()
	comps.Create(context.Background(), comp)

	svc := service.NewScanService(comps, "")
	svc.TrivyBin = "/no/such/binary"

	_, err := svc.Scan(context.Background(), comp.ID, "alpine:latest")
	if err == nil {
		t.Fatal("expected ErrTrivyNotInstalled")
	}
	if !errors.Is(err, service.ErrTrivyNotInstalled) {
		t.Fatalf("expected ErrTrivyNotInstalled, got %v", err)
	}
}

func TestScanService_ParseTrivyJSON(t *testing.T) {
	// Inject a fake "trivy" that echoes a minimal JSON report.
	trivyOutput := `{
		"SchemaVersion": 2,
		"Results": [{
			"Target": "alpine:3.15",
			"Vulnerabilities": [
				{"VulnerabilityID":"CVE-2022-1234","PkgName":"busybox","InstalledVersion":"1.34.0","FixedVersion":"1.34.1","Severity":"HIGH","Title":"rce"},
				{"VulnerabilityID":"CVE-2022-5678","PkgName":"ssl","InstalledVersion":"1.1.1","Severity":"CRITICAL","Title":"overflow"}
			]
		}]
	}`

	findings, summary := exportParseTrivyJSON([]byte(trivyOutput))
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
	if summary.High != 1 || summary.Critical != 1 || summary.Total != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestScanService_GetResult_Empty(t *testing.T) {
	comp := newDockerComp("dockerhosted", "myimage", "v1")
	comps := testutil.NewComponentRepo()
	comps.Create(context.Background(), comp)

	svc := service.NewScanService(comps, "")
	result, err := svc.GetResult(context.Background(), comp.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result for unscan component")
	}
}

func TestScanService_GetResult_AfterPersist(t *testing.T) {
	comp := newDockerComp("dockerhosted", "myimage", "v1")
	comps := testutil.NewComponentRepo()
	comps.Create(context.Background(), comp)

	sr := &domain.ScanResult{
		ImageRef: "myimage:v1",
		Status:   domain.ScanStatusOK,
		Summary:  domain.ScanSummary{High: 2, Total: 2},
	}
	b, _ := json.Marshal(sr)
	var raw map[string]any
	json.Unmarshal(b, &raw)
	comps.UpdateExtra(context.Background(), comp.ID, map[string]any{"scan_result": raw})

	svc := service.NewScanService(comps, "")
	got, err := svc.GetResult(context.Background(), comp.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.Summary.High != 2 {
		t.Fatalf("expected High=2, got %d", got.Summary.High)
	}
}

// exportParseTrivyJSON is a test shim — it calls the unexported parseTrivyJSON
// via the service package's test-visible wrapper.
func exportParseTrivyJSON(data []byte) ([]domain.CVEFinding, domain.ScanSummary) {
	return service.ParseTrivyJSONForTest(data)
}
