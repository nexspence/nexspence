package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

type PrivilegeHandler struct {
	repo     repository.PrivilegeRepo
	roleRepo repository.RoleRepo
}

func NewPrivilegeHandler(repo repository.PrivilegeRepo, roleRepo repository.RoleRepo) *PrivilegeHandler {
	return &PrivilegeHandler{repo: repo, roleRepo: roleRepo}
}

// List handles GET /service/rest/v1/security/privileges
func (h *PrivilegeHandler) List(c *gin.Context) {
	items, err := h.repo.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []domain.Privilege{}
	}
	c.JSON(http.StatusOK, items)
}

// Get handles GET /service/rest/v1/security/privileges/:id
func (h *PrivilegeHandler) Get(c *gin.Context) {
	p, err := h.repo.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "privilege not found"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// Create handles POST /service/rest/v1/security/privileges
func (h *PrivilegeHandler) Create(c *gin.Context) {
	var p domain.Privilege
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if p.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if p.Type == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type is required"})
		return
	}
	if err := h.repo.Create(c.Request.Context(), &p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, p)
}

// Update handles PUT /service/rest/v1/security/privileges/:id
func (h *PrivilegeHandler) Update(c *gin.Context) {
	var p domain.Privilege
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p.ID = c.Param("id")
	if err := h.repo.Update(c.Request.Context(), &p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

// Delete handles DELETE /service/rest/v1/security/privileges/:id
func (h *PrivilegeHandler) Delete(c *gin.Context) {
	if err := h.repo.Delete(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// SetRolePrivileges handles PUT /service/rest/v1/security/roles/:id/privileges
// Body: {"privilegeIds": ["uuid1", "uuid2"]}
func (h *PrivilegeHandler) SetRolePrivileges(c *gin.Context) {
	roleID := c.Param("id")
	var req struct {
		PrivilegeIDs []string `json:"privilegeIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.roleRepo.SetPrivileges(c.Request.Context(), roleID, req.PrivilegeIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ListRolePrivileges handles GET /service/rest/v1/security/roles/:id/privileges
func (h *PrivilegeHandler) ListRolePrivileges(c *gin.Context) {
	items, err := h.repo.ListByRole(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []domain.Privilege{}
	}
	c.JSON(http.StatusOK, items)
}
