package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/repository"
)

// AccessGraphHandler serves GET /api/v1/security/access-graph.
type AccessGraphHandler struct {
	users     repository.UserRepo
	roles     repository.RoleRepo
	privs     repository.PrivilegeRepo
	selectors repository.ContentSelectorRepo
}

// NewAccessGraphHandler constructs an AccessGraphHandler from the RBAC repositories.
func NewAccessGraphHandler(
	users repository.UserRepo,
	roles repository.RoleRepo,
	privs repository.PrivilegeRepo,
	selectors repository.ContentSelectorRepo,
) *AccessGraphHandler {
	return &AccessGraphHandler{users: users, roles: roles, privs: privs, selectors: selectors}
}

// Response types — exported so tests can decode directly.

// GraphUser is a user node in the access-graph response, listing its role IDs.
type GraphUser struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Status   string   `json:"status"`
	Source   string   `json:"source"`
	RoleIDs  []string `json:"roleIds"`
}

// GraphRole is a role node in the access-graph response, listing its privilege and nested role IDs.
type GraphRole struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	PrivilegeIDs []string `json:"privilegeIds"`
	RoleIDs      []string `json:"roleIds"`
}

// GraphPrivilege is a privilege node in the access-graph response, optionally linked to a content selector.
type GraphPrivilege struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Type              string         `json:"type"`
	Attrs             map[string]any `json:"attrs,omitempty"`
	ContentSelectorID *string        `json:"contentSelectorId,omitempty"`
}

// GraphSelector is a content-selector node in the access-graph response.
type GraphSelector struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Expression string `json:"expression"`
}

// AccessGraphResponse is the full RBAC graph returned by the access-graph endpoint.
type AccessGraphResponse struct {
	Users      []GraphUser      `json:"users"`
	Roles      []GraphRole      `json:"roles"`
	Privileges []GraphPrivilege `json:"privileges"`
	Selectors  []GraphSelector  `json:"selectors"`
}

// Get handles GET /api/v1/security/access-graph (admin-only, wired in router).
func (h *AccessGraphHandler) Get(c *gin.Context) {
	ctx := c.Request.Context()

	users, err := h.users.List(ctx, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	roles, err := h.roles.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	privs, err := h.privs.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	selectors, err := h.selectors.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// user.Roles contains role *names*; build name→id map to resolve edges.
	roleByName := make(map[string]string, len(roles))
	for _, r := range roles {
		roleByName[r.Name] = r.ID
	}

	gUsers := make([]GraphUser, 0, len(users))
	for _, u := range users {
		roleIDs := make([]string, 0, len(u.Roles))
		for _, name := range u.Roles {
			if id, ok := roleByName[name]; ok {
				roleIDs = append(roleIDs, id)
			}
		}
		gUsers = append(gUsers, GraphUser{
			ID:       u.ID,
			Username: u.Username,
			Email:    u.Email,
			Status:   string(u.Status),
			Source:   string(u.Source),
			RoleIDs:  roleIDs,
		})
	}

	gRoles := make([]GraphRole, 0, len(roles))
	for _, r := range roles {
		privIDs := r.Privileges
		if privIDs == nil {
			privIDs = []string{}
		}
		nestedRoleIDs := r.Roles
		if nestedRoleIDs == nil {
			nestedRoleIDs = []string{}
		}
		gRoles = append(gRoles, GraphRole{
			ID:           r.ID,
			Name:         r.Name,
			Description:  r.Description,
			PrivilegeIDs: privIDs,
			RoleIDs:      nestedRoleIDs,
		})
	}

	gPrivs := make([]GraphPrivilege, 0, len(privs))
	for _, p := range privs {
		gPrivs = append(gPrivs, GraphPrivilege{
			ID:                p.ID,
			Name:              p.Name,
			Type:              string(p.Type),
			Attrs:             p.Attrs,
			ContentSelectorID: p.ContentSelectorID,
		})
	}

	gSels := make([]GraphSelector, 0, len(selectors))
	for _, s := range selectors {
		gSels = append(gSels, GraphSelector{
			ID:         s.ID,
			Name:       s.Name,
			Expression: s.Expression,
		})
	}

	c.JSON(http.StatusOK, AccessGraphResponse{
		Users:      gUsers,
		Roles:      gRoles,
		Privileges: gPrivs,
		Selectors:  gSels,
	})
}
