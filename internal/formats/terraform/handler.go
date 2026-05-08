// Package terraform implements a Terraform provider/module registry mirror.
//
// Terraform Registry Protocol v1:
//
//	GET /repository/<repo>/.well-known/terraform.json              → service discovery
//	GET /repository/<repo>/v1/providers/:ns/:type/versions         → list provider versions
//	GET /repository/<repo>/v1/providers/:ns/:type/:ver/download/:os/:arch → provider binary redirect
//	PUT /repository/<repo>/v1/providers/:ns/:type/:ver/upload/:os/:arch  → upload binary (hosted)
//	GET /repository/<repo>/v1/modules/:ns/:name/:provider/versions        → list module versions
//	GET /repository/<repo>/v1/modules/:ns/:name/:provider/:ver/download   → module download redirect
//	PUT /repository/<repo>/v1/modules/:ns/:name/:provider/:ver            → upload module (hosted)
//
// Proxy (mirror) mode: API calls forwarded to registry.terraform.io (or custom remote_url),
// provider binaries cached via repoproxy.ServeGET.
// Hosted mode: provider/module binaries stored in blob store, index served from DB.
package terraform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

type Handler struct{ deps formats.Deps }

func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }
func (h *Handler) Name() string      { return "terraform" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	// Service discovery is served locally for all repo types.
	if c.Request.Method == http.MethodGet && p == "/.well-known/terraform.json" {
		h.serveDiscovery(c, repoName)
		return
	}

	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		h.serveProxy(c, repo, repoName, p)
		return
	}

	switch {
	case strings.HasPrefix(p, "/v1/providers/"):
		h.serveHostedProvider(c, repoName, p)
	case strings.HasPrefix(p, "/v1/modules/"):
		h.serveHostedModule(c, repoName, p)
	default:
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown terraform endpoint"})
	}
}

// serveHostedProvider dispatches hosted provider requests.
func (h *Handler) serveHostedProvider(c *gin.Context, repoName, p string) {
	// PUT /v1/providers/<ns>/<type>/<ver>/upload/<os>/<arch>  → upload binary
	if c.Request.Method == http.MethodPut && strings.Contains(p, "/upload/") {
		h.handleProviderUpload(c, repoName, p)
		return
	}
	// GET /v1/providers/<ns>/<type>/<ver>/download/<os>/<arch> → binary download redirect
	if c.Request.Method == http.MethodGet && strings.Contains(p, "/download/") {
		h.handleProviderDownload(c, repoName, p)
		return
	}
	// GET /v1/providers/<ns>/<type>/versions → list versions
	if c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/versions") {
		h.handleProviderVersions(c, repoName, p)
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "unknown provider endpoint"})
}

func (h *Handler) handleProviderUpload(c *gin.Context, repoName, p string) {
	// p = /v1/providers/<ns>/<type>/<ver>/upload/<os>/<arch>
	rest := strings.TrimPrefix(p, "/v1/providers/")
	parts := strings.Split(rest, "/")
	// parts: [ns, type, ver, "upload", os, arch]
	if len(parts) != 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/providers/<ns>/<type>/<ver>/upload/<os>/<arch>"})
		return
	}
	ns, typ, ver, _, osName, arch := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]
	filePath := fmt.Sprintf("/v1/providers/%s/%s/%s/%s_%s.zip", ns, typ, ver, osName, arch)

	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	coords := base.Coords{Group: ns, Name: typ, Version: ver}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps, repoName, filePath,
		"application/zip", coords, bytes.NewReader(data), int64(len(data))); err != nil {
		c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"saved": true})
}

func (h *Handler) handleProviderVersions(c *gin.Context, repoName, p string) {
	// p = /v1/providers/<ns>/<type>/versions
	rest := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/providers/"), "/versions")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/providers/<ns>/<type>/versions"})
		return
	}
	ns, typ := parts[0], parts[1]

	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName,
		Group:      ns,
		Name:       typ,
		Limit:      500,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type platform struct {
		OS   string `json:"os"`
		Arch string `json:"arch"`
	}
	type version struct {
		Version   string     `json:"version"`
		Protocols []string   `json:"protocols"`
		Platforms []platform `json:"platforms"`
	}

	seen := map[string]*version{}
	for _, comp := range page.Items {
		if _, ok := seen[comp.Version]; !ok {
			seen[comp.Version] = &version{
				Version:   comp.Version,
				Protocols: []string{"5.0"},
			}
		}
		if osStr, ok := comp.Extra["os"].(string); ok {
			if archStr, ok := comp.Extra["arch"].(string); ok {
				seen[comp.Version].Platforms = append(seen[comp.Version].Platforms,
					platform{OS: osStr, Arch: archStr})
			}
		}
	}

	versions := make([]*version, 0, len(seen))
	for _, v := range seen {
		versions = append(versions, v)
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

func (h *Handler) handleProviderDownload(c *gin.Context, repoName, p string) {
	// p = /v1/providers/<ns>/<type>/<ver>/download/<os>/<arch>
	rest := strings.TrimPrefix(p, "/v1/providers/")
	parts := strings.Split(rest, "/")
	// parts: [ns, type, ver, "download", os, arch]
	if len(parts) != 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/providers/<ns>/<type>/<ver>/download/<os>/<arch>"})
		return
	}
	ns, typ, ver, _, osName, arch := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]
	filePath := fmt.Sprintf("/v1/providers/%s/%s/%s/%s_%s.zip", ns, typ, ver, osName, arch)

	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	rc.Close() // only need metadata

	downloadURL := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repoName + filePath
	c.JSON(http.StatusOK, gin.H{
		"os":           osName,
		"arch":         arch,
		"filename":     fmt.Sprintf("terraform-provider-%s_%s_%s_%s.zip", typ, ver, osName, arch),
		"download_url": downloadURL,
		"sha256_sum":   asset.SHA256,
	})
}

