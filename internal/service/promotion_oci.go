package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// A Docker/OCI image is not one component. The registry stores a tag, the
// manifest under its digest, and every config/layer blob as separate
// components (Browse shows them as Tags / Manifests / Blobs), so copying only
// the component the caller named leaves a tag in the target whose digest and
// blobs resolve nowhere — `docker pull` fails (#541). Promoting an OCI
// manifest therefore promotes its closure: this file works that closure out.
//
// The paths mirror internal/formats/oci (manifestPath / blobPath, unexported
// there); the integration test pins that the two agree.
const (
	ociManifestPrefix = "/manifests/"
	ociBlobPrefix     = "/blobs/"

	// ociMaxManifestBytes is the OCI Distribution Spec manifest cap the push
	// path enforces; a stored manifest is never larger.
	ociMaxManifestBytes = 4 << 20

	// ociMaxIndexDepth bounds index-of-index recursion. The spec allows
	// nesting, real images use one level; the cap only stops a crafted cycle.
	ociMaxIndexDepth = 4
)

// ociDigestRe is the OCI digest grammar. A manifest is caller-supplied JSON:
// a "digest" outside it is refused rather than turned into a path lookup.
var ociDigestRe = regexp.MustCompile(`^[a-z0-9]+(?:[+._-][a-z0-9]+)*:[a-zA-Z0-9=_-]+$`)

// promotionUnit is one component's worth of assets to copy.
type promotionUnit struct {
	// comp is the source component the target component mirrors.
	comp *domain.Component
	// assets are the source assets to copy into it.
	assets []domain.Asset
	// contentAddressed marks a unit pulled in by image expansion. Its path
	// names its content (a digest), so a target asset at the same path with
	// the same SHA-256 is the same bytes and is left alone instead of copied.
	contentAddressed bool
}

// isOCIFormat reports whether components of this format are stored in the
// OCI Distribution layout.
func isOCIFormat(format string) bool {
	return format == string(domain.FormatDocker) || format == string(domain.FormatOCI)
}

// ociDescriptor is the part of an OCI/Docker content descriptor expansion needs.
type ociDescriptor struct {
	Digest string   `json:"digest"`
	URLs   []string `json:"urls"`
}

// promotedManifestDoc covers every manifest shape the registry accepts: Docker v2
// schema2 and OCI image manifests (config + layers), OCI artifact manifests
// (blobs), OCI image indexes and Docker manifest lists (manifests), and the
// legacy Docker schema1 (fsLayers).
type promotedManifestDoc struct {
	Config    *ociDescriptor  `json:"config"`
	Layers    []ociDescriptor `json:"layers"`
	Blobs     []ociDescriptor `json:"blobs"`
	Manifests []ociDescriptor `json:"manifests"`
	FSLayers  []struct {
		BlobSum string `json:"blobSum"`
	} `json:"fsLayers"`
}

