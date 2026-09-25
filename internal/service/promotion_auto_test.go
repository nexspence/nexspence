package service_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

const (
	testSettle   = 30 * time.Second
	testScanWait = 10 * time.Minute
)

// autoFixture wires a promotion service with auto-promotion to the same
// in-memory repositories a format handler writes through, so publishes go
// through base.StoreArtifact — the real hook — and not a hand-made enqueue.
type autoFixture struct {
	t        *testing.T
	svc      *service.PromotionService
	promo    *testutil.PromotionRepo
	comps    *testutil.ComponentRepo
	assets   *testutil.AssetRepo
	store    *testutil.BlobStore
	blobs    *testutil.BlobStoreRepo
	repos    *testutil.RepoRepo
	scans    *testutil.ScanResultRepo
	queue    *testutil.AutoPromotionQueue
	audit    *testutil.AuditRepo
	deps     formats.Deps
	from, to *domain.Repository
	rule     *domain.PromotionRule

	mu  sync.Mutex
	now time.Time
}

func newAutoFixture(t *testing.T, format string, rule domain.PromotionRule) *autoFixture {
	t.Helper()
	f := &autoFixture{
		t:      t,
		promo:  testutil.NewPromotionRepo(),
		comps:  testutil.NewComponentRepo(),
		assets: testutil.NewAssetRepo(),
		store:  testutil.NewBlobStore(),
		blobs:  testutil.NewBlobStoreRepo(),
		repos:  testutil.NewRepoRepo(),
		scans:  testutil.NewScanResultRepo(),
		audit:  testutil.NewAuditRepo(),
		now:    time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	}
	f.queue = testutil.NewAutoPromotionQueue(f.promo)
	ctx := context.Background()
	f.from = testutil.SimpleRepo("src", format)
	f.to = testutil.SimpleRepo("dst", format)
	_ = f.repos.Create(ctx, f.from)
	_ = f.repos.Create(ctx, f.to)
	f.svc = f.newService()
	f.deps = formats.Deps{
		Repos: f.repos, Components: f.comps, Assets: f.assets, Blobs: f.blobs,
		BlobStore: f.store, Publishes: f.svc,
	}
	if rule.Name == "" {
		rule.Name = "src-to-dst"
	}
	rule.FromRepo, rule.ToRepo = f.from.Name, f.to.Name
	if err := f.svc.CreateRule(ctx, &rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	f.rule = &rule
	return f
}

// newService builds another service over the same repositories and queue — a
// second replica, or the same one after a restart.
func (f *autoFixture) newService() *service.PromotionService {
	svc, err := service.NewPromotionService(f.promo, f.comps, f.assets, f.repos, f.blobs, f.scans,
		testutil.NewFakeResolver(f.store))
	if err != nil {
		f.t.Fatal(err)
	}
	return svc.WithAutoPromotion(f.queue, f.audit, nil, service.AutoPromotionOptions{
		SettleWindow: testSettle,
		ScanWait:     testScanWait,
		Now:          f.clock,
	})
}

func (f *autoFixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *autoFixture) advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

// publish is a client deploy of path into the source repository.
func (f *autoFixture) publish(path string, coords base.Coords, body string) *domain.Asset {
	f.t.Helper()
	res, err := base.StoreArtifact(context.Background(), f.deps, f.from.Name, path, "application/octet-stream",
		coords, strings.NewReader(body), int64(len(body)))
	if err != nil {
		f.t.Fatalf("publish %s: %v", path, err)
	}
	return res.Asset
}

func (f *autoFixture) run() int {
	f.t.Helper()
	n, err := f.svc.ProcessAutoPromotions(context.Background())
	if err != nil {
		f.t.Fatalf("ProcessAutoPromotions: %v", err)
	}
	return n
}

func (f *autoFixture) targetHas(path string) bool {
	_, err := f.assets.GetByPath(context.Background(), f.to.Name, path)
	return err == nil
}

func (f *autoFixture) targetBody(path string) string {
	f.t.Helper()
	a, err := f.assets.GetByPath(context.Background(), f.to.Name, path)
	if err != nil {
		f.t.Fatalf("target has no %s: %v", path, err)
	}
	b, err := f.store.Read(a.BlobKey)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func (f *autoFixture) requests() []domain.PromotionRequest {
	reqs, _ := f.promo.ListRequests(context.Background(), "")
	return reqs
}

func (f *autoFixture) queued() []domain.AutoPromotionEntry {
	es, _ := f.queue.List(context.Background())
	return es
}

func (f *autoFixture) auditActions() []string {
	var out []string
	for _, e := range f.audit.Snapshot() {
		out = append(out, e.Action)
	}
	return out
}

func (f *autoFixture) scan(compID string, sev map[string]int, at time.Time) {
	f.t.Helper()
	row := &domain.ScanResultRow{ComponentID: compID, Scanner: "osv", Status: domain.ScanStatusOK, ScannedAt: at,
		Malicious: sev["malicious"], Critical: sev["critical"], High: sev["high"],
		Medium: sev["medium"], Low: sev["low"], Unknown: sev["unknown"]}
	if err := f.scans.Insert(context.Background(), row); err != nil {
		f.t.Fatal(err)
	}
}

var mavenLib = base.Coords{Group: "com.example", Name: "lib", Version: "1.0.0"}

const (
	jarPath     = "/com/example/lib/1.0.0/lib-1.0.0.jar"
	pomPath     = "/com/example/lib/1.0.0/lib-1.0.0.pom"
	sourcesPath = "/com/example/lib/1.0.0/lib-1.0.0-sources.jar"
)

func TestAutoPromotion_OffDoesNothing(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{})
	f.publish(jarPath, mavenLib, "jar")
	f.advance(time.Hour)
	if n := f.run(); n != 0 {
		t.Fatalf("claimed %d rows for a rule without auto_promote", n)
	}
	if len(f.queued()) != 0 || len(f.requests()) != 0 || f.targetHas(jarPath) {
		t.Fatal("a rule without auto_promote reacted to a publish")
	}
}

func TestAutoPromotion_CopiesAfterSettle(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	f.publish(jarPath, mavenLib, "jar bytes")

	// Not before the settle window has passed.
	f.advance(testSettle - time.Second)
	if n := f.run(); n != 0 {
		t.Fatalf("claimed %d rows inside the settle window", n)
	}
	f.advance(time.Second)
	if n := f.run(); n != 1 {
		t.Fatalf("claimed %d rows after the settle window, want 1", n)
	}
	if got := f.targetBody(jarPath); got != "jar bytes" {
		t.Fatalf("target jar = %q", got)
	}
	reqs := f.requests()
	if len(reqs) != 1 || !reqs[0].Automatic || reqs[0].Status != domain.PromotionCompleted || reqs[0].RequestedBy != "" {
		t.Fatalf("requests = %+v, want one completed automatic request", reqs)
	}
	if len(f.queued()) != 0 {
		t.Fatal("queue row left behind after a completed promotion")
	}
	if got := strings.Join(f.auditActions(), ","); got != "AUTO_PROMOTE_STARTED,AUTO_PROMOTE_COPIED" {
		t.Fatalf("audit = %s", got)
	}
	ev := f.audit.Snapshot()[1]
	if ev.Domain != "PROMOTION" || ev.Username != "system" || ev.EntityName != "com.example:lib:1.0.0" ||
		ev.Context["to_repo"] != "dst" || ev.Result != "success" {
		t.Fatalf("audit event = %+v", ev)
	}
}

// A multi-file deploy is promoted once, whole: every asset restarts the settle
// window. An asset arriving after the promotion is copied by a later one, and
// only it — the target's copy of the jar is not rewritten.
func TestAutoPromotion_MavenMultiAssetSettlesThenCopiesLateAsset(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	// allow_once proves the second promotion does not rewrite the jar: a
	// rewrite would be refused as a redeploy.
	f.to.FormatConfig = map[string]any{domain.WritePolicyKey: string(domain.WritePolicyAllowOnce)}

	f.publish(jarPath, mavenLib, "jar")
	f.advance(20 * time.Second)
	f.publish(pomPath, mavenLib, "pom")
	f.advance(20 * time.Second) // 40s after the jar, 20s after the pom
	if n := f.run(); n != 0 {
		t.Fatalf("promoted %d rows while the upload was still arriving", n)
	}
	f.advance(10 * time.Second)
	if n := f.run(); n != 1 {
		t.Fatalf("claimed %d rows, want one promotion for the whole component", n)
	}
	if !f.targetHas(jarPath) || !f.targetHas(pomPath) {
		t.Fatal("jar and pom not both promoted")
	}
	if len(f.requests()) != 1 {
		t.Fatalf("requests = %d, want 1", len(f.requests()))
	}

	f.advance(time.Hour)
	f.publish(sourcesPath, mavenLib, "sources")
	f.advance(testSettle)
	f.run()
	if got := f.targetBody(sourcesPath); got != "sources" {
		t.Fatalf("late sources jar = %q", got)
	}
	reqs := f.requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	for _, r := range reqs {
		if r.Status != domain.PromotionCompleted {
			t.Fatalf("request %+v not completed", r)
		}
	}

	// A re-upload of identical bytes copies nothing and files nothing.
	f.publish(sourcesPath, mavenLib, "sources")
	f.advance(testSettle)
	f.run()
	if len(f.requests()) != 2 || len(f.queued()) != 0 {
		t.Fatalf("an identical re-upload filed a request: %+v", f.requests())
	}
}

// Docker: only a tag push starts the rule — blobs, and manifests pushed by
// digest, are parts of an image — and the tag's promotion brings the whole
// image along (#541).
func TestAutoPromotion_DockerTriggersOnTagPushOnly(t *testing.T) {
	f := newAutoFixture(t, "docker", domain.PromotionRule{AutoPromote: true})
	const image = "team/app"
	blob := func(content string) string {
		d := "sha256:" + sha256Hex([]byte(content))
		f.publish("/blobs/"+image+"/"+d, base.Coords{Name: image, Version: d}, content)
		return d
	}
	cfg := blob(`{"os":"linux"}`)
	l1 := blob("layer one")
	body := string(imageManifest(cfg, l1))
	digest := "sha256:" + sha256Hex([]byte(body))
	if len(f.queued()) != 0 {
		t.Fatal("a blob upload queued a promotion")
	}
	// A manifest pushed by digest only (a child of an index, say).
	f.publish("/manifests/"+image+"/"+digest, base.Coords{Name: image, Version: digest}, body)
	if len(f.queued()) != 0 {
		t.Fatal("a manifest pushed by digest queued a promotion")
	}

	f.publish("/manifests/"+image+"/1.0", base.Coords{Name: image, Version: "1.0"}, body)
	if q := f.queued(); len(q) != 1 {
		t.Fatalf("queued = %+v, want the tag push only", q)
	}
	f.advance(testSettle)
	f.run()
	for _, p := range []string{"/manifests/" + image + "/1.0", "/manifests/" + image + "/" + digest,
		"/blobs/" + image + "/" + cfg, "/blobs/" + image + "/" + l1} {
		if !f.targetHas(p) {
			t.Errorf("target lacks %s", p)
		}
	}
	reqs := f.requests()
	if len(reqs) != 1 || reqs[0].Status != domain.PromotionCompleted {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestAutoPromotion_ScanRequired_WaitsThenCopiesWhenClean(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true, RequireScanPass: true})
	a := f.publish(jarPath, mavenLib, "jar")
	published := f.clock()
	// A scan from before this publish (the previous content) does not count.
	f.scan(a.ComponentID, nil, published.Add(-time.Minute))

	f.advance(testSettle)
	f.run()
	if f.targetHas(jarPath) || len(f.requests()) != 0 {
		t.Fatal("promoted without a scan of the publish")
	}
	q := f.queued()
	if len(q) != 1 || !q[0].WaitingForScan {
		t.Fatalf("queue = %+v, want one row waiting for a scan", q)
	}
	// Nothing due until the backoff, or a finished scan wakes it.
	if n := f.run(); n != 0 {
		t.Fatalf("re-claimed a waiting row %d times before its backoff", n)
	}

	f.advance(time.Second)
	f.scan(a.ComponentID, map[string]int{"low": 3}, f.clock())
	f.svc.NotifyScanned(context.Background(), a.ComponentID)
	if n := f.run(); n != 1 {
		t.Fatalf("a finished scan did not wake the row (claimed %d)", n)
	}
	if !f.targetHas(jarPath) {
		t.Fatal("clean scan, but nothing promoted")
	}
}

func TestAutoPromotion_ScanRequired_BlockedByRuleSeverities(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true, RequireScanPass: true,
		ScanFailSeverities: []string{"medium"}})
	a := f.publish(jarPath, mavenLib, "jar")
	f.scan(a.ComponentID, map[string]int{"medium": 2}, f.clock())
	f.advance(testSettle)
	f.run()
	if f.targetHas(jarPath) {
		t.Fatal("promoted despite a failing scan")
	}
	reqs := f.requests()
	if len(reqs) != 1 || reqs[0].Status != domain.PromotionFailed || !reqs[0].Automatic ||
		!strings.Contains(reqs[0].Error, "2 medium") {
		t.Fatalf("requests = %+v, want one failed automatic request naming the findings", reqs)
	}
	if len(f.queued()) != 0 {
		t.Fatal("a blocked promotion stayed queued")
	}
	acts := f.auditActions()
	if acts[len(acts)-1] != "AUTO_PROMOTE_BLOCKED" {
		t.Fatalf("audit = %v", acts)
	}
}

