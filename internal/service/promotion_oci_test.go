package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ociFixture is a source registry repository populated the way the OCI handler
// stores an image: one component per tag, per manifest digest and per blob.
type ociFixture struct {
	t         *testing.T
	svc       *service.PromotionService
	promoRepo *testutil.PromotionRepo
	compRepo  *testutil.ComponentRepo
	assetRepo *testutil.AssetRepo
	store     *testutil.BlobStore
	scanRepo  *testutil.ScanResultRepo
	from, to  *domain.Repository
	rule      *domain.PromotionRule
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func newOCIFixture(t *testing.T, format string, store storage.BlobStore) *ociFixture {
	t.Helper()
	promoRepo := testutil.NewPromotionRepo()
	compRepo := testutil.NewComponentRepo()
	assetRepo := testutil.NewAssetRepo()
	mem := testutil.NewBlobStore()
	if store == nil {
		store = mem
	} else if fs, ok := store.(*failingPutStore); ok {
		mem = fs.BlobStore
	}
	repoRepo := testutil.NewRepoRepo()
	scanRepo := testutil.NewScanResultRepo()
	svc, err := service.NewPromotionService(promoRepo, compRepo, assetRepo, repoRepo,
		testutil.NewBlobStoreRepo(), scanRepo, testutil.NewFakeResolver(store))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	from := testutil.SimpleRepo("docker-a", format)
	to := testutil.SimpleRepo("docker-b", format)
	repoRepo.Create(ctx, from)
	repoRepo.Create(ctx, to)
	rule := &domain.PromotionRule{Name: "a-to-b", FromRepo: from.Name, ToRepo: to.Name}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	return &ociFixture{t: t, svc: svc, promoRepo: promoRepo, compRepo: compRepo, assetRepo: assetRepo,
		store: mem, scanRepo: scanRepo, from: from, to: to, rule: rule}
}

// put stores content at path in repo under its own component (image, version)
// and returns that component.
func (f *ociFixture) put(repo *domain.Repository, image, version, path string, content []byte, ct string) *domain.Component {
	f.t.Helper()
	ctx := context.Background()
	comp := &domain.Component{RepositoryID: repo.ID, Repository: repo.Name, Format: string(repo.Format),
		Name: image, Version: version}
	if err := f.compRepo.Create(ctx, comp); err != nil {
		f.t.Fatal(err)
	}
	key := base.BlobKey(repo.Name, path)
	if err := f.store.PutBytes(ctx, key, content); err != nil {
		f.t.Fatal(err)
	}
	if err := f.assetRepo.Create(ctx, &domain.Asset{ComponentID: comp.ID, RepositoryID: repo.ID,
		Repository: repo.Name, Path: path, BlobKey: key, SizeBytes: int64(len(content)),
		ContentType: ct, SHA256: sha256Hex(content)}); err != nil {
		f.t.Fatal(err)
	}
	return comp
}

// blob stores a blob in the source and returns its digest.
func (f *ociFixture) blob(image string, content []byte) string {
	d := "sha256:" + sha256Hex(content)
	f.put(f.from, image, d, "/blobs/"+image+"/"+d, content, "application/octet-stream")
	return d
}

// manifest stores a manifest by tag (and, when alias is set, by digest, as a
// push does) and returns the tag component and the digest.
func (f *ociFixture) manifest(image, tag string, body []byte, alias bool) (*domain.Component, string) {
	d := "sha256:" + sha256Hex(body)
	ct := "application/vnd.oci.image.manifest.v1+json"
	var comp *domain.Component
	if tag != "" {
		comp = f.put(f.from, image, tag, "/manifests/"+image+"/"+tag, body, ct)
	}
	if alias || tag == "" {
		c := f.put(f.from, image, d, "/manifests/"+image+"/"+d, body, ct)
		if comp == nil {
			comp = c
		}
	}
	return comp, d
}

func imageManifest(config string, layers ...string) []byte {
	var ls []string
	for _, l := range layers {
		ls = append(ls, fmt.Sprintf(`{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":%q,"size":1}`, l))
	}
	return []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",`+
		`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":1},"layers":[%s]}`,
		config, strings.Join(ls, ",")))
}

