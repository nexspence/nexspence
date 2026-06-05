package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// ReplicationHandler serves the content-replication REST endpoints.
type ReplicationHandler struct {
	svc *service.ReplicationService
}

// NewReplicationHandler constructs a ReplicationHandler backed by the given replication service.
func NewReplicationHandler(svc *service.ReplicationService) *ReplicationHandler {
	return &ReplicationHandler{svc: svc}
}

// List handles GET /api/v1/replication/rules
func (h *ReplicationHandler) List(c *gin.Context) {
	rules, err := h.svc.ListRules(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rules)
}

type ruleInput struct {
	Name           string `json:"name"`
	SourceRepo     string `json:"source_repo"`
	TargetURL      string `json:"target_url"`
	TargetRepo     string `json:"target_repo"`
	TargetUsername string `json:"target_username"`
	TargetPassword string `json:"target_password"` // plaintext, never stored
	CronExpr       string `json:"cron_expr"`
	Enabled        bool   `json:"enabled"`
}

// Create handles POST /api/v1/replication/rules
func (h *ReplicationHandler) Create(c *gin.Context) {
	var inp ruleInput
	if err := c.ShouldBindJSON(&inp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if inp.Name == "" || inp.SourceRepo == "" || inp.TargetURL == "" || inp.TargetRepo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name, source_repo, target_url, target_repo are required"})
		return
	}
	if inp.CronExpr == "" {
		inp.CronExpr = "0 2 * * *"
	}
	rule := &domain.ReplicationRule{
		Name:           inp.Name,
		SourceRepo:     inp.SourceRepo,
		TargetURL:      inp.TargetURL,
		TargetRepo:     inp.TargetRepo,
		TargetUsername: inp.TargetUsername,
		CronExpr:       inp.CronExpr,
		Enabled:        inp.Enabled,
	}
	if err := h.svc.CreateRule(c.Request.Context(), rule, inp.TargetPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	go h.svc.ReloadRule(c.Request.Context(), rule.ID)
	c.JSON(http.StatusCreated, rule)
}

// Update handles PUT /api/v1/replication/rules/:id
func (h *ReplicationHandler) Update(c *gin.Context) {
	var inp ruleInput
	if err := c.ShouldBindJSON(&inp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rule := &domain.ReplicationRule{
		ID:             c.Param("id"),
		Name:           inp.Name,
		SourceRepo:     inp.SourceRepo,
		TargetURL:      inp.TargetURL,
		TargetRepo:     inp.TargetRepo,
		TargetUsername: inp.TargetUsername,
		CronExpr:       inp.CronExpr,
		Enabled:        inp.Enabled,
	}
	if err := h.svc.UpdateRule(c.Request.Context(), rule, inp.TargetPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	go h.svc.ReloadRule(c.Request.Context(), rule.ID)
	c.JSON(http.StatusOK, rule)
}

// Delete handles DELETE /api/v1/replication/rules/:id
func (h *ReplicationHandler) Delete(c *gin.Context) {
	if err := h.svc.DeleteRule(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ManualRun handles POST /api/v1/replication/rules/:id/run
func (h *ReplicationHandler) ManualRun(c *gin.Context) {
	id := c.Param("id")
	_, err := h.svc.GetRule(c.Request.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "rule not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	go func() {
		_ = h.svc.RunRule(c.Request.Context(), id)
	}()
	c.JSON(http.StatusAccepted, gin.H{"message": "replication started"})
}

// TestConnection handles POST /api/v1/replication/rules/:id/test
func (h *ReplicationHandler) TestConnection(c *gin.Context) {
	err := h.svc.TestConnection(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) || err.Error() == "rule not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "rule not found"})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ListHistory handles GET /api/v1/replication/rules/:id/history
func (h *ReplicationHandler) ListHistory(c *gin.Context) {
	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	hist, err := h.svc.ListHistory(c.Request.Context(), c.Param("id"), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, hist)
}
