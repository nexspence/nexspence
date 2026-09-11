// Package huggingface implements the Hugging Face Hub protocol for ML models,
// datasets and spaces.
//
// Layout under /repository/:repoName/ — <repo_id> is "namespace/name", or a
// bare "name" for a canonical repo such as bert-base-uncased:
//
//	GET    /api/models/<repo_id>                        → repository metadata
//	GET    /api/models/<repo_id>/revision/<revision>    → metadata at a revision
//	GET    /api/models/<repo_id>/tree/<revision>[/dir]  → file tree
//	GET    /<repo_id>/resolve/<revision>/<filename>     → download a file
//	HEAD   /<repo_id>/resolve/<revision>/<filename>     → that file's metadata
//	PUT    /<repo_id>/resolve/<revision>/<filename>     → publish a file (hosted)
//
// Datasets and spaces speak the same shapes under their own prefix
// (/api/datasets/<repo_id>, /datasets/<repo_id>/resolve/...); a model download
// path carries no type prefix at all, which is why the repo type cannot be read
// off a fixed segment of the URL.
//
// huggingface_hub, transformers and diffusers point every call at another host
// through HF_ENDPOINT (or endpoint=), so a Nexspence repository needs no other
// client-side change. Those clients never speak the git-lfs protocol to
// download: huggingface.co resolves LFS server-side before answering
// resolve/, so a proxy repository caches those responses as plain binaries and
// implements nothing of LFS itself.
//
// Publishing is a Nexspence convention, not the Hub's own protocol — see
// handlePublish.
package huggingface

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// maxRepoFiles caps how many stored files one repository can contribute to a
// metadata or tree answer. Model repositories are wide (sharded weights,
// tokenizer files, revisions), and the tree endpoint is unpaginated in this
// first version, so the bound is here rather than in the client. The paging
// limits the two asset lookups run under are the same ones tracked in #443.
const maxRepoFiles = 5000

// assetPageSize is one page of the asset lookup; SearchAssets caps a page at
// 500 rows.
const assetPageSize = 500

// Handler serves the Hugging Face Hub protocol.
type Handler struct{ deps formats.Deps }

// New creates a Hugging Face format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "huggingface" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)
	isProxy := repo != nil && repo.Type == domain.TypeProxy
	if isProxy && repoproxy.RejectMutation(c, repo) {
		return
	}

	// Metadata surface. The Hub has no service-discovery endpoint — a real
	// client calls these directly, without negotiating capabilities first —
	// so there is nothing to serve locally for a proxy repository.
	if ref, kind, revision, subpath, ok := parseAPIPath(p); ok {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "the huggingface metadata API is read-only"})
			return
		}
		switch {
		case isProxy:
			h.serveProxyAPI(c, repo, repoName, p)
		case kind == kindTree:
			h.serveHostedTree(c, repoName, ref, revision, subpath)
		default:
			h.serveHostedInfo(c, repoName, ref, revision)
		}
		return
	}

	ref, revision, filename, ok := parseResolve(p)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown huggingface endpoint"})
		return
	}
	if isProxy {
		h.serveProxyResolve(c, repo, p, ref, revision, filename)
		return
	}

	switch c.Request.Method {
	case http.MethodGet, http.MethodHead:
		h.serveHostedFile(c, repoName, ref, revision, filename)
	case http.MethodPut:
		h.handlePublish(c, repoName, ref, revision, filename)
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed on a huggingface file path"})
	}
}

// ── hosted: metadata ─────────────────────────────────────────────

// repoSibling is one entry of a repository's file list.
type repoSibling struct {
	RFilename string `json:"rfilename"`
}

// repoInfo is the metadata document huggingface_hub reads as ModelInfo /
// DatasetInfo / SpaceInfo. Only the fields a real client needs are answered:
// "siblings" is what list_repo_files and snapshot_download enumerate, and "sha"
// is the commit snapshot_download resolves the whole snapshot under. Presented
// Hub fields that describe a social object rather than a repository's contents
// (tags, pipeline_tag, cardData, downloads, likes) are left out.
type repoInfo struct {
	ID           string        `json:"id"`
	Author       string        `json:"author,omitempty"`
	SHA          string        `json:"sha"`
	LastModified string        `json:"lastModified"`
	Private      bool          `json:"private"`
	Siblings     []repoSibling `json:"siblings"`
}

