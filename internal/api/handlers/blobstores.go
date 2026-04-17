package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// BlobStoreHandler handles blob store management endpoints.
type BlobStoreHandler struct {
	repo repository.BlobStoreRepo
}

func NewBlobStoreHandler(repo repository.BlobStoreRepo) *BlobStoreHandler {
	return &BlobStoreHandler{repo: repo}
}

// List handles GET /service/rest/v1/blobstores
func (h *BlobStoreHandler) List(c *gin.Context) {
	stores, err := h.repo.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if stores == nil {
		stores = []domain.BlobStore{}
	}
	c.JSON(http.StatusOK, stores)
}

// Get handles GET /service/rest/v1/blobstores/:name
func (h *BlobStoreHandler) Get(c *gin.Context) {
	name := c.Param("name")
	bs, err := h.repo.Get(c.Request.Context(), name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if bs == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "blob store not found"})
		return
	}
	c.JSON(http.StatusOK, bs)
}

// Create handles POST /service/rest/v1/blobstores/:type
func (h *BlobStoreHandler) Create(c *gin.Context) {
	blobType := c.Param("type")

	var bs domain.BlobStore
	if err := c.ShouldBindJSON(&bs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	bs.Type = blobType

	if bs.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	if err := h.repo.Create(c.Request.Context(), &bs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, bs)
}

// Update handles PUT /service/rest/v1/blobstores/:type/:name
func (h *BlobStoreHandler) Update(c *gin.Context) {
	name := c.Param("name")

	existing, err := h.repo.Get(c.Request.Context(), name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "blob store not found"})
		return
	}

	var updates domain.BlobStore
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updates.Name = name
	updates.Type = existing.Type

	if err := h.repo.Update(c.Request.Context(), &updates); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, updates)
}

// Delete handles DELETE /service/rest/v1/blobstores/:name
func (h *BlobStoreHandler) Delete(c *gin.Context) {
	name := c.Param("name")
	if err := h.repo.Delete(c.Request.Context(), name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
