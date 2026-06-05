// Package group implements the "group" repository type.
//
// A group repository aggregates multiple hosted/proxy repositories under one URL.
// GET/HEAD are delegated to each member's format handler in order; the first
// non-404 response is returned.
//
// PUT/POST/PATCH are forwarded to the first hosted member (or the member named
// by formatConfig["writable_member"] if set). Groups with no hosted members
// return 405.
package group

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// Handler implements the group repository type.
type Handler struct {
	deps           formats.Deps
	formatRegistry map[string]formats.FormatHandler
}

// New creates a group handler. formatRegistry is the same map used in the router.
func New(deps formats.Deps, formatRegistry map[string]formats.FormatHandler) *Handler {
	return &Handler{deps: deps, formatRegistry: formatRegistry}
}

// Name returns the format identifier.
func (h *Handler) Name() string { return "group" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodGet, http.MethodHead:
		h.serveGet(c)
	case http.MethodPut, http.MethodPost, http.MethodPatch:
		h.serveWrite(c)
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

	var rule *domain.RoutingRule
	if repoDef.RoutingRuleID != nil && h.deps.RoutingRules != nil {
		rule, _ = h.deps.RoutingRules.Get(ctx, *repoDef.RoutingRuleID)
	}

	for _, memberName := range members {
		if !service.Allow(rule, filePath) {
			continue
		}
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

func (h *Handler) serveWrite(c *gin.Context) {
	repoName := c.Param("repoName")
	filePath := c.Param("path")
	ctx := c.Request.Context()

	repoDef, err := h.deps.Repos.Get(ctx, repoName)
	if err != nil || repoDef == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group repository not found: " + repoName})
		return
	}

	// Resolve writable member: explicit config wins, then first TypeHosted member.
	targetName := domain.GroupWritableMember(repoDef)
	if targetName == "" {
		for _, memberName := range domain.GroupMemberNames(repoDef) {
			memberRepo, err := h.deps.Repos.Get(ctx, memberName)
			if err != nil || memberRepo == nil || !memberRepo.Online {
				continue
			}
			if memberRepo.Type == domain.TypeHosted && string(memberRepo.Format) == string(repoDef.Format) {
				targetName = memberName
				break
			}
		}
	}

	if targetName == "" {
		c.JSON(http.StatusMethodNotAllowed, gin.H{
			"error": "group repository has no hosted member — publish directly to a hosted repository",
		})
		return
	}

	targetRepo, err := h.deps.Repos.Get(ctx, targetName)
	if err != nil || targetRepo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "writable member not found: " + targetName})
		return
	}
	if targetRepo.Type != domain.TypeHosted || !targetRepo.Online || string(targetRepo.Format) != string(repoDef.Format) {
		c.JSON(http.StatusConflict, gin.H{"error": "writable_member is not an online hosted repository matching group format"})
		return
	}

	handler, ok := h.formatRegistry[string(targetRepo.Format)]
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no handler for format: " + string(targetRepo.Format)})
		return
	}

	rec := httptest.NewRecorder()
	sub, _ := gin.CreateTestContext(rec)
	sub.Request = c.Request.Clone(ctx)
	sub.Params = gin.Params{
		{Key: "repoName", Value: targetName},
		{Key: "path", Value: filePath},
	}

	handler.ServeHTTP(sub)
	sub.Writer.WriteHeaderNow() // flush buffered status to rec.Code

	code := rec.Code
	if code == 0 {
		code = http.StatusOK
	}
	for k, vals := range rec.Header() {
		for _, v := range vals {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Status(code)
	if rec.Body.Len() > 0 {
		_, _ = io.Copy(c.Writer, rec.Body)
	}
}