func (h *Handler) serveHostedInfo(c *gin.Context, repoName string, ref repoRef, revision string) {
	revision, files, err := h.resolveRevision(c.Request.Context(), repoName, ref, revision)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(files) == 0 {
		// A repo_id nothing was ever published under does not exist, and saying
		// so is what lets a group skip this member instead of being shadowed by
		// an empty 200 (#99).
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found: " + ref.id()})
		return
	}

	prefix := ref.revisionPrefix(revision)
	siblings := make([]repoSibling, 0, len(files))
	var lastModified time.Time
	for _, a := range files {
		siblings = append(siblings, repoSibling{RFilename: strings.TrimPrefix(a.Path, prefix)})
		if a.LastModified.After(lastModified) {
			lastModified = a.LastModified
		}
	}

	c.JSON(http.StatusOK, repoInfo{
		ID:           ref.id(),
		Author:       ref.Namespace,
		SHA:          revisionCommit(ref, revision, files),
		LastModified: lastModified.UTC().Format(time.RFC3339),
		Siblings:     siblings,
	})
}

// treeEntry is one entry of the tree endpoint. "oid" is synthetic — see blobOID.
type treeEntry struct {
	Type string `json:"type"` // "file" or "directory"
	OID  string `json:"oid"`
	Size int64  `json:"size"`
	Path string `json:"path"`
}

func (h *Handler) serveHostedTree(c *gin.Context, repoName string, ref repoRef, revision, subpath string) {
	revision, files, err := h.resolveRevision(c.Request.Context(), repoName, ref, revision)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(files) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found: " + ref.id()})
		return
	}
	c.JSON(http.StatusOK, buildTree(files, ref.revisionPrefix(revision), subpath, recursiveRequested(c)))
}

// recursiveRequested reports whether the client asked for the whole subtree.
// huggingface_hub's list_repo_tree sends recursive=True (Python's bool, title
// case) when list_repo_files walks a repository.
func recursiveRequested(c *gin.Context) bool {
	v := c.Query("recursive")
	return strings.EqualFold(v, "true") || v == "1"
}

// buildTree renders the entries under subpath of a revision rooted at root.
// Entry paths stay repository-relative ("onnx/model.onnx"), the way the Hub
// reports them — not relative to the directory that was listed.
//
// Non-recursively it answers one level, collapsing everything deeper into a
// directory entry, which is the shape huggingface.co answers with and the
// reason a client that wants the whole repository asks with recursive=True.
func buildTree(files []domain.Asset, root, subpath string, recursive bool) []treeEntry {
	prefix := root
	if subpath != "" {
		prefix += subpath + "/"
	}
	entries := make([]treeEntry, 0, len(files))
	dirs := map[string]bool{}
	for _, a := range files {
		if !strings.HasPrefix(a.Path, prefix) {
			continue
		}
		rel := strings.TrimPrefix(a.Path, prefix)
		if rel == "" {
			continue
		}
		if dir, _, nested := strings.Cut(rel, "/"); nested && !recursive {
			if dirs[dir] {
				continue
			}
			dirs[dir] = true
			entries = append(entries, treeEntry{
				Type: "directory",
				OID:  pathOID(prefix + dir),
				Path: strings.TrimPrefix(prefix+dir, root),
			})
			continue
		}
		entries = append(entries, treeEntry{
			Type: "file",
			OID:  blobOID(a),
			Size: a.SizeBytes,
			Path: strings.TrimPrefix(a.Path, root),
		})
	}
	return entries
}

// ── hosted: files ────────────────────────────────────────────────

