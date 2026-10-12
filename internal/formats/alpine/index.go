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
	"log"
	"path"
	"strings"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
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

	comps, err := base.AllComponents(ctx, h.deps.Components, repoName)
	if err != nil {
		return nil, err
	}
	assets, err := h.deps.Assets.ListByRepoAndPath(ctx, repoName, prefix)
	if err != nil {
		return nil, err
	}
	compByID := make(map[string]*domain.Component, len(comps))
	for i := range comps {
		compByID[comps[i].ID] = &comps[i]
	}

	repo, _ := h.deps.Repos.Get(ctx, repoName)
	repairs, deadline := 0, time.Now().Add(repairTimeBudget)
	var sb strings.Builder
	for _, a := range assets {
		if !strings.HasSuffix(a.Path, ".apk") {
			continue
		}
		comp := compByID[a.ComponentID]
		if comp == nil {
			continue
		}
		name, version, extra := comp.Name, comp.Version, comp.Extra
		if comp.Group != indexDir(a.Path) {
			// Uploaded before the index directory was a coordinate (#671): the
			// shared component may hold another build's metadata. Read this
			// file's own, and move it to its component so this happens once.
			if repo != nil && repairs < maxRepairsPerIndex && time.Now().Before(deadline) {
				repairs++
				if n, v, x, ok := h.repairLegacyAsset(ctx, repo, a, comp); ok {
					name, version, extra = n, v, x
				}
			}
		}
		checksum, _ := extra["checksum"].(string)
		if checksum == "" {
			continue
		}
		fmt.Fprintf(&sb, "C:%s\n", checksum)
		fmt.Fprintf(&sb, "P:%s\n", name)
		fmt.Fprintf(&sb, "V:%s\n", version)
		fmt.Fprintf(&sb, "A:%s\n", arch)
		fmt.Fprintf(&sb, "S:%d\n", a.SizeBytes)
		// I: (installed/uncompressed size) comes from .PKGINFO's own "size"
		// when the upload could be parsed; falls back to the stored .apk size
		// otherwise (better than nothing, though not the real uncompressed size).
		installedSize := a.SizeBytes
		if n, ok := extraInt64(extra["installedSize"]); ok && n > 0 {
			installedSize = n
		}
		fmt.Fprintf(&sb, "I:%d\n", installedSize)
		if desc, _ := extra["description"].(string); desc != "" {
			fmt.Fprintf(&sb, "T:%s\n", desc)
		}
		if lic, _ := extra["license"].(string); lic != "" {
			fmt.Fprintf(&sb, "L:%s\n", lic)
		}
		if depends := extraStringSlice(extra["depends"]); len(depends) > 0 {
			fmt.Fprintf(&sb, "D:%s\n", strings.Join(depends, " "))
		}
		if provides := extraStringSlice(extra["provides"]); len(provides) > 0 {
			fmt.Fprintf(&sb, "p:%s\n", strings.Join(provides, " "))
		}
		sb.WriteString("\n")
	}
	return []byte(sb.String()), nil
}

// One APKINDEX request repairs at most maxRepairsPerIndex pre-#671 files, for
// at most repairTimeBudget; the rest keep their old entries until the requests
// that follow. Every attempt records metadata, so no file is read twice.
const (
	maxRepairsPerIndex = 32
	repairTimeBudget   = 10 * time.Second
	// The control segment sits within the first members of an .apk: up to
	// maxSignatureMembers signatures and the control member, each bounded by
	// maxMemberBytes decompressed. Twice that covers their compressed form.
	maxControlPrefixBytes = 2 * (maxSignatureMembers + 1) * maxMemberBytes
)

// repairLegacyAsset reads a pre-#671 asset's metadata from its own bytes and
// moves it to the component of its index directory. A file that cannot be
// read moves with what its shared component says, so it is not retried on
// every request. Concurrent requests repairing the same file wait for each
// other and reuse the result. ok is false when the path no longer holds a
// file the index can describe. A failed move is only logged: the metadata is
// still this file's.
func (h *Handler) repairLegacyAsset(ctx context.Context, repo *domain.Repository, a domain.Asset, legacy *domain.Component) (name, version string, extra map[string]any, ok bool) {
	unlock := base.LockLocal("alpine-index-repair:" + repo.Name + ":" + a.Path)
	defer unlock()
	cur, err := h.deps.Assets.GetByPath(ctx, repo.Name, a.Path)
	if err != nil || cur == nil {
		return "", "", nil, false
	}
	if cur.ComponentID != a.ComponentID || cur.SHA256 != a.SHA256 {
		// Repaired by a request that held the lock first, or replaced by an
		// upload: its component describes it now.
		comp, err := h.deps.Components.Get(ctx, cur.ComponentID)
		if err != nil || comp == nil {
			return "", "", nil, false
		}
		return comp.Name, comp.Version, comp.Extra, true
	}

	compressed, rawControl, err := h.readStoredControl(ctx, cur)
	if err != nil {
		log.Printf("nexspence: alpine %s%s: index repair keeps the shared component's metadata: %v", repo.Name, a.Path, err)
		name, version = legacy.Name, legacy.Version
		extra = make(map[string]any, len(legacy.Extra))
		for k, v := range legacy.Extra {
			extra[k] = v
		}
	} else {
		name, version, extra = apkMetadata(path.Base(a.Path), compressed, rawControl)
	}
	coords := base.Coords{Group: indexDir(a.Path), Name: name, Version: version}
	if err := h.placeInIndexDir(ctx, repo, cur, cur.SHA256, coords, extra); err != nil {
		log.Printf("nexspence: alpine %s%s: move to its index directory's component: %v", repo.Name, a.Path, err)
	}
	return name, version, extra, true
}

// readStoredControl reads the start of a stored .apk straight from its blob
// store — not through FetchArtifact, which would count the read as a download
// and keep the file from download-age cleanup — and isolates its control
// segment.
func (h *Handler) readStoredControl(ctx context.Context, a *domain.Asset) (compressed, raw []byte, err error) {
	store, err := base.AssetPhysicalStore(ctx, h.deps, a)
	if err != nil {
		return nil, nil, err
	}
	rc, _, err := store.Get(ctx, a.BlobKey)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rc.Close() }()
	head, err := io.ReadAll(io.LimitReader(rc, maxControlPrefixBytes))
	if err != nil {
		return nil, nil, err
	}
	return controlSegment(head)
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
