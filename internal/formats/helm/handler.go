// Package helm implements the Helm chart repository protocol.
//
// GET  /index.yaml              → Chart.yaml index (generated from DB)
// GET  /:chart-:version.tgz    → download chart archive
// PUT  /:chart-:version.tgz    → upload chart (Nexus/ChartMuseum-compatible, `curl -T`)
// POST /api/charts              → upload chart (multipart or raw body)
// DELETE /api/charts/:name/:ver → delete chart version
package helm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// Handler serves the Helm chart repository protocol.
type Handler struct {
	deps    formats.Deps
	indexes *chartIndexCache
}

// New creates a Helm format Handler with the given dependencies.
func New(deps formats.Deps) *Handler {
	return &Handler{deps: deps, indexes: newChartIndexCache()}
}

// Name returns the format identifier.
func (h *Handler) Name() string { return "helm" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	// Proxy: block mutations; rewrite index.yaml; cache chart binaries.
	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		if (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && p == "/index.yaml" {
			h.fetchAndRewriteHelmIndex(c, repo)
			return
		}
		// The request path may be nested (charts/mychart-1.2.3.tgz), so the component
		// coordinates come from the filename, never from the path. Helm fetches a
		// "<chart>.tgz.prov" signature alongside each chart; file that under the
		// chart's own coordinates instead of as a component in its own right.
		filename := strings.TrimSuffix(path.Base(p), ".prov")
		coords := base.Coords{Name: filename}
		upstream := ""
		if strings.HasSuffix(filename, ".tgz") {
			chartName, version := splitChartFilename(filename)
			coords = base.Coords{Name: chartName, Version: version}
			u, name, ver := h.proxyChartUpstream(c, repo, p)
			if name != "" {
				coords = base.Coords{Name: name, Version: ver}
			}
			upstream = u
			if c.Writer.Written() {
				return
			}
		}
		// Chart tarballs are immutable (index.yaml — the mutable index — is
		// fetched-and-rewritten above, not cached through here). A 403 from
		// the wrong member (Bitnami S3 AccessDenied on a foreign chart) must
		// not stop group first-non-404 — treat it as a miss while fanning out.
		// On a direct request the 403 is the answer: masking it would report a
		// wrong upstream credential as "no such chart".
		if c.GetBool(formats.GroupMemberKey) {
			c.Writer = forbidAsNotFound{ResponseWriter: c.Writer}
		}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, upstream, coords, "application/x-tar", 0); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	switch {
	// Helm index
	case c.Request.Method == http.MethodGet && p == "/index.yaml":
		h.serveIndex(c, repoName)

	// Upload: POST /api/charts
	case c.Request.Method == http.MethodPost && p == "/api/charts":
		h.handleUpload(c, repoName, "")

	// Upload: PUT /:chart-:version.tgz (Nexus/ChartMuseum-compatible, `curl -T`)
	case c.Request.Method == http.MethodPut && strings.HasSuffix(p, ".tgz"):
		h.handleUpload(c, repoName, path.Base(p))

	// Delete: DELETE /api/charts/:name/:version
	case c.Request.Method == http.MethodDelete && strings.HasPrefix(p, "/api/charts/"):
		h.handleDelete(c, repoName, p)

	// Download: GET /:chart-:version.tgz
	case c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead:
		h.serveFile(c, repoName, p)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) serveIndex(c *gin.Context, repoName string) {
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Limit: 500,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type chartEntry struct {
		Name        string    `yaml:"name"`
		Version     string    `yaml:"version"`
		Description string    `yaml:"description,omitempty"`
		Created     time.Time `yaml:"created"`
		URLs        []string  `yaml:"urls"`
		Digest      string    `yaml:"digest,omitempty"`
	}

	entries := map[string][]chartEntry{}
	for _, comp := range page.Items {
		tgzName := comp.Name + "-" + comp.Version + ".tgz"
		url := h.deps.BaseURL + "/repository/" + repoName + "/" + tgzName
		entries[comp.Name] = append(entries[comp.Name], chartEntry{
			Name:    comp.Name,
			Version: comp.Version,
			Created: comp.CreatedAt,
			URLs:    []string{url},
		})
	}

	index := map[string]any{
		"apiVersion": "v1",
		"entries":    entries,
		"generated":  time.Now().UTC().Format(time.RFC3339),
	}

	data, err := yaml.Marshal(index)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/yaml", data)
}

// handleUpload stores an uploaded chart. pathFilename, when non-empty (PUT to the
// .tgz path), supplies the chart filename; otherwise it comes from the multipart
// part or the X-Chart-Name header.
func (h *Handler) handleUpload(c *gin.Context, repoName, pathFilename string) {
	var chartName, version, filename string
	var data []byte
	var size int64

	ct := c.GetHeader("Content-Type")
	if pathFilename == "" && strings.HasPrefix(ct, "multipart/form-data") {
		if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		f, fh, err := c.Request.FormFile("chart")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing 'chart' file"})
			return
		}
		defer func() { _ = f.Close() }()
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(f)
		data = buf.Bytes()
		size = int64(len(data))
		filename = fh.Filename
	} else {
		// Raw body (helm push --plain-http)
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(c.Request.Body)
		data = buf.Bytes()
		size = int64(len(data))
		switch {
		case pathFilename != "":
			filename = pathFilename
		case c.GetHeader("X-Chart-Name") != "":
			filename = c.GetHeader("X-Chart-Name")
		default:
			filename = "chart.tgz"
		}
	}

	chartName, version = splitChartFilename(filename)

	filePath := "/" + filename
	coords := base.Coords{Name: chartName, Version: version}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, filePath, "application/x-tar", coords,
		bytes.NewReader(data), size); err != nil {
		c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"saved": true})
}