// Findings outside the rule's severities do not block (#543).
func TestAutoPromotion_ScanRequired_OtherSeveritiesPass(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true, RequireScanPass: true,
		ScanFailSeverities: []string{"critical"}})
	a := f.publish(jarPath, mavenLib, "jar")
	f.scan(a.ComponentID, map[string]int{"high": 4}, f.clock())
	f.advance(testSettle)
	f.run()
	if !f.targetHas(jarPath) {
		t.Fatalf("not promoted: %+v", f.requests())
	}
}

// A scan that errored is not a clean scan.
func TestAutoPromotion_ScanFailedToRun_Blocked(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true, RequireScanPass: true})
	a := f.publish(jarPath, mavenLib, "jar")
	if err := f.scans.Insert(context.Background(), &domain.ScanResultRow{ComponentID: a.ComponentID,
		Scanner: "osv", Status: domain.ScanStatusFailed, Error: "osv.dev unreachable", ScannedAt: f.clock()}); err != nil {
		t.Fatal(err)
	}
	f.advance(testSettle)
	f.run()
	reqs := f.requests()
	if len(reqs) != 1 || reqs[0].Status != domain.PromotionFailed || !strings.Contains(reqs[0].Error, "osv.dev unreachable") {
		t.Fatalf("requests = %+v", reqs)
	}
	if f.targetHas(jarPath) {
		t.Fatal("promoted on a scan that did not run")
	}
}