func (h *Handler) serveHostedFile(c *gin.Context, repoName string, ref repoRef, revision, filename string) {
	ctx := c.Request.Context()

	revision, files, err := h.resolveRevision(ctx, repoName, ref, revision)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	notFound := gin.H{"error": "not found: " + ref.id() + "/" + filename + " at revision " + revision}
	if len(files) == 0 {
		c.JSON(http.StatusNotFound, notFound)
		return
	}
	commit := revisionCommit(ref, revision, files)

	// Looked up directly rather than picked out of files: that list is capped
	// (maxRepoFiles), and a wide repository's file must download even when it
	// falls outside the listing.
	storePath := ref.storePath(revision, filename)
	asset, err := h.deps.Assets.GetByPath(ctx, repoName, storePath)
	if err != nil || asset == nil {
		c.JSON(http.StatusNotFound, notFound)
		return
	}

	if c.Request.Method == http.MethodHead {
		// huggingface_hub probes with HEAD before it downloads anything
		// (get_hf_file_metadata) and refuses to continue without an ETag or
		// without an X-Repo-Commit — it would have nothing to name its local
		// cache entry after. A HEAD is answered from the asset record alone,
		// without opening the blob or counting a download.
		applyFileHeaders(c, asset, commit)
		c.Header("Content-Type", contentTypeFor(filename, asset.ContentType))
		c.Header("Content-Length", strconv.FormatInt(asset.SizeBytes, 10))
		c.Status(http.StatusOK)
		return
	}

	rc, a, err := base.FetchArtifact(ctx, h.deps, repoName, storePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = rc.Close() }()
	applyFileHeaders(c, a, commit)
	c.DataFromReader(http.StatusOK, a.SizeBytes, contentTypeFor(filename, a.ContentType), rc, nil)
}

// applyFileHeaders sets the response headers huggingface_hub reads off a
// resolve/ answer: the ETag it keys its local blob cache by, and the commit it
// names the snapshot directory after.
func applyFileHeaders(c *gin.Context, a *domain.Asset, commit string) {
	if a.SHA256 != "" {
		c.Header("ETag", `"`+a.SHA256+`"`)
		c.Header("X-Checksum-SHA256", a.SHA256)
	}
	c.Header("X-Repo-Commit", commit)
}

