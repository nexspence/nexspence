package pypi

// Group index merging (#99): PEP 503 simple pages are merged across group
// members. Hosted pypi members answer 200 with an empty link list for unknown
// packages, which under first-non-404 fan-out shadowed every member behind
// them; merging makes the empty page just an anchor-less contribution.

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// anchorRe extracts <a ...>text</a> links from PEP 503 pages
// (machine-generated anchor lists, so regex extraction is safe), keeping the
// whole attribute list: PEP 503/592/658/714 attributes such as
// data-requires-python, data-yanked and data-core-metadata live there, and
// pip depends on them (#661). anchorHrefRe finds the link inside it.
var (
	anchorRe     = regexp.MustCompile(`<a\s+([^>]*)>([^<]+)</a>`)
	anchorHrefRe = regexp.MustCompile(`(^|\s)href="([^"]*)"`)
)

// GroupIndexSourcePath implements formats.GroupIndexMerger.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if p == "/simple" || strings.HasPrefix(p, "/simple/") {
		return p, true
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(groupName, p string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	groupBase := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + groupName

	type anchor struct{ attrs, text string }
	var anchors []anchor
	seen := map[string]bool{}

	for _, part := range parts {
		memberPrefix := "/repository/" + part.Member
		for _, m := range anchorRe.FindAllStringSubmatch(string(part.Body), -1) {
			attrs, text := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
			hm := anchorHrefRe.FindStringSubmatchIndex(attrs)
			if hm == nil {
				continue // not a link
			}
			if seen[text] {
				continue // first member wins per filename/package name
			}
			seen[text] = true
			// Re-root member URLs at the group; leave anything else as-is,
			// the other attributes included.
			href := attrs[hm[4]:hm[5]]
			if i := strings.Index(href, memberPrefix); i >= 0 {
				href = groupBase + href[i+len(memberPrefix):]
			}
			attrs = attrs[:hm[4]] + href + attrs[hm[5]:]
			anchors = append(anchors, anchor{attrs: attrs, text: text})
		}
	}

	title := "Links for " + path.Base(p)
	if p == "/simple" {
		title = "Simple index"
	}
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html><head><title>" + title + "</title></head><body><h1>" + title + "</h1>\n")
	for _, a := range anchors {
		fmt.Fprintf(&sb, `<a %s>%s</a><br/>`+"\n", a.attrs, a.text)
	}
	sb.WriteString("</body></html>\n")
	return []byte(sb.String()), "text/html; charset=utf-8", nil
}
