package oci

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// Three OCI documents are merged across group members rather than taken from the
// first member that answers: the referrers index, the tag list and the catalog.
// All three are aggregations, and all three answer "I hold nothing" with a 200
// carrying an empty list rather than a 404 — so the group's first-non-404 fan-out
// would let member one's empty document shadow every member behind it. A
// signature pushed to the second member would be reported as absent, a tag
// promoted into the second member as unpublished, and an image that lives only
// in the second member as not present in the registry at all.
//
// Nothing else under /v2/ is merged. A manifest or a blob is one artifact, and
// the first member holding it is the right answer.

// ociIndexKind is which of the merged documents a request path asks for.
type ociIndexKind int

const (
	indexNone ociIndexKind = iota
	indexReferrers
	indexTags
	indexCatalog
)

// paginated reports whether the document takes the spec's ?n=/?last= arguments.
// The referrers index does not: it is filtered, not paged.
func (k ociIndexKind) paginated() bool { return k == indexTags || k == indexCatalog }

// jsonListContentType is what gin's c.JSON writes, which is what a client gets
// from a single repository. The merged document keeps the same content type so a
// group is indistinguishable from a hosted repository on the wire.
const jsonListContentType = "application/json; charset=utf-8"

// groupIndexKind classifies a request path, returning the image name for a tag
// list. The classification mirrors ServeHTTP's routing exactly — a path is
// mergeable as the document the member handler would have produced for it, never
// as some other one.
func groupIndexKind(p string) (kind ociIndexKind, imageName string) {
	norm := normPath(p)
	rest := strings.TrimPrefix(norm, "/v2/")
	if rest == norm {
		return indexNone, ""
	}
	// Matched whole, exactly as ServeHTTP matches it: "_catalog" is not a legal
	// image name, so no image is shadowed, and an image name merely ending in
	// "_catalog" still has its own endpoint segment behind it.
	if rest == "_catalog" {
		return indexCatalog, ""
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return indexNone, ""
	}
	switch {
	case endsWithSegments(parts, "tags", "list"):
		return indexTags, strings.Join(parts[:len(parts)-2], "/")
	case referrersIndex(parts) >= 0:
		return indexReferrers, ""
	}
	return indexNone, ""
}

// GroupIndexSourcePath implements formats.GroupIndexMerger. Each of the merged
// documents is its own source: every member is asked the same question.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if kind, _ := groupIndexKind(p); kind == indexNone {
		return "", false
	}
	return p, true
}

// MergeGroupIndex implements formats.GroupIndexMerger, dispatching on the shape
// of the document the path asks for.
func (h *Handler) MergeGroupIndex(_, p string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	switch kind, imageName := groupIndexKind(p); kind {
	case indexReferrers:
		return mergeReferrers(parts)
	case indexTags:
		return mergeTagLists(imageName, parts)
	case indexCatalog:
		return mergeCatalogs(parts)
	default:
		return nil, "", fmt.Errorf("path %q is not a mergeable OCI index", p)
	}
}

