package apt

// Group index merging (#99 phase 2): the Packages index answers 200-empty
// for members without matching debs, shadowing later members under
// first-non-404 fan-out, and a single member's index hides the others'
// packages. Stanzas are unioned, deduped by Filename. Release/InRelease
// are generated boilerplate (no member URLs) and stay on plain fan-out.

import (
	"bytes"
	"compress/gzip"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// GroupIndexSourcePath implements formats.GroupIndexMerger. The .gz flavor
// fans out on the PLAIN document — members serve plain text, the merger
// gzips the merged result.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if !strings.HasPrefix(p, "/dists/") || !strings.Contains(p, "/Packages") {
		return "", false
	}
	return strings.TrimSuffix(p, ".gz"), true
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(_, p string, parts []formats.GroupIndexPart) ([]byte, string, error) {
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
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		_, _ = gw.Write(plain)
		_ = gw.Close()
		return buf.Bytes(), "application/x-gzip", nil
	}
	return plain, "text/plain; charset=utf-8", nil
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