// targetBytes returns what the target serves at path, failing when it does not.
func (f *ociFixture) targetBytes(path string) []byte {
	f.t.Helper()
	ctx := context.Background()
	a, err := f.assetRepo.GetByPath(ctx, f.to.Name, path)
	if err != nil {
		f.t.Fatalf("target has no asset at %s: %v", path, err)
	}
	rc, _, err := f.store.Get(ctx, a.BlobKey)
	if err != nil {
		f.t.Fatalf("target asset %s has no bytes: %v", path, err)
	}
	defer func() { _ = rc.Close() }()
	b, _ := io.ReadAll(rc)
	if got := sha256Hex(b); got != a.SHA256 {
		f.t.Errorf("target %s: bytes hash %s, row says %s", path, got, a.SHA256)
	}
	return b
}

func (f *ociFixture) targetCount() (comps, assets int) {
	f.t.Helper()
	ctx := context.Background()
	page, err := f.compRepo.ListByRepoNames(ctx, []string{f.to.Name}, 1000, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, c := range page.Items {
		rows, _ := f.assetRepo.ListByComponentID(ctx, c.ID)
		assets += len(rows)
	}
	return len(page.Items), assets
}

func (f *ociFixture) promote(compID string) domain.PromotionRequest {
	f.t.Helper()
	res, err := f.svc.Promote(context.Background(), f.rule.ID, []string{compID}, "u")
	if err != nil {
		f.t.Fatalf("Promote: %v", err)
	}
	if len(res) != 1 || res[0].Status != domain.PromotionCompleted {
		f.t.Fatalf("promotion did not complete: %+v", res)
	}
	return res[0]
}

// #541: promoting a tag copies the whole image — tag, digest alias, config and
// every layer — so the target can serve a pull.
func TestPromotion_DockerTag_CopiesWholeImage(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	cfg := f.blob("team/app", []byte(`{"architecture":"amd64"}`))
	l1 := f.blob("team/app", []byte("layer-one"))
	l2 := f.blob("team/app", []byte("layer-two"))
	body := imageManifest(cfg, l1, l2)
	tag, digest := f.manifest("team/app", "1.2.3", body, true)

	req := f.promote(tag.ID)
	if req.IncludedComponents != 4 {
		t.Errorf("IncludedComponents = %d, want 4 (alias, config, 2 layers)", req.IncludedComponents)
	}
	if got := f.targetBytes("/manifests/team/app/1.2.3"); string(got) != string(body) {
		t.Errorf("tag manifest differs: %s", got)
	}
	if got := f.targetBytes("/manifests/team/app/" + digest); string(got) != string(body) {
		t.Errorf("digest alias differs: %s", got)
	}
	for _, d := range []string{cfg, l1, l2} {
		f.targetBytes("/blobs/team/app/" + d)
	}
	if comps, assets := f.targetCount(); comps != 5 || assets != 5 {
		t.Errorf("target holds %d components / %d assets, want 5/5", comps, assets)
	}
	// Each promoted piece keeps its coordinates, so Browse shows it where a
	// push would have put it.
	a, _ := f.assetRepo.GetByPath(context.Background(), f.to.Name, "/blobs/team/app/"+l1)
	c, _ := f.compRepo.Get(context.Background(), a.ComponentID)
	if c.Name != "team/app" || c.Version != l1 || c.Repository != f.to.Name {
		t.Errorf("layer component = %+v", c)
	}
}

// A manifest cached by tag alone (no alias row in the source) still arrives
// with its alias: clients fetch the manifest by digest after resolving a tag.
func TestPromotion_DockerTag_SynthesizesMissingAlias(t *testing.T) {
	f := newOCIFixture(t, "oci", nil)
	cfg := f.blob("app", []byte("cfg"))
	body := imageManifest(cfg)
	tag, digest := f.manifest("app", "latest", body, false)

	req := f.promote(tag.ID)
	if req.IncludedComponents != 2 {
		t.Errorf("IncludedComponents = %d, want 2", req.IncludedComponents)
	}
	if got := f.targetBytes("/manifests/app/" + digest); string(got) != string(body) {
		t.Errorf("synthesized alias differs: %s", got)
	}
	a, _ := f.assetRepo.GetByPath(context.Background(), f.to.Name, "/manifests/app/"+digest)
	c, _ := f.compRepo.Get(context.Background(), a.ComponentID)
	if c.Version != digest || c.Name != "app" {
		t.Errorf("alias component = %+v", c)
	}
}

// A multi-arch tag (image index / manifest list) brings every child manifest
// and each child's blobs; a layer shared by two platforms is copied once.
func TestPromotion_DockerIndex_CopiesChildren(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	shared := f.blob("app", []byte("shared-base"))
	cfgA := f.blob("app", []byte("cfg-amd64"))
	cfgB := f.blob("app", []byte("cfg-arm64"))
	lA := f.blob("app", []byte("layer-amd64"))
	_, dA := f.manifest("app", "", imageManifest(cfgA, shared, lA), true)
	_, dB := f.manifest("app", "", imageManifest(cfgB, shared), true)
	index := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.list.v2+json",`+
		`"manifests":[{"digest":%q,"platform":{"architecture":"amd64","os":"linux"}},`+
		`{"digest":%q,"platform":{"architecture":"arm64","os":"linux"}}]}`, dA, dB))
	tag, dIndex := f.manifest("app", "2.0", index, true)

	req := f.promote(tag.ID)
	// alias + 2 children + shared + cfgA + lA + cfgB
	if req.IncludedComponents != 7 {
		t.Errorf("IncludedComponents = %d, want 7", req.IncludedComponents)
	}
	for _, p := range []string{
		"/manifests/app/2.0", "/manifests/app/" + dIndex,
		"/manifests/app/" + dA, "/manifests/app/" + dB,
		"/blobs/app/" + shared, "/blobs/app/" + cfgA, "/blobs/app/" + lA, "/blobs/app/" + cfgB,
	} {
		f.targetBytes(p)
	}
}

