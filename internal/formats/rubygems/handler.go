package rubygems

import (
	"bytes"
	"crypto/md5" //nolint:gosec // compact-index integrity hash, not security
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// Handler serves the RubyGems repository protocol:
//
//	GET  /versions                → compact index master list
//	GET  /info/:gem               → one gem's versions, deps and checksums
//	GET  /names                   → gem name list
//	GET  /gems/:file.gem          → gem download
//	POST /api/v1/gems             → publish (body is the .gem)
//	DELETE /api/v1/gems/yank      → yank one version
//
// The compact index is what Bundler resolves against; it is generated from
// the stored components (hosted) or proxied verbatim (proxy) — its entries
// carry no embedded URLs, so no rewriting is needed.
type Handler struct{ deps formats.Deps }

// New creates a RubyGems format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "rubygems" }

// maxGemBytes bounds a published .gem read into memory for parsing. Real gems
// run from KBs to a few MB; the largest on rubygems.org are tens of MB.
const maxGemBytes = 256 << 20

// gemInfoLineKey is the component Extra key holding the precomputed
// compact-index /info line for that version.
const gemInfoLineKey = "gem_info_line"

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, err := h.deps.Repos.Get(c.Request.Context(), repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		// The compact index is mutable metadata (new versions appear); .gem
		// files are immutable. Nothing embeds URLs, so nothing is rewritten.
		var maxAge time.Duration
		if !strings.HasPrefix(p, "/gems/") {
			maxAge = repoproxy.MetadataMaxAge(repo)
		}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", proxyCoords(p), "application/octet-stream", maxAge); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	switch {
	// The legacy Marshal indexes are not generated: modern gem/bundler fall
	// back to the compact index on a clean 404 (verified live).
	case c.Request.Method == http.MethodGet &&
		(p == "/specs.4.8.gz" || p == "/latest_specs.4.8.gz" || p == "/prerelease_specs.4.8.gz"):
		c.Status(http.StatusNotFound)
	case c.Request.Method == http.MethodGet && p == "/versions":
		h.serveVersions(c, repoName)
	case c.Request.Method == http.MethodGet && p == "/names":
		h.serveNames(c, repoName)
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/info/"):
		h.serveInfo(c, repoName, strings.TrimPrefix(p, "/info/"))
	case (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && strings.HasPrefix(p, "/gems/"):
		h.serveGem(c, repoName, p)
	case c.Request.Method == http.MethodPost && p == "/api/v1/gems":
		h.handlePublish(c, repoName)
	case c.Request.Method == http.MethodDelete && p == "/api/v1/gems/yank":
		h.handleYank(c, repoName)
	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

// proxyCoords derives component coordinates for a proxied path, so a cached
// gem carries its real name and version (the #336 lesson: a path-fallback
// name makes the component invisible to search and any future scanning).
func proxyCoords(p string) base.Coords {
	if file, ok := strings.CutPrefix(p, "/gems/"); ok && !strings.Contains(file, "/") {
		if name, version, ok := parseGemFilename(file); ok {
			return base.Coords{Name: name, Version: version}
		}
	}
	if gem, ok := strings.CutPrefix(p, "/info/"); ok && gem != "" && !strings.Contains(gem, "/") {
		return base.Coords{Name: gem, Version: "metadata"}
	}
	switch strings.TrimPrefix(p, "/") {
	case "versions", "names":
		return base.Coords{Name: "_index", Version: "metadata"}
	}
	return base.Coords{}
}

// parseGemFilename splits "name-version[-platform].gem" at the first hyphen
// followed by a digit — gem names may carry hyphens, version segments start
// numeric.
func parseGemFilename(file string) (name, version string, ok bool) {
	stem, found := strings.CutSuffix(file, ".gem")
	if !found || stem == "" {
		return "", "", false
	}
	for i := 1; i < len(stem)-1; i++ {
		if stem[i] == '-' && stem[i+1] >= '0' && stem[i+1] <= '9' {
			return stem[:i], stem[i+1:], true
		}
	}
	return "", "", false
}

// ── hosted: compact index ─────────────────────────────────────────────────────

// gemComponents returns every stored gem component of the repository, ordered
// by name then version-with-platform.
func (h *Handler) gemComponents(c *gin.Context, repoName string) ([]domain.Component, bool) {
	var out []domain.Component
	offset := 0
	for {
		page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
			Repository: repoName, Limit: 500, Offset: offset,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return nil, false
		}
		out = append(out, page.Items...)
		if page.ContinuationToken == nil {
			break
		}
		offset += 500
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return versionLess(out[i].Version, out[j].Version)
	})
	return out, true
}

// infoBody renders the /info/<gem> document for the given components.
func infoBody(comps []domain.Component) string {
	var b strings.Builder
	b.WriteString("---\n")
	for _, comp := range comps {
		if line, ok := comp.Extra[gemInfoLineKey].(string); ok && line != "" {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (h *Handler) serveInfo(c *gin.Context, repoName, gem string) {
	comps, ok := h.gemComponents(c, repoName)
	if !ok {
		return
	}
	var mine []domain.Component
	for _, comp := range comps {
		if comp.Name == gem {
			mine = append(mine, comp)
		}
	}
	if len(mine) == 0 {
		c.Status(http.StatusNotFound)
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(infoBody(mine)))
}

func (h *Handler) serveNames(c *gin.Context, repoName string) {
	comps, ok := h.gemComponents(c, repoName)
	if !ok {
		return
	}
	var b strings.Builder
	b.WriteString("---\n")
	last := ""
	for _, comp := range comps {
		if comp.Name != last {
			b.WriteString(comp.Name)
			b.WriteByte('\n')
			last = comp.Name
		}
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(b.String()))
}

func (h *Handler) serveVersions(c *gin.Context, repoName string) {
	comps, ok := h.gemComponents(c, repoName)
	if !ok {
		return
	}
	// Group by gem, preserving the sorted order.
	type gemEntry struct {
		name  string
		comps []domain.Component
	}
	var gems []gemEntry
	for _, comp := range comps {
		if len(gems) == 0 || gems[len(gems)-1].name != comp.Name {
			gems = append(gems, gemEntry{name: comp.Name})
		}
		gems[len(gems)-1].comps = append(gems[len(gems)-1].comps, comp)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "created_at: %s\n---\n", time.Now().UTC().Format(time.RFC3339))
	for _, g := range gems {
		versions := make([]string, 0, len(g.comps))
		for _, comp := range g.comps {
			versions = append(versions, comp.Version)
		}
		// The MD5 of the /info file is how Bundler validates the info it
		// fetches — the two documents must be generated consistently.
		sum := md5.Sum([]byte(infoBody(g.comps))) //nolint:gosec
		fmt.Fprintf(&b, "%s %s %s\n", g.name, strings.Join(versions, ","), hex.EncodeToString(sum[:]))
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(b.String()))
}

// versionLess orders gem versions numerically segment by segment, falling back
// to string order for non-numeric segments (prereleases, platforms).
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		an, aerr := parseUint(as[i])
		bn, berr := parseUint(bs[i])
		if aerr == nil && berr == nil {
			return an < bn
		}
		return as[i] < bs[i]
	}
	return len(as) < len(bs)
}

func parseUint(s string) (uint64, error) {
	var n uint64
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not numeric")
		}
		n = n*10 + uint64(r-'0')
	}
	return n, nil
}

