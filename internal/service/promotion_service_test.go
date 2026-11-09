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

func newTestPromotionSvc(t *testing.T) (
	*service.PromotionService,
	*testutil.PromotionRepo,
	*testutil.ComponentRepo,
	*testutil.AssetRepo,
	*testutil.BlobStore,
	*testutil.RepoRepo,
	*testutil.BlobStoreRepo,
	*testutil.ScanResultRepo,
) {
	t.Helper()
	promoRepo := testutil.NewPromotionRepo()
	compRepo := testutil.NewComponentRepo()
	assetRepo := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()
	blobRepo := testutil.NewBlobStoreRepo()
	scanRepo := testutil.NewScanResultRepo()
	repoRepo := testutil.NewRepoRepo()

	svc, err := service.NewPromotionService(
		promoRepo, compRepo, assetRepo, repoRepo, blobRepo, scanRepo, testutil.NewFakeResolver(blobStore),
	)
	if err != nil {
		t.Fatalf("NewPromotionService: %v", err)
	}
	return svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, blobRepo, scanRepo
}

// UpdateRule used to check only FromRepo == ToRepo, which catches a rule with
// *both* sides blank ("" == "") but not one with a single side blank — a PUT
// with from_repo:"" persisted an orphaned rule no promotion could ever match.
func TestPromotionService_UpdateRule_RejectsBlankRepo(t *testing.T) {
	svc, promoRepo, _, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	rule := &domain.PromotionRule{Name: "staging-to-prod", FromRepo: "staging", ToRepo: "production"}
	if err := svc.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	for _, tc := range []struct {
		name     string
		fromRepo string
		toRepo   string
	}{
		{"blank from_repo", "", "production"},
		{"blank to_repo", "staging", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.UpdateRule(ctx, &domain.PromotionRule{
				ID: rule.ID, Name: rule.Name, FromRepo: tc.fromRepo, ToRepo: tc.toRepo,
			})
			if err == nil || !strings.Contains(err.Error(), "are required") {
				t.Fatalf("expected from_repo/to_repo required error, got: %v", err)
			}
		})
	}

	// The stored rule is untouched by the rejected updates.
	stored, err := promoRepo.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if stored.FromRepo != "staging" || stored.ToRepo != "production" {
		t.Fatalf("rule was mutated by a rejected update: %+v", stored)
	}
}

// TestPromotionService_CreateRule_Validation checks that invalid rule inputs are rejected.
func TestPromotionService_CreateRule_Validation(t *testing.T) {
	svc, _, _, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	t.Run("empty name", func(t *testing.T) {
		err := svc.CreateRule(ctx, &domain.PromotionRule{
			FromRepo: "staging",
			ToRepo:   "production",
		})
		if err == nil || !strings.Contains(err.Error(), "name is required") {
			t.Fatalf("expected name required error, got: %v", err)
		}
	})

	t.Run("same from and to", func(t *testing.T) {
		err := svc.CreateRule(ctx, &domain.PromotionRule{
			Name:     "self-loop",
			FromRepo: "staging",
			ToRepo:   "staging",
		})
		if err == nil || !strings.Contains(err.Error(), "must be different") {
			t.Fatalf("expected same-repo error, got: %v", err)
		}
	})

	t.Run("invalid CEL expression", func(t *testing.T) {
		err := svc.CreateRule(ctx, &domain.PromotionRule{
			Name:       "bad-cel",
			FromRepo:   "staging",
			ToRepo:     "production",
			PathFilter: "this is not valid CEL !!!",
		})
		if err == nil || !strings.Contains(err.Error(), "path_filter") {
			t.Fatalf("expected CEL validation error, got: %v", err)
		}
	})
}