func TestAutoPromotion_ScanNeverArrives_Blocked(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true, RequireScanPass: true})
	f.publish(jarPath, mavenLib, "jar")
	for i := 0; i < 100 && len(f.queued()) > 0; i++ {
		f.advance(time.Minute)
		f.run()
	}
	reqs := f.requests()
	if len(reqs) != 1 || reqs[0].Status != domain.PromotionFailed ||
		!strings.Contains(reqs[0].Error, "no scan of this publish arrived within 10m0s") {
		t.Fatalf("requests = %+v, want a blocked request explaining the missing scan", reqs)
	}
	if f.targetHas(jarPath) {
		t.Fatal("promoted without a scan")
	}
}

func TestAutoPromotion_ManualApproval_OnePendingRequestThenApprove(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true, RequireManualApproval: true})
	f.publish(jarPath, mavenLib, "jar")
	f.advance(testSettle)
	f.run()
	f.publish(pomPath, mavenLib, "pom")
	f.advance(testSettle)
	f.run()

	pending, _ := f.promo.ListRequests(context.Background(), string(domain.PromotionPending))
	if len(pending) != 1 || !pending[0].Automatic {
		t.Fatalf("pending = %+v, want exactly one automatic request", pending)
	}
	if f.targetHas(jarPath) {
		t.Fatal("copied before approval")
	}
	if err := f.svc.Approve(context.Background(), pending[0].ID, "reviewer"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !f.targetHas(jarPath) || !f.targetHas(pomPath) {
		t.Fatal("Approve did not copy the component")
	}
	acts := strings.Join(f.auditActions(), ",")
	if strings.Count(acts, "AUTO_PROMOTE_PENDING") != 1 {
		t.Fatalf("audit = %s, want one pending event", acts)
	}
}

