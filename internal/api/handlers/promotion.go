package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// PromotionHandler serves the staging and build-promotion REST endpoints.
type PromotionHandler struct {
	svc     *service.PromotionService
	repos   repository.RepositoryRepo
	rbacSvc *service.RBACService
}

// NewPromotionHandler constructs a PromotionHandler backed by the given promotion service.
func NewPromotionHandler(svc *service.PromotionService) *PromotionHandler {
	return &PromotionHandler{svc: svc}
}

// WithRBAC wires the repository repo and RBAC service so ListRules/
// GetComponentRules hide rules naming a repository the caller has no browse
// privilege over. Nil-safe: unset, both endpoints keep their prior
// (unfiltered) behavior.
func (h *PromotionHandler) WithRBAC(repos repository.RepositoryRepo, rbacSvc *service.RBACService) *PromotionHandler {
	h.repos = repos
	h.rbacSvc = rbacSvc
	return h
}

// visibleRules filters rules to ones the caller can browse both FromRepo and
// ToRepo. A rule names both repositories, so browse on only one side would
// still leak the other side's name, path filter, and approval gate — e.g.
// someone with access to a "staging" repo could enumerate the production
// repositories it promotes into. Nil-safe: with WithRBAC unset, returns rules
// unchanged.
func (h *PromotionHandler) visibleRules(c *gin.Context, rules []domain.PromotionRule) []domain.PromotionRule {
	if h.repos == nil || h.rbacSvc == nil {
		return rules
	}
	ctx := c.Request.Context()
	userID, _ := c.Get("userID")
	roles, _ := c.Get("roles")
	userIDStr, _ := userID.(string)
	rolesSlice, _ := roles.([]string)

	repoCache := map[string]*domain.Repository{}
	canBrowse := func(name string) bool {
		repo, cached := repoCache[name]
		if !cached {
			repo, _ = h.repos.Get(ctx, name)
			repoCache[name] = repo
		}
		if repo == nil {
			return false
		}
		ok, _ := h.rbacSvc.CanAccessRepo(ctx, userIDStr, rolesSlice, repo, "", "browse")
		return ok
	}

	visible := make([]domain.PromotionRule, 0, len(rules))
	for _, r := range rules {
		if canBrowse(r.FromRepo) && canBrowse(r.ToRepo) {
			visible = append(visible, r)
		}
	}
	return visible
}

