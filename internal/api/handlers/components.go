package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// ComponentHandler handles /service/rest/v1/components endpoints.
type ComponentHandler struct {
	components repository.ComponentRepo
	assets     repository.AssetRepo
	repos      repository.RepositoryRepo
	rbacSvc    *service.RBACService
	baseURL    string
}

func NewComponentHandler(components repository.ComponentRepo, assets repository.AssetRepo, repos repository.RepositoryRepo, baseURL string) *ComponentHandler {
	return &ComponentHandler{components: components, assets: assets, repos: repos, baseURL: baseURL}
}

// WithRBAC attaches the RBAC service so search/list results are filtered by
// content-selector privileges.
func (h *ComponentHandler) WithRBAC(rbac *service.RBACService) *ComponentHandler {
	h.rbacSvc = rbac
	return h
}

// allowAnonMap loads AllowAnonymous for each unique repository name in the
// result set. One DB call per distinct repo name, called at most once per request.
func (h *ComponentHandler) allowAnonMap(ctx context.Context, repoNames []string) map[string]bool {
	m := make(map[string]bool, len(repoNames))
	for _, name := range repoNames {
		if _, ok := m[name]; ok {
			continue
		}
		if r, err := h.repos.Get(ctx, name); err == nil && r != nil {
			m[name] = r.AllowAnonymous
		}
	}
	return m
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

	names, err := expandGroupMemberRepoNames(c.Request.Context(), h.repos, repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(names) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"items":             []domain.Component{},
			"continuationToken": nil,
		})
		return
	}

	page, err := h.components.ListByRepoNames(c.Request.Context(), names, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	items := page.Items
	if items == nil {
		items = []domain.Component{}
	}
	if h.rbacSvc != nil {
		anonMap := h.allowAnonMap(c.Request.Context(), names)
		userID, _ := c.Get("userID")
		roles, _ := c.Get("roles")
		items = h.rbacSvc.FilterComponents(c.Request.Context(),
			stringVal(userID), stringSliceVal(roles), items, anonMap)
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
	assets, err := h.assets.ListByComponentID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	comp.Assets = assets
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

// SetTags handles PUT /service/rest/v1/components/:id/tags
func (h *ComponentHandler) SetTags(c *gin.Context) {
	id := c.Param("id")
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON"})
		return
	}
	if body.Tags == nil {
		body.Tags = []string{}
	}
	clean := make([]string, 0, len(body.Tags))
	for _, t := range body.Tags {
		t = strings.TrimSpace(t)
		if t != "" {
			clean = append(clean, t)
		}
	}
	if err := h.components.SetTags(c.Request.Context(), id, clean); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tags": clean})
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
		Tag:             c.Query("tag"),
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

	if p.Repository != "" {
		names, err := expandGroupMemberRepoNames(c.Request.Context(), h.repos, p.Repository)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if len(names) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"items":             []domain.Component{},
				"continuationToken": nil,
			})
			return
		}
		p.RepositoryNames = names
		p.Repository = ""
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
	if h.rbacSvc != nil && len(items) > 0 {
		// Collect unique repo names from results (may span group members).
		repoSet := make(map[string]struct{}, 4)
		for _, comp := range items {
			repoSet[comp.Repository] = struct{}{}
		}
		repoList := make([]string, 0, len(repoSet))
		for n := range repoSet {
			repoList = append(repoList, n)
		}
		anonMap := h.allowAnonMap(c.Request.Context(), repoList)
		userID, _ := c.Get("userID")
		roles, _ := c.Get("roles")
		items = h.rbacSvc.FilterComponents(c.Request.Context(),
			stringVal(userID), stringSliceVal(roles), items, anonMap)
	}
	// Preload assets so the UI gets path/lastModified for each component.
	// Search response caps at 50 items, so N+1 is acceptable here.
	for i := range items {
		if len(items[i].Assets) == 0 {
			assets, err := h.assets.ListByComponentID(c.Request.Context(), items[i].ID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			items[i].Assets = assets
		}
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

	if p.Repository != "" {
		names, err := expandGroupMemberRepoNames(c.Request.Context(), h.repos, p.Repository)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if len(names) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"items":             []domain.Asset{},
				"continuationToken": nil,
			})
			return
		}
		p.RepositoryNames = names
		p.Repository = ""
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
	if h.rbacSvc != nil && len(items) > 0 {
		repoSet := make(map[string]struct{}, 4)
		for _, a := range items {
			repoSet[a.Repository] = struct{}{}
		}
		repoList := make([]string, 0, len(repoSet))
		for n := range repoSet {
			repoList = append(repoList, n)
		}
		anonMap := h.allowAnonMap(c.Request.Context(), repoList)
		userID, _ := c.Get("userID")
		roles, _ := c.Get("roles")
		items = h.rbacSvc.FilterAssets(c.Request.Context(),
			stringVal(userID), stringSliceVal(roles), items, anonMap)
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

// GetQuota handles GET /api/v1/repositories/:name/quota
// Returns current storage usage and quota limit for the repository.
func (h *ComponentHandler) GetQuota(c *gin.Context) {
	name := c.Param("name")
	repo, err := h.repos.Get(c.Request.Context(), name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}

	used, err := h.assets.SumSizeByRepo(c.Request.Context(), name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := gin.H{
		"repository": name,
		"usedBytes":  used,
		"quotaBytes": repo.QuotaBytes,
	}
	if repo.QuotaBytes != nil && *repo.QuotaBytes > 0 {
		resp["percentUsed"] = float64(used) / float64(*repo.QuotaBytes) * 100
	} else {
		resp["percentUsed"] = nil
	}
	c.JSON(http.StatusOK, resp)
}
