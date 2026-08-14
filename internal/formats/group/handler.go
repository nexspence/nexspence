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
	// A routing rule — the only path-level policy a group has, since RBAC grants
	// a repository rather than a path inside it — is matched against the path as
	// requested, while the member that finally serves it normalizes the path
	// first. A path that names the same artifact a second way would therefore be
	// checked as one string and served as another. Refused rather than quietly
	// rewritten, so no legitimate path changes meaning on the way through: an
	// artifact path never contains a ".." segment.
	if hasTraversalSegment(c.Param("path")) {
		c.JSON(http.StatusBadRequest, gin.H{"error": `path must not contain a ".." segment`})
		return
	}

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

// hasTraversalSegment reports whether p has a ".." path segment. A name that
// merely contains dots ("we..ird.txt") is an ordinary artifact name and passes.
func hasTraversalSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
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

		rec := h.callMember(ctx, c, memberName, filePath, handler)

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

// callMember invokes a member's format handler on a cloned request/recorder,
// asking the question the client asked.
func (h *Handler) callMember(ctx context.Context, c *gin.Context, memberName, filePath string, handler formats.FormatHandler) *httptest.ResponseRecorder {
	return h.callMemberWithQuery(ctx, c, memberName, filePath, c.Request.URL.RawQuery, handler)
}

// callMemberWithQuery is callMember with the member's query spelled out, so a
// merged index can ask members something the client did not ask literally — see
// formats.GroupIndexPaginator for why a paginated index must.
func (h *Handler) callMemberWithQuery(ctx context.Context, c *gin.Context, memberName, filePath, rawQuery string,
	handler formats.FormatHandler,
) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	sub, _ := gin.CreateTestContext(rec)
	sub.Request = c.Request.Clone(ctx)
	// Clone deep-copies the URL, so the client's own request is untouched.
	sub.Request.URL.RawQuery = rawQuery
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
//
// A format implementing formats.GroupIndexStrictMerger inverts both defaults for
// the failures it calls fatal: the member's own response is relayed instead of
// being merged around, and a merge that fails is reported as one. See that
// interface for why an index nobody may read as short needs it.
func (h *Handler) serveMergedIndex(c *gin.Context, repoDef *domain.Repository, members []string,
	rule *domain.RoutingRule, merger formats.GroupIndexMerger, source, requestPath string,
) {
	strict, isStrict := merger.(formats.GroupIndexStrictMerger)
	pager, isPager := merger.(formats.GroupIndexPaginator)

	// A paginated index is asked of members without the client's paging
	// arguments: a member that truncated its own contribution would put the
	// entries past its cut out of reach of every later page.
	memberQuery := c.Request.URL.RawQuery
	if isPager {
		memberQuery = pager.GroupIndexMemberQuery(source, memberQuery)
	}

	parts, contributing, failure := h.collectIndexParts(c, repoDef, members, rule, strict, source, memberQuery)
	if failure != nil {
		// Relayed verbatim: the member's handler already phrased the failure in
		// its own protocol's error shape, and re-wrapping it would cost the
		// client the reason.
		h.relayMemberFailure(c, failure.member, failure.rec)
		return
	}

	if len(parts) == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"error": fmt.Sprintf("index not found in any member of group %q", repoDef.Name),
		})
		return
	}

	c.Writer.Header().Set("X-Nexspence-Source", strings.Join(contributing, ","))
	body, contentType, err := h.mergeIndexParts(c, repoDef, members, rule, merger, strict, requestPath, parts)
	if err == nil && isPager {
		// Paged after the merge, so the page and the cursor that walks it are
		// both cut out of the order the client is being served.
		body, err = pager.PageGroupIndex(c, requestPath, body)
	}
	if err != nil {
		if isStrict {
			// A member's document that could not be merged is a member whose
			// contribution is missing from the result, which is the same fact a
			// fatal member failure reports.
			c.JSON(http.StatusBadGateway, gin.H{
				"error": fmt.Sprintf("members of group %q could not be merged into one index, so the answer "+
					"would be incomplete: %v", repoDef.Name, err),
			})
			return
		}
		// Degrade to the first member's document rather than failing the group.
		c.Data(http.StatusOK, "application/octet-stream", parts[0].Body)
		return
	}
	c.Data(http.StatusOK, contentType, body)
}

