package service_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// dispatchFunc adapts a func to domain.WebhookDispatcher for tests.
type dispatchFunc func(domain.WebhookPayload)

func (f dispatchFunc) Dispatch(p domain.WebhookPayload) { f(p) }

// promoFixture seeds source/target repos and a two-asset component (jar + pom)
// the atomicity tests promote.
type promoFixture struct {
	rule       *domain.PromotionRule
	comp       *domain.Component
	jarKey     string
	pomKey     string
	blobStore  *testutil.BlobStore
	promoRepo  *testutil.PromotionRepo
	compRepo   *testutil.ComponentRepo
	assetRepo  *testutil.AssetRepo
	beforeKeys []string
}

func seedTwoAssetPromotion(t *testing.T, svcDeps func() (
	*testutil.PromotionRepo, *testutil.ComponentRepo, *testutil.AssetRepo,
	*testutil.BlobStore, *testutil.RepoRepo), manualApproval bool,
) *promoFixture {
	t.Helper()
	ctx := context.Background()
	promoRepo, compRepo, assetRepo, blobStore, repoRepo := svcDeps()

	fromRepo := testutil.SimpleRepo("staging", "raw")
	toRepo := testutil.SimpleRepo("production", "raw")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	comp := &domain.Component{
		ID:           "comp-atomic-1",
		RepositoryID: fromRepo.ID,
		Repository:   fromRepo.Name,
		Format:       "raw",
		Group:        "com/example",
		Name:         "mylib",
		Version:      "1.0.0",
	}
	compRepo.AddComponent(comp)

	jarKey := "staging:mylib-1.0.0.jar"
	pomKey := "staging:mylib-1.0.0.pom"
	if err := blobStore.PutBytes(ctx, jarKey, []byte("jar bytes")); err != nil {
		t.Fatal(err)
	}
	if err := blobStore.PutBytes(ctx, pomKey, []byte("pom bytes")); err != nil {
		t.Fatal(err)
	}
	for path, key := range map[string]string{"mylib-1.0.0.jar": jarKey, "mylib-1.0.0.pom": pomKey} {
		assetRepo.Create(ctx, &domain.Asset{
			ComponentID: comp.ID, RepositoryID: fromRepo.ID, Repository: fromRepo.Name,
			Path: path, BlobKey: key, SizeBytes: 9, ContentType: "application/octet-stream",
		})
	}

	rule := &domain.PromotionRule{
		Name: "to-prod", FromRepo: "staging", ToRepo: "production",
		RequireManualApproval: manualApproval,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatal(err)
	}

	keys, err := blobStore.ListKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys)
	return &promoFixture{
		rule: rule, comp: comp, jarKey: jarKey, pomKey: pomKey,
		blobStore: blobStore, promoRepo: promoRepo, compRepo: compRepo,
		assetRepo: assetRepo, beforeKeys: keys,
	}
}

func targetState(t *testing.T, f *promoFixture) (comps int, assets int, keys []string) {
	t.Helper()
	ctx := context.Background()
	page, err := f.compRepo.ListByRepoNames(ctx, []string{"production"}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := f.assetRepo.ListByRepoAndPath(ctx, "production", "")
	if err != nil {
		t.Fatal(err)
	}
	keys, err = f.blobStore.ListKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys)
	return len(page.Items), len(rows), keys
}

// A promotion that fails halfway must not leave a half-populated component in
// the target repo: the jar copied, the pom's source blob unreadable — before
// compensation, every consumer of the target saw a component missing half its
// files, silently.
func TestPromotion_ExecuteCopy_PartialFailureLeavesNoHalfComponent(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	f := seedTwoAssetPromotion(t, func() (*testutil.PromotionRepo, *testutil.ComponentRepo, *testutil.AssetRepo, *testutil.BlobStore, *testutil.RepoRepo) {
		return promoRepo, compRepo, assetRepo, blobStore, repoRepo
	}, false)
	ctx := context.Background()

	// The pom's source blob vanishes before the copy reaches it (a corrupted or
	// GC'd source blob). ListByComponentID orders by path: jar copies first.
	if err := blobStore.Delete(ctx, f.pomKey); err != nil {
		t.Fatal(err)
	}

	results, err := svc.Promote(ctx, f.rule.ID, []string{f.comp.ID}, "user-1")
	if err != nil {
		t.Fatalf("Promote (batch validation) failed outright: %v", err)
	}
	if results[0].Status != domain.PromotionFailed {
		t.Fatalf("status = %s, want failed", results[0].Status)
	}

	comps, assets, keys := targetState(t, f)
	if comps != 0 {
		t.Errorf("target repo holds %d component(s) after a failed promotion — a half-populated component is visible to every consumer", comps)
	}
	if assets != 0 {
		t.Errorf("target repo holds %d asset row(s) after a failed promotion", assets)
	}
	// The source pom blob was deleted by the test itself, so "no orphans" means:
	// nothing NEW beyond the pre-promotion keys minus the deleted pom.
	want := make([]string, 0, len(f.beforeKeys))
	for _, k := range f.beforeKeys {
		if k != f.pomKey {
			want = append(want, k)
		}
	}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Errorf("blob keys after failed promotion = %v, want %v (no orphaned copies)", keys, want)
	}
}

