package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	users *service.UserService
}

func NewAuthHandler(users *service.UserService) *AuthHandler {
	return &AuthHandler{users: users}
}

// Login handles POST /api/v1/login  and  POST /service/rest/v1/security/users/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	token, user, err := h.users.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id":        user.ID,
			"username":  user.Username,
			"email":     user.Email,
			"firstName": user.FirstName,
			"lastName":  user.LastName,
			"roles":     user.Roles,
		},
	})
}

// Me handles GET /api/v1/me — returns info about the currently authenticated user.
func (h *AuthHandler) Me(c *gin.Context) {
	claims, ok := c.Get("claims")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	cl := claims.(*auth.Claims)

	user, err := h.users.GetByID(c.Request.Context(), cl.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":        user.ID,
		"username":  user.Username,
		"email":     user.Email,
		"firstName": user.FirstName,
		"lastName":  user.LastName,
		"roles":     user.Roles,
		"status":    user.Status,
		"source":    user.Source,
	})
}

// AuthMiddleware validates Bearer JWT or Basic auth.
func AuthMiddleware(users *service.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var token string

		// Bearer token
		if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}

		// Basic auth fallback
		if token == "" {
			if username, password, ok := c.Request.BasicAuth(); ok {
				_, user, err := users.Login(c.Request.Context(), username, password)
				if err != nil {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
					return
				}
				c.Set("username", user.Username)
				c.Set("userID", user.ID)
				c.Set("roles", user.Roles)
				c.Next()
				return
			}
		}

		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}

		claims, err := users.ValidateToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set("claims", claims)
		c.Set("username", claims.Username)
		c.Set("userID", claims.UserID)
		c.Set("roles", claims.Roles)
		c.Next()
	}
}

// OptionalAuth sets user context if a valid token is present, but does not block unauthenticated requests.
func OptionalAuth(users *service.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if authHeader := c.GetHeader("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if claims, err := users.ValidateToken(token); err == nil {
				c.Set("claims", claims)
				c.Set("username", claims.Username)
				c.Set("userID", claims.UserID)
				c.Set("roles", claims.Roles)
			}
		}
		c.Next()
	}
}