// Promoting a manifest by digest (Browse → Manifests) brings its blobs; there
// is no alias to add, it is the alias.
func TestPromotion_DockerDigestManifest_CopiesBlobs(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	cfg := f.blob("app", []byte("cfg"))
	l := f.blob("app", []byte("l"))
	byDigest, _ := f.manifest("app", "", imageManifest(cfg, l), true)

	req := f.promote(byDigest.ID)
	if req.IncludedComponents != 2 {
		t.Errorf("IncludedComponents = %d, want 2", req.IncludedComponents)
	}
	f.targetBytes("/blobs/app/" + cfg)
	f.targetBytes("/blobs/app/" + l)
}

// A layer missing from the source refuses the promotion up front — no request,
// nothing in the target — with an error naming the missing blob.
func TestPromotion_DockerTag_MissingLayerRefused(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	cfg := f.blob("app", []byte("cfg"))
	missing := "sha256:" + sha256Hex([]byte("never pushed"))
	tag, _ := f.manifest("app", "1", imageManifest(cfg, missing), true)

	_, err := f.svc.Promote(context.Background(), f.rule.ID, []string{tag.ID}, "u")
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("Promote error = %v, want one naming %s", err, missing)
	}
	if reqs, _ := f.promoRepo.ListRequests(context.Background(), ""); len(reqs) != 0 {
		t.Errorf("a refused promotion filed %d request(s)", len(reqs))
	}
	if comps, assets := f.targetCount(); comps != 0 || assets != 0 {
		t.Errorf("target holds %d/%d after a refused promotion", comps, assets)
	}
}