// mergeReferrers builds one index carrying every member's descriptors, in member
// order, each manifest named once.
//
// Descriptors are carried across as the raw JSON the member produced instead of
// being decoded into this package's descriptor struct and re-encoded, which
// would drop the spec fields it does not model — platform, urls, subject,
// per-descriptor annotations this package does not read. Only the digest is
// decoded, because that is the key a duplicate is recognized by: the same
// manifest legitimately exists in two members, and it is the same referrer in
// both. Anything else — the member it came from, its position, its size — would
// either split one referrer into two entries or fuse two into one.
//
// The client's OCI-Filters-Applied is deliberately not reproduced here. Each
// member applied the artifactType filter to its own answer, but a proxy member
// forwards the filter to an upstream that may ignore it, so the group cannot
// promise the merged list is filtered. Leaving the header off is the honest
// reading: a client that wanted the filter re-applies it to a superset and lands
// on the same set.
func mergeReferrers(parts []formats.GroupIndexPart) ([]byte, string, error) {
	merged := make([]json.RawMessage, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))

	for _, part := range parts {
		var idx struct {
			Manifests []json.RawMessage `json:"manifests"`
		}
		if err := json.Unmarshal(part.Body, &idx); err != nil {
			// A member's answer we cannot read is a member whose referrers are
			// missing from the result. Reported, never skipped: a short index is
			// what a signature checker reads as "unsigned".
			return nil, "", fmt.Errorf("member %q did not answer the referrers request with an image index: %w",
				part.Member, err)
		}
		for _, raw := range idx.Manifests {
			var desc struct {
				Digest string `json:"digest"`
			}
			if err := json.Unmarshal(raw, &desc); err != nil || desc.Digest == "" {
				return nil, "", fmt.Errorf(
					"member %q listed a referrer with no readable digest, so its referrers cannot be merged",
					part.Member)
			}
			// Member order is priority: the first member to name a manifest is
			// the one whose descriptor is kept.
			if _, dup := seen[desc.Digest]; dup {
				continue
			}
			seen[desc.Digest] = struct{}{}
			merged = append(merged, raw)
		}
	}

	// Manifests is non-nil so an empty result serializes as [] and not null.
	body, err := json.Marshal(struct {
		SchemaVersion int               `json:"schemaVersion"`
		MediaType     string            `json:"mediaType"`
		Manifests     []json.RawMessage `json:"manifests"`
	}{SchemaVersion: 2, MediaType: mediaTypeImageIndex, Manifests: merged})
	if err != nil {
		return nil, "", err
	}
	return body, mediaTypeImageIndex, nil
}

// mergeTagLists builds the union of the members' tags for one image.
//
// Member order is not priority here and cannot be: a tag is a name, not a
// document, so the same tag in two members contributes one entry and there is
// nothing to choose between them. Which manifest that tag resolves to IS a
// choice, and it stays where it belongs — a manifest request is not merged, so
// the earlier member still wins the pull.
//
// The name is taken from the requested path rather than from a member's answer:
// it is the name the client addressed, and it is the name every later request
// against this group has to use.
func mergeTagLists(imageName string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	tags, err := mergeStringLists(parts, "tag", func(doc *mergedLists) []string { return doc.Tags })
	if err != nil {
		return nil, "", err
	}
	body, err := json.Marshal(struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}{Name: imageName, Tags: tags})
	if err != nil {
		return nil, "", err
	}
	return body, jsonListContentType, nil
}

// mergeCatalogs builds the union of the image names the members hold.
//
// An image in two members is one image name: the client pulls it from the group,
// and the group resolves the manifest through its usual first-member fan-out. A
// name listed twice would also break the ?last= cursor, which assumes a strictly
// ascending list.
//
// The catalog of a group is deliberately the catalog of its MEMBERS' images and
// not the member repository names. Each Nexspence repository is its own registry
// namespace, so a client that connected to the group addresses
// <group>/<image> — the member it happens to live in is never part of a
// pullable name, and listing member names would hand the client names it cannot
// pull from where it is.
func mergeCatalogs(parts []formats.GroupIndexPart) ([]byte, string, error) {
	repos, err := mergeStringLists(parts, "repository", func(doc *mergedLists) []string { return doc.Repositories })
	if err != nil {
		return nil, "", err
	}
	body, err := json.Marshal(struct {
		Repositories []string `json:"repositories"`
	}{Repositories: repos})
	if err != nil {
		return nil, "", err
	}
	return body, jsonListContentType, nil
}

// mergedLists is the shape of the merged list documents; each merge reads its
// own field out of it.
type mergedLists struct {
	Name         string   `json:"name"`
	Tags         []string `json:"tags"`
	Repositories []string `json:"repositories"`
}

