// Package group implements the "group" repository type.
//
// A group repository aggregates multiple hosted/proxy repositories under one URL.
// GET/HEAD are delegated to each member's format handler in order; the first
// non-404 response is returned (so hosted, proxy cache-miss upstream fetch, and
// generated metadata all work).
//
// Members are stored in repo.FormatConfig["member_names"] as []interface{} (JSON array of strings).
//
// Mutations (PUT/POST/DELETE/PATCH) are rejected — group repos are read-only.
// Publishing must go directly to a hosted member.
package group

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
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

	members := domain.GroupMemberNames(repoDef)
	if len(members) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "group repository has no members configured"})
		return
	}

	// Fan-out: delegate to each member's format handler until one returns not-404.
	for _, memberName := range members {
		memberRepo, err := h.deps.Repos.Get(ctx, memberName)
		if err != nil || memberRepo == nil || !memberRepo.Online {
			continue
		}
		if memberRepo.Type == domain.TypeGroup {
			continue
		}
		if string(memberRepo.Format) != string(repoDef.Format) {
			continue
		}
		handler, ok := h.formatRegistry[string(memberRepo.Format)]
		if !ok {
			continue
		}

		rec := httptest.NewRecorder()
		sub, _ := gin.CreateTestContext(rec)
		sub.Request = c.Request.Clone(ctx)
		sub.Params = gin.Params{
			{Key: "repoName", Value: memberName},
			{Key: "path", Value: filePath},
		}

		handler.ServeHTTP(sub)

		code := rec.Code
		if code == 0 {
			code = http.StatusOK
		}
		if code == http.StatusNotFound {
			continue
		}

		for k, vals := range rec.Header() {
			for _, v := range vals {
				c.Writer.Header().Add(k, v)
			}
		}
		c.Writer.Header().Set("X-Nexspence-Source", memberName)
		c.Status(code)
		if c.Request.Method != http.MethodHead && rec.Body.Len() > 0 {
			_, _ = io.Copy(c.Writer, rec.Body)
		}
		return
	}

	c.JSON(http.StatusNotFound, gin.H{
		"error": fmt.Sprintf("artifact not found in any member of group %q", repoName),
	})
}