// Under manual approval the image is re-resolved when the copy runs: a layer
// deleted while the request sat pending fails the approval with nothing copied.
func TestPromotion_DockerTag_LayerGoneBeforeApproval(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	ctx := context.Background()
	f.rule.RequireManualApproval = true
	if err := f.promoRepo.UpdateRule(ctx, f.rule); err != nil {
		t.Fatal(err)
	}
	cfg := f.blob("app", []byte("cfg"))
	l := f.blob("app", []byte("layer"))
	tag, _ := f.manifest("app", "1", imageManifest(cfg, l), true)

	res, err := f.svc.Promote(ctx, f.rule.ID, []string{tag.ID}, "u")
	if err != nil || len(res) != 1 || res[0].Status != domain.PromotionPending {
		t.Fatalf("Promote = %+v, %v; want one pending request", res, err)
	}
	if res[0].IncludedComponents != 3 {
		t.Errorf("pending IncludedComponents = %d, want 3", res[0].IncludedComponents)
	}

	// The layer's bytes vanish (GC, a corrupted store) — the row stays.
	_ = f.store.Delete(ctx, base.BlobKey(f.from.Name, "/blobs/app/"+l))
	if err := f.svc.Approve(ctx, res[0].ID, "admin"); err == nil || !strings.Contains(err.Error(), l) {
		t.Fatalf("Approve error = %v, want one naming %s", err, l)
	}
	if comps, assets := f.targetCount(); comps != 0 || assets != 0 {
		t.Errorf("target holds %d/%d after a failed approval", comps, assets)
	}
}

// Blobs the target already holds with the same digest are skipped, not
// rewritten; one at the same path with different content is replaced.
func TestPromotion_DockerTag_SkipsBlobsAlreadyInTarget(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	ctx := context.Background()
	cfg := f.blob("app", []byte("cfg"))
	l := f.blob("app", []byte("layer"))
	tag, _ := f.manifest("app", "1", imageManifest(cfg, l), true)

	// The layer is already in the target (another tag shared it) under a key
	// the promotion would not pick, so a rewrite would be visible.
	layerPath := "/blobs/app/" + l
	f.put(f.to, "app", l, layerPath, []byte("layer"), "application/octet-stream")
	pre, _ := f.assetRepo.GetByPath(ctx, f.to.Name, layerPath)
	const sentinelKey = "preexisting-layer-key"
	_ = f.store.PutBytes(ctx, sentinelKey, []byte("layer"))
	pre.BlobKey = sentinelKey
	// The config path holds stale bytes: same path, different digest.
	f.put(f.to, "app", cfg, "/blobs/app/"+cfg, []byte("stale"), "application/octet-stream")

	f.promote(tag.ID)
	after, _ := f.assetRepo.GetByPath(ctx, f.to.Name, layerPath)
	if after.BlobKey != sentinelKey {
		t.Errorf("layer already in target was rewritten (key %s)", after.BlobKey)
	}
	if got := f.targetBytes("/blobs/app/" + cfg); string(got) != "cfg" {
		t.Errorf("stale config not replaced: %q", got)
	}
}

