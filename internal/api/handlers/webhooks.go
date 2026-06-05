package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// WebhookHandler handles CRUD for webhook subscriptions.
type WebhookHandler struct {
	svc *service.WebhookService
}

// NewWebhookHandler constructs a WebhookHandler backed by the given webhook service.
func NewWebhookHandler(svc *service.WebhookService) *WebhookHandler {
	return &WebhookHandler{svc: svc}
}

// List handles GET /api/v1/webhooks
func (h *WebhookHandler) List(c *gin.Context) {
	hooks, err := h.svc.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, hooks)
}

// Get handles GET /api/v1/webhooks/:id
func (h *WebhookHandler) Get(c *gin.Context) {
	wh, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "webhook not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, wh)
}

// Create handles POST /api/v1/webhooks
func (h *WebhookHandler) Create(c *gin.Context) {
	var wh domain.Webhook
	if err := c.ShouldBindJSON(&wh); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.Create(c.Request.Context(), &wh); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, wh)
}

// Update handles PUT /api/v1/webhooks/:id
func (h *WebhookHandler) Update(c *gin.Context) {
	var wh domain.Webhook
	if err := c.ShouldBindJSON(&wh); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	wh.ID = c.Param("id")
	if err := h.svc.Update(c.Request.Context(), &wh); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, wh)
}

// Delete handles DELETE /api/v1/webhooks/:id
func (h *WebhookHandler) Delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// Test handles POST /api/v1/webhooks/:id/test
// Sends a synchronous test ping and returns the remote HTTP status + latency.
func (h *WebhookHandler) Test(c *gin.Context) {
	res, err := h.svc.Test(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) || err.Error() == "webhook \""+c.Param("id")+"\" not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "webhook not found"})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}
