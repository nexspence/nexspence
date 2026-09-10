package alpine

// APKINDEX is a plain-text index (one-letter key:value stanzas, blank-line
// separated) packed inside a single-entry APKINDEX.tar.gz — generated on the
// fly from stored components/assets, never persisted as a file, same approach
// as apt's Packages/yum's primary.xml.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// maxIndexEntryBytes bounds a member's APKINDEX read during group merge — a
// misbehaving/compromised member should not make the merge read unbounded
// data into memory.
const maxIndexEntryBytes = 64 << 20

// buildIndex generates the plain-text APKINDEX for one architecture,
// identified by the full request path (e.g. "/x86_64/APKINDEX.tar.gz" or, in
// Alpine's real published layout, "/v3.20/main/x86_64/APKINDEX.tar.gz"). Only
// packages whose checksum was computed at upload time (see handleUpload) are
// listed — one that failed checksumming would otherwise publish an index
// entry no real apk client could ever install.
func (h *Handler) buildIndex(ctx context.Context, repoName, p string) ([]byte, error) {
	arch := pathArch(p)
	// Filter assets by the exact directory the index was requested under, not
	// just by the trailing arch segment — a bare "/"+arch+"/" prefix would
	// also match a sibling architecture under the same branch/repo path (e.g.
	// "/v3.20/main/x86_64/" and "/v3.20/main/aarch64/" both start with
	// "/v3.20/"), mixing every architecture into one index (PR #440 review).
	prefix := path.Dir(p) + "/"

	page, err := h.deps.Components.Search(ctx, domain.SearchParams{Repository: repoName, Limit: 1000}) // see #443: hardcoded page size shared with apt/cran
	if err != nil {
		return nil, err
	}
	assetPage, err := h.deps.Assets.List(ctx, repoName, 1000, 0) // see #443: hardcoded page size shared with apt/cran
	if err != nil {
		return nil, err
	}
	compByID := make(map[string]*domain.Component, len(page.Items))
	for i := range page.Items {
		compByID[page.Items[i].ID] = &page.Items[i]
	}

	var sb strings.Builder
	for _, a := range assetPage.Items {
		if !strings.HasSuffix(a.Path, ".apk") || !strings.HasPrefix(a.Path, prefix) {
			continue
		}
		comp := compByID[a.ComponentID]
		if comp == nil {
			continue
		}
		checksum, _ := comp.Extra["checksum"].(string)
		if checksum == "" {
			continue
		}
		fmt.Fprintf(&sb, "C:%s\n", checksum)
		fmt.Fprintf(&sb, "P:%s\n", comp.Name)
		fmt.Fprintf(&sb, "V:%s\n", comp.Version)
		fmt.Fprintf(&sb, "A:%s\n", arch)
		fmt.Fprintf(&sb, "S:%d\n", a.SizeBytes)
		// I: (installed/uncompressed size) comes from .PKGINFO's own "size"
		// when the upload could be parsed; falls back to the stored .apk size
		// otherwise (better than nothing, though not the real uncompressed size).
		installedSize := a.SizeBytes
		if n, ok := extraInt64(comp.Extra["installedSize"]); ok && n > 0 {
			installedSize = n
		}
		fmt.Fprintf(&sb, "I:%d\n", installedSize)
		if desc, _ := comp.Extra["description"].(string); desc != "" {
			fmt.Fprintf(&sb, "T:%s\n", desc)
		}
		if lic, _ := comp.Extra["license"].(string); lic != "" {
			fmt.Fprintf(&sb, "L:%s\n", lic)
		}
		if depends := extraStringSlice(comp.Extra["depends"]); len(depends) > 0 {
			fmt.Fprintf(&sb, "D:%s\n", strings.Join(depends, " "))
		}
		if provides := extraStringSlice(comp.Extra["provides"]); len(provides) > 0 {
			fmt.Fprintf(&sb, "p:%s\n", strings.Join(provides, " "))
		}
		sb.WriteString("\n")
	}
	return []byte(sb.String()), nil
}

// extraStringSlice reads a []string out of Component.Extra, tolerating both
// the native type (in-memory testutil mocks) and []interface{} (real Postgres
// JSONB round trip).
func extraStringSlice(v any) []string {
	switch vv := v.(type) {
	case []string:
		return vv
	case []interface{}:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// extraInt64 reads an int64 out of Component.Extra, tolerating both the
// native type and float64 (real Postgres JSONB round trip decodes numbers
// that way).
func extraInt64(v any) (int64, bool) {
	switch vv := v.(type) {
	case int64:
		return vv, true
	case int:
		return int64(vv), true
	case float64:
		return int64(vv), true
	default:
		return 0, false
	}
}

// pathArch extracts the architecture segment from an apk-protocol path — the
// last directory component before the file name. This works for both a
// single-level layout ("/x86_64/APKINDEX.tar.gz" -> "x86_64") and Alpine's
// real published multi-level layout ("/v3.20/main/x86_64/APKINDEX.tar.gz" ->
// "x86_64", not "v3.20": taking the first path segment, as this function used
// to, silently matched the wrong directory and mixed architectures together —
// see PR #440 review).
func pathArch(p string) string {
	dir := path.Dir(p)
	if dir == "/" || dir == "." {
		return ""
	}
	return path.Base(dir)
}

// packIndexTarGz wraps a plain-text APKINDEX in the single-entry tar.gz that
// apk clients expect — Alpine has no "plain" index endpoint like apt's
// uncompressed Packages.
func packIndexTarGz(index []byte) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{Name: "APKINDEX", Mode: 0o644, Size: int64(len(index))}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, err
	}
	if _, err := tw.Write(index); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// unpackIndexTarGz reads the APKINDEX entry back out of a member's
// APKINDEX.tar.gz, for group merging.
func unpackIndexTarGz(data []byte) (string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("alpine: not a gzip APKINDEX: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", errors.New("alpine: no APKINDEX entry in tar")
		}
		if err != nil {
			return "", err
		}
		if hdr.Name != "APKINDEX" {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(tr, maxIndexEntryBytes))
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
}
