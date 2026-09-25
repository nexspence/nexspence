package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// seedPolicyPromotion seeds the two-asset fixture and puts the target repo
// ("production") under the given write policy.
func seedPolicyPromotion(t *testing.T, policy domain.WritePolicy) (*promoFixture, func() error) {
	t.Helper()
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	f := seedTwoAssetPromotion(t, func() (*testutil.PromotionRepo, *testutil.ComponentRepo, *testutil.AssetRepo, *testutil.BlobStore, *testutil.RepoRepo) {
		return promoRepo, compRepo, assetRepo, blobStore, repoRepo
	}, false)
	target, err := repoRepo.Get(context.Background(), "production")
	if err != nil {
		t.Fatal(err)
	}
	target.FormatConfig = map[string]any{domain.WritePolicyKey: string(policy)}
	promote := func() error {
		results, err := svc.Promote(context.Background(), f.rule.ID, []string{f.comp.ID}, "user-1")
		if err != nil {
			return err
		}
		if results[0].Status != domain.PromotionCompleted {
			return &promotionFailed{results[0].Error}
		}
		return nil
	}
	return f, promote
}

type promotionFailed struct{ msg string }

func (e *promotionFailed) Error() string { return e.msg }

// Under allow_once a promotion that would overwrite ANY of the component's
// paths in the target is refused before the first byte moves: the jar sorts
// before the pom, so a check made inside the copy loop would already have
// written the jar when it reached the taken pom.
func TestPromotion_AllowOnceTarget_TakenPathRejectsWholePromotion(t *testing.T) {
	f, promote := seedPolicyPromotion(t, domain.WritePolicyAllowOnce)
	ctx := context.Background()

	pomKey := base.BlobKey("production", "mylib-1.0.0.pom")
	if err := f.blobStore.PutBytes(ctx, pomKey, []byte("released pom")); err != nil {
		t.Fatal(err)
	}
	if err := f.assetRepo.Create(ctx, &domain.Asset{
		RepositoryID: "repo-production", Repository: "production",
		Path: "mylib-1.0.0.pom", BlobKey: pomKey, SizeBytes: 12,
	}); err != nil {
		t.Fatal(err)
	}

	err := promote()
	if err == nil {
		t.Fatal("promotion over a taken path succeeded under allow_once")
	}
	if !strings.Contains(err.Error(), "Repository does not allow updating assets: production") {
		t.Errorf("error = %q, want the redeploy message", err)
	}
	if f.blobStore.Has(base.BlobKey("production", "mylib-1.0.0.jar")) {
		t.Error("the jar was copied before the promotion was refused")
	}
	if got, _ := f.blobStore.Read(pomKey); got != "released pom" {
		t.Errorf("target pom = %q, want the released bytes untouched", got)
	}
	_, assets, _ := targetState(t, f)
	if assets != 1 {
		t.Errorf("target holds %d assets, want only the pre-existing pom", assets)
	}
}

func TestPromotion_AllowOnceTarget_FirstPromotionOKSecondRejected(t *testing.T) {
	f, promote := seedPolicyPromotion(t, domain.WritePolicyAllowOnce)
	if err := promote(); err != nil {
		t.Fatalf("first promotion into an empty allow_once target: %v", err)
	}
	_, assets, _ := targetState(t, f)
	if assets != 2 {
		t.Fatalf("target holds %d assets after the first promotion, want 2", assets)
	}
	if err := promote(); err == nil || !strings.Contains(err.Error(), "does not allow updating assets") {
		t.Fatalf("second promotion of the same version: err = %v, want a redeploy refusal", err)
	}
}

func TestPromotion_DenyTarget_RejectsOutright(t *testing.T) {
	f, promote := seedPolicyPromotion(t, domain.WritePolicyDeny)
	err := promote()
	if err == nil || !strings.Contains(err.Error(), "Repository is read-only: production") {
		t.Fatalf("err = %v, want a read-only refusal", err)
	}
	comps, assets, keys := targetState(t, f)
	if comps != 0 || assets != 0 {
		t.Errorf("read-only target got %d components / %d assets", comps, assets)
	}
	if strings.Join(keys, ",") != strings.Join(f.beforeKeys, ",") {
		t.Errorf("blob keys = %v, want unchanged %v", keys, f.beforeKeys)
	}
}

func TestPromotion_AllowTarget_StillOverwrites(t *testing.T) {
	_, promote := seedPolicyPromotion(t, domain.WritePolicyAllow)
	if err := promote(); err != nil {
		t.Fatal(err)
	}
	if err := promote(); err != nil {
		t.Fatalf("re-promotion into an allow target: %v", err)
	}
}
