package oci

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// ManifestReader returns the stored bytes of a manifest asset.
type ManifestReader func(ctx context.Context, a *domain.Asset) ([]byte, error)

// manifestRefs is what one manifest names: the blobs it is built from and the
// other manifests it points at (index children and its referrer subject).
type manifestRefs struct {
	blobs     []string
	manifests []string
}

func parseManifestRefs(body []byte) manifestRefs {
	var m struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
		Subject *struct {
			Digest string `json:"digest"`
		} `json:"subject"`
	}
	var out manifestRefs
	if json.Unmarshal(body, &m) != nil {
		return out
	}
	if m.Config.Digest != "" {
		out.blobs = append(out.blobs, m.Config.Digest)
	}
	for _, l := range m.Layers {
		if l.Digest != "" {
			out.blobs = append(out.blobs, l.Digest)
		}
	}
	for _, c := range m.Manifests {
		if c.Digest != "" {
			out.manifests = append(out.manifests, c.Digest)
		}
	}
	if m.Subject != nil && m.Subject.Digest != "" {
		out.manifests = append(out.manifests, m.Subject.Digest)
	}
	return out
}

// PlanImageRelease returns what of imageName is left without a user once the
// manifests in released are gone (#618, #620): the digest aliases no remaining
// tag resolves to and no remaining manifest names (as an index child or a
// referrer subject), and the blobs no remaining manifest is built from. It
// only reads; the caller deletes the returned paths, aliases first.
//
// released maps the hex sha256 of each manifest already deleted to its bytes,
// read before the delete. A manifest that cannot be read, or a listing error,
// returns the error: without knowing what it names, nothing is safe to drop.
func PlanImageRelease(ctx context.Context, assets repository.AssetRepo, read ManifestReader,
	repoName, imageName string, released map[string][]byte,
) ([]string, error) {
	plan := &releasePlan{
		prefix:         manifestPath(imageName, ""),
		byPath:         map[string]*releaseEntry{},
		removed:        map[*releaseEntry]bool{},
		candidates:     map[string]bool{},
		blobCandidates: map[string]bool{},
	}
	if err := plan.load(ctx, assets, read, repoName); err != nil {
		return nil, err
	}
	for sha, body := range released {
		plan.candidates["sha256:"+sha] = true
		plan.release(parseManifestRefs(body))
	}
	plan.dropUnneeded()

	aliases := make([]string, 0, len(plan.removed))
	for e := range plan.removed {
		aliases = append(aliases, e.asset.Path)
	}
	var blobs []string
	for _, b := range plan.unusedBlobs() {
		p := blobPath(imageName, b)
		if a, err := assets.GetByPath(ctx, repoName, p); err == nil && a != nil {
			blobs = append(blobs, p)
		}
	}
	sort.Strings(aliases)
	sort.Strings(blobs)
	return append(aliases, blobs...), nil
}

type releaseEntry struct {
	asset domain.Asset
	ref   string
	refs  manifestRefs
}

type releasePlan struct {
	prefix         string
	entries        []*releaseEntry
	byPath         map[string]*releaseEntry
	removed        map[*releaseEntry]bool
	candidates     map[string]bool // manifest digests that may have lost their last user
	blobCandidates map[string]bool
}

// load reads every manifest of the image. A nested image's manifests name its
// own blobs, never this image's, so they are left out.
func (p *releasePlan) load(ctx context.Context, assets repository.AssetRepo, read ManifestReader, repoName string) error {
	listed, err := assets.ListByRepoAndPath(ctx, repoName, p.prefix)
	if err != nil {
		return err
	}
	for i := range listed {
		ref := strings.TrimPrefix(listed[i].Path, p.prefix)
		if ref == "" || strings.Contains(ref, "/") {
			continue
		}
		body, err := read(ctx, &listed[i])
		if err != nil {
			return err
		}
		e := &releaseEntry{asset: listed[i], ref: ref, refs: parseManifestRefs(body)}
		p.entries = append(p.entries, e)
		p.byPath[e.asset.Path] = e
	}
	return nil
}

// release marks what a manifest that is going away named as possibly unused.
func (p *releasePlan) release(refs manifestRefs) {
	for _, b := range refs.blobs {
		p.blobCandidates[b] = true
	}
	for _, m := range refs.manifests {
		p.candidates[m] = true
	}
}

// needed reports whether a remaining manifest other than self still uses digest.
func (p *releasePlan) needed(digest string, self *releaseEntry) bool {
	for _, o := range p.entries {
		if o == self || p.removed[o] {
			continue
		}
		if !strings.Contains(o.ref, ":") && "sha256:"+o.asset.SHA256 == digest {
			return true // a tag resolves to it
		}
		for _, m := range o.refs.manifests {
			if m == digest {
				return true // an index child or a referrer subject
			}
		}
	}
	return false
}

// dropUnneeded removes candidate aliases until none changes: dropping an
// index can leave its children unneeded.
func (p *releasePlan) dropUnneeded() {
	for changed := true; changed; {
		changed = false
		digests := make([]string, 0, len(p.candidates))
		for d := range p.candidates {
			digests = append(digests, d)
		}
		sort.Strings(digests)
		for _, d := range digests {
			e := p.byPath[p.prefix+d]
			if e == nil || p.removed[e] || p.needed(d, e) {
				continue
			}
			p.removed[e] = true
			changed = true
			p.release(e.refs)
		}
	}
}

// unusedBlobs is the candidate blobs no remaining manifest is built from.
func (p *releasePlan) unusedBlobs() []string {
	live := map[string]bool{}
	for _, e := range p.entries {
		if p.removed[e] {
			continue
		}
		for _, b := range e.refs.blobs {
			live[b] = true
		}
	}
	var out []string
	for b := range p.blobCandidates {
		if !live[b] {
			out = append(out, b)
		}
	}
	return out
}
