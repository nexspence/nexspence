// Package helm implements the Helm chart repository protocol.
//
// GET  /index.yaml              → Chart.yaml index (generated from DB)
// GET  /:chart-:version.tgz    → download chart archive
// POST /api/charts              → upload chart (multipart or raw body)
// DELETE /api/charts/:name/:ver → delete chart version
package helm

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// Handler serves the Helm chart repository protocol.
type Handler struct{ deps formats.Deps }

// New creates a Helm format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

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
		coords := base.Coords{Name: strings.TrimSuffix(strings.TrimPrefix(p, "/"), ".tgz")}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/x-tar"); err != nil {
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
		h.handleUpload(c, repoName)

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

func (h *Handler) handleUpload(c *gin.Context, repoName string) {
	var chartName, version, filename string
	var data []byte
	var size int64

	ct := c.GetHeader("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
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
		filename = c.GetHeader("X-Chart-Name")
		if filename == "" {
			filename = "chart.tgz"
		}
	}

	// Parse name-version from filename: "mychart-1.2.3.tgz"
	base2 := strings.TrimSuffix(filename, ".tgz")
	lastDash := strings.LastIndex(base2, "-")
	if lastDash > 0 {
		chartName = base2[:lastDash]
		version = base2[lastDash+1:]
	} else {
		chartName = base2
		version = "0.0.0"
	}

	filePath := "/" + filename
	coords := base.Coords{Name: chartName, Version: version}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, filePath, "application/x-tar", coords,
		bytes.NewReader(data), size); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteBase+"/index.yaml", nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid upstream URL: " + err.Error()})
		return
	}
	resp, err := repoproxy.ClientFor(repo).Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream fetch failed: " + err.Error()})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("upstream returned %d", resp.StatusCode)})
		return
	}

	var index map[string]any
	if err := yaml.NewDecoder(resp.Body).Decode(&index); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "invalid upstream index.yaml: " + err.Error()})
		return
	}

	// Rewrite each chart's download URLs to point through this proxy.
	localBase := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repo.Name + "/"
	if entries, ok := index["entries"].(map[string]any); ok {
		for _, v := range entries {
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
							urls[i] = localBase + path.Base(us)
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
