package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// PromotionHandler serves the staging and build-promotion REST endpoints.
type PromotionHandler struct {
	svc *service.PromotionService
}

// NewPromotionHandler constructs a PromotionHandler backed by the given promotion service.
func NewPromotionHandler(svc *service.PromotionService) *PromotionHandler {
	return &PromotionHandler{svc: svc}
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
	c.JSON(http.StatusOK, rules)
}

type promotionRuleInput struct {
	Name                  string `json:"name"`
	FromRepo              string `json:"from_repo"`
	ToRepo                string `json:"to_repo"`
	PathFilter            string `json:"path_filter"`
	RequireScanPass       bool   `json:"require_scan_pass"`
	RequireManualApproval bool   `json:"require_manual_approval"`
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
		PathFilter: inp.PathFilter, RequireScanPass: inp.RequireScanPass,
		RequireManualApproval: inp.RequireManualApproval,
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
		PathFilter: inp.PathFilter, RequireScanPass: inp.RequireScanPass,
		RequireManualApproval: inp.RequireManualApproval,
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
	c.JSON(http.StatusOK, rules)
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

	requests, err := h.svc.Promote(c.Request.Context(), body.RuleID, body.ComponentIDs, uid)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"requests": requests})
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
	c.JSON(http.StatusOK, requests)
}

// Approve handles POST /api/v1/promotion/requests/:id/approve (admin only)
func (h *PromotionHandler) Approve(c *gin.Context) {
	reviewerID, _ := c.Get("userID")
	uid, _ := reviewerID.(string)
	if err := h.svc.Approve(c.Request.Context(), c.Param("id"), uid); err != nil {
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