func (h *Handler) handleDelete(c *gin.Context, repoName, p string) {
	// /api/charts/:name/:version
	rest := strings.TrimPrefix(p, "/api/charts/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /api/charts/:name/:version"})
		return
	}
	chartName, version := parts[0], parts[1]
	filePath := "/" + chartName + "-" + version + ".tgz"
	if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, filePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (h *Handler) serveFile(c *gin.Context, repoName, filePath string) {
	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = rc.Close() }()
	if asset.SHA256 != "" {
		c.Header("X-Checksum-SHA256", asset.SHA256)
	}
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
		c.Status(http.StatusOK)
		return
	}
	c.DataFromReader(http.StatusOK, asset.SizeBytes, "application/x-tar", rc, nil)
}

// fetchAndRewriteHelmIndex fetches index.yaml from upstream, rewrites chart download
// URLs to point to this proxy, and returns the patched YAML to the client.
// The index is not cached — it is always fetched live so new upstream charts appear promptly.
func (h *Handler) fetchAndRewriteHelmIndex(c *gin.Context, repo *domain.Repository) {
	remoteBase, err := repoproxy.RemoteURL(repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	index, err := fetchHelmIndexDoc(c.Request.Context(), repo, remoteBase)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	// Rewrite each chart's download URLs to point through this proxy.
	localBase := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repo.Name + "/"
	if entries, ok := index["entries"].(map[string]any); ok {
		for key, v := range entries {
			charts, ok := v.([]any)
			if !ok {
				continue
			}
			for _, cv := range charts {
				chart, ok := cv.(map[string]any)
				if !ok {
					continue
				}
				if urls, ok := chart["urls"].([]any); ok {
					for i, u := range urls {
						if us, ok := u.(string); ok {
							urls[i] = rewriteChartURL(us, remoteBase, localBase, canonicalChartFile(chart, key))
						}
					}
					chart["urls"] = urls
				}
			}
		}
	}

	data, err := yaml.Marshal(index)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Length", fmt.Sprintf("%d", len(data)))
		c.Status(http.StatusOK)
		return
	}
	c.Data(http.StatusOK, "application/yaml", data)
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}

// chartVersionRe matches the "-<version>" tail of a chart archive name, right-anchored
// on a SemVer-shaped version. Splitting at the last dash instead would cut a
// prerelease in half: "cert-manager-v1.13.0-beta.0" is version "v1.13.0-beta.0", not
// name "cert-manager-v1.13.0" at version "beta.0".
var chartVersionRe = regexp.MustCompile(
	`-(v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$`)

// splitChartFilename splits a chart archive filename ("mychart-1.2.3.tgz") into its
// chart name and version. Charts whose version is not SemVer-shaped fall back to a
// split at the last dash; a filename with no usable dash keeps the whole stem as the
// name and gets the placeholder version "0.0.0".
func splitChartFilename(filename string) (chartName, version string) {
	stem := strings.TrimSuffix(filename, ".tgz")
	if m := chartVersionRe.FindStringSubmatchIndex(stem); m != nil {
		return stem[:m[0]], stem[m[2]:m[3]]
	}
	if lastDash := strings.LastIndex(stem, "-"); lastDash > 0 {
		return stem[:lastDash], stem[lastDash+1:]
	}
	return stem, "0.0.0"
}