// handlePublish stores one file at one revision.
//
// This is a Nexspence convention, NOT the Hub's own publish protocol: uploading
// to huggingface.co goes through its Commit API (a preupload call, an
// LFS-batch-like negotiation for large files, then an atomic commit over git),
// which is specific to that backend and has no public specification to
// implement against. A hosted repository here accepts the plain per-file PUT
// that raw and terraform also offer, at the same URL the file is downloaded
// from, so publishing is possible without claiming to speak the real thing.
func (h *Handler) handlePublish(c *gin.Context, repoName string, ref repoRef, revision, filename string) {
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps, repoName,
		ref.storePath(revision, filename), contentTypeFor(filename, c.GetHeader("Content-Type")),
		ref.coords(revision),
		c.Request.Body, c.Request.ContentLength); err != nil {
		c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

// ── hosted: asset lookups ────────────────────────────────────────

// resolveRevision resolves the revision a client named to the one the files are
// actually stored under, and returns that revision's files.
//
// Every metadata endpoint needs this, not only the download path:
// snapshot_download reads the commit sha out of the metadata document and then
// asks the TREE endpoint at that sha before it fetches a single file, so a
// mapping that only covered resolve/ would fail the whole snapshot on its
// second call. Found against the real client, which is the only place this
// order of calls is visible.
func (h *Handler) resolveRevision(ctx context.Context, repoName string, ref repoRef, revision string) (string, []domain.Asset, error) {
	files, err := h.revisionFiles(ctx, repoName, ref, revision)
	if err != nil {
		return revision, nil, err
	}
	if len(files) > 0 || !isCommitSHA(revision) {
		return revision, files, nil
	}
	mapped, mappedFiles, err := h.revisionForCommit(ctx, repoName, ref, revision)
	if err != nil || mapped == "" {
		return revision, nil, err
	}
	return mapped, mappedFiles, nil
}

// revisionFiles lists one revision's stored files, in path order.
func (h *Handler) revisionFiles(ctx context.Context, repoName string, ref repoRef, revision string) ([]domain.Asset, error) {
	return h.assetsUnder(ctx, repoName, ref.revisionPrefix(revision))
}

// assetsUnder lists the repository's assets whose path starts with prefix.
func (h *Handler) assetsUnder(ctx context.Context, repoName, prefix string) ([]domain.Asset, error) {
	var out []domain.Asset
	offset := 0
	for {
		page, err := h.deps.Assets.SearchAssets(ctx, domain.SearchParams{
			Repository: repoName,
			// SearchAssets matches Name as a SUBSTRING of the asset path, so
			// the prefix is re-checked exactly here: without that, a listing of
			// "myns/bert" would also collect "otherns/myns/bert" — and of
			// "bert" every file of "bert-large" (#443 tracks the paging limits
			// these two lookups run under).
			Name:   prefix,
			Limit:  assetPageSize,
			Offset: offset,
		})
		if err != nil {
			return nil, fmt.Errorf("huggingface: asset lookup: %w", err)
		}
		for _, a := range page.Items {
			if strings.HasPrefix(a.Path, prefix) {
				out = append(out, a)
			}
		}
		if len(out) >= maxRepoFiles || page.ContinuationToken == nil {
			break
		}
		next, err := strconv.Atoi(*page.ContinuationToken)
		if err != nil || next <= offset {
			break
		}
		offset = next
	}
	if len(out) > maxRepoFiles {
		out = out[:maxRepoFiles]
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// revisionForCommit finds the stored revision whose contents the given commit
// sha names, with its files, or "" when the sha names nothing this repository
// holds.
//
// Only the CURRENT contents of each stored revision are addressable this way: a
// hosted repository keeps no history, so a sha handed out before a re-upload
// stops resolving once the contents behind it change. That is the honest answer
// — the tree that sha described is genuinely no longer here.
func (h *Handler) revisionForCommit(ctx context.Context, repoName string, ref repoRef, commit string) (string, []domain.Asset, error) {
	if !isCommitSHA(commit) {
		return "", nil, nil
	}
	all, err := h.assetsUnder(ctx, repoName, ref.storePrefix()+"/resolve/")
	if err != nil {
		return "", nil, err
	}
	byRevision := map[string][]domain.Asset{}
	var revisions []string
	for _, a := range all {
		rev := revisionOf(ref, a.Path)
		if rev == "" {
			continue
		}
		if _, seen := byRevision[rev]; !seen {
			revisions = append(revisions, rev)
		}
		byRevision[rev] = append(byRevision[rev], a)
	}
	sort.Strings(revisions)
	want := strings.ToLower(commit)
	for _, rev := range revisions {
		if revisionCommit(ref, rev, byRevision[rev]) == want {
			return rev, byRevision[rev], nil
		}
	}
	return "", nil, nil
}

// revisionOf extracts the revision segment out of a stored asset path.
func revisionOf(ref repoRef, assetPath string) string {
	rest, found := strings.CutPrefix(assetPath, ref.storePrefix()+"/resolve/")
	if !found {
		return ""
	}
	rev, _, _ := strings.Cut(rest, "/")
	return rev
}

// ── proxy ────────────────────────────────────────────────────────

// serveProxyResolve caches a file downloaded from upstream.
//
// The cache lifetime follows what the revision means, a distinction no other
// format needs to make about an artifact: a commit sha identifies its contents
// permanently, so a hit is served forever, while a branch or tag is a moving
// name and is revalidated on the metadata TTL like any mutable document.
//
// huggingface.co answers a large file with a 302 to its own CDN; the upstream
// client follows redirects as any HTTP client would, which is all "the client
// never speaks LFS" requires of a mirror.
func (h *Handler) serveProxyResolve(c *gin.Context, repo *domain.Repository, p string, ref repoRef, revision, filename string) {
	if c.Request.Method == http.MethodHead {
		// A HEAD is huggingface_hub asking for the file's ETag and
		// X-Repo-Commit before it downloads. The blob cache cannot answer
		// either — it knows our own sha256, and nothing at all about the
		// upstream commit a branch pointed at when the copy was cached — so
		// the probe is forwarded to upstream and answered with its headers,
		// while the bytes themselves still come from the cache below.
		h.forwardUpstreamHead(c, repo, p)
		return
	}

	maxAge := time.Duration(0)
	if !isCommitSHA(revision) {
		maxAge = repoproxy.MetadataMaxAge(repo)
	} else {
		// Correct by definition, and the one case a cache hit can still answer
		// on its own: the sha the client asked for IS the commit.
		c.Header("X-Repo-Commit", strings.ToLower(revision))
	}
	// The cache key is the canonical local layout; upstream is asked with the
	// path the client used, which is the only one huggingface.co knows.
	if err := repoproxy.ServeGET(c, h.deps, repo, ref.storePath(revision, filename), p,
		ref.coords(revision), "application/octet-stream", maxAge); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
	}
}

// forwardUpstreamHead relays a HEAD to upstream and answers with its headers.
//
// It must not let the shared client auto-follow a redirect off the Hub: a
// real Hub host answers HEAD on an LFS-backed file with a redirect to its CDN
// (confirmed live against bert-base-uncased's real pytorch_model.bin, 420 MB),
// and X-Repo-Commit/X-Linked-ETag/X-Linked-Size live ONLY on that redirect
// response — the CDN's own final response carries none of them, so a client
// that saw only the final hop would refuse to download at all
// (FileMetadataError). huggingface_hub's own redirect follower
// (_httpx_follow_hub_redirects_with_backoff) does exactly this: it keeps
// following same-host redirects but stops at the first one that leaves the
// host, and reads the file's metadata off THAT response. Mirrored here with
// upstreamHeadHop, then answered as 200 rather than relayed as a redirect —
// get_hf_file_metadata falls back to the REQUESTED url as "location" whenever
// the response it read metadata from wasn't itself a redirect, so this keeps
// the client's follow-up GET coming back through Nexspence (and its cache)
// instead of sending it straight to upstream's CDN, which is exactly the kind
// of large file this cache exists for.
//
// X-Xet-Hash is dropped from the relay. Confirmed live: most of huggingface.co's
// real weight files today (model.safetensors, pytorch_model.bin, tf_model.h5 —
// not the small text files) are stored on Xet, its newer content-addressable
// backend, and the client treats X-Xet-Hash as the sole trigger
// (parse_xet_file_data_from_response) to fetch the file through a THREE-PARTY
// CAS reconstruction protocol (a xet-read-token call back to the Hub, then
// chunk reconstruction against a third host, cas-server.xethub.hf.co) instead
// of a plain download — a protocol this proxy does not, and is not trying to,
// speak. Relaying that header verbatim sent every real client into that path
// and then straight into a 404, since Nexspence answers none of its endpoints.
// Withholding it is not a lossy shortcut: huggingface.co keeps a "xet-bridge"
// fallback URL behind the SAME redirect precisely for non-Xet-aware HTTP
// clients (confirmed live — it answers a plain GET with the complete file over
// ordinary HTTP), which is exactly what falls out of the client's own
// xet_file_data-is-None branch, and exactly the URL this proxy's GET path
// already follows and caches. The bytes served are identical either way; only
// the Xet client's own content-defined dedup transport is skipped.
func (h *Handler) forwardUpstreamHead(c *gin.Context, repo *domain.Repository, p string) {
	resp, err := h.upstreamHeadHop(c, repo, p)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for k, vv := range resp.Header {
		switch k {
		case "Location", "Content-Length", "X-Xet-Hash":
			continue
		}
		for _, v := range vv {
			c.Writer.Header().Add(k, v)
		}
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		c.Status(http.StatusOK)
		return
	}
	c.Status(resp.StatusCode)
}

// maxRedirectHops bounds upstreamHeadHop's manual redirect-following — same
// order of magnitude as net/http's own default (10) and
// huggingface_hub's _MAX_REDIRECTS (20).
const maxRedirectHops = 20

// upstreamHeadHop issues a HEAD to upstream, following same-host redirects
// (a Hub canonicalizing a path) but stopping at and returning the first
// redirect that leaves the host — the response a CDN hand-off's metadata
// lives on. A terminal non-redirect response (200, 404, ...) is returned as
// received.
func (h *Handler) upstreamHeadHop(c *gin.Context, repo *domain.Repository, p string) (*http.Response, error) {
	remoteBase, err := repoproxy.RemoteURL(repo)
	if err != nil {
		return nil, err
	}
	upstream, err := repoproxy.JoinURL(remoteBase, p)
	if err != nil {
		return nil, err
	}

	// A shallow copy: the redirect policy changes for this call without
	// touching the shared client's pooled Transport or its own policy.
	client := *repoproxy.ClientFor(repo)
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	for hop := 0; ; hop++ {
		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodHead, upstream, nil)
		if err != nil {
			return nil, err
		}
		if ac := c.GetHeader("Accept"); ac != "" {
			req.Header.Set("Accept", ac)
		}
		repoproxy.SetUpstreamAuth(req, repo)

		resp, err := client.Do(req)
		if err != nil {
			repoproxy.DispatchProxyError(h.deps, repo.Name, p, upstream, err)
			return nil, fmt.Errorf("upstream: %w", err)
		}

		loc := resp.Header.Get("Location")
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || loc == "" || hop >= maxRedirectHops {
			return resp, nil
		}
		target, parseErr := resp.Request.URL.Parse(loc)
		if parseErr != nil {
			// An unparseable Location is not this function's error to raise —
			// the response otherwise stands on its own, so it's returned as
			// the (possibly non-redirect) terminal answer, same as any other
			// hop that isn't a same-host redirect.
			return resp, nil //nolint:nilerr // deliberate: malformed Location just means "don't follow it"
		}
		if target.Host != resp.Request.URL.Host {
			return resp, nil // leaving the host: this response carries the file's metadata
		}
		_ = resp.Body.Close()
		upstream = target.String()
	}
}

// serveProxyAPI relays the metadata surface.
//
// The body is passed through untouched: unlike terraform, whose version
// documents carry absolute download URLs that have to be pointed back at this
// proxy, a Hub metadata document names files relatively ("siblings[].rfilename",
// a tree entry's "path"). The client builds the download URL itself out of its
// own HF_ENDPOINT, so there is nothing in the body to rewrite.
func (h *Handler) serveProxyAPI(c *gin.Context, repo *domain.Repository, repoName, p string) {
	// The client's own method is what upstream is asked with: a metadata HEAD
	// wants headers, and fetching a body to throw away is not more correct.
	resp, err := h.upstreamRequest(c, repo, c.Request.Method, p)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	// A paginated tree answers with a Link header naming the next page on
	// huggingface.co. Relayed verbatim it would send the client straight to
	// upstream for page two, past this repository's own credentials, rules and
	// audit trail — so the next-page link is pointed back here.
	if link := resp.Header.Get("Link"); link != "" {
		c.Header("Link", rewriteLinkHeader(link, strings.TrimRight(h.deps.BaseURL, "/")+"/repository/"+repoName))
	}
	if xc := resp.Header.Get("X-Repo-Commit"); xc != "" {
		c.Header("X-Repo-Commit", xc)
	}
	c.Header("Content-Type", ct)
	c.Status(resp.StatusCode)
	if c.Request.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(c.Writer, resp.Body)
}

// upstreamRequest issues one request to the configured remote.
func (h *Handler) upstreamRequest(c *gin.Context, repo *domain.Repository, method, p string) (*http.Response, error) {
	remoteBase, err := repoproxy.RemoteURL(repo)
	if err != nil {
		return nil, err
	}
	upstream, err := repoproxy.JoinURL(remoteBase, p)
	if err != nil {
		return nil, err
	}
	if q := c.Request.URL.RawQuery; q != "" {
		upstream += "?" + q
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), method, upstream, nil)
	if err != nil {
		return nil, err
	}
	if ac := c.GetHeader("Accept"); ac != "" {
		req.Header.Set("Accept", ac)
	} else {
		req.Header.Set("Accept", "application/json")
	}
	repoproxy.SetUpstreamAuth(req, repo)
	resp, err := repoproxy.ClientFor(repo).Do(req)
	if err != nil {
		repoproxy.DispatchProxyError(h.deps, repo.Name, p, upstream, err)
		return nil, fmt.Errorf("upstream: %w", err)
	}
	return resp, nil
}

// rewriteLinkHeader repoints the URLs of an RFC 8288 Link header at localBase,
// keeping each link's parameters. A URL it cannot parse is left alone.
func rewriteLinkHeader(header, localBase string) string {
	parts := strings.Split(header, ",")
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		start, end := strings.Index(trimmed, "<"), strings.Index(trimmed, ">")
		if start != 0 || end < 0 {
			continue
		}
		u, err := url.Parse(trimmed[1:end])
		if err != nil {
			continue
		}
		local := localBase + u.EscapedPath()
		if u.RawQuery != "" {
			local += "?" + u.RawQuery
		}
		parts[i] = "<" + local + ">" + trimmed[end+1:]
	}
	return strings.Join(parts, ", ")
}

// decodeTree reads a tree document back, for the group merger's benefit.
func decodeTree(body []byte) ([]treeEntry, error) {
	var entries []treeEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}
