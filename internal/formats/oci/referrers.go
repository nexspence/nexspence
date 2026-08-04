package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/repository"
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

	// A proxy repository answers from upstream. Its cache holds only the
	// referring manifests that happen to have been pulled through it, so the
	// local query would under-report — and an under-reported index reads to a
	// signature checker as "this image is unsigned".
	//
	// A lookup that fails is therefore not the same as one that finds nothing:
	// swallowing the error would leave repo nil, skip the proxy branch and answer
	// a proxy's request from its cache without ever touching the network. Only a
	// genuine absence falls through to the local query.
	repo, err := h.deps.Repos.Get(ctx, repoName)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		dockerError(c, http.StatusBadGateway, "UNKNOWN",
			fmt.Sprintf("could not determine whether repository %q is a proxy, so its referrers cannot be listed: %v",
				repoName, err))
		return
	}
	if repo != nil && repo.Type == domain.TypeProxy {
		h.proxyReferrers(c, repo, imageName, subjectDigest)
		return
	}

	index := newIndex()
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

// proxyReferrers forwards the request upstream. An empty index is reserved for
// the answers that genuinely mean "this registry holds no referrers information
// for that subject" — 404, 405 and 501, i.e. an upstream with no referrers API.
// Every other outcome, from an unreachable host to a 500 to a 200 whose body is
// not an index, is a failure to look, and a failure to look must never be
// reported as an absence: a policy gate reads an empty index as "unsigned".
func (h *Handler) proxyReferrers(c *gin.Context, repo *domain.Repository, imageName, subjectDigest string) {
	upPath := "/v2/" + imageName + "/referrers/" + subjectDigest
	hdr := http.Header{"Accept": []string{mediaTypeImageIndex}}
	// The query goes as its own argument: JoinURL escapes everything it is given
	// as a path, so a "?" glued on would reach upstream as %3F and the
	// artifactType filter would silently never be applied.
	resp, err := repoproxy.FetchUpstreamOnce(c.Request.Context(), repo, upPath, c.Request.URL.RawQuery, hdr)
	if err != nil {
		// No response at all: DNS failure, refused connection, TLS error, timeout,
		// the SSRF guard, or a proxy_config that cannot even produce a URL. The
		// upstream URL is unknown here, so the record carries the path alone.
		h.upstreamUnusable(c, repo, upPath, "", "UNKNOWN",
			fmt.Sprintf("could not reach the upstream registry to list referrers: %v", err))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		// The upstream has no referrers API. That is a fact about the registry
		// which a client can act on, and the only one that justifies an empty
		// index: we did reach the registry and it has nothing to tell us.
		emptyIndex(c)
		return
	case http.StatusUnauthorized, http.StatusForbidden:
		// "I could not check" is not "there is nothing to check". Collapsing an
		// upstream refusal into an empty index would let a policy gate read a
		// proxy's missing or rejected credentials as proof that an image is
		// unsigned — the one direction this endpoint must never fail in.
		h.upstreamRefused(c, repo, upPath, upstreamURLOf(resp), resp.StatusCode)
		return
	default:
		code := "UNKNOWN"
		if resp.StatusCode == http.StatusTooManyRequests {
			code = "TOOMANYREQUESTS"
		}
		h.upstreamUnusable(c, repo, upPath, upstreamURLOf(resp), code, fmt.Sprintf(
			"upstream registry answered the referrers request with %d %s: proxy repository %q could not list "+
				"referrers, so this is an upstream failure and not an absence of referrers",
			resp.StatusCode, http.StatusText(resp.StatusCode), repo.Name))
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReferrersBytes+1))
	if err != nil {
		// A body that died mid-read is a truncated list, which can only ever
		// under-report — the same direction as the size cap below.
		h.upstreamUnusable(c, repo, upPath, upstreamURLOf(resp), "UNKNOWN", fmt.Sprintf(
			"upstream referrers index for %q could not be read whole: %v", repo.Name, err))
		return
	}
	if len(body) > maxReferrersBytes {
		// A list too large to read whole can only ever under-report referrers, so
		// it is an error for the same reason a refusal is.
		h.upstreamUnusable(c, repo, upPath, upstreamURLOf(resp), "TOOBIG", fmt.Sprintf(
			"upstream referrers index for %q exceeds the %d byte limit and cannot be served complete",
			repo.Name, maxReferrersBytes))
		return
	}
	if !looksLikeIndex(body) {
		// A 200 carrying an error page or a captive portal's HTML means we never
		// reached a registry, so we learned nothing about this subject.
		h.upstreamUnusable(c, repo, upPath, upstreamURLOf(resp), "UNKNOWN", fmt.Sprintf(
			"upstream answered the referrers request for %q with 200 but the body is not an image index, "+
				"so no registry was reached", repo.Name))
		return
	}
	if applied := resp.Header.Get("OCI-Filters-Applied"); applied != "" {
		c.Header("OCI-Filters-Applied", applied)
	}
	c.Data(http.StatusOK, mediaTypeImageIndex, body)
}

