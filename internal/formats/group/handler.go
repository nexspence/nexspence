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
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

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

	// Index documents are merged across ALL members (member order = priority)
	// instead of first-non-404: a single member's index would hide the others,
	// and formats whose "not found" is an empty 200 would shadow every member
	// behind them (#99).
	if merger, ok := h.formatRegistry[string(repoDef.Format)].(formats.GroupIndexMerger); ok {
		if source, isIndex := merger.GroupIndexSourcePath(filePath); isIndex {
			h.serveMergedIndex(c, repoDef, members, rule, merger, source, filePath)
			return
		}
	}

	for _, memberName := range members {
		if !service.Allow(rule, filePath) {
			continue
		}
		memberRepo := h.eligibleMember(ctx, memberName, repoDef)
		if memberRepo == nil {
			continue
		}
		handler, ok := h.formatRegistry[string(memberRepo.Format)]
		if !ok {
			continue
		}

		rec := h.callMember(c, ctx, memberName, filePath, handler)

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

// eligibleMember resolves a member repo eligible for fan-out: online,
// not a nested group, and format-matched to the group. Returns nil otherwise.
func (h *Handler) eligibleMember(ctx context.Context, memberName string, groupDef *domain.Repository) *domain.Repository {
	memberRepo, err := h.deps.Repos.Get(ctx, memberName)
	if err != nil || memberRepo == nil || !memberRepo.Online {
		return nil
	}
	if memberRepo.Type == domain.TypeGroup {
		return nil
	}
	if string(memberRepo.Format) != string(groupDef.Format) {
		return nil
	}
	return memberRepo
}

// callMember invokes a member's format handler on a cloned request/recorder.
func (h *Handler) callMember(c *gin.Context, ctx context.Context, memberName, filePath string, handler formats.FormatHandler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	sub, _ := gin.CreateTestContext(rec)
	sub.Request = c.Request.Clone(ctx)
	sub.Params = gin.Params{
		{Key: "repoName", Value: memberName},
		{Key: "path", Value: filePath},
	}
	handler.ServeHTTP(sub)
	sub.Writer.WriteHeaderNow() // flush buffered status to rec.Code
	return rec
}

// serveMergedIndex fans an index request out to every eligible member on the
// merger's source path, collects the 2xx bodies (member order preserved,
// failing members skipped so the group survives a down upstream), and serves
// the merged document. A merge failure degrades to the first member body —
// never a 500 for a merge bug.
func (h *Handler) serveMergedIndex(c *gin.Context, repoDef *domain.Repository, members []string,
	rule *domain.RoutingRule, merger formats.GroupIndexMerger, source, requestPath string,
) {
	ctx := c.Request.Context()

	var parts []formats.GroupIndexPart
	var contributing []string
	for _, memberName := range members {
		if !service.Allow(rule, source) {
			continue
		}
		memberRepo := h.eligibleMember(ctx, memberName, repoDef)
		if memberRepo == nil {
			continue
		}
		handler, ok := h.formatRegistry[string(memberRepo.Format)]
		if !ok {
			continue
		}

		rec := h.callMember(c, ctx, memberName, source, handler)
		code := rec.Code
		if code == 0 {
			code = http.StatusOK
		}
		if code < 200 || code > 299 {
			continue
		}
		parts = append(parts, formats.GroupIndexPart{Member: memberName, Body: rec.Body.Bytes()})
		contributing = append(contributing, memberName)
	}

	if len(parts) == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"error": fmt.Sprintf("index not found in any member of group %q", repoDef.Name),
		})
		return
	}

	c.Writer.Header().Set("X-Nexspence-Source", strings.Join(contributing, ","))
	body, contentType, err := merger.MergeGroupIndex(repoDef.Name, requestPath, parts)
	if err != nil {
		// Degrade to the first member's document rather than failing the group.
		c.Data(http.StatusOK, "application/octet-stream", parts[0].Body)
		return
	}
	c.Data(http.StatusOK, contentType, body)
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