// memberFailure is a member answer the merger calls fatal — the member could not
// be consulted, so the group must not present a result merged around it.
type memberFailure struct {
	member string
	rec    *httptest.ResponseRecorder
}

// collectIndexParts fans source out to every eligible member and returns their
// 2xx bodies in member order (member order = priority). A member that failed is
// skipped so one down upstream cannot take the group with it, unless the merger
// calls that failure fatal — then it comes back for the caller to relay.
func (h *Handler) collectIndexParts(c *gin.Context, repoDef *domain.Repository, members []string,
	rule *domain.RoutingRule, strict formats.GroupIndexStrictMerger, source, memberQuery string,
) ([]formats.GroupIndexPart, []string, *memberFailure) {
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

		rec := h.callMemberWithQuery(ctx, c, memberName, source, memberQuery, handler)
		code := rec.Code
		if code == 0 {
			code = http.StatusOK
		}
		if code < 200 || code > 299 {
			if strict != nil && strict.GroupIndexMemberFailureIsFatal(source, code) {
				return nil, nil, &memberFailure{member: memberName, rec: rec}
			}
			continue
		}
		parts = append(parts, formats.GroupIndexPart{Member: memberName, Body: rec.Body.Bytes()})
		contributing = append(contributing, memberName)
	}
	return parts, contributing, nil
}

// mergeIndexParts merges the collected member bodies. A format whose document
// describes other index documents (apt Release, yum repomd.xml) is additionally
// handed a fetcher for the group's own merged bodies at those paths, so the
// checksums it writes describe what the group actually serves.
func (h *Handler) mergeIndexParts(c *gin.Context, repoDef *domain.Repository, members []string,
	rule *domain.RoutingRule, merger formats.GroupIndexMerger, strict formats.GroupIndexStrictMerger,
	requestPath string, parts []formats.GroupIndexPart,
) ([]byte, string, error) {
	dependent, ok := merger.(formats.GroupIndexDependentMerger)
	if !ok {
		return merger.MergeGroupIndex(repoDef.Name, requestPath, parts)
	}
	return dependent.MergeGroupIndexWithFetch(repoDef.Name, requestPath, parts,
		h.subIndexFetcher(c, repoDef, members, rule, merger, strict))
}

// subIndexFetcher returns the group's own merged body at another index path.
// Member responses are collected once per source path and reused, so a document
// covering both the plain and the gzipped flavor of an index costs one fan-out.
func (h *Handler) subIndexFetcher(c *gin.Context, repoDef *domain.Repository, members []string,
	rule *domain.RoutingRule, merger formats.GroupIndexMerger, strict formats.GroupIndexStrictMerger,
) formats.GroupIndexFetcher {
	cache := map[string][]formats.GroupIndexPart{}
	return func(p string) ([]byte, error) {
		// The path a merger asks for is built from its members' documents, which
		// for a proxy member is whatever the upstream serves. It gets the same
		// answer a client would: a traversal segment is not a path here either.
		if hasTraversalSegment(p) {
			return nil, fmt.Errorf("index path %q must not contain a \"..\" segment", p)
		}
		source, ok := merger.GroupIndexSourcePath(p)
		if !ok {
			source = p
		}
		parts, cached := cache[source]
		if !cached {
			var failure *memberFailure
			parts, _, failure = h.collectIndexParts(c, repoDef, members, rule, strict, source, "")
			if failure != nil {
				return nil, fmt.Errorf("member %q could not serve %q", failure.member, source)
			}
			cache[source] = parts
		}
		if len(parts) == 0 {
			return nil, fmt.Errorf("index %q not found in any member of group %q", p, repoDef.Name)
		}
		// Merged without a fetcher of its own: a sub-index describes artifacts,
		// not other indexes, and letting one reach back through the group layer
		// would be a cycle waiting to happen.
		body, _, err := merger.MergeGroupIndex(repoDef.Name, p, parts)
		return body, err
	}
}

// relayMemberFailure passes a member's failed response through unchanged, so the
// client reads the reason in the format's own error shape.
func (h *Handler) relayMemberFailure(c *gin.Context, memberName string, rec *httptest.ResponseRecorder) {
	for k, vals := range rec.Header() {
		for _, v := range vals {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.Header().Set("X-Nexspence-Source", memberName)
	c.Status(rec.Code)
	if c.Request.Method != http.MethodHead && rec.Body.Len() > 0 {
		_, _ = io.Copy(c.Writer, rec.Body)
	}
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