// upstreamRefused relays a 401/403 from upstream as a 502 in the OCI error shape.
func (h *Handler) upstreamRefused(c *gin.Context, repo *domain.Repository, upPath, upstream string, status int) {
	code := "UNAUTHORIZED"
	if status == http.StatusForbidden {
		code = "DENIED"
	}
	h.upstreamUnusable(c, repo, upPath, upstream, code, fmt.Sprintf(
		"upstream registry refused the referrers request with %d %s: proxy repository %q could not list referrers, "+
			"so this is a credentials problem on the proxy and not an absence of referrers",
		status, http.StatusText(status), repo.Name))
}

// upstreamUnusable answers 502 in the OCI error shape and records the failure on
// the proxy-error bus. formats.Deps carries no logger — the webhook bus is the
// reporting channel repoproxy already uses for a failed upstream fetch, so an
// operator sees this the same way.
//
// upPath is the upstream request path, which is what the other DispatchProxyError
// callers pass as well: repoproxy's own cache paths are repository-relative and
// are the path it asked upstream for.
func (h *Handler) upstreamUnusable(c *gin.Context, repo *domain.Repository, upPath, upstream, code, msg string) {
	repoproxy.DispatchProxyError(h.deps, repo.Name, upPath, upstream, errors.New(msg))
	dockerError(c, http.StatusBadGateway, code, msg)
}

// upstreamURLOf reports the URL actually requested, for the error record. The
// client fills in Response.Request, but a nil guard keeps a doubled transport
// from turning a reporting path into a panic.
func upstreamURLOf(resp *http.Response) string {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return ""
	}
	return resp.Request.URL.String()
}

// looksLikeIndex reports whether the upstream body is at least shaped like an
// image index. The body is passed through byte for byte rather than re-encoded —
// re-encoding through the local structs would drop spec fields this package does
// not model (platform, urls, subject, top-level annotations) and corrupt valid
// answers. So the check is deliberately shallow: it exists to stop a captive
// portal's HTML, or an error document served with a 200, from being handed to a
// client under an image-index content type.
func looksLikeIndex(body []byte) bool {
	var probe struct {
		Manifests []json.RawMessage `json:"manifests"`
	}
	return json.Unmarshal(body, &probe) == nil && probe.Manifests != nil
}

// newIndex is a referrers index with no entries. Manifests is non-nil so an
// empty result serializes as [] and not null: a null breaks clients that range
// over the list.
func newIndex() imageIndex {
	return imageIndex{SchemaVersion: 2, MediaType: mediaTypeImageIndex, Manifests: []descriptor{}}
}

// emptyIndex answers with a well-formed index carrying no referrers.
func emptyIndex(c *gin.Context) {
	// Set before c.JSON, which only fills in application/json when no
	// Content-Type is present yet.
	c.Header("Content-Type", mediaTypeImageIndex)
	c.JSON(http.StatusOK, newIndex())
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
