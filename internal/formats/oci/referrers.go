package oci

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// mediaTypeImageIndex is the media type of the referrers response document.
const mediaTypeImageIndex = "application/vnd.oci.image.index.v1+json"

// descriptor is one entry of the referrers image index.
type descriptor struct {
	MediaType    string            `json:"mediaType"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// imageIndex is the referrers response document.
type imageIndex struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Manifests     []descriptor `json:"manifests"`
}

// handleReferrers answers GET /v2/{name}/referrers/{digest} with an image index
// of every manifest whose subject is that digest. An unknown digest yields an
// empty index rather than a 404: clients read "no referrers" from the empty
// list, and a 404 would read as "this registry has no referrers API".
func (h *Handler) handleReferrers(c *gin.Context, repoName, imageName, subjectDigest string) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	ctx := c.Request.Context()

	// Non-nil so an empty result serializes as [] and not null: a null breaks
	// clients that range over the list.
	index := imageIndex{SchemaVersion: 2, MediaType: mediaTypeImageIndex, Manifests: []descriptor{}}
	filter := c.Query("artifactType")

	comps, err := h.deps.Components.ListOCIReferrers(ctx, []string{repoName}, imageName, subjectDigest)
	if err != nil {
		dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}

	seen := make(map[string]struct{}, len(comps))
	for _, comp := range comps {
		desc, ok := h.descriptorOf(ctx, repoName, imageName, comp)
		if !ok {
			continue
		}
		// A referrer pushed by tag has both a tag component and a digest-alias
		// component; both name the same manifest, so list it once. Recorded
		// before the filter runs, so which copy is seen first cannot decide
		// whether the other one slips through.
		if _, dup := seen[desc.Digest]; dup {
			continue
		}
		seen[desc.Digest] = struct{}{}
		if filter != "" && desc.ArtifactType != filter {
			continue
		}
		index.Manifests = append(index.Manifests, desc)
	}

	if filter != "" {
		c.Header("OCI-Filters-Applied", "artifactType")
	}
	// Set before c.JSON, which only fills in application/json when no
	// Content-Type is present yet.
	c.Header("Content-Type", mediaTypeImageIndex)
	c.JSON(http.StatusOK, index)
}

// descriptorOf renders one referring component as an index entry. The digest and
// size come from the stored manifest asset, the rest from the metadata phase 1
// recorded on the component.
func (h *Handler) descriptorOf(ctx context.Context, repoName, imageName string, comp domain.Component) (descriptor, bool) {
	asset, err := h.deps.Assets.GetByPath(ctx, repoName, manifestPath(imageName, comp.Version))
	if err != nil || asset == nil || asset.SHA256 == "" {
		return descriptor{}, false
	}
	mediaType, _ := comp.Extra[extraMediaTypeKey].(string)
	if mediaType == "" {
		mediaType = asset.ContentType
	}
	artifactType, _ := comp.Extra[extraArtifactTypeKey].(string)
	return descriptor{
		MediaType:    mediaType,
		Digest:       "sha256:" + asset.SHA256,
		Size:         asset.SizeBytes,
		ArtifactType: artifactType,
		Annotations:  annotationsOf(comp),
	}, true
}

// annotationsOf converts the stored annotation map back to string values.
func annotationsOf(comp domain.Component) map[string]string {
	raw, _ := comp.Extra[extraAnnotationsKey].(map[string]any)
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// referrersIndex reports where the "referrers" keyword sits in a split path, or
// -1 when the path is not a referrers request. The keyword is only recognized as
// the second-to-last segment — the spec's shape is {name}/referrers/{digest} and
// a digest is always exactly one segment. Matching it anywhere (as the manifests
// and blobs cases do) would let an image legitimately named ".../referrers"
// swallow its own manifest and blob requests.
func referrersIndex(parts []string) int {
	idx := len(parts) - 2
	if idx < 1 || parts[idx] != "referrers" {
		return -1
	}
	return idx
}