// A rule switched from manual approval to automatic copies through the
// request already waiting rather than filing a second one.
func TestAutoPromotion_ExistingPendingRequestIsCopied(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true, RequireManualApproval: true})
	f.publish(jarPath, mavenLib, "jar")
	f.advance(testSettle)
	f.run()
	f.rule.RequireManualApproval = false
	if err := f.svc.UpdateRule(context.Background(), f.rule); err != nil {
		t.Fatal(err)
	}
	f.publish(pomPath, mavenLib, "pom")
	f.advance(testSettle)
	f.run()
	reqs := f.requests()
	if len(reqs) != 1 || reqs[0].Status != domain.PromotionCompleted {
		t.Fatalf("requests = %+v, want the one request, completed", reqs)
	}
	if !f.targetHas(pomPath) {
		t.Fatal("not copied")
	}
}

// A changed file the allow_once target already holds is refused, recorded
// once, and not retried.
func TestAutoPromotion_WritePolicyRefusalRecordedNotRetried(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	f.to.FormatConfig = map[string]any{domain.WritePolicyKey: string(domain.WritePolicyAllowOnce)}
	f.publish(jarPath, mavenLib, "v1")
	f.advance(testSettle)
	f.run()

	f.publish(jarPath, mavenLib, "v2") // the source allows redeploy
	f.advance(testSettle)
	f.run()
	f.advance(time.Hour)
	if n := f.run(); n != 0 {
		t.Fatalf("a refused promotion was retried (%d rows)", n)
	}
	if got := f.targetBody(jarPath); got != "v1" {
		t.Fatalf("target jar = %q, want the released bytes", got)
	}
	var failed []domain.PromotionRequest
	for _, r := range f.requests() {
		if r.Status == domain.PromotionFailed {
			failed = append(failed, r)
		}
	}
	if len(failed) != 1 || !strings.Contains(failed[0].Error, "Repository does not allow updating assets") {
		t.Fatalf("failed requests = %+v", failed)
	}
}