// TestPromotionService_AutoApprove_CopiesBlob verifies that a rule with RequireManualApproval=false
// immediately copies the blob and sets status=completed.
func TestPromotionService_AutoApprove_CopiesBlob(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	// Seed source and target repos.
	fromRepo := testutil.SimpleRepo("staging", "raw")
	toRepo := testutil.SimpleRepo("production", "raw")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	// Seed a component in staging.
	comp := &domain.Component{
		ID:           "comp-src-1",
		RepositoryID: fromRepo.ID,
		Repository:   fromRepo.Name,
		Format:       "raw",
		Group:        "com/example",
		Name:         "mylib",
		Version:      "1.0.0",
	}
	compRepo.AddComponent(comp)

	// Seed an asset with a blob.
	blobKey := "staging:mylib-1.0.0.jar"
	if err := blobStore.PutBytes(ctx, blobKey, []byte("binary content")); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	assetRepo.Create(ctx, &domain.Asset{
		ComponentID:  comp.ID,
		RepositoryID: fromRepo.ID,
		Repository:   fromRepo.Name,
		Path:         "mylib-1.0.0.jar",
		BlobKey:      blobKey,
		SizeBytes:    14,
		ContentType:  "application/java-archive",
	})

	// Create an auto-approve rule.
	rule := &domain.PromotionRule{
		Name:                  "auto-to-prod",
		FromRepo:              "staging",
		ToRepo:                "production",
		RequireManualApproval: false,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	// Execute promotion.
	results, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-1")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != domain.PromotionCompleted {
		t.Errorf("expected status completed, got %s", results[0].Status)
	}

	// Verify blob was written to the target store.
	newKey := results[0].ComponentID // use the blob key we can predict indirectly
	_ = newKey
	// The new blob key is base.BlobKey("production", "mylib-1.0.0.jar") — just check at least one new key exists.
	keys, err := blobStore.ListKeys(ctx)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(keys) < 2 {
		t.Errorf("expected at least 2 blobs (source + copied), got %d", len(keys))
	}

	// Verify a new component was created in the target repo.
	page, err := compRepo.ListByRepoNames(ctx, []string{"production"}, 100, 0)
	if err != nil {
		t.Fatalf("ListByRepoNames: %v", err)
	}
	if len(page.Items) == 0 {
		t.Error("expected a component to be created in target repo")
	}
}

// TestPromotionService_ManualApproval_StaysPending verifies that a manual-approval rule
// creates a pending request and Approve transitions it to completed.
func TestPromotionService_ManualApproval_StaysPending(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := testutil.SimpleRepo("dev", "raw")
	toRepo := testutil.SimpleRepo("qa", "raw")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	comp := &domain.Component{
		ID:           "comp-manual-1",
		RepositoryID: fromRepo.ID,
		Repository:   fromRepo.Name,
		Format:       "raw",
		Group:        "com/example",
		Name:         "app",
		Version:      "2.0.0",
	}
	compRepo.AddComponent(comp)

	blobKey := "dev:app-2.0.0.tar.gz"
	if err := blobStore.PutBytes(ctx, blobKey, []byte("app content")); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	assetRepo.Create(ctx, &domain.Asset{
		ComponentID:  comp.ID,
		RepositoryID: fromRepo.ID,
		Repository:   fromRepo.Name,
		Path:         "app-2.0.0.tar.gz",
		BlobKey:      blobKey,
		SizeBytes:    11,
		ContentType:  "application/gzip",
	})

	rule := &domain.PromotionRule{
		Name:                  "manual-dev-to-qa",
		FromRepo:              "dev",
		ToRepo:                "qa",
		RequireManualApproval: true,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	results, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-2")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != domain.PromotionPending {
		t.Errorf("expected status pending before approval, got %s", results[0].Status)
	}

	reqID := results[0].ID

	// Approve the request.
	if err := svc.Approve(ctx, reqID, "reviewer-1"); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// Check updated status in the repo.
	updated := promoRepo.Requests[reqID]
	if updated == nil {
		t.Fatal("request not found in repo after approve")
	}
	if updated.Status != domain.PromotionCompleted {
		t.Errorf("expected completed after approve, got %s", updated.Status)
	}
	if updated.ReviewedBy == nil || *updated.ReviewedBy != "reviewer-1" {
		t.Error("expected ReviewedBy to be set to reviewer-1")
	}
}