// mergeStringLists unions one field of every member's document, sorted and
// deduplicated. The result is never nil, so an empty merge serializes as [] and
// not null — a null breaks clients that range over the list.
func mergeStringLists(parts []formats.GroupIndexPart, kind string, pick func(*mergedLists) []string) ([]string, error) {
	merged := []string{}
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		var doc mergedLists
		if err := json.Unmarshal(part.Body, &doc); err != nil {
			// Same rule as the referrers merge: a member whose answer cannot be
			// read is a member missing from the result, and a list one entry
			// short is indistinguishable from a complete one.
			return nil, fmt.Errorf("member %q did not answer with a %s list: %w", part.Member, kind, err)
		}
		for _, entry := range pick(&doc) {
			if _, dup := seen[entry]; dup {
				continue
			}
			seen[entry] = struct{}{}
			merged = append(merged, entry)
		}
	}
	sort.Strings(merged)
	return merged, nil
}

// GroupIndexMemberFailureIsFatal implements formats.GroupIndexStrictMerger.
//
// A member that answered anything but 2xx or 404 could not tell us what it holds
// — a proxy whose upstream was unreachable, rate-limited or refused its
// credentials answers 502 for exactly that reason. Merging the remaining members
// and calling the result complete would convert "I could not check" into a
// statement about the content: no referrers, so the image is unsigned; no such
// tag, so that version was never published; no such image name, so nothing is
// there to keep. Signature gates, retention jobs and mirroring jobs act on all
// three, and a list one entry short reads exactly like a complete one.
//
// This is affordable because none of the three endpoints consults an upstream: a
// proxy member answers all of them from what it has cached, so a non-2xx here is
// a local fault rather than the ordinary flakiness of somebody else's registry. 404
// stays the one non-2xx that says something about the request rather than about
// the member, and it contributes nothing.
func (h *Handler) GroupIndexMemberFailureIsFatal(p string, status int) bool {
	if _, ok := h.GroupIndexSourcePath(p); !ok {
		return false
	}
	return status != http.StatusNotFound
}

// GroupIndexMemberQuery implements formats.GroupIndexPaginator: members are
// asked for their complete lists, and the group pages the merge.
//
// Only the paging arguments are dropped. The referrers index is filtered rather
// than paged, and its artifactType must reach the members — a filter narrows
// what a member answers without putting anything the merge needs out of reach.
func (h *Handler) GroupIndexMemberQuery(p, clientQuery string) string {
	if kind, _ := groupIndexKind(p); !kind.paginated() {
		return clientQuery
	}
	q, err := url.ParseQuery(clientQuery)
	if err != nil {
		// A query we cannot parse is one we cannot strip the paging arguments
		// out of, and asking members for a page is the one thing this function
		// exists to prevent.
		return ""
	}
	q.Del("n")
	q.Del("last")
	return q.Encode()
}

// PageGroupIndex implements formats.GroupIndexPaginator: the client's ?n=/?last=
// are applied to the merged list, and the Link header is built from the group's
// own URL, so the cursor a client sends back names an entry of the list it was
// actually served.
func (h *Handler) PageGroupIndex(c *gin.Context, p string, merged []byte) ([]byte, error) {
	kind, _ := groupIndexKind(p)
	if !kind.paginated() {
		return merged, nil
	}
	var doc mergedLists
	if err := json.Unmarshal(merged, &doc); err != nil {
		return nil, fmt.Errorf("the merged list could not be paginated: %w", err)
	}
	if kind == indexCatalog {
		return json.Marshal(struct {
			Repositories []string `json:"repositories"`
		}{Repositories: pageMergedList(c, doc.Repositories)})
	}
	return json.Marshal(struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}{Name: doc.Name, Tags: pageMergedList(c, doc.Tags)})
}

// pageMergedList cuts the requested page out of a merged list and sets the Link
// header for a truncated one, exactly as the single-repository endpoints do —
// the page is a page of the same sorted list either way.
func pageMergedList(c *gin.Context, entries []string) []string {
	params := ParsePageParams(c)
	page, more := Paginate(entries, params)
	SetNextLink(c, params, page, more)
	if page == nil {
		page = []string{}
	}
	return page
}