func TestAutoPromotion_PathFilterNonMatchIgnored(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true,
		PathFilter: `path.startsWith("/org.other/")`})
	f.publish(jarPath, mavenLib, "jar")
	f.advance(testSettle)
	if n := f.run(); n != 1 {
		t.Fatalf("claimed %d", n)
	}
	if f.targetHas(jarPath) || len(f.requests()) != 0 || len(f.queued()) != 0 || len(f.audit.Snapshot()) != 0 {
		t.Fatal("a component outside the path filter left a trace")
	}
}

func TestAutoPromotion_AmbiguousRulesBlocked(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	if err := f.svc.CreateRule(context.Background(), &domain.PromotionRule{Name: "twin",
		FromRepo: f.from.Name, ToRepo: f.to.Name, RequireManualApproval: true}); err != nil {
		t.Fatal(err)
	}
	f.publish(jarPath, mavenLib, "jar")
	f.advance(testSettle)
	f.run()
	reqs := f.requests()
	if len(reqs) != 1 || reqs[0].Status != domain.PromotionFailed || !strings.Contains(reqs[0].Error, "both cover") {
		t.Fatalf("requests = %+v", reqs)
	}
	if f.targetHas(jarPath) {
		t.Fatal("an ambiguous rule promoted")
	}
}

