package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// #539 over #541's expanded image: the target's write policy is applied to
// every path the promotion of a tag writes — tag, digest alias, config,
// layers — before anything is copied.

func setTargetPolicy(f *ociFixture, cfg map[string]any) { f.to.FormatConfig = cfg }

func TestPromotion_DockerTag_AllowOnceTargetWithTagTaken_Refused(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	setTargetPolicy(f, map[string]any{domain.WritePolicyKey: "allow_once"})
	cfg := f.blob("app", []byte("cfg"))
	l := f.blob("app", []byte("layer"))
	tag, _ := f.manifest("app", "1.0", imageManifest(cfg, l), true)
	// The target already has a different image at the same tag.
	f.put(f.to, "app", "1.0", "/manifests/app/1.0", []byte("released"), "application/vnd.oci.image.manifest.v1+json")

	_, err := f.svc.Promote(context.Background(), f.rule.ID, []string{tag.ID}, "u")
	if err == nil || !strings.Contains(err.Error(), "Repository does not allow updating assets: docker-b") {
		t.Fatalf("Promote err = %v, want a redeploy refusal", err)
	}
	if got := f.targetBytes("/manifests/app/1.0"); string(got) != "released" {
		t.Errorf("target tag = %q, want the released bytes untouched", got)
	}
	if comps, assets := f.targetCount(); comps != 1 || assets != 1 {
		t.Errorf("target holds %d/%d, want only the pre-existing tag (nothing copied)", comps, assets)
	}
}

func TestPromotion_DockerTag_AllowOnceTargetWithBlobsPresent_Allowed(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	setTargetPolicy(f, map[string]any{domain.WritePolicyKey: "allow_once"})
	cfg := f.blob("app", []byte("cfg"))
	l := f.blob("app", []byte("layer"))
	body := imageManifest(cfg, l)
	tag, digest := f.manifest("app", "2.0", body, true)
	// Layer and digest manifest already in the target: content-addressed, exempt.
	f.put(f.to, "app", l, "/blobs/app/"+l, []byte("layer"), "application/octet-stream")
	f.put(f.to, "app", digest, "/manifests/app/"+digest, body, "application/vnd.oci.image.manifest.v1+json")

	f.promote(tag.ID)
	if got := f.targetBytes("/manifests/app/2.0"); string(got) != string(body) {
		t.Errorf("tag not promoted: %q", got)
	}
	f.targetBytes("/blobs/app/" + cfg)
}

func TestPromotion_DockerTag_LatestWithRedeployLatest_Allowed(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	setTargetPolicy(f, map[string]any{domain.WritePolicyKey: "allow_once", domain.AllowRedeployLatestKey: true})
	cfg := f.blob("app", []byte("cfg"))
	l := f.blob("app", []byte("layer"))
	body := imageManifest(cfg, l)
	tag, _ := f.manifest("app", "latest", body, true)
	f.put(f.to, "app", "latest", "/manifests/app/latest", []byte("old latest"), "application/vnd.oci.image.manifest.v1+json")

	f.promote(tag.ID)
	if got := f.targetBytes("/manifests/app/latest"); string(got) != string(body) {
		t.Errorf("latest not replaced: %q", got)
	}
}

func TestPromotion_DockerTag_LatestWithoutFlag_Refused(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	setTargetPolicy(f, map[string]any{domain.WritePolicyKey: "allow_once"})
	cfg := f.blob("app", []byte("cfg"))
	tag, _ := f.manifest("app", "latest", imageManifest(cfg), true)
	f.put(f.to, "app", "latest", "/manifests/app/latest", []byte("old latest"), "application/vnd.oci.image.manifest.v1+json")

	if _, err := f.svc.Promote(context.Background(), f.rule.ID, []string{tag.ID}, "u"); err == nil {
		t.Fatal("re-promoting latest without allow_redeploy_latest succeeded")
	}
}

func TestPromotion_DockerTag_DenyTarget_Refused(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	setTargetPolicy(f, map[string]any{domain.WritePolicyKey: "deny"})
	cfg := f.blob("app", []byte("cfg"))
	tag, _ := f.manifest("app", "1.0", imageManifest(cfg), true)

	_, err := f.svc.Promote(context.Background(), f.rule.ID, []string{tag.ID}, "u")
	if err == nil || !strings.Contains(err.Error(), "Repository is read-only: docker-b") {
		t.Fatalf("Promote err = %v, want a read-only refusal", err)
	}
	if comps, assets := f.targetCount(); comps != 0 || assets != 0 {
		t.Errorf("read-only target got %d/%d", comps, assets)
	}
}

// A request filed while the target allowed it is checked again at approval:
// a tag taken in the meantime refuses the copy with nothing written.
func TestPromotion_DockerTag_TagTakenBeforeApproval_NothingCopied(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	ctx := context.Background()
	f.rule.RequireManualApproval = true
	if err := f.promoRepo.UpdateRule(ctx, f.rule); err != nil {
		t.Fatal(err)
	}
	setTargetPolicy(f, map[string]any{domain.WritePolicyKey: "allow_once"})
	cfg := f.blob("app", []byte("cfg"))
	l := f.blob("app", []byte("layer"))
	tag, _ := f.manifest("app", "3.0", imageManifest(cfg, l), true)

	res, err := f.svc.Promote(ctx, f.rule.ID, []string{tag.ID}, "u")
	if err != nil || len(res) != 1 || res[0].Status != domain.PromotionPending {
		t.Fatalf("Promote = %+v, %v; want one pending request", res, err)
	}
	f.put(f.to, "app", "3.0", "/manifests/app/3.0", []byte("pushed meanwhile"), "application/vnd.oci.image.manifest.v1+json")

	if err := f.svc.Approve(ctx, res[0].ID, "admin"); err == nil || !strings.Contains(err.Error(), "does not allow updating assets") {
		t.Fatalf("Approve err = %v, want a redeploy refusal", err)
	}
	if comps, assets := f.targetCount(); comps != 1 || assets != 1 {
		t.Errorf("target holds %d/%d after a refused approval, want only the pushed tag", comps, assets)
	}
	if got := f.targetBytes("/manifests/app/3.0"); string(got) != "pushed meanwhile" {
		t.Errorf("target tag = %q", got)
	}
}
