package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// TokenHandler exposes CRUD for API tokens belonging to the authenticated user.
// An admin listing of all users' tokens is not exposed — tokens are scoped to
// their owner so only the owner (or the admin impersonating them) can manage
// them.
type TokenHandler struct {
	tokens *service.TokenService
	users  *service.UserService
}

func NewTokenHandler(tokens *service.TokenService, users *service.UserService) *TokenHandler {
	return &TokenHandler{tokens: tokens, users: users}
}

// callerUserID pulls the authenticated userID out of gin context. Returns
// empty string when missing; callers must check.
func callerUserID(c *gin.Context) string {
	uid, ok := c.Get("userID")
	if !ok {
		return ""
	}
	s, _ := uid.(string)
	return s
}

// List returns tokens owned by the authenticated user.
func (h *TokenHandler) List(c *gin.Context) {
	userID := callerUserID(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	out, err := h.tokens.ListByUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

// Create issues a new token for the authenticated user. The plaintext value
// appears exactly once in the response body.
func (h *TokenHandler) Create(c *gin.Context) {
	userID := callerUserID(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	var req struct {
		Name      string     `json:"name" binding:"required"`
		Scopes    []string   `json:"scopes"`
		ExpiresAt *time.Time `json:"expiresAt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tok, err := h.tokens.Create(c.Request.Context(), userID, req.Name, req.Scopes, req.ExpiresAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, tok)
}

// Delete revokes a token the authenticated user owns. Other users' tokens are
// not addressable through this endpoint.
func (h *TokenHandler) Delete(c *gin.Context) {
	userID := callerUserID(c)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	tok, err := h.tokens.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if tok == nil || tok.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "token not found"})
		return
	}
	if err := h.tokens.Delete(c.Request.Context(), tok.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
