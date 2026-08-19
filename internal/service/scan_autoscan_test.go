package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestScanService_TriggerAsync_ScansInBackground(t *testing.T) {
	comp := &domain.Component{Repository: "npmhosted", Format: "npm", Name: "lodash", Version: "4.17.20"}
	comps := testutil.NewComponentRepo()
	comps.Create(context.Background(), comp)

	scanRepo := testutil.NewScanResultRepo()
	svc := service.NewScanService(comps, "").WithScanResults(scanRepo)
	svc.OSVClient.BaseURL = osvServerWithOneVuln(t).URL

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.StartScheduler(ctx, "") // empty schedule: queue worker only, no cron

	svc.TriggerAsync(comp.ID)

	waitFor(t, "the queued component to be scanned", func() bool {
		return len(scanRepo.Rows()) == 1
	})
	if got := scanRepo.Rows()[0].ComponentID; got != comp.ID {
		t.Errorf("scanned component %q, want %q", got, comp.ID)
	}
}

// An upload must never wait on a scan, and must never be refused because the
// scanner is behind. The queue drops rather than blocks; the daily bulk scan is
// the safety net for what it drops.
func TestScanService_TriggerAsync_NeverBlocks(t *testing.T) {
	comps := testutil.NewComponentRepo()
	svc := service.NewScanService(comps, "")
	// No scheduler started: nothing is draining the queue.

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			svc.TriggerAsync("comp-overflow")
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("TriggerAsync blocked once the queue filled up")
	}
}