// TestPromotionService_Reject verifies that a pending request can be rejected with a reason.
func TestPromotionService_Reject(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := testutil.SimpleRepo("dev", "raw")
	toRepo := testutil.SimpleRepo("prod", "raw")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	comp := &domain.Component{
		ID:           "comp-reject-1",
		RepositoryID: fromRepo.ID,
		Repository:   fromRepo.Name,
		Format:       "raw",
		Group:        "com/example",
		Name:         "svc",
		Version:      "3.0.0",
	}
	compRepo.AddComponent(comp)

	rule := &domain.PromotionRule{
		Name:                  "reject-test-rule",
		FromRepo:              "dev",
		ToRepo:                "prod",
		RequireManualApproval: true,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	results, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-3")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	reqID := results[0].ID

	reason := "security review failed"
	if err := svc.Reject(ctx, reqID, "reviewer-2", reason); err != nil {
		t.Fatalf("Reject: %v", err)
	}

	updated := promoRepo.Requests[reqID]
	if updated == nil {
		t.Fatal("request not found after reject")
	}
	if updated.Status != domain.PromotionRejected {
		t.Errorf("expected rejected status, got %s", updated.Status)
	}
	if updated.Error != reason {
		t.Errorf("expected error=%q, got %q", reason, updated.Error)
	}
	if updated.ReviewedBy == nil || *updated.ReviewedBy != "reviewer-2" {
		t.Error("expected ReviewedBy to be set")
	}
}

// TestPromotionService_PathFilter verifies that ListRulesForComponent only returns rules
// whose path filter matches the component.
func TestPromotionService_PathFilter(t *testing.T) {
	svc, promoRepo, compRepo, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	// Create a rule that matches only paths starting with /com/myco/
	rule := &domain.PromotionRule{
		Name:       "myco-only",
		FromRepo:   "staging",
		ToRepo:     "production",
		PathFilter: `path.startsWith("/com/myco/")`,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	// Component whose path matches: /com/myco/ + name
	matchingComp := &domain.Component{
		ID:         "comp-matching",
		Repository: "staging",
		Format:     "raw",
		Group:      "com/myco",
		Name:       "artifact",
		Version:    "1.0",
	}
	compRepo.AddComponent(matchingComp)

	// Component whose path does not match: /org/other/ + name
	nonMatchingComp := &domain.Component{
		ID:         "comp-nonmatching",
		Repository: "staging",
		Format:     "raw",
		Group:      "org/other",
		Name:       "artifact",
		Version:    "1.0",
	}
	compRepo.AddComponent(nonMatchingComp)

	t.Run("matching component gets rule", func(t *testing.T) {
		rules, err := svc.ListRulesForComponent(ctx, matchingComp.ID)
		if err != nil {
			t.Fatalf("ListRulesForComponent: %v", err)
		}
		if len(rules) != 1 {
			t.Errorf("expected 1 matching rule, got %d", len(rules))
		}
	})

	t.Run("non-matching component gets no rule", func(t *testing.T) {
		rules, err := svc.ListRulesForComponent(ctx, nonMatchingComp.ID)
		if err != nil {
			t.Fatalf("ListRulesForComponent: %v", err)
		}
		if len(rules) != 0 {
			t.Errorf("expected 0 rules for non-matching component, got %d", len(rules))
		}
	})
}

// A component's Extra is what the OCI layer reads to answer the referrers API:
// a signature manifest is found through Extra["oci_subject"]. Promoting one to
// another repository without its Extra lands a signature the target repository
// can never list, so the promoted copy carries the metadata over.
func TestPromotionService_CopiesComponentExtra(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := testutil.SimpleRepo("oci-staging", "oci")
	toRepo := testutil.SimpleRepo("oci-production", "oci")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	subject := "sha256:" + strings.Repeat("a", 64)
	comp := &domain.Component{
		ID:           "comp-sig-1",
		RepositoryID: fromRepo.ID,
		Repository:   fromRepo.Name,
		Format:       "oci",
		Name:         "charts/nginx",
		Version:      strings.Replace(subject, ":", "-", 1) + ".sig",
		Extra: map[string]any{
			"oci_subject":       subject,
			"oci_artifact_type": "application/vnd.dev.cosign.artifact.sig.v1+json",
			"oci_media_type":    "application/vnd.oci.image.manifest.v1+json",
			"oci_annotations":   map[string]any{"org.opencontainers.image.created": "2026-08-04T00:00:00Z"},
			// Deliberately not promoted: see below.
			"scan_result": map[string]any{"imageRef": "localhost/oci-staging/charts/nginx", "status": "ok"},
		},
	}
	compRepo.AddComponent(comp)

	blobKey := "oci-staging:signature-manifest"
	if err := blobStore.PutBytes(ctx, blobKey, []byte(`{"schemaVersion":2}`)); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	assetRepo.Create(ctx, &domain.Asset{
		ComponentID:  comp.ID,
		RepositoryID: fromRepo.ID,
		Repository:   fromRepo.Name,
		Path:         "/manifests/charts/nginx/" + comp.Version,
		BlobKey:      blobKey,
		SizeBytes:    19,
		ContentType:  "application/vnd.oci.image.manifest.v1+json",
	})

	rule := &domain.PromotionRule{
		Name:     "oci-auto",
		FromRepo: "oci-staging",
		ToRepo:   "oci-production",
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	results, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-1")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if len(results) != 1 || results[0].Status != domain.PromotionCompleted {
		t.Fatalf("promotion did not complete: %+v", results)
	}

	page, err := compRepo.ListByRepoNames(ctx, []string{"oci-production"}, 100, 0)
	if err != nil {
		t.Fatalf("ListByRepoNames: %v", err)
	}
	// The tag travels with its digest alias (#541), which carries the same
	// metadata; the assertions below are about the tag itself.
	var promoted *domain.Component
	for i := range page.Items {
		if page.Items[i].Version == comp.Version {
			promoted = &page.Items[i]
		}
	}
	if promoted == nil {
		t.Fatalf("promoted tag component missing from target: %+v", page.Items)
	}
	if got := promoted.Extra["oci_subject"]; got != subject {
		t.Errorf("promoted component lost oci_subject: got %v want %s", got, subject)
	}
	if got := promoted.Extra["oci_artifact_type"]; got != "application/vnd.dev.cosign.artifact.sig.v1+json" {
		t.Errorf("promoted component lost oci_artifact_type: got %v", got)
	}
	if got := promoted.Extra["oci_media_type"]; got != "application/vnd.oci.image.manifest.v1+json" {
		t.Errorf("promoted component lost oci_media_type: got %v", got)
	}
	ann, ok := promoted.Extra["oci_annotations"].(map[string]any)
	if !ok || ann["org.opencontainers.image.created"] != "2026-08-04T00:00:00Z" {
		t.Errorf("promoted component lost oci_annotations: got %v", promoted.Extra["oci_annotations"])
	}

	// A scan result is a record of a scan run against the SOURCE repository's
	// image reference, and the scan rows the promotion gate reads are not copied
	// either, so it does not travel: the promoted copy has not been scanned.
	if got, ok := promoted.Extra["scan_result"]; ok {
		t.Errorf("scan_result must not be promoted: got %v", got)
	}

	// The copy is its own map: mutating the source afterwards must not reach it.
	comp.Extra["oci_subject"] = "sha256:" + strings.Repeat("b", 64)
	if got := promoted.Extra["oci_subject"]; got != subject {
		t.Errorf("promoted component shares the source map: got %v", got)
	}
}

// Ensure time package is used (referenced in domain types).
var _ = time.Now

// A promoted copy replaces the metadata the target's component held: storing
// keeps a component's extra (#668), but a promotion brings the source's content
// and must not leave the target's scan or OCI keys describing something else.
func TestPromotionService_ReplacesTargetComponentExtra(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := testutil.SimpleRepo("raw-staging", "raw")
	toRepo := testutil.SimpleRepo("raw-production", "raw")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	comp := &domain.Component{
		ID: "comp-src-1", RepositoryID: fromRepo.ID, Repository: fromRepo.Name,
		Format: "raw", Name: "acme-tool", Version: "1.0",
	}
	compRepo.AddComponent(comp)
	stale := &domain.Component{
		ID: "comp-dst-1", RepositoryID: toRepo.ID, Repository: toRepo.Name,
		Format: "raw", Name: "acme-tool", Version: "1.0",
		Extra: map[string]any{
			"scan_result": map[string]any{"status": "ok"},
			"oci_subject": "sha256:" + strings.Repeat("b", 64),
		},
	}
	compRepo.AddComponent(stale)

	blobKey := "raw-staging:acme-tool"
	if err := blobStore.PutBytes(ctx, blobKey, []byte("tool")); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	assetRepo.Create(ctx, &domain.Asset{
		ComponentID: comp.ID, RepositoryID: fromRepo.ID, Repository: fromRepo.Name,
		Path: "/acme-tool/1.0/acme-tool.bin", BlobKey: blobKey, SizeBytes: 4,
		ContentType: "application/octet-stream",
	})

	rule := &domain.PromotionRule{Name: "raw-promote", FromRepo: "raw-staging", ToRepo: "raw-production"}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	results, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-1")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if len(results) != 1 || results[0].Status != domain.PromotionCompleted {
		t.Fatalf("promotion did not complete: %+v", results)
	}

	got, err := compRepo.Get(ctx, stale.ID)
	if err != nil || got == nil {
		t.Fatalf("Get target: err=%v got=%v", err, got)
	}
	if len(got.Extra) != 0 {
		t.Errorf("the target's stale metadata must be replaced by the source's (none), got %v", got.Extra)
	}
}

// Where a component is listed is its own repository's business (#727): apt
// suites recorded in the source do not travel with the copy, and a copy that
// overwrites a target component keeps the target's own record.
func TestPromotionService_LeavesListingKeysToEachRepository(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := testutil.SimpleRepo("apt-dev", "apt")
	toRepo := testutil.SimpleRepo("apt-prod", "apt")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)
	rule := &domain.PromotionRule{Name: "apt-promote", FromRepo: "apt-dev", ToRepo: "apt-prod"}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	promote := func(t *testing.T, name string) *domain.Component {
		t.Helper()
		comp := &domain.Component{
			ID: "comp-src-" + name, RepositoryID: fromRepo.ID, Repository: fromRepo.Name,
			Format: "apt", Group: "amd64", Name: name, Version: "1.0",
			Extra: map[string]any{
				"deb_control": "Package: " + name + "\n",
				"apt_suites":  []any{"acme-dev"}, "apt_suites_changed": "2026-01-01T00:00:00Z", "apt_suites_by": "change",
			},
		}
		compRepo.AddComponent(comp)
		blobKey := "apt-dev:" + name
		if err := blobStore.PutBytes(ctx, blobKey, []byte(name)); err != nil {
			t.Fatalf("PutBytes: %v", err)
		}
		assetRepo.Create(ctx, &domain.Asset{
			ComponentID: comp.ID, RepositoryID: fromRepo.ID, Repository: fromRepo.Name,
			Path: "/pool/main/" + name + "_1.0_amd64.deb", BlobKey: blobKey, SizeBytes: int64(len(name)),
			ContentType: "application/vnd.debian.binary-package",
		})
		results, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-1")
		if err != nil || len(results) != 1 || results[0].Status != domain.PromotionCompleted {
			t.Fatalf("Promote: err=%v results=%+v", err, results)
		}
		page, _ := compRepo.Search(ctx, domain.SearchParams{Repository: "apt-prod"})
		for i := range page.Items {
			if c := &page.Items[i]; c.Repository == "apt-prod" && c.Name == name {
				return c
			}
		}
		t.Fatalf("no target component for %s", name)
		return nil
	}

	got := promote(t, "fresh")
	if got.Extra["deb_control"] == nil {
		t.Errorf("the content's metadata travels: %v", got.Extra)
	}
	for _, k := range domain.ListingExtraKeys {
		if _, ok := got.Extra[k]; ok {
			t.Errorf("a new copy has no listing record of its own, got %s=%v", k, got.Extra[k])
		}
	}

	// A target component whose file was deleted (no assets left) describes
	// deleted content: the copy does not inherit its suites.
	compRepo.AddComponent(&domain.Component{
		ID: "comp-dst-orphan", RepositoryID: toRepo.ID, Repository: toRepo.Name,
		Format: "apt", Group: "amd64", Name: "orphan", Version: "1.0",
		Extra: map[string]any{"apt_suites": []any{"old"}, "apt_suites_changed": "2026-05-05T00:00:00Z", "apt_suites_by": "change"},
	})
	compRepo.HasAssets = func(id string) bool {
		as, _ := assetRepo.ListByComponentID(ctx, id)
		return len(as) > 0
	}
	got = promote(t, "orphan")
	for _, k := range domain.ListingExtraKeys {
		if _, ok := got.Extra[k]; ok {
			t.Errorf("a copy into an emptied component starts without its record, got %s=%v", k, got.Extra[k])
		}
	}

	compRepo.AddComponent(&domain.Component{
		ID: "comp-dst-kept", RepositoryID: toRepo.ID, Repository: toRepo.Name,
		Format: "apt", Group: "amd64", Name: "kept", Version: "1.0",
		Extra: map[string]any{"apt_suites": []any{"stable"}, "apt_suites_changed": "2026-05-05T00:00:00Z", "apt_suites_by": "change"},
	})
	assetRepo.Create(ctx, &domain.Asset{
		ComponentID: "comp-dst-kept", RepositoryID: toRepo.ID, Repository: toRepo.Name,
		Path: "/pool/main/kept_1.0_amd64.deb", BlobKey: "apt-prod:kept-old", SizeBytes: 3,
		ContentType: "application/vnd.debian.binary-package",
	})
	got = promote(t, "kept")
	if suites, _ := got.Extra["apt_suites"].([]any); len(suites) != 1 || suites[0] != "stable" {
		t.Errorf("an overwritten target keeps its own suites, got %v", got.Extra["apt_suites"])
	}
	if got.Extra["apt_suites_changed"] != "2026-05-05T00:00:00Z" {
		t.Errorf("and its own stamp, got %v", got.Extra["apt_suites_changed"])
	}
}
