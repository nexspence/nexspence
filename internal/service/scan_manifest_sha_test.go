package service_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// An image scan records the sha256 of the manifest the tag held when the scan
// started (#641), in the result and in scan_results.raw, so the pull gate can
// tell a scan of the current content from one of a re-pushed tag's old one.
func TestScanService_Scan_RecordsScannedManifestSHA256(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "trivy")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then echo 'Version: 0.70.0'; exit 0; fi\n" +
		"echo '{\"SchemaVersion\":2,\"Results\":[{\"Vulnerabilities\":[{\"VulnerabilityID\":\"CVE-1\",\"PkgName\":\"p\",\"InstalledVersion\":\"1\",\"Severity\":\"HIGH\"}]}]}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	comp := newDockerComp("dockerhosted", "alpine", "latest")
	comps := testutil.NewComponentRepo()
	_ = comps.Create(ctx, comp)
	assets := testutil.NewAssetRepo()
	const sha = "0123abcd"
	if err := assets.Create(ctx, &domain.Asset{
		ComponentID: comp.ID, Repository: "dockerhosted", Path: "/manifests/alpine/latest", SHA256: sha,
	}); err != nil {
		t.Fatal(err)
	}
	scans := testutil.NewScanResultRepo()
	svc := service.NewScanService(comps, "http://localhost:8081").
		WithScanResults(scans).
		WithAssets(assets).
		WithTrivy(service.TrivyOptions{Enabled: true, Bin: bin})

	res, err := svc.Scan(ctx, comp.ID, "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.Status != domain.ScanStatusOK || res.Summary.High != 1 {
		t.Fatalf("result = %+v, want an ok scan with one high finding", res)
	}
	if res.ManifestSHA256 != sha {
		t.Errorf("result ManifestSHA256 = %q, want %q", res.ManifestSHA256, sha)
	}
	row, err := scans.GetLatestByComponent(ctx, comp.ID)
	if err != nil {
		t.Fatalf("GetLatestByComponent: %v", err)
	}
	if got := row.ScannedManifestSHA256(); got != sha {
		t.Errorf("scan row raw = %v, want %s=%q", row.Raw, domain.ScanRawManifestSHA256Key, sha)
	}
}

// Without an asset repository — or a manifest to read — nothing is recorded,
// and the row reads like one written before the check existed.
func TestScanService_Scan_NoManifestSHA256WithoutAssets(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "trivy")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then echo 'Version: 0.70.0'; exit 0; fi\n" +
		"echo '{\"SchemaVersion\":2,\"Results\":[]}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	comp := newDockerComp("dockerhosted", "alpine", "latest")
	comps := testutil.NewComponentRepo()
	_ = comps.Create(ctx, comp)
	scans := testutil.NewScanResultRepo()
	for _, assets := range []*testutil.AssetRepo{nil, testutil.NewAssetRepo()} {
		svc := service.NewScanService(comps, "http://localhost:8081").
			WithScanResults(scans).
			WithTrivy(service.TrivyOptions{Enabled: true, Bin: bin})
		if assets != nil {
			svc = svc.WithAssets(assets)
		}
		res, err := svc.Scan(ctx, comp.ID, "")
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if res.ManifestSHA256 != "" {
			t.Errorf("ManifestSHA256 = %q, want empty", res.ManifestSHA256)
		}
		row, _ := scans.GetLatestByComponent(ctx, comp.ID)
		if row.Raw != nil {
			t.Errorf("raw = %v, want nil", row.Raw)
		}
	}
}