// Proxy caching and migration writes are not publishes.
func TestAutoPromotion_NonClientWritesIgnored(t *testing.T) {
	f := newAutoFixture(t, "raw", domain.PromotionRule{AutoPromote: true})
	if _, err := base.StoreArtifact(base.WithoutWritePolicy(context.Background()), f.deps, f.from.Name,
		"/migrated.txt", "text/plain", base.Coords{Name: "migrated"}, strings.NewReader("x"), 1); err != nil {
		t.Fatal(err)
	}
	f.from.Type = domain.TypeProxy
	if _, err := base.StoreArtifact(context.Background(), f.deps, f.from.Name,
		"/cached.txt", "text/plain", base.Coords{Name: "cached"}, strings.NewReader("x"), 1); err != nil {
		t.Fatal(err)
	}
	if q := f.queued(); len(q) != 0 {
		t.Fatalf("queued = %+v", q)
	}
}

// Two replicas draining one queue promote each component once.
func TestAutoPromotion_TwoWorkersPromoteOnce(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	for i := 0; i < 10; i++ {
		f.publish(fmt.Sprintf("/com/example/lib/1.0.%d/lib-1.0.%d.jar", i, i),
			base.Coords{Group: "com.example", Name: "lib", Version: fmt.Sprintf("1.0.%d", i)}, "jar")
	}
	f.advance(testSettle)
	other := f.newService()
	var wg sync.WaitGroup
	for _, svc := range []*service.PromotionService{f.svc, other} {
		wg.Add(1)
		go func(svc *service.PromotionService) {
			defer wg.Done()
			for {
				n, err := svc.ProcessAutoPromotions(context.Background())
				if err != nil || n == 0 {
					return
				}
			}
		}(svc)
	}
	wg.Wait()
	reqs := f.requests()
	if len(reqs) != 10 {
		t.Fatalf("requests = %d, want one per component", len(reqs))
	}
	seen := map[string]bool{}
	for _, r := range reqs {
		if seen[r.ComponentID] {
			t.Fatalf("component %s promoted twice", r.ComponentID)
		}
		seen[r.ComponentID] = true
	}
}

// The queue, not the process, holds the pending work: a service built after a
// "restart" over the same queue promotes what the old one recorded.
func TestAutoPromotion_SurvivesRestart(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	f.publish(jarPath, mavenLib, "jar")
	f.svc = f.newService() // the old process is gone
	f.advance(testSettle)
	f.run()
	if !f.targetHas(jarPath) {
		t.Fatal("the restarted service did not pick up the recorded publish")
	}
}