// serveHostedModule dispatches hosted module requests.
func (h *Handler) serveHostedModule(c *gin.Context, repoName, p string) {
	// GET /v1/modules/<ns>/<name>/<provider>/versions
	if c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/versions") {
		h.handleModuleVersions(c, repoName, p)
		return
	}
	// GET /v1/modules/<ns>/<name>/<provider>/<ver>/download
	if c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/download") {
		h.handleModuleDownload(c, repoName, p)
		return
	}
	// PUT /v1/modules/<ns>/<name>/<provider>/<ver>
	if c.Request.Method == http.MethodPut {
		h.handleModuleUpload(c, repoName, p)
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "unknown module endpoint"})
}

func (h *Handler) handleModuleVersions(c *gin.Context, repoName, p string) {
	rest := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/modules/"), "/versions")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/modules/<ns>/<name>/<provider>/versions"})
		return
	}
	ns, name, provider := parts[0], parts[1], parts[2]

	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName,
		Group:      ns + "/" + name,
		Name:       provider,
		Limit:      500,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	versions := make([]map[string]string, 0, len(page.Items))
	for _, comp := range page.Items {
		versions = append(versions, map[string]string{"version": comp.Version})
	}
	c.JSON(http.StatusOK, gin.H{"modules": []map[string]any{{"versions": versions}}})
}

func (h *Handler) handleModuleUpload(c *gin.Context, repoName, p string) {
	// p = /v1/modules/<ns>/<name>/<provider>/<ver>
	rest := strings.TrimPrefix(p, "/v1/modules/")
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) != 4 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/modules/<ns>/<name>/<provider>/<ver>"})
		return
	}
	ns, name, provider, ver := parts[0], parts[1], parts[2], parts[3]
	filePath := fmt.Sprintf("/v1/modules/%s/%s/%s/%s.tar.gz", ns, name, provider, ver)

	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	coords := base.Coords{Group: ns + "/" + name, Name: provider, Version: ver}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps, repoName, filePath,
		"application/x-tar", coords, bytes.NewReader(data), int64(len(data))); err != nil {
		c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"saved": true})
}

func (h *Handler) handleModuleDownload(c *gin.Context, repoName, p string) {
	// p = /v1/modules/<ns>/<name>/<provider>/<ver>/download
	rest := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/modules/"), "/download")
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) != 4 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/modules/<ns>/<name>/<provider>/<ver>/download"})
		return
	}
	ns, name, provider, ver := parts[0], parts[1], parts[2], parts[3]
	filePath := fmt.Sprintf("/v1/modules/%s/%s/%s/%s.tar.gz", ns, name, provider, ver)
	downloadURL := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repoName + filePath
	c.Header("X-Terraform-Get", downloadURL)
	c.Status(http.StatusNoContent)
}

// serveDiscovery returns the Terraform service discovery document.
func (h *Handler) serveDiscovery(c *gin.Context, repoName string) {
	base := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repoName
	c.JSON(http.StatusOK, gin.H{
		"providers.v1": base + "/v1/providers/",
		"modules.v1":   base + "/v1/modules/",
	})
}

func (h *Handler) serveProxy(c *gin.Context, repo *domain.Repository, repoName, p string) {
	remoteBase, err := repoproxy.RemoteURL(repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Provider binary downloads: cache in blob store.
	// Pattern: /v1/providers/<ns>/<type>/<ver>/download/<os>/<arch>
	if strings.HasPrefix(p, "/v1/providers/") && strings.Contains(p, "/download/") {
		coords := base.Coords{Name: p}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/zip"); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}
	// Module downloads: cache in blob store.
	// Pattern: /v1/modules/<ns>/<name>/<provider>/<ver>/download (redirect target)
	if strings.HasPrefix(p, "/v1/modules/") && !strings.HasSuffix(p, "/download") {
		// Module source archives (the actual .tar.gz URLs, not the /download redirect)
		coords := base.Coords{Name: p}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/x-tar"); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	// All other API calls (versions, download redirect): proxy JSON response.
	upstreamURL := strings.TrimRight(remoteBase, "/") + p
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, upstreamURL, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Header.Set("Accept", "application/json")

	resp, err := repoproxy.UpstreamClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.Status(resp.StatusCode)
		io.Copy(c.Writer, resp.Body)
		return
	}

	// For /download endpoints that return a redirect header (X-Terraform-Get):
	if xGet := resp.Header.Get("X-Terraform-Get"); xGet != "" {
		c.Header("X-Terraform-Get", xGet)
		c.Status(http.StatusNoContent)
		return
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "parse upstream JSON: " + err.Error()})
		return
	}

	localBase := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repoName
	rewriteTerraformURLs(body, localBase)

	c.JSON(http.StatusOK, body)
}

// rewriteTerraformURLs rewrites download_url fields so provider binaries route through Nexspence.
func rewriteTerraformURLs(body map[string]any, localBase string) {
	// Provider download response has "download_url" pointing to releases.hashicorp.com.
	// We rewrite it to route through our proxy so the binary gets cached.
	if u, ok := body["download_url"].(string); ok {
		// Extract the path portion and route through our /v1/providers-dl/ prefix.
		if parsed, err := url.Parse(u); err == nil {
			body["download_url"] = localBase + parsed.Path
		}
	}
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
