package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// RBACMiddleware enforces repository access control on artifact endpoints.
// Must run after OptionalAuth (which sets "userID" and "roles" in context).
func RBACMiddleware(rbacSvc *service.RBACService, repoRepo repository.RepositoryRepo) gin.HandlerFunc {
	return func(c *gin.Context) {
		repoName := c.Param("repoName")

		// /v2/repository/:repoName/*dockerpath uses param name "dockerpath", not "path".
		path, _ := c.Params.Get("path")
		if path == "" {
			path, _ = c.Params.Get("dockerpath")
		}
		if path == "" {
			path = "/"
		}

		action := methodToAction(c.Request.Method)

		userID, _ := c.Get("userID")
		roles, _ := c.Get("roles")
		userIDStr, _ := userID.(string)
		rolesSlice, _ := roles.([]string)

		repo, err := repoRepo.Get(c.Request.Context(), repoName)
		if err != nil || repo == nil {
			denyAccess(c, userIDStr)
			return
		}

		ok, err := rbacSvc.CanAccessRepo(c.Request.Context(), userIDStr, rolesSlice, repo, path, action)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "access check failed"})
			return
		}
		if !ok {
			denyAccess(c, userIDStr)
			return
		}
		c.Next()
	}
}

// denyAccess returns 401 for unauthenticated requests (so Docker/clients can retry with
// credentials) and 403 for authenticated users who lack permission.
func denyAccess(c *gin.Context, userIDStr string) {
	if userIDStr == "" {
		c.Header("WWW-Authenticate", `Basic realm="Nexspence"`)
		if strings.HasPrefix(c.Request.URL.Path, "/v2/") {
			c.Header("Docker-Distribution-API-Version", "registry/2.0")
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "access denied"})
}

func methodToAction(method string) string {
	switch method {
	case http.MethodPut, http.MethodPost, http.MethodPatch:
		return "write"
	case http.MethodDelete:
		return "delete"
	default:
		return "read"
	}
}
