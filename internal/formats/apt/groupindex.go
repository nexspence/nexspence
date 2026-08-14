package apt

// Group index merging (#99 phase 2): the Packages index answers 200-empty
// for members without matching debs, shadowing later members under
// first-non-404 fan-out, and a single member's index hides the others'
// packages. Stanzas are unioned, deduped by Filename.
//
// Release/InRelease (#221) are rebuilt rather than relayed: a member's own
// document describes that member's indexes, while the group serves the union,
// so its Architectures line and checksums would describe something the client
// never downloads — a hash-sum mismatch, or an architecture apt never looks
// for. Release.gpg keeps plain fan-out: a detached signature can only be made
// with a key, and the merger has no way to answer "not applicable" (404)
// distinct from "merge failed", which degrades to a member's raw body.

import (
	"context"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// GroupIndexSourcePath implements formats.GroupIndexMerger. The .gz flavor
// fans out on the PLAIN document — members serve plain text, the merger
// gzips the merged result.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if !strings.HasPrefix(p, "/dists/") {
		return "", false
	}
	if strings.Contains(p, "/Packages") {
		return strings.TrimSuffix(p, ".gz"), true
	}
	// The members' own Release documents are the source: they are what names
	// the architectures and components the group has to cover.
	switch path.Base(p) {
	case "Release", "InRelease":
		return p, true
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(groupName, p string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	return h.MergeGroupIndexWithFetch(groupName, p, parts, nil)
}

// MergeGroupIndexWithFetch implements formats.GroupIndexDependentMerger.
func (h *Handler) MergeGroupIndexWithFetch(groupName, p string, parts []formats.GroupIndexPart,
	fetch formats.GroupIndexFetcher,
) ([]byte, string, error) {
	if base := path.Base(p); base == "Release" || base == "InRelease" {
		return h.mergeRelease(groupName, p, base == "InRelease", parts, fetch)
	}
	return mergePackages(p, parts), packagesContentType(p), nil
}

func packagesContentType(p string) string {
	if strings.HasSuffix(p, ".gz") {
		return "application/x-gzip"
	}
	return "text/plain; charset=utf-8"
}

// mergePackages unions the members' stanzas, deduped by Filename (first member
// wins), and gzips the result for the .gz flavor.
func mergePackages(p string, parts []formats.GroupIndexPart) []byte {
	var stanzas []string
	seen := map[string]bool{}
	for _, part := range parts {
		for _, stanza := range strings.Split(strings.TrimSpace(string(part.Body)), "\n\n") {
			stanza = strings.TrimSpace(stanza)
			if stanza == "" {
				continue
			}
			key := stanzaFilename(stanza)
			if key != "" && seen[key] {
				continue // first member wins per Filename
			}
			if key != "" {
				seen[key] = true
			}
			stanzas = append(stanzas, stanza)
		}
	}

	var sb strings.Builder
	for _, s := range stanzas {
		sb.WriteString(s)
		sb.WriteString("\n\n")
	}
	plain := []byte(sb.String())

	if strings.HasSuffix(p, ".gz") {
		return gzipBytes(plain)
	}
	return plain
}

// mergeRelease builds the group's own Release: every architecture and component
// its members declare, with checksums taken over the merged Packages indexes the
// group itself serves at those paths.
func (h *Handler) mergeRelease(groupName, p string, inline bool, parts []formats.GroupIndexPart,
	fetch formats.GroupIndexFetcher,
) ([]byte, string, error) {
	const contentType = "text/plain; charset=utf-8"

	dist := releaseDist(p)
	archs := unionReleaseField(parts, "Architectures", func(v string) bool { return v != "all" })
	components := unionReleaseField(parts, "Components", func(string) bool { return true })
	if len(components) == 0 {
		components = []string{"main"}
	}

	var files []releaseIndexFile
	if fetch != nil {
		for _, component := range components {
			for _, arch := range archs {
				for _, suffix := range []string{"", ".gz"} {
					rel := component + "/binary-" + arch + "/Packages" + suffix
					body, err := fetch("/dists/" + dist + "/" + rel)
					if err != nil {
						// A path no member serves is a path the group does not
						// serve either, so it belongs in no checksum section.
						continue
					}
					files = append(files, releaseIndexFile{relPath: rel, body: body})
				}
			}
		}
	}

	body := renderRelease(dist, archs, components, releaseDateOf(parts), files)
	if !inline {
		return body, contentType, nil
	}

	// Signed with the GROUP's own key, never a member's: a member's key vouches
	// for that member's index, not for a union it never served. An unsigned
	// group keeps serving the plain document, which is what [trusted=yes]
	// sources expect.
	repo, _ := h.deps.Repos.Get(context.Background(), groupName) // unreadable group == unsigned group
	if repo == nil || !signingConfigured(repo) {
		return body, contentType, nil
	}
	signed, err := clearSign(repo, body)
	if err != nil {
		return nil, "", err
	}
	return signed, contentType, nil
}

// unionReleaseField collects the values of a space-separated Release header
// across members, keeping those keep reports, sorted so the document is stable.
func unionReleaseField(parts []formats.GroupIndexPart, field string, keep func(string) bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range parts {
		for _, v := range strings.Fields(releaseHeader(string(part.Body), field)) {
			if !keep(v) || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// releaseDateOf is the newest Date the members declare. The document has to be
// byte-identical across requests — apt fetches Release and its signature
// separately and verifies one against the other — so it tracks the members'
// content rather than the wall clock, exactly as a single repository's does.
func releaseDateOf(parts []formats.GroupIndexPart) string {
	var newest time.Time
	for _, part := range parts {
		d, err := time.Parse(releaseDateLayout, releaseHeader(string(part.Body), "Date"))
		if err == nil && d.After(newest) {
			newest = d
		}
	}
	if newest.IsZero() {
		return time.Now().UTC().Format(releaseDateLayout)
	}
	return newest.UTC().Format(releaseDateLayout)
}

// releaseHeader reads a single-line "Field: value" header out of a Release
// document, skipping the indented checksum lines.
func releaseHeader(doc, field string) string {
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, " ") {
			continue
		}
		if v, found := strings.CutPrefix(line, field+":"); found {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// stanzaFilename extracts the Filename: field used as the dedup key.
func stanzaFilename(stanza string) string {
	for _, line := range strings.Split(stanza, "\n") {
		if strings.HasPrefix(line, "Filename:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Filename:"))
		}
	}
	return ""
}