// splitOCIManifestPath splits "/manifests/<image>/<reference>" into its parts.
// The image name may itself contain slashes; the reference never does.
func splitOCIManifestPath(p string) (image, reference string, ok bool) {
	rest, found := strings.CutPrefix(p, ociManifestPrefix)
	if !found {
		return "", "", false
	}
	i := strings.LastIndex(rest, "/")
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// imageExpansion collects the closure of one or more manifests.
type imageExpansion struct {
	s        *PromotionService
	repoName string
	// seen holds every path already in the plan, the chosen component's own
	// included, so a layer shared by two platforms is copied once.
	seen  map[string]bool
	units []promotionUnit
}

// expandImage returns the components that must travel with comp for the
// target to hold a pullable image: for every manifest asset of comp, its
// digest alias, the config and layer blobs it names, and — for an image index
// or manifest list — each child manifest with its own blobs. Everything comes
// from comp's own repository, the one the rule promotes from.
//
// It only reads. A referenced blob or child manifest missing from the source
// is an error naming it, returned before anything is copied: a partial image
// in the target is exactly the failure being fixed. The one exception is a
// foreign layer (a descriptor carrying urls, e.g. Windows base layers), which
// registries are not expected to hold.
//
// Non-OCI components expand to nothing.
func (s *PromotionService) expandImage(ctx context.Context, comp *domain.Component, assets []domain.Asset) ([]promotionUnit, error) {
	if !isOCIFormat(comp.Format) {
		return nil, nil
	}
	x := &imageExpansion{s: s, repoName: comp.Repository, seen: map[string]bool{}}
	for _, a := range assets {
		x.seen[a.Path] = true
	}
	for i := range assets {
		a := &assets[i]
		image, reference, ok := splitOCIManifestPath(a.Path)
		if !ok {
			continue
		}
		body, err := s.readManifest(ctx, a)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		digestRef := "sha256:" + hex.EncodeToString(sum[:])
		if reference != digestRef {
			if err := x.addAlias(ctx, comp, a, image, digestRef); err != nil {
				return nil, err
			}
		}
		if err := x.addReferences(ctx, image, a.Path, body, 0); err != nil {
			return nil, err
		}
	}
	return x.units, nil
}

// imageDependents is expandImage for a component whose assets are not loaded
// yet: how many components its image brings along, zero for non-OCI formats.
func (s *PromotionService) imageDependents(ctx context.Context, comp *domain.Component) (int, error) {
	if !isOCIFormat(comp.Format) {
		return 0, nil
	}
	assets, err := s.assetRepo.ListByComponentID(ctx, comp.ID)
	if err != nil {
		return 0, fmt.Errorf("list assets: %w", err)
	}
	units, err := s.expandImage(ctx, comp, assets)
	return len(units), err
}

// addAlias brings the manifest's by-digest path along with a tag: clients
// resolve a tag and then fetch the manifest by digest. When the source has no
// alias row (a manifest cached by tag from an upstream, say), the target gets
// one anyway, holding the tag's own bytes — the digest names those bytes.
func (x *imageExpansion) addAlias(ctx context.Context, tagComp *domain.Component, tag *domain.Asset, image, digestRef string) error {
	aliasPath := ociManifestPrefix + image + "/" + digestRef
	if x.seen[aliasPath] {
		return nil
	}
	unit, found, err := x.lookup(ctx, aliasPath)
	if err != nil {
		return err
	}
	if !found {
		synth := *tag
		synth.ID = ""
		synth.Path = aliasPath
		comp := &domain.Component{
			Group:   tagComp.Group,
			Name:    tagComp.Name,
			Version: digestRef,
			Extra:   tagComp.Extra,
		}
		unit = promotionUnit{comp: comp, assets: []domain.Asset{synth}, contentAddressed: true}
	}
	x.seen[aliasPath] = true
	x.units = append(x.units, unit)
	return nil
}

// addReferences adds what the manifest at manifestPath names.
func (x *imageExpansion) addReferences(ctx context.Context, image, manifestPath string, body []byte, depth int) error {
	var doc promotedManifestDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("manifest %s is not valid JSON: %w", manifestPath, err)
	}

	var blobs []ociDescriptor
	if doc.Config != nil && doc.Config.Digest != "" {
		blobs = append(blobs, *doc.Config)
	}
	blobs = append(blobs, doc.Layers...)
	blobs = append(blobs, doc.Blobs...)
	for _, l := range doc.FSLayers {
		blobs = append(blobs, ociDescriptor{Digest: l.BlobSum})
	}
	for _, d := range blobs {
		if err := x.addBlob(ctx, image, manifestPath, d); err != nil {
			return err
		}
	}

	for _, d := range doc.Manifests {
		if err := x.addChildManifest(ctx, image, manifestPath, d, depth); err != nil {
			return err
		}
	}
	return nil
}

