package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// UserHandler handles user management endpoints (Nexus-compatible).
type UserHandler struct {
	svc *service.UserService
}

func NewUserHandler(svc *service.UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

// List handles GET /service/rest/v1/security/users
func (h *UserHandler) List(c *gin.Context) {
	source := c.Query("source")
	users, err := h.svc.List(c.Request.Context(), source)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if users == nil {
		users = []domain.User{}
	}
	// Strip password hashes before returning
	for i := range users {
		users[i].PasswordHash = ""
	}
	c.JSON(http.StatusOK, users)
}

// Get handles GET /service/rest/v1/security/users/:userId
func (h *UserHandler) Get(c *gin.Context) {
	username := c.Param("userId")
	u, err := h.svc.Get(c.Request.Context(), username)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	u.PasswordHash = ""
	c.JSON(http.StatusOK, u)
}

// Create handles POST /service/rest/v1/security/users
func (h *UserHandler) Create(c *gin.Context) {
	var req struct {
		domain.User
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.svc.Create(c.Request.Context(), &req.User, req.Password); err != nil {
		if isAlreadyExists(err) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if isInvalidInput(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	req.PasswordHash = ""
	c.JSON(http.StatusCreated, req.User)
}

// Update handles PUT /service/rest/v1/security/users/:userId
func (h *UserHandler) Update(c *gin.Context) {
	username := c.Param("userId")

	var updates domain.User
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	u, err := h.svc.Update(c.Request.Context(), username, &updates)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	u.PasswordHash = ""
	c.JSON(http.StatusOK, u)
}

// Delete handles DELETE /service/rest/v1/security/users/:userId
func (h *UserHandler) Delete(c *gin.Context) {
	username := c.Param("userId")
	if err := h.svc.Delete(c.Request.Context(), username); err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ChangePassword handles PUT /service/rest/v1/security/users/:userId/change-password
func (h *UserHandler) ChangePassword(c *gin.Context) {
	username := c.Param("userId")

	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Admin can set password without old password; self-change requires old password
	callerRoles, _ := c.Get("roles")
	isAdmin := hasRole(callerRoles, "nx-admin")
	callerUsername, _ := c.Get("username")

	if !isAdmin && callerUsername.(string) == username {
		if err := h.svc.ChangePassword(c.Request.Context(), username, req.OldPassword, req.NewPassword); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	} else if isAdmin {
		if err := h.svc.SetPassword(c.Request.Context(), username, req.NewPassword); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	} else {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot change another user's password"})
		return
	}

	c.Status(http.StatusNoContent)
}

func hasRole(roles any, role string) bool {
	if roles == nil {
		return false
	}
	list, ok := roles.([]string)
	if !ok {
		return false
	}
	for _, r := range list {
		if r == role {
			return true
		}
	}
	return false
}
