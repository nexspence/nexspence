package oci

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
)

// The pull gate (#641) — Harbor's "Prevent vulnerable images from running".
// With formatConfig.pull_block_severity set, a GET or HEAD of a manifest is
// refused with 403 DENIED while the latest scan of the image has a finding at
// or above that severity (domain.EvaluatePullGate holds the ranking). Blobs are
// not gated: they are content-addressed layers a client only learns about from
// a manifest, and one layer is shared by images the gate passes.
//
// Which scan judges a request:
//
//   - by tag: the scan of that tag.
//   - by digest: a scan of the manifest under that digest, or of any tag in
//     the repository that resolves to it — the newest of those. Failing that,
//     the scan of a tagged image index or manifest list in the repository that
//     names the digest among its platform manifests: that is how a client
//     pulls one platform of a multi-arch image, and the tag is what was
//     scanned. A platform manifest that has a scan of its own is judged by
//     that scan instead.
//
// A manifest the repository does not hold yet is let through to the handler:
// a hosted repository answers MANIFEST_UNKNOWN, a proxy fetches it, and there
// is no scan to judge either by. A stored manifest whose type is a non-image
// artifact (a signature, an SBOM, a Helm chart — anything whose config is not
// an image config) is not refused for being unscanned, since Trivy does not
// scan those; a finished scan that has findings still refuses it.

// imageConfigTypes are the config media types of a runnable image.
var imageConfigTypes = []string{
	"application/vnd.docker.container.image.v1+json",
	"application/vnd.oci.image.config.v1+json",
}

// indexMediaTypes are the media types of a manifest that lists per-platform
// manifests.
var indexMediaTypes = []string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
}

// pullThreshold is the threshold in force for one request and the repository
// that set it, which the refusal names.
type pullThreshold struct {
	severity string
	repo     string
}

// effectivePullThreshold combines repo's own threshold with the group's when
// the request arrives through one: the stricter of the two applies, so a group
// can tighten a member's gate and a member's gate still holds inside any group.
func (h *Handler) effectivePullThreshold(c *gin.Context, repo *domain.Repository) pullThreshold {
	t := pullThreshold{severity: domain.RepoPullBlockSeverity(repo), repo: repo.Name}
	groupName := c.GetString(formats.GroupRepoKey)
	if groupName == "" {
		return t
	}
	group, err := h.deps.Repos.Get(c.Request.Context(), groupName)
	if err != nil || group == nil {
		return t
	}
	gs := domain.RepoPullBlockSeverity(group)
	if gs != "" && domain.StricterPullSeverity(t.severity, gs) != t.severity {
		return pullThreshold{severity: gs, repo: group.Name}
	}
	return t
}

// pullGateAllows applies the pull gate to a manifest GET/HEAD. It answers the
// refusal itself and returns false, or returns true for the handler to serve.
func (h *Handler) pullGateAllows(c *gin.Context, repo *domain.Repository, imageName, reference string) bool {
	if repo == nil {
		return true
	}
	t := h.effectivePullThreshold(c, repo)
	if t.severity == "" {
		return true
	}
	ctx := c.Request.Context()
	if u := h.deps.ScanPullUser; u != "" && requestctx.Username(ctx) == u {
		return true
	}
	held, scan, image := h.manifestScan(ctx, repo.Name, imageName, reference)
	if !held {
		return true
	}
	v := domain.EvaluatePullGate(t.severity, repo.Type == domain.TypeHosted, scan)
	if !v.Blocked || (v.Unscanned && !image) {
		return true
	}
	policy := fmt.Sprintf("configured policy in repository %q: prevent images with vulnerability severity of %q or higher from running",
		t.repo, t.severity)
	var msg string
	if v.Unscanned {
		msg = fmt.Sprintf("current image %s has no finished vulnerability scan and cannot be pulled due to %s; scan the image first",
			imageRef(imageName, reference), policy)
	} else {
		msg = fmt.Sprintf("current image %s with %d %s %s cannot be pulled due to %s",
			imageRef(imageName, reference), v.Count, v.Severity, findingNoun(v.Severity, v.Count), policy)
	}
	if c.Request.Method == http.MethodHead {
		c.Status(http.StatusForbidden)
		return false
	}
	dockerError(c, http.StatusForbidden, "DENIED", msg)
	return false
}

