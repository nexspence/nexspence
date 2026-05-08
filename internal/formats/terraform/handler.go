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
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
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
		// proxy implementation — Task 7
		c.JSON(http.StatusNotImplemented, gin.H{"error": "terraform proxy not yet implemented"})
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
// Stub — implemented in Task 7.
func (h *Handler) serveDiscovery(c *gin.Context, repoName string) {
	// Task 7 implementation
	c.JSON(http.StatusNotImplemented, gin.H{"error": "not yet implemented"})
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
