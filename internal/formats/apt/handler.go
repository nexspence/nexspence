// Package apt implements the Debian APT repository protocol.
//
// Layout under /repository/:repoName/:
//
//	GET  /dists/:dist/:component/binary-:arch/Packages[.gz] → packages index
//	GET  /dists/:dist/Release                               → Release file
//	GET  /pool/:component/:prefix/:name_ver_arch.deb        → deb download
//	PUT  /pool/:component/:name_ver_arch.deb                → upload .deb
//	DELETE /pool/:component/:name_ver_arch.deb              → delete .deb
package apt

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5" //nolint:gosec // apt protocol checksum, not security
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// debControlKey holds a component's control paragraph, as stored by
// handleUpload and emitted by buildPackagesIndex.
const debControlKey = "deb_control"

// Handler serves the Debian APT repository protocol.
type Handler struct{ deps formats.Deps }

// New creates an APT format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "apt" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	// Proxy: block uploads/deletes, pass reads through to upstream (e.g. archive.ubuntu.com)
	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		ct := "application/octet-stream"
		if strings.HasSuffix(p, "/Release") || strings.HasSuffix(p, "/InRelease") {
			ct = "text/plain"
		} else if strings.Contains(p, "/Packages") {
			ct = "text/plain"
		}
		// /pool/ holds immutable .deb artifacts; everything under /dists/
		// (Release/InRelease/Packages and other indexes) is mutable metadata that
		// upstreams re-sign with an expiry, so it must be revalidated on a TTL.
		var maxAge time.Duration
		if !strings.HasPrefix(p, "/pool/") {
			maxAge = repoproxy.MetadataMaxAge(repo)
		}
		// proxyCoords gives cached files real component coordinates (name/version
		// from .deb filenames, path-based for indexes) so they browse and delete
		// individually instead of collapsing into one nameless component.
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", proxyCoords(p), ct, maxAge); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	h.serveHosted(c, repo, repoName, p)
}

