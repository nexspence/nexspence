package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// A signature, SBOM or attestation is linked to the image it describes only by
// the subject digest inside its own manifest — nothing points the other way. The
// browse tree therefore lists them as unrelated siblings under "Manifests", and
// an image with three referrers looks like four unconnected artifacts (#199).
// This endpoint answers the other direction, so the component panel can show
// what refers to the manifest being looked at.
//
// It is the browse-facing counterpart of /v2/{name}/referrers/{digest}: same
// data, shaped for a UI (component id to navigate to, the same friendly label
// the tree computes) instead of for the OCI protocol.

// ociReferrer is one artifact that names the selected manifest as its subject.
type ociReferrer struct {
	ComponentID string `json:"componentId"`
	// Reference is how the referrer is addressed in this repository: a digest
	// for the usual attach, a tag when it was pushed as one.
	Reference string `json:"reference"`
	// Digest is the referrer manifest's own digest, empty when its manifest
	// asset is missing (a component whose bytes were deleted under it).
	Digest string `json:"digest,omitempty"`
	// ArtifactType is the friendly label — "sbom", "signature" — and RawType the
	// media type it was derived from, so the panel can show what the registry
	// actually stored when the label is a simplification of it.
	ArtifactType string `json:"artifactType,omitempty"`
	RawType      string `json:"rawType,omitempty"`
	MediaType    string `json:"mediaType,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

// OCIReferrers handles GET /api/v1/browse/repositories/:name/oci-referrers
// Query params: image (the /v2/{name} path), reference (the subject manifest,
// by digest or by tag).
func (h *BrowseHandler) OCIReferrers(c *gin.Context) {
	repoName := c.Param("name")
	image := c.Query("image")
	reference := c.Query("reference")
	ctx := c.Request.Context()

	if image == "" || reference == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "image and reference query params required"})
		return
	}

	repo, err := h.deps.Repos.Get(ctx, repoName)
	if err != nil || repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}
	if !repo.Format.IsOCIRegistry() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repository is not an OCI registry format"})
		return
	}

	// A referrer names its subject by digest only, but the panel is usually
	// opened on a tag. Resolving the tag here — through the same manifest asset
	// the registry serves — is what lets "what is attached to this tag?" be
	// answerable at all; an unresolvable reference has no referrers to list,
	// which is not the same as an image that has none.
	subjectDigest, resolved := h.subjectDigestOf(ctx, repoName, image, reference)
	if !resolved {
		c.JSON(http.StatusNotFound, gin.H{"error": "manifest not found for reference " + reference})
		return
	}

	repoNames := []string{repoName}
	if repo.Type == domain.TypeGroup {
		repoNames = domain.GroupMemberNames(repo)
	}

	// source tells the panel how complete this list can be. A proxy only ever
	// knows the referrers that were pulled through it, and presenting its cache
	// as the whole set would read as "this image has no signature" when the
	// truth is "we have not fetched one". The protocol endpoint goes upstream
	// for exactly this reason; a browse panel says so instead of pretending.
	source := "local"
	if repo.Type == domain.TypeProxy {
		source = "cache"
	}

	referrers := make([]ociReferrer, 0)
	if len(repoNames) > 0 {
		comps, lerr := h.deps.Components.ListOCIReferrers(ctx, repoNames, image, subjectDigest)
		if lerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": lerr.Error()})
			return
		}
		userID, _ := c.Get("userID")
		roles, _ := c.Get("roles")
		referrers = h.referrersOf(ctx, repoName, comps,
			stringVal(userID), stringSliceVal(roles), repo.AllowAnonymous)
	}

	c.JSON(http.StatusOK, gin.H{
		"repository": repoName,
		"image":      image,
		"subject":    subjectDigest,
		"source":     source,
		"referrers":  referrers,
	})
}

// subjectDigestOf resolves the reference the panel was opened on into the
// digest referrers actually point at. A digest reference is already that;
// a tag is resolved through its manifest asset. ok is false when a tag names no
// manifest we hold, so the caller can say "no such manifest" instead of
// answering "no referrers" about something that is not there.
func (h *BrowseHandler) subjectDigestOf(ctx context.Context, repoName, image, reference string) (string, bool) {
	if strings.Contains(reference, ":") {
		return reference, true
	}
	asset, err := h.deps.Assets.GetByPath(ctx, repoName, ociManifestPath(image, reference))
	if err != nil || asset == nil || asset.SHA256 == "" {
		return "", false
	}
	return "sha256:" + asset.SHA256, true
}

// referrersOf turns referrer components into panel rows: RBAC-filtered through
// the same content selectors the tree uses, de-duplicated by manifest digest,
// and labeled by the same rules the tree labels leaves with.
func (h *BrowseHandler) referrersOf(ctx context.Context, repoName string, comps []domain.Component,
	userID string, roles []string, allowAnonymous bool,
) []ociReferrer {
	rows := make([]domain.DockerBrowseRow, 0, len(comps))
	byComponent := make(map[string]domain.Component, len(comps))
	for _, comp := range comps {
		rows = append(rows, domain.DockerBrowseRow{
			ComponentID: comp.ID,
			ImageName:   comp.Name,
			Version:     comp.Version,
			SamplePath:  ociManifestPath(comp.Name, comp.Version),
		})
		byComponent[comp.ID] = comp
	}
	rows = h.rbac.FilterDockerRows(ctx, userID, roles, repoName, allowAnonymous, rows)

	out := make([]ociReferrer, 0, len(rows))
	// A referrer pushed by tag exists twice — as the tag and as its digest
	// alias — and both name the same manifest. The panel lists the manifest,
	// not the number of ways to address it.
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		comp, ok := byComponent[row.ComponentID]
		if !ok {
			continue
		}
		ref := h.referrerOf(ctx, repoName, comp)
		if ref.Digest != "" {
			if _, dup := seen[ref.Digest]; dup {
				continue
			}
			seen[ref.Digest] = struct{}{}
		}
		out = append(out, ref)
	}
	return out
}

// referrerOf reads one component's manifest metadata into a panel row. The
// digest and size come from the manifest asset, which is where the registry
// records what was actually stored; a component whose asset is gone still
// appears, without them, rather than vanishing from a list whose whole job is
// to show what is attached.
func (h *BrowseHandler) referrerOf(ctx context.Context, repoName string, comp domain.Component) ociReferrer {
	rawType, _ := comp.Extra["oci_artifact_type"].(string)
	mediaType, _ := comp.Extra["oci_media_type"].(string)
	ref := ociReferrer{
		ComponentID:  comp.ID,
		Reference:    comp.Version,
		RawType:      rawType,
		MediaType:    mediaType,
		ArtifactType: ociArtifactLabel(rawType, ociPredicateAnnotation(comp)),
	}
	asset, err := h.deps.Assets.GetByPath(ctx, repoName, ociManifestPath(comp.Name, comp.Version))
	if err == nil && asset != nil {
		if asset.SHA256 != "" {
			ref.Digest = "sha256:" + asset.SHA256
		}
		ref.Size = asset.SizeBytes
		if ref.MediaType == "" {
			ref.MediaType = asset.ContentType
		}
	}
	return ref
}

// ociPredicateAnnotation reads the in-toto predicate a cosign attestation
// records on its manifest, which is the only place the payload's real type
// appears when the artifact type names the DSSE envelope instead.
func ociPredicateAnnotation(comp domain.Component) string {
	raw, _ := comp.Extra["oci_annotations"].(map[string]any)
	if len(raw) == 0 {
		return ""
	}
	predicate, _ := raw["dev.sigstore.bundle.predicateType"].(string)
	return predicate
}

// ociManifestPath mirrors the OCI handler's manifest storage path. Browse reads
// the same assets the registry wrote, so it has to address them the same way.
func ociManifestPath(imageName, reference string) string {
	return "/manifests/" + imageName + "/" + reference
}