// ── hosted: artifacts ─────────────────────────────────────────────────────────

func (h *Handler) serveGem(c *gin.Context, repoName, p string) {
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
	c.DataFromReader(http.StatusOK, asset.SizeBytes, "application/octet-stream", rc, nil)
}

func (h *Handler) handlePublish(c *gin.Context, repoName string) {
	repo, err := h.deps.Repos.Get(c.Request.Context(), repoName)
	if err != nil || repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}
	if repoproxy.RejectMutation(c, repo) {
		return
	}

	// The body IS the .gem. It is buffered because it is read twice: once to
	// parse the gemspec (coordinates, dependencies), once to store.
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxGemBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(body) > maxGemBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "gem exceeds the maximum size"})
		return
	}
	spec, err := ParseGem(bytes.NewReader(body))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sum := sha256.Sum256(body)
	filePath := "/gems/" + spec.Filename()
	coords := base.Coords{Name: spec.Name, Version: spec.VersionWithPlatform()}
	res, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, filePath, "application/octet-stream", coords,
		bytes.NewReader(body), int64(len(body)))
	if err != nil {
		c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
		return
	}

	// The compact-index line is computed once here and stored on the
	// component; the index endpoints just concatenate.
	if err := h.deps.Components.UpdateExtra(c.Request.Context(), res.Asset.ComponentID, map[string]any{
		gemInfoLineKey: spec.InfoLine(hex.EncodeToString(sum[:])),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.String(http.StatusOK, "Successfully registered gem: %s (%s)", spec.Name, spec.VersionWithPlatform())
}

func (h *Handler) handleYank(c *gin.Context, repoName string) {
	repo, err := h.deps.Repos.Get(c.Request.Context(), repoName)
	if err != nil || repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}
	if repoproxy.RejectMutation(c, repo) {
		return
	}
	// The gem client sends the parameters as a form-encoded DELETE body, which
	// Go's FormValue does not parse for DELETE — read both places.
	name := c.Query("gem_name")
	version := c.Query("version")
	if name == "" || version == "" {
		body, _ := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
		if vals, err := url.ParseQuery(string(body)); err == nil {
			if name == "" {
				name = vals.Get("gem_name")
			}
			if version == "" {
				version = vals.Get("version")
			}
		}
	}
	if name == "" || version == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gem_name and version are required"})
		return
	}
	filePath := "/gems/" + name + "-" + version + ".gem"
	if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, filePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// The compact index is generated from components, so the yanked version's
	// component goes too — leaving it would keep advertising a gem whose file
	// is gone.
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Name: name, Version: version, Limit: 500,
	})
	if err == nil {
		for _, comp := range page.Items {
			if comp.Name == name && comp.Version == version {
				_ = h.deps.Components.Delete(c.Request.Context(), comp.ID)
			}
		}
	}
	c.String(http.StatusOK, "Successfully yanked gem: %s (%s)", name, version)
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