// ListRules handles GET /api/v1/promotion/rules
func (h *PromotionHandler) ListRules(c *gin.Context) {
	rules, err := h.svc.ListRules(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rules == nil {
		rules = []domain.PromotionRule{}
	}
	// This endpoint sits under the non-admin authed group and, unfiltered,
	// returns every promotion rule system-wide — full topology (source repo,
	// target repo, path filter, approval gate) regardless of privilege.
	rules = h.visibleRules(c, rules)
	c.JSON(http.StatusOK, rules)
}

type promotionRuleInput struct {
	Name                  string   `json:"name"`
	FromRepo              string   `json:"from_repo"`
	ToRepo                string   `json:"to_repo"`
	PathFilter            string   `json:"path_filter"`
	RequireScanPass       bool     `json:"require_scan_pass"`
	ScanFailSeverities    []string `json:"scan_fail_severities"`
	RequireManualApproval bool     `json:"require_manual_approval"`
	AutoPromote           bool     `json:"auto_promote"`
}

// CreateRule handles POST /api/v1/promotion/rules (admin only)
func (h *PromotionHandler) CreateRule(c *gin.Context) {
	var inp promotionRuleInput
	if err := c.ShouldBindJSON(&inp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rule := &domain.PromotionRule{
		Name: inp.Name, FromRepo: inp.FromRepo, ToRepo: inp.ToRepo,
		PathFilter: inp.PathFilter, RequireScanPass: inp.RequireScanPass, ScanFailSeverities: inp.ScanFailSeverities,
		RequireManualApproval: inp.RequireManualApproval, AutoPromote: inp.AutoPromote,
	}
	if err := h.svc.CreateRule(c.Request.Context(), rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, rule)
}

// UpdateRule handles PUT /api/v1/promotion/rules/:id (admin only)
func (h *PromotionHandler) UpdateRule(c *gin.Context) {
	var inp promotionRuleInput
	if err := c.ShouldBindJSON(&inp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rule := &domain.PromotionRule{
		ID: c.Param("id"), Name: inp.Name, FromRepo: inp.FromRepo, ToRepo: inp.ToRepo,
		PathFilter: inp.PathFilter, RequireScanPass: inp.RequireScanPass, ScanFailSeverities: inp.ScanFailSeverities,
		RequireManualApproval: inp.RequireManualApproval, AutoPromote: inp.AutoPromote,
	}
	if err := h.svc.UpdateRule(c.Request.Context(), rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rule)
}

// DeleteRule handles DELETE /api/v1/promotion/rules/:id (admin only)
func (h *PromotionHandler) DeleteRule(c *gin.Context) {
	if err := h.svc.DeleteRule(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// GetComponentRules handles GET /api/v1/components/:id/promotion-rules
func (h *PromotionHandler) GetComponentRules(c *gin.Context) {
	rules, err := h.svc.ListRulesForComponent(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if rules == nil {
		rules = []domain.PromotionRule{}
	}
	rules = h.visibleRules(c, rules)
	c.JSON(http.StatusOK, rules)
}

// authorizePromotion reports whether the caller may promote artifacts under
// this rule.
//
// The rule's own gates (scan, path filter, target write policy) decide *what*
// is promoted; this decides *who* may ask for it. The endpoint sits in the
// non-admin group, so without this check any authenticated account could
// promote whatever a rule matched — including into a repository it has no
// privilege over, and with an auto-approving rule the copy happens at once.
//
// The caller needs browse on the source repository (the promotion takes
// artifacts out of it) and write on the target (the copy puts bytes in it).
// Administrators pass through RBAC unchanged.
//
// Fails closed: with no repository repo or RBAC service wired the endpoint
// refuses instead of promoting on trust.
func (h *PromotionHandler) authorizePromotion(c *gin.Context, rule *domain.PromotionRule) bool {
	if h.repos == nil || h.rbacSvc == nil {
		return false
	}
	ctx := c.Request.Context()
	userID, _ := c.Get("userID")
	roles, _ := c.Get("roles")
	userIDStr, _ := userID.(string)
	rolesSlice, _ := roles.([]string)

	from, _ := h.repos.Get(ctx, rule.FromRepo)
	if from == nil {
		return false
	}
	if ok, _ := h.rbacSvc.CanAccessRepo(ctx, userIDStr, rolesSlice, from, "", "browse"); !ok {
		return false
	}
	to, _ := h.repos.Get(ctx, rule.ToRepo)
	if to == nil {
		return false
	}
	ok, _ := h.rbacSvc.CanAccessRepo(ctx, userIDStr, rolesSlice, to, "", "write")
	return ok
}

// Promote handles POST /api/v1/promotion/promote
// Body: { "rule_id": "...", "component_ids": ["..."] }
func (h *PromotionHandler) Promote(c *gin.Context) {
	var body struct {
		RuleID       string   `json:"rule_id"`
		ComponentIDs []string `json:"component_ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.RuleID == "" || len(body.ComponentIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rule_id and component_ids are required"})
		return
	}
	userID, _ := c.Get("userID")
	uid, _ := userID.(string)

	// The rule is read before the promotion so the caller can be checked
	// against the repositories it names. A missing rule keeps the answer it
	// always had: rule IDs are UUIDs, so naming one that does not exist
	// reveals nothing.
	rule, err := h.svc.GetRule(c.Request.Context(), body.RuleID)
	if err != nil || rule == nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "promotion rule not found: " + body.RuleID})
		return
	}
	if !h.authorizePromotion(c, rule) {
		c.JSON(http.StatusForbidden, gin.H{"error": "write privilege on the target repository is required to promote into it"})
		return
	}

	requests, err := h.svc.Promote(c.Request.Context(), body.RuleID, body.ComponentIDs, uid)
	if err != nil {
		// Two rules covering the same component is a conflict in the rules, not
		// a malformed request: the caller is told to make the configuration
		// unambiguous rather than to fix its own payload (#366).
		if errors.Is(err, service.ErrAmbiguousPromotionRule) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"requests": requests})
}

// visibleRequests keeps the requests whose rule the caller may see.
//
// A request names its rule and its component — everything the promote
// endpoint needs — so a caller who cannot browse both sides of a rule must
// not see the requests filed under it either.
//
// A request whose rule no longer exists names no repository, so there is
// nothing to check the caller against: it stays visible to administrators
// (who see everything) and is hidden from the rest.
//
// Follows visibleRules: with WithRBAC unset the requests are returned
// unchanged.
func (h *PromotionHandler) visibleRequests(c *gin.Context, requests []domain.PromotionRequest) []domain.PromotionRequest {
	if h.repos == nil || h.rbacSvc == nil {
		return requests
	}
	if IsAdmin(c) {
		return requests
	}
	rules, err := h.svc.ListRules(c.Request.Context())
	if err != nil {
		return requests
	}
	visible := make(map[string]bool, len(rules))
	for _, rule := range h.visibleRules(c, rules) {
		visible[rule.ID] = true
	}
	out := make([]domain.PromotionRequest, 0, len(requests))
	for _, req := range requests {
		if visible[req.RuleID] {
			out = append(out, req)
		}
	}
	return out
}

// ListRequests handles GET /api/v1/promotion/requests?status=pending
func (h *PromotionHandler) ListRequests(c *gin.Context) {
	status := c.Query("status")
	requests, err := h.svc.ListRequests(c.Request.Context(), status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if requests == nil {
		requests = []domain.PromotionRequest{}
	}
	requests = h.visibleRequests(c, requests)
	c.JSON(http.StatusOK, requests)
}

// Approve handles POST /api/v1/promotion/requests/:id/approve (admin only)
func (h *PromotionHandler) Approve(c *gin.Context) {
	reviewerID, _ := c.Get("userID")
	uid, _ := reviewerID.(string)
	if err := h.svc.Approve(c.Request.Context(), c.Param("id"), uid); err != nil {
		// A rule created while the request sat pending can make it ambiguous
		// only at approval time; same answer as on Promote (#366).
		if errors.Is(err, service.ErrAmbiguousPromotionRule) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Reject handles POST /api/v1/promotion/requests/:id/reject (admin only)
// Body: { "reason": "..." }
func (h *PromotionHandler) Reject(c *gin.Context) {
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	reviewerID, _ := c.Get("userID")
	uid, _ := reviewerID.(string)
	if err := h.svc.Reject(c.Request.Context(), c.Param("id"), uid, body.Reason); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
