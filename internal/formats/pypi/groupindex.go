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

// anchorRe extracts <a href="...">text</a> pairs from PEP 503 pages
// (machine-generated anchor lists, so regex extraction is safe).
var anchorRe = regexp.MustCompile(`<a\s+href="([^"]+)"[^>]*>([^<]+)</a>`)

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

	type anchor struct{ href, text string }
	var anchors []anchor
	seen := map[string]bool{}

	for _, part := range parts {
		memberPrefix := "/repository/" + part.Member
		for _, m := range anchorRe.FindAllStringSubmatch(string(part.Body), -1) {
			href, text := m[1], strings.TrimSpace(m[2])
			if seen[text] {
				continue // first member wins per filename/package name
			}
			seen[text] = true
			// Re-root member URLs at the group; leave anything else as-is.
			if i := strings.Index(href, memberPrefix); i >= 0 {
				href = groupBase + href[i+len(memberPrefix):]
			}
			anchors = append(anchors, anchor{href: href, text: text})
		}
	}

	title := "Links for " + path.Base(p)
	if p == "/simple" {
		title = "Simple index"
	}
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html><head><title>" + title + "</title></head><body><h1>" + title + "</h1>\n")
	for _, a := range anchors {
		fmt.Fprintf(&sb, `<a href="%s">%s</a><br/>`+"\n", a.href, a.text)
	}
	sb.WriteString("</body></html>\n")
	return []byte(sb.String()), "text/html; charset=utf-8", nil
}