// A foreign layer (urls set, never pushed to a registry) does not block the
// promotion; a malformed digest or manifest does.
func TestPromotion_DockerTag_ForeignLayerAndMalformedManifests(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	cfg := f.blob("app", []byte("cfg"))
	foreign := "sha256:" + sha256Hex([]byte("windows base"))
	body := []byte(fmt.Sprintf(`{"schemaVersion":2,"config":{"digest":%q},"layers":[`+
		`{"mediaType":"application/vnd.docker.image.rootfs.foreign.diff.tar.gzip","digest":%q,"urls":["https://example.invalid/l"]}]}`,
		cfg, foreign))
	tag, _ := f.manifest("app", "win", body, true)
	if req := f.promote(tag.ID); req.IncludedComponents != 2 {
		t.Errorf("IncludedComponents = %d, want 2 (alias, config)", req.IncludedComponents)
	}

	for name, bad := range map[string]string{
		"traversal digest": `{"schemaVersion":2,"layers":[{"digest":"../../etc/passwd"}]}`,
		"bad child digest": `{"schemaVersion":2,"manifests":[{"digest":"nope"}]}`,
		"not json":         `not a manifest`,
		"missing child":    `{"schemaVersion":2,"manifests":[{"digest":"sha256:` + strings.Repeat("0", 64) + `"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			tag, _ := f.manifest("app", strings.ReplaceAll(name, " ", "-"), []byte(bad), true)
			if _, err := f.svc.Promote(context.Background(), f.rule.ID, []string{tag.ID}, "u"); err == nil {
				t.Fatal("Promote succeeded on a malformed image")
			}
		})
	}
}

// Legacy Docker schema1 names its layers under fsLayers[].blobSum.
func TestPromotion_DockerSchema1_CopiesLayers(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	l := f.blob("old", []byte("v1 layer"))
	tag, _ := f.manifest("old", "v1", []byte(fmt.Sprintf(`{"schemaVersion":1,"fsLayers":[{"blobSum":%q},{"blobSum":%q}]}`, l, l)), true)
	if req := f.promote(tag.ID); req.IncludedComponents != 2 {
		t.Errorf("IncludedComponents = %d, want 2 (alias, one distinct layer)", req.IncludedComponents)
	}
	f.targetBytes("/blobs/old/" + l)
}

// The gates are the tag's: a rule demanding a clean scan is satisfied by the
// tag's scan, even though no blob was ever scanned on its own.
func TestPromotion_DockerTag_GatesApplyToTagOnly(t *testing.T) {
	f := newOCIFixture(t, "docker", nil)
	ctx := context.Background()
	f.rule.RequireScanPass = true
	f.rule.PathFilter = `path == "//team/app"`
	if err := f.promoRepo.UpdateRule(ctx, f.rule); err != nil {
		t.Fatal(err)
	}
	cfg := f.blob("team/app", []byte("cfg"))
	tag, _ := f.manifest("team/app", "1", imageManifest(cfg), true)

	if _, err := f.svc.Promote(ctx, f.rule.ID, []string{tag.ID}, "u"); err == nil {
		t.Fatal("an unscanned tag passed a require_scan_pass rule")
	}
	if err := f.scanRepo.Insert(ctx, &domain.ScanResultRow{ComponentID: tag.ID, Status: domain.ScanStatusOK}); err != nil {
		t.Fatal(err)
	}
	req := f.promote(tag.ID)
	if req.IncludedComponents != 2 {
		t.Errorf("IncludedComponents = %d, want 2", req.IncludedComponents)
	}
	f.targetBytes("/blobs/team/app/" + cfg)
}

// Non-OCI formats are untouched: a raw asset that happens to live under
// /manifests/ is copied as itself, not parsed.
func TestPromotion_NonOCIFormat_NotExpanded(t *testing.T) {
	f := newOCIFixture(t, "raw", nil)
	tag, _ := f.manifest("app", "1", []byte("not json at all"), false)
	if req := f.promote(tag.ID); req.IncludedComponents != 0 {
		t.Errorf("IncludedComponents = %d, want 0", req.IncludedComponents)
	}
	if comps, _ := f.targetCount(); comps != 1 {
		t.Errorf("target holds %d components, want 1", comps)
	}
}

// failingPutStore fails writes to keys containing failOn.
type failingPutStore struct {
	*testutil.BlobStore
	failOn string
}

func (s *failingPutStore) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if strings.Contains(key, s.failOn) {
		return errors.New("simulated write failure")
	}
	return s.BlobStore.Put(ctx, key, r, size)
}

// A write failing halfway through the image rolls back every piece already
// copied — the tag included — so the target never shows a partial image.
func TestPromotion_DockerTag_MidCopyFailureRollsBackImage(t *testing.T) {
	fs := &failingPutStore{BlobStore: testutil.NewBlobStore()}
	f := newOCIFixture(t, "docker", fs)
	cfg := f.blob("app", []byte("cfg"))
	l := f.blob("app", []byte("layer"))
	tag, _ := f.manifest("app", "1", imageManifest(cfg, l), true)
	fs.failOn = base.BlobKey(f.to.Name, "/blobs/app/"+l)
	before, _ := fs.ListKeys(context.Background())

	res, err := f.svc.Promote(context.Background(), f.rule.ID, []string{tag.ID}, "u")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if res[0].Status != domain.PromotionFailed {
		t.Fatalf("status = %s, want failed", res[0].Status)
	}
	if comps, assets := f.targetCount(); comps != 0 || assets != 0 {
		t.Errorf("target holds %d/%d after a failed image copy", comps, assets)
	}
	if after, _ := fs.ListKeys(context.Background()); len(after) != len(before) {
		t.Errorf("blob keys %d before, %d after a failed copy: orphans left behind", len(before), len(after))
	}
}
