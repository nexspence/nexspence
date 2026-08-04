package oci

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// The referrers index is merged across group members rather than taken from the
// first member that answers. GET /v2/{name}/referrers/{digest} never returns
// 404 — an unknown subject is a 200 carrying an empty index, because that is
// what a client reads as "no signatures" — so the group's first-non-404 fan-out
// would let member one's empty index shadow every member behind it. A signature
// pushed to the second member would be reported as absent, which is the one
// direction this endpoint must never fail in.

// GroupIndexSourcePath implements formats.GroupIndexMerger. A referrers request
// is its own source: every member is asked the same question. Nothing else under
// /v2/ is merged — a manifest or a blob is one artifact, and the first member
// holding it is the right answer.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	rest := strings.TrimPrefix(normPath(p), "/v2/")
	if rest == normPath(p) {
		return "", false
	}
	if referrersIndex(strings.Split(rest, "/")) < 0 {
		return "", false
	}
	return p, true
}

// MergeGroupIndex implements formats.GroupIndexMerger: one index carrying every
// member's descriptors, in member order, each manifest named once.
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
func (h *Handler) MergeGroupIndex(_, _ string, parts []formats.GroupIndexPart) ([]byte, string, error) {
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

// GroupIndexMemberFailureIsFatal implements formats.GroupIndexStrictMerger.
//
// A member that answered anything but 2xx or 404 could not tell us what it holds
// — a proxy whose upstream was unreachable, rate-limited or refused its
// credentials answers 502 for exactly that reason. Merging the remaining members
// and calling the result complete would convert "I could not check" into "this
// subject has no referrers", which is what a policy gate reads as an unsigned
// image. 404 is the one non-2xx that says something about the subject rather
// than about the member, and it contributes nothing.
func (h *Handler) GroupIndexMemberFailureIsFatal(p string, status int) bool {
	if _, ok := h.GroupIndexSourcePath(p); !ok {
		return false
	}
	return status != http.StatusNotFound
}
