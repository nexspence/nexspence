// Package group implements the "group" repository type.
//
// A group repository aggregates multiple hosted/proxy repositories under one URL.
// Requests are fanned out to member repositories in order; the first successful
// response (2xx) is returned to the client.
//
// Members are stored in repo.FormatConfig["member_names"] as []interface{} (JSON array of strings).
//
// Mutations (PUT/POST/DELETE/PATCH) are rejected — group repos are read-only.
// Publishing must go directly to a hosted member.
package group

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
)

// Handler implements the group repository type.
// It holds a reference to the format registry and repository store so it can
// fan-out to member repos at request time.
type Handler struct {
	deps           formats.Deps
	formatRegistry map[string]formats.FormatHandler
}

// New creates a group handler. formatRegistry is the same map used in the router.
func New(deps formats.Deps, formatRegistry map[string]formats.FormatHandler) *Handler {
	return &Handler{deps: deps, formatRegistry: formatRegistry}
}

func (h *Handler) Name() string { return "group" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodGet, http.MethodHead:
		h.serveGet(c)
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{
			"error": "group repository is read-only — publish to a member hosted repository",
		})
	}
}

func (h *Handler) serveGet(c *gin.Context) {
	repoName := c.Param("repoName")
	filePath := c.Param("path")
	ctx := c.Request.Context()

	// Load the group repo definition to get member list.
	repoDef, err := h.deps.Repos.Get(ctx, repoName)
	if err != nil || repoDef == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group repository not found: " + repoName})
		return
	}

	members := memberNames(repoDef)
	if len(members) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "group repository has no members configured"})
		return
	}

	// Fan-out: try each member in order, return first hit.
	for _, memberName := range members {
		rc, asset, err := base.FetchArtifact(ctx, h.deps, memberName, filePath)
		if err != nil {
			continue // not in this member, try next
		}
		defer rc.Close()

		// Forward checksum headers if available.
		if asset.SHA256 != "" {
			c.Header("X-Checksum-SHA256", asset.SHA256)
			c.Header("ETag", `"`+asset.SHA256+`"`)
		}
		if asset.SHA1 != "" {
			c.Header("X-Checksum-SHA1", asset.SHA1)
		}
		if asset.MD5 != "" {
			c.Header("X-Checksum-MD5", asset.MD5)
		}
		c.Header("X-Nexspence-Source", memberName)

		if c.Request.Method == http.MethodHead {
			c.Header("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
			c.Header("Content-Type", asset.ContentType)
			c.Status(http.StatusOK)
			return
		}
		c.DataFromReader(http.StatusOK, asset.SizeBytes, asset.ContentType, rc, nil)
		return
	}

	c.JSON(http.StatusNotFound, gin.H{
		"error": fmt.Sprintf("artifact not found in any member of group %q", repoName),
	})
}

// memberNames extracts the ordered member repository names from the repository's
// FormatConfig["member_names"] field (JSON array of strings).
func memberNames(repo *domain.Repository) []string {
	if repo.FormatConfig == nil {
		return nil
	}
	raw, ok := repo.FormatConfig["member_names"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}