// serveHosted routes the hosted (non-proxy) surface of the apt protocol.
func (h *Handler) serveHosted(c *gin.Context, repo *domain.Repository, repoName, p string) {
	switch {
	// Packages index (plain or gzip)
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/dists/") && strings.Contains(p, "/Packages"):
		h.servePackagesIndex(c, repoName, p)

	// Public half of the signing key, so clients can trust this repository.
	case c.Request.Method == http.MethodGet && p == "/public.gpg":
		h.servePublicKey(c, repo)

	// Detached signature of Release (#103)
	case c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/Release.gpg"):
		h.serveReleaseSignature(c, repo, repoName, strings.TrimSuffix(p, ".gpg"))

	// Release file
	case c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/Release"):
		h.serveRelease(c, repoName, p)

	// InRelease — the same document, signed inline when a key is configured.
	case c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/InRelease"):
		h.serveInRelease(c, repo, repoName, p)

	// Download .deb
	case (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && strings.HasPrefix(p, "/pool/"):
		h.serveFile(c, repoName, p)

	// Upload .deb: PUT /pool/:component/:file.deb, or a root-level PUT of a .deb
	// (apt clients and `curl --upload-file foo.deb .../repository/<repo>/` upload
	// to the repository root rather than an explicit pool path). POST is the
	// Nexus-compatible verb: either to a .deb path (raw body) or to the repo
	// root as multipart/form-data with a "file"/"package" field (#96).
	case (c.Request.Method == http.MethodPut || c.Request.Method == http.MethodPost) &&
		(p == "/" || strings.HasPrefix(p, "/pool/") || strings.HasSuffix(p, ".deb")):
		h.handleUpload(c, repoName, p)

	// Delete .deb
	case c.Request.Method == http.MethodDelete && strings.HasPrefix(p, "/pool/"):
		if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, p); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

// debArch extracts the architecture from a "name_version_arch.deb" filename.
func debArch(filePath string) string {
	filename := path.Base(filePath)
	if parts := strings.Split(strings.TrimSuffix(filename, ".deb"), "_"); len(parts) >= 3 {
		return parts[len(parts)-1]
	}
	return "amd64" // default for non-conforming names
}

// buildPackagesIndex generates the Packages index. arch filters to that
// architecture (plus "all" debs, per Debian convention); empty = no filter.
func (h *Handler) buildPackagesIndex(ctx context.Context, repoName, arch string) ([]byte, error) {
	comps, err := base.AllComponents(ctx, h.deps.Components, repoName)
	if err != nil {
		return nil, err
	}
	// Only /pool/ debs: that is where apt fetches Filename from (#443).
	assets, err := h.deps.Assets.ListByRepoAndPath(ctx, repoName, "/pool/")
	if err != nil {
		return nil, err
	}
	compMap := map[string]*domain.Component{}
	for i := range comps {
		compMap[comps[i].ID] = &comps[i]
	}

	var sb strings.Builder
	listed := map[string]bool{}
	for _, a := range assets {
		if !strings.HasSuffix(a.Path, ".deb") {
			continue
		}
		comp := compMap[a.ComponentID]
		if comp == nil {
			continue
		}
		debA := assetArch(comp, a)
		if arch != "" && debA != arch && debA != "all" {
			continue // the binary-<arch> index lists that arch plus "all" (#103)
		}
		// One stanza per identity. Uploads refuse a second file for one, but
		// paths that bypass the handler (promotion into a repository that
		// already holds the identity) can still attach one; listing both would
		// give apt two candidates it cannot tell apart. Assets come in path
		// order, so the first path wins deterministically.
		key := comp.ID + "/" + debA
		if listed[key] {
			continue
		}
		listed[key] = true
		writeStanza(&sb, comp, a, debA)
	}
	return []byte(sb.String()), nil
}

// assetArch is a deb's architecture: from its control file (stored as the
// component Group, #637), or from the filename for debs stored without one.
func assetArch(comp *domain.Component, a domain.Asset) string {
	if comp != nil && comp.Group != "" {
		return comp.Group
	}
	return debArch(a.Path)
}

// writeStanza writes one Packages paragraph. A deb uploaded with a readable
// control file contributes that paragraph verbatim — Depends, Pre-Depends,
// Provides, Conflicts, Description and the rest — followed by the fields the
// server computes from the stored file, in dpkg-scanpackages' order (#637).
// Debs stored before that (or without an ar body) keep the filename stanza.
func writeStanza(sb *strings.Builder, comp *domain.Component, a domain.Asset, arch string) {
	if ctl, _ := comp.Extra[debControlKey].(string); ctl != "" {
		sb.WriteString(strings.TrimRight(ctl, "\n"))
		sb.WriteString("\n")
		fmt.Fprintf(sb, "Filename: %s\n", a.Path)
		fmt.Fprintf(sb, "Size: %d\n", a.SizeBytes)
		if a.MD5 != "" {
			fmt.Fprintf(sb, "MD5sum: %s\n", a.MD5)
		}
		if a.SHA1 != "" {
			fmt.Fprintf(sb, "SHA1: %s\n", a.SHA1)
		}
		if a.SHA256 != "" {
			fmt.Fprintf(sb, "SHA256: %s\n", a.SHA256)
		}
		sb.WriteString("\n")
		return
	}
	fmt.Fprintf(sb, "Package: %s\n", comp.Name)
	fmt.Fprintf(sb, "Version: %s\n", comp.Version)
	fmt.Fprintf(sb, "Architecture: %s\n", arch)
	fmt.Fprintf(sb, "Filename: %s\n", a.Path)
	fmt.Fprintf(sb, "Size: %d\n", a.SizeBytes)
	if a.SHA256 != "" {
		fmt.Fprintf(sb, "SHA256: %s\n", a.SHA256)
	}
	if a.MD5 != "" {
		fmt.Fprintf(sb, "MD5sum: %s\n", a.MD5)
	}
	sb.WriteString("\n")
}

func (h *Handler) servePackagesIndex(c *gin.Context, repoName, p string) {
	gzipped := strings.HasSuffix(p, ".gz")

	// Parse the requested architecture from /dists/:dist/:component/binary-:arch/...
	arch := ""
	for _, seg := range strings.Split(p, "/") {
		if a, found := strings.CutPrefix(seg, "binary-"); found {
			arch = a
			break
		}
	}

	data, err := h.buildPackagesIndex(c.Request.Context(), repoName, arch)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if gzipped {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		_, _ = gw.Write(data)
		_ = gw.Close()
		c.Data(http.StatusOK, "application/x-gzip", buf.Bytes())
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", data)
}

// repoArchitectures collects the concrete architectures of the debs the
// Packages indexes are built from: every /pool/ deb, not a first page of the
// repository's assets — an architecture missing here is an index the Release
// never vouches for, which a verifying apt then rejects. "all" is not one of
// them: renderRelease declares it unconditionally and buildRelease always
// vouches for its binary-all index.
func (h *Handler) repoArchitectures(ctx context.Context, repoName string) []string {
	assets, err := h.deps.Assets.ListByRepoAndPath(ctx, repoName, "/pool/")
	if err != nil {
		return nil
	}
	// The same arch the Packages index files each deb under (assetArch), or
	// Release would vouch for one set of indexes and serve another.
	comps, err := base.AllComponents(ctx, h.deps.Components, repoName)
	if err != nil {
		return nil
	}
	compMap := make(map[string]*domain.Component, len(comps))
	for i := range comps {
		compMap[comps[i].ID] = &comps[i]
	}
	seen := map[string]bool{}
	var archs []string
	for _, a := range assets {
		if !strings.HasSuffix(a.Path, ".deb") {
			continue
		}
		arch := assetArch(compMap[a.ComponentID], a)
		if arch == "all" || seen[arch] {
			continue
		}
		seen[arch] = true
		archs = append(archs, arch)
	}
	sort.Strings(archs)
	return archs
}

func (h *Handler) serveRelease(c *gin.Context, repoName, p string) {
	body, err := h.buildRelease(c.Request.Context(), repoName, p)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", body)
}

// serveInRelease serves the clearsigned Release. Unsigned repositories keep
// serving the plain document, which is what [trusted=yes] sources expect.
func (h *Handler) serveInRelease(c *gin.Context, repo *domain.Repository, repoName, p string) {
	body, err := h.buildRelease(c.Request.Context(), repoName, p)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !signingConfigured(repo) {
		c.Data(http.StatusOK, "text/plain; charset=utf-8", body)
		return
	}
	signed, err := clearSign(repo, body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", signed)
}

// serveReleaseSignature serves Release.gpg — the detached signature apt checks
// against the Release document it fetched separately.
func (h *Handler) serveReleaseSignature(c *gin.Context, repo *domain.Repository, repoName, releasePath string) {
	if !signingConfigured(repo) {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository is not signed"})
		return
	}
	body, err := h.buildRelease(c.Request.Context(), repoName, releasePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	sig, err := detachSign(repo, body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/pgp-signature", sig)
}

func (h *Handler) servePublicKey(c *gin.Context, repo *domain.Repository) {
	if !signingConfigured(repo) {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository is not signed"})
		return
	}
	key, err := armoredPublicKey(repo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/pgp-keys", key)
}

// buildRelease renders the Release document for the distribution named in p.
//
// The result must be byte-identical across requests: apt fetches Release and
// its signature separately and verifies one against the other, so a wall-clock
// Date would break verification. The date therefore tracks the content — the
// newest package in the repository (#103).
func (h *Handler) buildRelease(ctx context.Context, repoName, p string) ([]byte, error) {
	dist := releaseDist(p)
	archs := h.repoArchitectures(ctx, repoName)

	// apt verifies the Packages indexes against these checksum sections —
	// without them a default (verifying) client rejects the repo (#103).
	// binary-all is vouched for as well: renderRelease always declares "all",
	// and in a repository whose debs are all Architecture: all it is the ONLY
	// index apt reads — without its checksums a signed repository carries no
	// hash entries at all and apt refuses it as "weak security information".
	var files []releaseIndexFile
	for _, arch := range indexArchs(archs) {
		plain, err := h.buildPackagesIndex(ctx, repoName, arch)
		if err != nil {
			continue
		}
		files = append(files,
			releaseIndexFile{relPath: "main/binary-" + arch + "/Packages", body: plain},
			releaseIndexFile{relPath: "main/binary-" + arch + "/Packages.gz", body: gzipBytes(plain)},
		)
	}

	date := h.releaseDate(ctx, repoName).UTC().Format(releaseDateLayout)
	return renderRelease(dist, archs, []string{"main"}, date, files), nil
}

// indexArchs is every architecture a Release vouches for: the concrete ones
// plus "all", which renderRelease declares on every Architectures line.
func indexArchs(archs []string) []string {
	return append(append([]string{}, archs...), "all")
}

// releaseDateLayout is the Date format apt reads, and the one a group parses
// back out of its members' documents.
const releaseDateLayout = "Mon, 02 Jan 2006 15:04:05 UTC"

// releaseIndexFile is one index document a Release vouches for: the path
// relative to the distribution, and the exact bytes served at it.
type releaseIndexFile struct {
	relPath string
	body    []byte
}

// releaseDist parses the distribution out of /dists/:dist/Release.
func releaseDist(p string) string {
	parts := strings.Split(strings.TrimPrefix(p, "/dists/"), "/")
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return "stable"
}

func gzipBytes(plain []byte) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, _ = gw.Write(plain)
	_ = gw.Close()
	return buf.Bytes()
}

// renderRelease writes the Release document. One repository and a group produce
// it through this same code, differing only in where archs, components and the
// index bodies come from — a group's are the union across its members, so the
// checksums always describe the indexes that repository actually serves.
func renderRelease(dist string, archs, components []string, date string, files []releaseIndexFile) []byte {
	archList := strings.Join(append(append([]string{}, archs...), "all"), " ")

	var sb strings.Builder
	fmt.Fprintf(&sb, `Origin: Nexspence
Label: Nexspence
Suite: %s
Codename: %s
Date: %s
Architectures: %s
Components: %s
Description: Nexspence APT Repository
`, dist, dist, date, archList, strings.Join(components, " "))
	sb.WriteString("MD5Sum:\n")
	for _, f := range files {
		fmt.Fprintf(&sb, " %x %d %s\n", md5.Sum(f.body), len(f.body), f.relPath) //nolint:gosec // apt protocol checksum
	}
	sb.WriteString("SHA256:\n")
	for _, f := range files {
		fmt.Fprintf(&sb, " %x %d %s\n", sha256.Sum256(f.body), len(f.body), f.relPath)
	}
	return []byte(sb.String())
}

// releaseDate is the timestamp stamped into Release. It follows the newest
// stored package so the document only changes when its content does; an empty
// repository has nothing to pin it to and falls back to the current time.
func (h *Handler) releaseDate(ctx context.Context, repoName string) time.Time {
	assetPage, err := h.deps.Assets.List(ctx, repoName, 1000, 0)
	if err != nil {
		return time.Now()
	}
	var newest time.Time
	for _, a := range assetPage.Items {
		if !strings.HasSuffix(a.Path, ".deb") {
			continue
		}
		if a.LastModified.After(newest) {
			newest = a.LastModified
		}
	}
	if newest.IsZero() {
		return time.Now()
	}
	return newest
}

// debCoords parses the Debian "name_version_arch.deb" filename convention.
// Non-conforming names keep the whole filename as the package name.
func debCoords(filename string) (pkgName, version string) {
	pkgName, version = filename, "0.0.0"
	if parts := strings.Split(strings.TrimSuffix(filename, ".deb"), "_"); len(parts) >= 2 {
		pkgName, version = parts[0], parts[1]
	}
	return pkgName, version
}

// proxyCoords derives component coordinates for a file cached from upstream.
// Packages get real package/version coordinates so they browse like hosted ones;
// index files (Release, InRelease, Packages) are keyed by their path, since each
// is a distinct document rather than a version of a package.
func proxyCoords(p string) base.Coords {
	if strings.HasSuffix(p, ".deb") {
		name, version := debCoords(path.Base(p))
		return base.Coords{Name: name, Version: version}
	}
	return base.Coords{Name: strings.TrimPrefix(p, "/"), Version: "metadata"}
}

func (h *Handler) handleUpload(c *gin.Context, repoName, p string) {
	filename := path.Base(p)
	body := io.Reader(c.Request.Body)
	size := c.Request.ContentLength

	// Nexus-style root POST: the .deb arrives as a multipart file field
	// ("file" or "package") and the filename comes from the part, not the path.
	if !strings.HasSuffix(filename, ".deb") {
		f, fh, err := c.Request.FormFile("file")
		if err != nil {
			f, fh, err = c.Request.FormFile("package")
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected a *.deb path or a multipart 'file' field"})
			return
		}
		defer func() { _ = f.Close() }()
		filename, body, size = fh.Filename, f, fh.Size
	}

	// The control file is authoritative for Package/Version/Architecture and
	// supplies the Packages stanza (#637), as dpkg-scanpackages does. Only the
	// ar header and the control member are read here; the bytes are replayed
	// in front of the rest of the body, so a large .deb still streams.
	head, ctl, err := readDebHead(body)
	switch {
	case errors.Is(err, errDebTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": err.Error()})
		return
	case err != nil && !errors.Is(err, errNotDeb):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	body = io.MultiReader(bytes.NewReader(head), body)

	// Not an ar archive at all: keep the filename convention, as before.
	pkgName, version := debCoords(filename)
	coords := base.Coords{Name: pkgName, Version: version}
	arch := debArch(filename)
	if ctl != nil {
		pkgName, arch = ctl.Package, ctl.Architecture
		coords = base.Coords{Group: ctl.Architecture, Name: ctl.Package, Version: ctl.Version}
	}

	// Normalize root-level uploads into the canonical pool layout so the
	// Packages index (which lists /pool/ assets) still finds them.
	storePath := p
	if !strings.HasPrefix(storePath, "/pool/") {
		storePath = poolPath(pkgName, filename)
	}

	ctx := c.Request.Context()
	store := func(ctx context.Context) (int, error) {
		res, err := base.StoreArtifact(ctx, h.deps,
			repoName, storePath, "application/vnd.debian.binary-package",
			coords, body, size)
		if err != nil {
			return base.HTTPStatusForError(err), err
		}
		if status, err := h.settleIdentity(ctx, repoName, storePath, coords, arch); err != nil {
			return status, err
		}
		if ctl == nil {
			return http.StatusCreated, nil
		}
		if err := h.deps.Components.UpdateExtra(ctx, res.Asset.ComponentID,
			map[string]any{debControlKey: ctl.Paragraph}); err != nil {
			return http.StatusInternalServerError, err
		}
		return http.StatusCreated, nil
	}
	// Every upload claims its identity, a filename-named one included:
	// otherwise a non-deb body could take an identity a real deb holds.
	status, err := h.withDebLocks(ctx, repoName, storePath, coords, arch, func() (int, error) {
		if status, err := h.claimDebIdentity(ctx, repoName, storePath, coords, arch); err != nil {
			return status, err
		}
		return store(ctx)
	})
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.Status(status)
}

// withDebLocks runs fn holding the upload's path lock and then its identity
// lock, always in that order, so two uploads can never wait on each other.
// The path lock keeps two first uploads of one path from both seeing it free;
// the identity lock keeps two paths from both claiming one identity.
//
// These are in-process locks only. fn streams the whole body, and a lock held
// by a database transaction for that long pins a pool connection per upload:
// a few concurrent uploads then starve every other request. Instances in a
// cluster are reconciled after the store instead, by settleIdentity.
func (h *Handler) withDebLocks(_ context.Context, repoName, storePath string, coords base.Coords, arch string,
	fn func() (int, error),
) (int, error) {
	defer base.LockLocal(debPathKey(repoName, storePath))()
	defer base.LockLocal(debIdentityKey(repoName, coords, arch))()
	return fn()
}

// ':' namespaces the lock keys like the quota locks; repository names, package
// names and architectures cannot contain it, and the free-form parts (path,
// version) come last.
func debPathKey(repoName, storePath string) string { return "apt-path:" + repoName + ":" + storePath }

func debIdentityKey(repoName string, coords base.Coords, arch string) string {
	return "apt-identity:" + repoName + ":" + arch + ":" + coords.Name + ":" + coords.Version
}

// settleIdentity is the cross-instance backstop for claimDebIdentity: another
// instance may have stored the same identity under another path while this
// one streamed. Every upload checks after its own store, and the earliest
// stored file keeps the identity, so the later one removes itself before its
// control paragraph is recorded. No lock is taken: holding a transaction while
// querying through the same pool starves it under concurrent uploads, and
// the rule needs none to agree on a winner. Within one instance the
// in-process locks already serialize the claim.
func (h *Handler) settleIdentity(ctx context.Context, repoName, storePath string, coords base.Coords, arch string) (int, error) {
	holders, err := h.identityHolders(ctx, repoName, coords, arch)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	winner := ""
	var first time.Time
	for _, a := range holders {
		if winner == "" || a.CreatedAt.Before(first) || (a.CreatedAt.Equal(first) && a.Path < winner) {
			winner, first = a.Path, a.CreatedAt
		}
	}
	if winner == "" || winner == storePath {
		return 0, nil
	}
	if err := base.DeleteArtifact(ctx, h.deps, repoName, storePath); err != nil {
		return http.StatusInternalServerError, err
	}
	return http.StatusConflict, fmt.Errorf("%s %s (%s) is already stored at %s", coords.Name, coords.Version, arch, winner)
}

// identityHolders lists the stored debs that carry one identity: those filed
// under its architecture, and pre-#637 ones (no Group) whose filename names it.
func (h *Handler) identityHolders(ctx context.Context, repoName string, coords base.Coords, arch string) ([]domain.Asset, error) {
	var out []domain.Asset
	for _, group := range []string{arch, ""} {
		comps, err := base.ExactComponents(ctx, h.deps.Components, domain.SearchParams{
			Repository: repoName, Format: string(domain.FormatApt),
			Group: group, Name: coords.Name, Version: coords.Version,
		})
		if err != nil {
			return nil, err
		}
		for i := range comps {
			assets, err := h.deps.Assets.ListByComponentID(ctx, comps[i].ID)
			if err != nil {
				return nil, err
			}
			for _, a := range assets {
				if assetArch(&comps[i], a) == arch {
					out = append(out, a)
				}
			}
		}
	}
	return out, nil
}

// claimDebIdentity enforces one .deb per (Package, Version, Architecture): apt
// can index only one file under that identity, so a second file claiming it
// under another path is refused, as reprepro and dak refuse it. A path keeps
// the identity it was first stored under: a redeploy whose control names a
// different one is refused too, rather than deleting a file the new upload
// might then fail to replace. Callers hold withDebLocks.
func (h *Handler) claimDebIdentity(ctx context.Context, repoName, storePath string, coords base.Coords, arch string) (int, error) {
	holders, err := h.identityHolders(ctx, repoName, coords, arch)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	for _, a := range holders {
		if a.Path != storePath {
			return http.StatusConflict, fmt.Errorf("%s %s (%s) is already stored at %s",
				coords.Name, coords.Version, arch, a.Path)
		}
	}

	existing, err := h.deps.Assets.GetByPath(ctx, repoName, storePath)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && existing == nil) {
		return 0, nil // nothing stored at this path yet
	}
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("look up %s: %w", storePath, err)
	}
	old, err := h.deps.Components.Get(ctx, existing.ComponentID)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && old == nil) {
		return 0, nil // dangling asset: the store re-registers it
	}
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("look up the package at %s: %w", storePath, err)
	}
	if old.Group != coords.Group || old.Name != coords.Name || old.Version != coords.Version {
		held := old.Name + " " + old.Version
		if old.Group != "" {
			held += " (" + old.Group + ")"
		}
		return http.StatusConflict, fmt.Errorf("%s holds %s; delete it before uploading %s %s (%s) there",
			storePath, held, coords.Name, coords.Version, arch)
	}
	return 0, nil
}

func (h *Handler) serveFile(c *gin.Context, repoName, p string) {
	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, p)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = rc.Close() }()
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
		c.Status(http.StatusOK)
		return
	}
	c.DataFromReader(http.StatusOK, asset.SizeBytes, asset.ContentType, rc, nil)
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}

// poolPath builds the canonical Debian pool location for a root-level upload:
// /pool/main/<prefix>/<pkg>/<file>.deb, where <prefix> follows Debian's
// convention (the first letter, or "lib<x>" for lib* packages).
func poolPath(pkgName, filename string) string {
	prefix := "_"
	if pkgName != "" {
		if strings.HasPrefix(pkgName, "lib") && len(pkgName) > 3 {
			prefix = pkgName[:4]
		} else {
			prefix = pkgName[:1]
		}
	}
	return "/pool/main/" + prefix + "/" + pkgName + "/" + filename
}
