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
	"encoding/json"
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
		// hosted provider — Task 8
		c.JSON(http.StatusNotImplemented, gin.H{"error": "not yet implemented"})
	case strings.HasPrefix(p, "/v1/modules/"):
		// hosted module — Task 8
		c.JSON(http.StatusNotImplemented, gin.H{"error": "not yet implemented"})
	default:
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown terraform endpoint"})
	}
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