// The rule changing under a queued row: switched off, deleted, re-pointed.
func TestAutoPromotion_RuleChangedSinceTheRowWasQueued(t *testing.T) {
	ctx := context.Background()
	for name, change := range map[string]func(f *autoFixture){
		"switched off": func(f *autoFixture) {
			f.rule.AutoPromote = false
			_ = f.svc.UpdateRule(ctx, f.rule)
		},
		"deleted": func(f *autoFixture) { _ = f.svc.DeleteRule(ctx, f.rule.ID) },
		"re-pointed": func(f *autoFixture) {
			other := testutil.SimpleRepo("elsewhere", "maven2")
			_ = f.repos.Create(ctx, other)
			f.rule.FromRepo = other.Name
			_ = f.svc.UpdateRule(ctx, f.rule)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
			f.publish(jarPath, mavenLib, "jar")
			change(f)
			f.advance(testSettle)
			f.run()
			if f.targetHas(jarPath) || len(f.requests()) != 0 || len(f.queued()) != 0 {
				t.Fatal("a stale row promoted or stayed")
			}
		})
	}
}

// A lengthened settle window holds a row queued under the shorter one.
func TestAutoPromotion_SettleWindowRecheckedAtEvaluation(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	f.publish(jarPath, mavenLib, "jar")
	f.svc = mustPromotionSvc(t, f).WithAutoPromotion(f.queue, f.audit, nil,
		service.AutoPromotionOptions{SettleWindow: 2 * testSettle, Now: f.clock})
	f.advance(testSettle)
	f.run()
	if f.targetHas(jarPath) {
		t.Fatal("promoted inside the new, longer window")
	}
	f.advance(testSettle)
	f.run()
	if !f.targetHas(jarPath) {
		t.Fatal("not promoted once the longer window passed")
	}
}

// A lookup that keeps failing is retried with backoff, then recorded blocked.
func TestAutoPromotion_TransientErrorsGiveUp(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	a := f.publish(jarPath, mavenLib, "jar")
	f.comps.Err = errors.New("db down")
	f.advance(testSettle)
	f.run()
	q := f.queued()
	if len(q) != 1 || q[0].Attempts != 1 || !strings.Contains(q[0].Reason, "db down") {
		t.Fatalf("queue = %+v, want one retried row", q)
	}
	for i := 0; i < 40 && len(f.queued()) > 0; i++ {
		f.advance(10 * time.Minute)
		f.run()
	}
	reqs := f.requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0].Error, "giving up after 20 attempts") ||
		reqs[0].ComponentID != a.ComponentID {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestAutoPromotion_MissingTargetBlocked(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	f.publish(jarPath, mavenLib, "jar")
	_ = f.repos.Delete(context.Background(), f.to.Name)
	f.advance(testSettle)
	f.run()
	reqs := f.requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0].Error, "target repository not found") {
		t.Fatalf("requests = %+v", reqs)
	}
}

// A publish that cannot be recorded is logged, never an upload error.
func TestAutoPromotion_NotifyFailureDoesNotFailUpload(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	f.queue.Err = errors.New("queue down")
	f.publish(jarPath, mavenLib, "jar") // fatal on error
	f.svc.NotifyScanned(context.Background(), "comp-1")
	if _, err := f.svc.ProcessAutoPromotions(context.Background()); err == nil {
		t.Fatal("a failing claim was not reported")
	}
}

func TestAutoPromotion_DisabledServiceIsInert(t *testing.T) {
	svc, _, _, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()
	svc.NotifyPublished(ctx, "src", "c")
	svc.NotifyScanned(ctx, "c")
	svc.RunAutoPromotion(ctx) // returns at once
	if n, err := svc.ProcessAutoPromotions(ctx); n != 0 || err != nil {
		t.Fatalf("got %d, %v", n, err)
	}
}