// rewriteChartURL maps one `urls` entry of an upstream index.yaml onto this proxy.
//
// Helm resolves each entry as a URL reference against the repository URL (see
// helm.sh/helm/v3/pkg/repo.ResolveReferenceURL), so an entry takes one of four shapes:
//
//  1. a repository-relative path of any depth ("charts/mychart-1.2.3.tgz") — kept
//     whole, because the download handler forwards the request path upstream verbatim;
//  2. an absolute URL under the configured remote — the remote's own path prefix is
//     stripped and the remainder is treated as case 1;
//  3. an absolute URL on another host (GitHub releases) or a sibling subtree of the
//     same host — rewritten to localBase + canonicalFile so helm pull stays on this
//     proxy, which then fetches the original URL (see originForRequestPath) and caches
//     the tarball. A query string still cannot round-trip and is left for the client.
//  4. a root-relative path ("/charts/mychart-1.2.3.tgz"), which resolves against the
//     HOST root and NOT the remote's path prefix — case 2 when it happens to land
//     inside the proxied subtree, case 3 otherwise.
//
// A ".." segment is never rewritten: the cleaned path would point at a different
// upstream file than the raw one, and quietly proxying that is worse than handing
// the client the original.
//
// localBase must end in "/"; remoteBase is the repository's remote_url.
// canonicalFile is the "<name>-<version>.tgz" of the index entry this URL belongs
// to, or empty when the entry does not name both; see proxyViaBasename.
func rewriteChartURL(rawURL, remoteBase, localBase, canonicalFile string) string {
	remote, err := url.Parse(remoteBase)
	if err != nil {
		return rawURL
	}
	ref, err := url.Parse(rawURL)
	if err != nil {
		return rawURL // unparsable: leave it for the client to deal with
	}

	// Resolve the reference the way Helm does: against the remote URL with a trailing
	// slash, so a relative entry lands beside index.yaml rather than beside the
	// remote's last path segment, and a root-relative entry lands at the host root.
	resolveBase := *remote
	resolveBase.Path = strings.TrimSuffix(remote.Path, "/") + "/"
	resolveBase.RawPath = ""
	abs := resolveBase.ResolveReference(ref)

	// unproxyable hands the client the upstream URL. An entry that was already
	// absolute goes back byte for byte rather than as a re-serialized copy.
	unproxyable := func() string {
		if ref.Scheme != "" || ref.Host != "" {
			return rawURL
		}
		return abs.String()
	}

	// A query cannot survive the round trip: the download path forwards only the
	// request path upstream, so a signed or tokenized URL would be re-fetched
	// unsigned and rejected. Send the client to the original instead.
	if abs.RawQuery != "" || abs.ForceQuery {
		return unproxyable()
	}
	if strings.Contains(ref.Path, "..") {
		return unproxyable()
	}

	remotePath := path.Clean("/" + strings.Trim(remote.Path, "/"))
	absPath := path.Clean("/" + strings.TrimPrefix(abs.Path, "/"))
	inside := true
	if remotePath != "/" {
		if absPath != remotePath && !strings.HasPrefix(absPath, remotePath+"/") {
			inside = false
		} else {
			absPath = strings.TrimPrefix(absPath, remotePath)
		}
	}
	if sameUpstreamHost(abs, remote) && inside {
		return localBase + strings.TrimPrefix(absPath, "/")
	}
	return proxyViaBasename(abs, localBase, canonicalFile, unproxyable)
}

// proxyViaBasename mints a proxy path for an origin URL we can fetch by its
// absolute form (GitHub releases, a sibling subtree). The download handler
// recovers the origin by the rewritten local path, so the minted name is the
// entry's own "<name>-<version>.tgz" — that keeps two off-host charts from
// colliding on a shared basename ("widget.tgz") and files the cached component
// under the entry's coordinates. Only an entry that fails to name its chart
// falls back to the origin's basename.
func proxyViaBasename(abs *url.URL, localBase, canonicalFile string, unproxyable func() string) string {
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return unproxyable()
	}
	file := canonicalFile
	if file == "" {
		file = path.Base(abs.Path)
	}
	if file == "" || file == "." || file == "/" {
		return unproxyable()
	}
	return localBase + file
}

// canonicalChartFile is the "<name>-<version>.tgz" of one index entry, or empty
// when the entry names neither a chart (the entries key is the fallback) nor a
// version.
func canonicalChartFile(chart map[string]any, entryKey string) string {
	name := asString(chart["name"])
	if name == "" {
		name = entryKey
	}
	version := asString(chart["version"])
	if name == "" || version == "" {
		return ""
	}
	return name + "-" + version + ".tgz"
}

