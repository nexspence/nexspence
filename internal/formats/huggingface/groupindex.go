package huggingface

// Group index merging: a repository's metadata document and its tree listing
// are aggregated views, so first-non-404 fan-out would show one member's file
// list and hide every other member's — the same shadowing #99's merger exists
// to prevent for apt/yum/CRAN indexes.
//
// Like CRAN, and unlike apt, the Hub has no index document that describes other
// index documents (no cross-file checksums to keep consistent), so this
// implements formats.GroupIndexMerger only — not GroupIndexDependentMerger.
//
// The tree endpoint is not paginated here (see maxRepoFiles), and its
// "recursive" parameter is a filter rather than a cursor — a member asked with
// it still answers completely for what it holds — so there is no
// GroupIndexPaginator either: the client's query reaches members unchanged.

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// errNotMetadata is returned when no member answered with something that can be
// read as a Hub metadata document.
var errNotMetadata = errors.New("huggingface: no group member returned a repository metadata document")

// GroupIndexSourcePath implements formats.GroupIndexMerger. Every metadata path
// is merged from the members' answers to that same path; the resolve/ paths are
// artifacts and keep the default first-non-404 fan-out.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if _, _, _, _, ok := parseAPIPath(normPath(p)); ok {
		return p, true
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger.
//
// One limitation is worth naming: the merged metadata document reports the first
// contributing member's commit sha, because there is no commit that spans two
// independent repositories. A client that resolves a whole snapshot by that sha
// (huggingface_hub's snapshot_download) therefore only reaches the files of the
// member that minted it; downloading by branch or tag
// (hf_hub_download(revision="main"), the common case) fans out across every
// member as usual.
func (h *Handler) MergeGroupIndex(_, p string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	if _, kind, _, _, ok := parseAPIPath(normPath(p)); ok && kind == kindTree {
		return mergeTree(parts)
	}
	return mergeInfo(parts)
}

// mergeTree unions the members' tree entries, deduped by path — member order is
// priority, so the first member that lists a path is the one whose entry (and
// therefore whose size and oid) is kept, matching which member's file the
// artifact fan-out would then serve.
func mergeTree(parts []formats.GroupIndexPart) ([]byte, string, error) {
	merged := []treeEntry{}
	seen := map[string]bool{}
	for _, part := range parts {
		entries, err := decodeTree(part.Body)
		if err != nil {
			continue // a member that answered something else contributes nothing
		}
		for _, e := range entries {
			if e.Path == "" || seen[e.Path] {
				continue
			}
			seen[e.Path] = true
			merged = append(merged, e)
		}
	}
	body, err := json.Marshal(merged)
	if err != nil {
		return nil, "", err
	}
	return body, "application/json; charset=utf-8", nil
}

// mergeInfo unions the members' file lists into the first member's document,
// deduped by rfilename.
func mergeInfo(parts []formats.GroupIndexPart) ([]byte, string, error) {
	var merged map[string]any
	siblings := []repoSibling{}
	seen := map[string]bool{}

	for _, part := range parts {
		var doc map[string]any
		if err := json.Unmarshal(part.Body, &doc); err != nil {
			continue
		}
		if merged == nil {
			merged = doc
		}
		for _, s := range docSiblings(doc) {
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			siblings = append(siblings, repoSibling{RFilename: s})
		}
	}
	if merged == nil {
		// Every member answered 2xx with something that is not a Hub metadata
		// document. Reporting that is better than inventing an empty repository:
		// a client would read one as "this model has no files".
		return nil, "", errNotMetadata
	}

	merged["siblings"] = siblings
	body, err := json.Marshal(merged)
	if err != nil {
		return nil, "", err
	}
	return body, "application/json; charset=utf-8", nil
}

// docSiblings reads the rfilenames out of a metadata document, tolerating a
// member that answered without a siblings array at all.
func docSiblings(doc map[string]any) []string {
	raw, ok := doc["siblings"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := entry["rfilename"].(string); ok {
			out = append(out, strings.TrimPrefix(name, "/"))
		}
	}
	return out
}