// The row insert failing right after the blob bytes were written must not
// leave the bytes behind with no row referencing them.
func TestPromotion_ExecuteCopy_CreateFailureLeavesNoOrphanBlob(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	f := seedTwoAssetPromotion(t, func() (*testutil.PromotionRepo, *testutil.ComponentRepo, *testutil.AssetRepo, *testutil.BlobStore, *testutil.RepoRepo) {
		return promoRepo, compRepo, assetRepo, blobStore, repoRepo
	}, false)
	ctx := context.Background()

	assetRepo.CreateErr = errors.New("simulated DB insert failure after blob write")

	results, err := svc.Promote(ctx, f.rule.ID, []string{f.comp.ID}, "user-1")
	if err != nil {
		t.Fatalf("Promote (batch validation) failed outright: %v", err)
	}
	if results[0].Status != domain.PromotionFailed {
		t.Fatalf("status = %s, want failed", results[0].Status)
	}
	assetRepo.CreateErr = nil

	_, _, keys := targetState(t, f)
	if strings.Join(keys, ",") != strings.Join(f.beforeKeys, ",") {
		t.Errorf("blob keys after failed promotion = %v, want unchanged %v — the written blob has no row referencing it", keys, f.beforeKeys)
	}
}

// Concurrent approvals of one pending request must execute the copy exactly
// once: both passing the pending check means double blob writes and a
// double-fired EventArtifactPublished webhook to external systems.
func TestPromotion_Approve_ConcurrentApprovalsCopyOnce(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	f := seedTwoAssetPromotion(t, func() (*testutil.PromotionRepo, *testutil.ComponentRepo, *testutil.AssetRepo, *testutil.BlobStore, *testutil.RepoRepo) {
		return promoRepo, compRepo, assetRepo, blobStore, repoRepo
	}, true)
	ctx := context.Background()

	var published atomic32
	svc.WithWebhooks(dispatchFunc(func(p domain.WebhookPayload) {
		if p.Event == domain.EventArtifactPublished {
			published.Add(1)
		}
	}))

	results, err := svc.Promote(ctx, f.rule.ID, []string{f.comp.ID}, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	reqID := results[0].ID

	// Line every approver up on the same pending snapshot: each reads the
	// request, then waits until all have read it (or a timeout, for the fixed
	// path where the lock serializes them and the barrier can never fill).
	const approvers = 8
	var barrierMu sync.Mutex
	arrived := 0
	allArrived := make(chan struct{})
	promoRepo.GetRequestHook = func(string) {
		barrierMu.Lock()
		arrived++
		if arrived == approvers {
			close(allArrived)
		}
		barrierMu.Unlock()
		select {
		case <-allArrived:
		case <-time.After(300 * time.Millisecond):
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, approvers)
	for i := 0; i < approvers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- svc.Approve(ctx, reqID, "reviewer-1")
		}()
	}
	wg.Wait()
	close(errs)

	succeeded := 0
	for err := range errs {
		if err == nil {
			succeeded++
		} else if !strings.Contains(err.Error(), "not pending") {
			t.Errorf("unexpected approve error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Errorf("%d of %d concurrent approvals succeeded, want exactly 1", succeeded, approvers)
	}
	if got := published.Load(); got != 1 {
		t.Errorf("EventArtifactPublished fired %d times, want exactly 1 — external systems see every duplicate", got)
	}
}

// An administrator editing the rule mid-batch must affect the components still
// to be processed: Approve re-reads the rule on every call, and the
// auto-approve batch loop must do the same instead of reusing one snapshot.
func TestPromotion_Promote_BatchRereadsRulePerComponent(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	f := seedTwoAssetPromotion(t, func() (*testutil.PromotionRepo, *testutil.ComponentRepo, *testutil.AssetRepo, *testutil.BlobStore, *testutil.RepoRepo) {
		return promoRepo, compRepo, assetRepo, blobStore, repoRepo
	}, false)
	ctx := context.Background()

	comp2 := &domain.Component{
		ID:           "comp-atomic-2",
		RepositoryID: f.comp.RepositoryID,
		Repository:   "staging",
		Format:       "raw",
		Group:        "com/example",
		Name:         "otherlib",
		Version:      "2.0.0",
	}
	compRepo.AddComponent(comp2)
	otherKey := "staging:otherlib-2.0.0.jar"
	if err := blobStore.PutBytes(ctx, otherKey, []byte("other bytes")); err != nil {
		t.Fatal(err)
	}
	assetRepo.Create(ctx, &domain.Asset{
		ComponentID: comp2.ID, RepositoryID: f.comp.RepositoryID, Repository: "staging",
		Path: "otherlib-2.0.0.jar", BlobKey: otherKey, SizeBytes: 11,
	})

	// The first component's copy fires the published webhook; the "operator"
	// edits the rule right there — mid-batch — to exclude everything.
	svc.WithWebhooks(dispatchFunc(func(p domain.WebhookPayload) {
		if p.Event != domain.EventArtifactPublished {
			return
		}
		edited := *f.rule
		edited.PathFilter = `path.startsWith("/nothing-matches-this/")`
		if err := promoRepo.UpdateRule(ctx, &edited); err != nil {
			t.Errorf("UpdateRule: %v", err)
		}
	}))

	results, err := svc.Promote(ctx, f.rule.ID, []string{f.comp.ID, comp2.ID}, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != domain.PromotionCompleted {
		t.Fatalf("first component: status = %s, want completed (rule was intact when it ran)", results[0].Status)
	}
	if results[1].Status != domain.PromotionFailed {
		t.Errorf("second component: status = %s, want failed — the rule edited mid-batch must apply to it", results[1].Status)
	}
}

// atomic32 is a minimal atomic counter (avoids importing sync/atomic's Int32
// under a name that collides with the test helpers).
type atomic32 struct {
	mu sync.Mutex
	n  int32
}

func (a *atomic32) Add(d int32) {
	a.mu.Lock()
	a.n += d
	a.mu.Unlock()
}

func (a *atomic32) Load() int32 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}