func TestAutoPromotion_RunLoopDrainsUntilCancelled(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	f.svc = mustPromotionSvc(t, f).WithAutoPromotion(f.queue, f.audit, nil,
		service.AutoPromotionOptions{SettleWindow: -1, PollInterval: 5 * time.Millisecond, BatchSize: 1})
	f.deps.Publishes = f.svc
	f.publish(jarPath, mavenLib, "jar")
	f.publish("/com/example/lib/2.0.0/lib-2.0.0.jar", base.Coords{Group: "com.example", Name: "lib", Version: "2.0.0"}, "jar2")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.svc.RunAutoPromotion(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(f.requests()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if len(f.requests()) != 2 {
		t.Fatalf("requests = %d", len(f.requests()))
	}
}

// auto_promote on a source nobody publishes into is refused.
func TestAutoPromotion_RuleValidation(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{})
	ctx := context.Background()
	proxy := testutil.SimpleRepo("central", "maven2")
	proxy.Type = domain.TypeProxy
	_ = f.repos.Create(ctx, proxy)
	err := f.svc.CreateRule(ctx, &domain.PromotionRule{Name: "p", FromRepo: "central", ToRepo: "dst", AutoPromote: true})
	if err == nil || !strings.Contains(err.Error(), "auto_promote needs a hosted from_repo") {
		t.Fatalf("err = %v", err)
	}
	f.rule.FromRepo = "central"
	f.rule.AutoPromote = true
	if err := f.svc.UpdateRule(ctx, f.rule); err == nil {
		t.Fatal("update to a proxy source with auto_promote accepted")
	}
	// Without auto_promote, a proxy source stays allowed (a manual rule).
	if err := f.svc.CreateRule(ctx, &domain.PromotionRule{Name: "m", FromRepo: "central", ToRepo: "dst"}); err != nil {
		t.Fatal(err)
	}
}

// With manual approval, a component that grew after its first approval gets a
// new pending request, and approving it copies only the new file — the jar the
// allow_once target already holds is not a redeploy.
func TestAutoPromotion_ApproveCopiesOnlyWhatTheTargetLacks(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true, RequireManualApproval: true})
	f.to.FormatConfig = map[string]any{domain.WritePolicyKey: string(domain.WritePolicyAllowOnce)}
	ctx := context.Background()
	approveAll := func() {
		pending, _ := f.promo.ListRequests(ctx, string(domain.PromotionPending))
		if len(pending) != 1 {
			t.Fatalf("pending = %+v, want one", pending)
		}
		if err := f.svc.Approve(ctx, pending[0].ID, "reviewer"); err != nil {
			t.Fatalf("Approve: %v", err)
		}
	}
	f.publish(jarPath, mavenLib, "jar")
	f.advance(testSettle)
	f.run()
	approveAll()

	f.publish(sourcesPath, mavenLib, "sources")
	f.advance(testSettle)
	f.run()
	approveAll()
	if got := f.targetBody(sourcesPath); got != "sources" {
		t.Fatalf("sources = %q", got)
	}
}

// A copy that fails part-way is settled as a failed automatic request with the
// cause, audited, and not retried.
func TestAutoPromotion_CopyFailureRecorded(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	a := f.publish(jarPath, mavenLib, "jar")
	if err := f.store.Delete(context.Background(), a.BlobKey); err != nil {
		t.Fatal(err)
	}
	f.advance(testSettle)
	f.run()
	reqs := f.requests()
	if len(reqs) != 1 || reqs[0].Status != domain.PromotionFailed || !strings.Contains(reqs[0].Error, "read blob") {
		t.Fatalf("requests = %+v", reqs)
	}
	acts := f.auditActions()
	if acts[len(acts)-1] != "AUTO_PROMOTE_BLOCKED" || len(f.queued()) != 0 {
		t.Fatalf("audit = %v, queue = %+v", acts, f.queued())
	}
}

// mustPromotionSvc builds a bare service over f's repositories; WithAutoPromotion
// is left to the caller.
func mustPromotionSvc(t *testing.T, f *autoFixture) *service.PromotionService {
	t.Helper()
	svc, err := service.NewPromotionService(f.promo, f.comps, f.assets, f.repos, f.blobs, f.scans,
		testutil.NewFakeResolver(f.store))
	if err != nil {
		t.Fatal(err)
	}
	return svc
}