// findingNoun names what a count of one severity is: malicious findings are
// package reports, not vulnerabilities.
func findingNoun(sev string, n int) string {
	switch {
	case sev == domain.ScanSeverityMalicious && n == 1:
		return "package report"
	case sev == domain.ScanSeverityMalicious:
		return "package reports"
	case n == 1:
		return "vulnerability"
	default:
		return "vulnerabilities"
	}
}

// imageRef spells imageName and reference the way a client names an image:
// name:tag, or name@digest.
func imageRef(imageName, reference string) string {
	if strings.Contains(reference, ":") {
		return imageName + "@" + reference
	}
	return imageName + ":" + reference
}

// manifestScan finds the scan that judges a pull of imageName:reference from
// repoName (see the comment at the top of this file for which one). held is
// false when the repository holds no such manifest; image is false when the
// manifest is a non-image artifact. scan is nil when nothing that applies was
// scanned.
func (h *Handler) manifestScan(ctx context.Context, repoName, imageName, reference string) (held bool, scan *domain.ScanResultRow, image bool) {
	asset, err := h.deps.Assets.GetByPath(ctx, repoName, manifestPath(imageName, reference))
	if err != nil || asset == nil {
		return false, nil, false
	}
	image = true
	if comp, cerr := h.deps.Components.Get(ctx, asset.ComponentID); cerr == nil && comp != nil {
		if at, _ := comp.Extra[extraArtifactTypeKey].(string); at != "" && !slices.Contains(imageConfigTypes, at) {
			image = false
		}
	}
	if !strings.HasPrefix(reference, "sha256:") {
		return true, h.newestScan(ctx, []scanCandidate{{asset.ComponentID, asset.SHA256}}), image
	}

	// One listing of the image's manifests answers both digest lookups.
	prefix := manifestPath(imageName, "")
	listed, err := h.deps.Assets.ListByRepoAndPath(ctx, repoName, prefix)
	if err != nil {
		listed = nil
	}
	var tags []domain.Asset
	for _, a := range listed {
		// A tag, not a digest alias; the prefix match also lists nested
		// image names, whose paths carry another "/".
		if ref := strings.TrimPrefix(a.Path, prefix); ref != "" && !strings.ContainsAny(ref, "/:") {
			tags = append(tags, a)
		}
	}
	hexSum := strings.TrimPrefix(reference, "sha256:")
	same := []scanCandidate{{asset.ComponentID, hexSum}}
	for _, a := range tags {
		if a.SHA256 == hexSum {
			same = append(same, scanCandidate{a.ComponentID, hexSum})
		}
	}
	if scan = h.newestScan(ctx, same); scan != nil {
		return true, scan, image
	}
	var parents []scanCandidate
	for i := range tags {
		a := &tags[i]
		if !slices.Contains(indexMediaTypes, a.ContentType) {
			continue
		}
		body, rerr := h.readAsset(ctx, a)
		if rerr != nil || len(body) > maxManifestBytes {
			continue
		}
		if slices.Contains(parseManifestRefs(body).manifests, reference) {
			// The index's scan is checked against the index, the
			// manifest its tag held when it was scanned.
			parents = append(parents, scanCandidate{a.ComponentID, a.SHA256})
		}
	}
	return true, h.newestScan(ctx, parents), image
}

// scanCandidate is a component whose latest scan may judge a pull, and the
// hex sha256 of the manifest that scan has to have looked at.
type scanCandidate struct {
	componentID string
	sha256      string
}

// newestScan returns the most recent applicable scan of any candidate, or nil.
//
// A scan that records the manifest it looked at applies only while that is
// still the candidate's manifest: a tag re-pushed after its scan would
// otherwise be served on the strength of a scan of other content until the
// re-scan lands — indefinitely with scan-on-upload off or its queue full. Such
// a scan counts as none, so a hosted repository refuses the image as
// unscanned. A row that records no manifest predates the check and is trusted
// as before, so an existing install does not start refusing everything.
func (h *Handler) newestScan(ctx context.Context, candidates []scanCandidate) *domain.ScanResultRow {
	if h.deps.ScanResults == nil {
		return nil
	}
	var newest *domain.ScanResultRow
	for _, c := range candidates {
		s, err := h.deps.ScanResults.GetLatestByComponent(ctx, c.componentID)
		if err != nil || s == nil {
			continue
		}
		if got := s.ScannedManifestSHA256(); got != "" && got != c.sha256 {
			continue
		}
		if newest == nil || s.ScannedAt.After(newest.ScannedAt) {
			newest = s
		}
	}
	return newest
}
