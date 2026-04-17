package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// ComponentHandler handles /service/rest/v1/components endpoints.
type ComponentHandler struct {
	components repository.ComponentRepo
	assets     repository.AssetRepo
	baseURL    string
}

func NewComponentHandler(components repository.ComponentRepo, assets repository.AssetRepo, baseURL string) *ComponentHandler {
	return &ComponentHandler{components: components, assets: assets, baseURL: baseURL}
}

// List handles GET /service/rest/v1/components?repository=X
func (h *ComponentHandler) List(c *gin.Context) {
	repoName := c.Query("repository")
	if repoName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repository parameter is required"})
		return
	}

	limit := 25
	offset := 0
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	if tok := c.Query("continuationToken"); tok != "" {
		if v, err := strconv.Atoi(tok); err == nil {
			offset = v
		}
	}

	page, err := h.components.List(c.Request.Context(), repoName, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Attach download URLs to assets
	items := page.Items
	if items == nil {
		items = []domain.Component{}
	}
	for i := range items {
		h.enrichComponent(c, &items[i])
	}

	c.JSON(http.StatusOK, gin.H{
		"items":             items,
		"continuationToken": page.ContinuationToken,
	})
}

// Get handles GET /service/rest/v1/components/:id
func (h *ComponentHandler) Get(c *gin.Context) {
	id := c.Param("id")
	comp, err := h.components.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if comp == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "component not found"})
		return
	}
	h.enrichComponent(c, comp)
	c.JSON(http.StatusOK, comp)
}

// Delete handles DELETE /service/rest/v1/components/:id
func (h *ComponentHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.components.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// Search handles GET /service/rest/v1/search
func (h *ComponentHandler) Search(c *gin.Context) {
	p := domain.SearchParams{
		Repository:      c.Query("repository"),
		Format:          c.Query("format"),
		Group:           c.Query("group"),
		Name:            c.Query("name"),
		Version:         c.Query("version"),
		SHA256:          c.Query("sha256"),
		MavenGroupID:    c.Query("maven.groupId"),
		MavenArtifactID: c.Query("maven.artifactId"),
		MavenVersion:    c.Query("maven.baseVersion"),
		DockerImageName: c.Query("docker.imageName"),
		DockerImageTag:  c.Query("docker.imageTag"),
		Limit:           50,
	}
	if tok := c.Query("continuationToken"); tok != "" {
		if v, _ := strconv.Atoi(tok); v > 0 {
			p.Offset = v
		}
	}

	page, err := h.components.Search(c.Request.Context(), p)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	items := page.Items
	if items == nil {
		items = []domain.Component{}
	}
	for i := range items {
		h.enrichComponent(c, &items[i])
	}

	c.JSON(http.StatusOK, gin.H{
		"items":             items,
		"continuationToken": page.ContinuationToken,
	})
}

// SearchAssets handles GET /service/rest/v1/search/assets
func (h *ComponentHandler) SearchAssets(c *gin.Context) {
	p := domain.SearchParams{
		Repository: c.Query("repository"),
		Format:     c.Query("format"),
		Name:       c.Query("name"),
		SHA256:     c.Query("sha256"),
		Limit:      50,
	}
	if tok := c.Query("continuationToken"); tok != "" {
		if v, _ := strconv.Atoi(tok); v > 0 {
			p.Offset = v
		}
	}

	page, err := h.assets.SearchAssets(c.Request.Context(), p)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	items := page.Items
	if items == nil {
		items = []domain.Asset{}
	}
	for i := range items {
		items[i].DownloadURL = h.baseURL + "/repository/" + items[i].Repository + items[i].Path
	}

	c.JSON(http.StatusOK, gin.H{
		"items":             items,
		"continuationToken": page.ContinuationToken,
	})
}

func (h *ComponentHandler) enrichComponent(_ *gin.Context, comp *domain.Component) {
	for i := range comp.Assets {
		comp.Assets[i].DownloadURL = h.baseURL + "/repository/" + comp.Repository + comp.Assets[i].Path
	}
}