func (x *imageExpansion) addBlob(ctx context.Context, image, manifestPath string, d ociDescriptor) error {
	if !ociDigestRe.MatchString(d.Digest) {
		return fmt.Errorf("manifest %s references an invalid digest %q", manifestPath, d.Digest)
	}
	p := ociBlobPrefix + image + "/" + d.Digest
	if x.seen[p] {
		return nil
	}
	unit, found, err := x.lookup(ctx, p)
	if err != nil {
		return err
	}
	if !found {
		if len(d.URLs) > 0 {
			// A foreign layer lives at its urls, not in any registry.
			x.seen[p] = true
			return nil
		}
		return fmt.Errorf("image is incomplete in repository %q: blob %s referenced by %s is not stored there",
			x.repoName, d.Digest, manifestPath)
	}
	x.seen[p] = true
	x.units = append(x.units, unit)
	return nil
}

func (x *imageExpansion) addChildManifest(ctx context.Context, image, parentPath string, d ociDescriptor, depth int) error {
	if !ociDigestRe.MatchString(d.Digest) {
		return fmt.Errorf("manifest %s references an invalid digest %q", parentPath, d.Digest)
	}
	p := ociManifestPrefix + image + "/" + d.Digest
	if x.seen[p] {
		return nil
	}
	if depth >= ociMaxIndexDepth {
		return fmt.Errorf("manifest %s nests image indexes deeper than %d levels", parentPath, ociMaxIndexDepth)
	}
	unit, found, err := x.lookup(ctx, p)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("image is incomplete in repository %q: manifest %s referenced by %s is not stored there",
			x.repoName, d.Digest, parentPath)
	}
	x.seen[p] = true
	x.units = append(x.units, unit)
	body, err := x.s.readManifest(ctx, &unit.assets[0])
	if err != nil {
		return err
	}
	return x.addReferences(ctx, image, p, body, depth+1)
}

// lookup finds the source asset stored at path and its component. found is
// false only when no asset row exists; a row whose bytes are gone is an error,
// since the copy would fail on it halfway through.
func (x *imageExpansion) lookup(ctx context.Context, path string) (promotionUnit, bool, error) {
	asset, err := x.s.assetRepo.GetByPath(ctx, x.repoName, path)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && asset == nil) {
		return promotionUnit{}, false, nil
	}
	if err != nil {
		return promotionUnit{}, false, fmt.Errorf("look up %s in %q: %w", path, x.repoName, err)
	}
	comp, err := x.s.componentRepo.Get(ctx, asset.ComponentID)
	if err != nil || comp == nil {
		return promotionUnit{}, false, fmt.Errorf("component of %s in %q not found", path, x.repoName)
	}
	blobStoreID := asset.BlobStoreID
	store, _, err := x.s.resolveStore(ctx, &blobStoreID)
	if err != nil {
		return promotionUnit{}, false, fmt.Errorf("source asset %s: %w", path, err)
	}
	ok, err := store.Exists(ctx, asset.BlobKey)
	if err != nil {
		return promotionUnit{}, false, fmt.Errorf("check blob of %s: %w", path, err)
	}
	if !ok {
		return promotionUnit{}, false, fmt.Errorf("image is incomplete in repository %q: %s has no stored content", x.repoName, path)
	}
	return promotionUnit{comp: comp, assets: []domain.Asset{*asset}, contentAddressed: true}, true, nil
}

// readManifest reads a stored manifest's bytes, refusing anything past the
// size cap rather than parsing a truncated prefix.
func (s *PromotionService) readManifest(ctx context.Context, a *domain.Asset) ([]byte, error) {
	blobStoreID := a.BlobStoreID
	store, _, err := s.resolveStore(ctx, &blobStoreID)
	if err != nil {
		return nil, fmt.Errorf("source asset %s: %w", a.Path, err)
	}
	rc, _, err := store.Get(ctx, a.BlobKey)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", a.Path, err)
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(io.LimitReader(rc, ociMaxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", a.Path, err)
	}
	if len(body) > ociMaxManifestBytes {
		return nil, fmt.Errorf("manifest %s exceeds the 4MiB limit", a.Path)
	}
	return body, nil
}