// A docker push registers every layer and the digest alias as their own
// components, versioned by digest. Scanning those means one Trivy run per layer
// against a reference that is not an image — wasted work that also evicts the
// real manifest scan from a bounded queue. BulkScan already skips them; the
// upload path has to agree.
func TestScanService_TriggerAsync_SkipsDigestAliases(t *testing.T) {
	// The rule is the version, not the format — the same one BulkScan applies.
	// A scannable format is used here so that "was not scanned" is observable:
	// with a docker component it would be indistinguishable from a scan that
	// simply found no Trivy.
	layer := &domain.Component{
		Repository: "npmhosted", Format: "npm", Name: "myapp",
		Version: "sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae",
	}
	// A scannable component queued behind the alias: once this one has been
	// scanned, the alias has demonstrably had its turn in the queue.
	marker := &domain.Component{Repository: "npmhosted", Format: "npm", Name: "lodash", Version: "4.17.20"}
	comps := testutil.NewComponentRepo()
	comps.Create(context.Background(), layer)
	comps.Create(context.Background(), marker)

	scanRepo := testutil.NewScanResultRepo()
	svc := service.NewScanService(comps, "").WithScanResults(scanRepo)
	svc.OSVClient.BaseURL = osvServerWithOneVuln(t).URL
	// Point Trivy at a binary that does not exist: if the alias is scanned after
	// all, it shells out and the assertion below catches it either way.
	svc = svc.WithTrivy(service.TrivyOptions{Enabled: true, Bin: "nexspence-no-such-trivy"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.StartScheduler(ctx, "")

	svc.TriggerAsync(layer.ID)
	svc.TriggerAsync(marker.ID)

	waitFor(t, "the queue to reach the scannable component", func() bool {
		return len(scanRepo.Rows()) > 0
	})

	for _, row := range scanRepo.Rows() {
		if row.ComponentID == layer.ID {
			t.Error("a digest-aliased component must not be scanned")
		}
	}
	c, err := comps.Get(context.Background(), layer.ID)
	if err != nil {
		t.Fatalf("get layer component: %v", err)
	}
	if c.Extra["scan_result"] != nil {
		t.Error("a digest-aliased component must not get a cached scan result")
	}
}

// A capability the operator has not provided is not an upload failure: an
// image component queued while the scanner is disabled must be skipped, not
// counted as failed.
func TestBulkScan_SkipsImagesWhenTheScannerIsNotReady(t *testing.T) {
	dockerComp := &domain.Component{Repository: "dockerhosted", Format: "docker", Name: "alpine", Version: "latest"}
	npmComp := &domain.Component{Repository: "npmhosted", Format: "npm", Name: "lodash", Version: "4.17.20"}
	comps := testutil.NewComponentRepo()
	comps.Create(context.Background(), dockerComp)
	comps.Create(context.Background(), npmComp)

	svc := service.NewScanService(comps, "http://localhost:8081").
		WithTrivy(service.TrivyOptions{Enabled: false})
	svc.OSVClient.BaseURL = osvServerWithOneVuln(t).URL

	scanned, failed, err := svc.BulkScan(context.Background(), "")
	if err != nil {
		t.Fatalf("BulkScan: %v", err)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0 — an image with no scanner is skipped, not failed", failed)
	}
	if scanned != 1 {
		t.Errorf("scanned = %d, want 1 — the npm component still scans via OSV", scanned)
	}
}

// The same rule applies on the upload-triggered queue path: a docker component
// queued while the scanner is disabled must not produce a Scan attempt (and
// therefore no scan result row), the same way a digest alias or an
// unsupported format does not.
func TestScanService_TriggerAsync_SkipsImagesWhenTheScannerIsNotReady(t *testing.T) {
	dockerComp := &domain.Component{Repository: "dockerhosted", Format: "docker", Name: "alpine", Version: "latest"}
	// Queued behind it: once this one has been scanned the docker component
	// has demonstrably had its turn in the queue.
	marker := &domain.Component{Repository: "npmhosted", Format: "npm", Name: "lodash", Version: "4.17.20"}
	comps := testutil.NewComponentRepo()
	comps.Create(context.Background(), dockerComp)
	comps.Create(context.Background(), marker)

	scanRepo := testutil.NewScanResultRepo()
	svc := service.NewScanService(comps, "http://localhost:8081").WithScanResults(scanRepo)
	svc.OSVClient.BaseURL = osvServerWithOneVuln(t).URL
	svc = svc.WithTrivy(service.TrivyOptions{Enabled: false})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Scan's own gate refuses a not-ready scanner before persisting anything,
	// so an unchanged scanRepo can't tell a working skip from a plain refusal.
	// The one thing that differs is drainQueue's "auto-scan skipped" log line,
	// which only fires when skipAutoScan does NOT catch the component first.
	out := captureLog(t, func() {
		go svc.StartScheduler(ctx, "")

		svc.TriggerAsync(dockerComp.ID)
		svc.TriggerAsync(marker.ID)

		waitFor(t, "the queue to reach the scannable component", func() bool {
			return len(scanRepo.Rows()) > 0
		})
	})

	if strings.Contains(out, "auto-scan skipped") {
		t.Errorf("log %q must not report an auto-scan skip for a component with no scanner available — skipAutoScan should have caught it silently", out)
	}
	for _, row := range scanRepo.Rows() {
		if row.ComponentID == dockerComp.ID {
			t.Error("a docker component must not be scanned while the scanner is disabled")
		}
	}
	c, err := comps.Get(context.Background(), dockerComp.ID)
	if err != nil {
		t.Fatalf("get docker component: %v", err)
	}
	if c.Extra["scan_result"] != nil {
		t.Error("a docker component must not get a cached scan result while the scanner is disabled")
	}
}

func TestScanService_StartScheduler_StopsOnContextCancel(t *testing.T) {
	svc := service.NewScanService(testutil.NewComponentRepo(), "")

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	go func() {
		svc.StartScheduler(ctx, "0 3 * * *")
		close(returned)
	}()

	cancel()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("StartScheduler did not return after its context was canceled")
	}
}

// A component whose format has no scanner (raw, helm, …) is a normal upload, not
// an error: the queue skips it without noise.
func TestScanService_TriggerAsync_UnsupportedFormatIsHarmless(t *testing.T) {
	comp := &domain.Component{Repository: "rawhosted", Format: "raw", Name: "notes.txt", Version: ""}
	// Queued behind it: once this one has been scanned the raw component has
	// demonstrably had its turn, which beats sleeping and hoping.
	marker := &domain.Component{Repository: "npmhosted", Format: "npm", Name: "lodash", Version: "4.17.20"}
	comps := testutil.NewComponentRepo()
	comps.Create(context.Background(), comp)
	comps.Create(context.Background(), marker)

	scanRepo := testutil.NewScanResultRepo()
	svc := service.NewScanService(comps, "").WithScanResults(scanRepo)
	svc.OSVClient.BaseURL = osvServerWithOneVuln(t).URL

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.StartScheduler(ctx, "")

	svc.TriggerAsync(comp.ID)
	svc.TriggerAsync(marker.ID)

	waitFor(t, "the queue to reach the scannable component", func() bool {
		return len(scanRepo.Rows()) > 0
	})

	for _, row := range scanRepo.Rows() {
		if row.ComponentID == comp.ID {
			t.Error("an unscannable format must not produce a scan row")
		}
	}
}