// forbidAsNotFound maps upstream 403 to 404 so group first-non-404 fan-out
// continues. Helm remotes such as Bitnami answer AccessDenied on a chart they
// do not host, which is a miss, not a finished lookup.
type forbidAsNotFound struct{ gin.ResponseWriter }

func (w forbidAsNotFound) WriteHeader(code int) {
	if code == http.StatusForbidden {
		code = http.StatusNotFound
	}
	w.ResponseWriter.WriteHeader(code)
}

// proxyChartUpstream returns the absolute origin URL ServeGET should fetch for
// this chart, or empty to use the request path against remote_url. The origin is
// looked up by the rewritten local path (the same path fetchAndRewriteHelmIndex
// emits), so a same-host entry named charts/widget.tgz still round-trips. On a
// group member, a readable index that does not list the path is a 404 — so a
// Bitnami proxy is not asked for a Cilium tarball. A direct request falls
// through to path forwarding: some remotes serve versions the index omits.
func (h *Handler) proxyChartUpstream(c *gin.Context, repo *domain.Repository, reqPath string) (origin, name, version string) {
	if h.deps.Assets != nil {
		if _, err := h.deps.Assets.GetByPath(c.Request.Context(), repo.Name, reqPath); err == nil {
			return "", "", ""
		} else if !errors.Is(err, repository.ErrNotFound) {
			return "", "", ""
		}
	}
	o, listed, indexOK := h.originForRequestPath(c.Request.Context(), repo, reqPath)
	if indexOK && !listed {
		if c.GetBool(formats.GroupMemberKey) {
			c.JSON(http.StatusNotFound, gin.H{"error": "chart not in upstream index"})
			return "", "", ""
		}
		return "", "", ""
	}
	if !listed || o.url == "" {
		return "", "", ""
	}
	origin = o.url
	if strings.HasSuffix(reqPath, ".prov") {
		origin += ".prov"
	}
	return origin, o.name, o.version
}

func (h *Handler) originForRequestPath(ctx context.Context, repo *domain.Repository, reqPath string) (o chartOrigin, listed, indexOK bool) {
	remoteBase, err := repoproxy.RemoteURL(repo)
	if err != nil {
		return chartOrigin{}, false, false
	}
	urls, err := h.chartURLs(ctx, repo, remoteBase)
	if err != nil {
		return chartOrigin{}, false, false
	}
	rel := strings.TrimSuffix(strings.TrimPrefix(reqPath, "/"), ".prov")
	o, ok := urls[rel]
	if !ok {
		return chartOrigin{}, false, true
	}
	return o, true, true
}

func fetchHelmIndexDoc(ctx context.Context, repo *domain.Repository, remoteBase string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteBase+"/index.yaml", nil)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream URL: %w", err)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Nexspence/1.0 (proxy)")
	}
	repoproxy.SetUpstreamAuth(req, repo)
	resp, err := repoproxy.ClientFor(repo).Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream fetch failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned %d", resp.StatusCode)
	}
	var index map[string]any
	if err := yaml.NewDecoder(resp.Body).Decode(&index); err != nil {
		return nil, fmt.Errorf("invalid upstream index.yaml: %w", err)
	}
	return index, nil
}

func resolvedUpstreamChartURL(rawURL, remoteBase string) string {
	remote, err := url.Parse(remoteBase)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	resolveBase := *remote
	resolveBase.Path = strings.TrimSuffix(remote.Path, "/") + "/"
	resolveBase.RawPath = ""
	abs := resolveBase.ResolveReference(ref)
	if abs.RawQuery != "" || abs.ForceQuery || strings.Contains(ref.Path, "..") {
		return ""
	}
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return ""
	}
	return abs.String()
}

// sameUpstreamHost reports whether two URLs address the same upstream host. The
// scheme itself is ignored — an index served over https routinely lists http URLs and
// the reverse — but each scheme's default port is normalized away first, so an entry
// on "host:443" and a remote of bare "host" are recognized as one upstream.
func sameUpstreamHost(a, b *url.URL) bool {
	return normalizedHost(a, b.Scheme) == normalizedHost(b, a.Scheme)
}

// normalizedHost lowercases u's host and drops the port when it is the default for
// u's scheme. fallbackScheme supplies the scheme for a protocol-relative reference.
func normalizedHost(u *url.URL, fallbackScheme string) string {
	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		scheme = strings.ToLower(fallbackScheme)
	}
	host := strings.ToLower(u.Host)
	switch scheme {
	case "http":
		return strings.TrimSuffix(host, ":80")
	case "https":
		return strings.TrimSuffix(host, ":443")
	}
	return host
}
