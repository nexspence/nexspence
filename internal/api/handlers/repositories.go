package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// formatConfigOnlyKeys are the settings that live in a repository's
// formatConfig.
//
// A payload that carries one of them at the TOP level is refused with a
// message that names the right place. The decoder drops an unknown top-level
// key, and a setting that is silently dropped is a setting the caller
// believes is in force and is not: a write policy that does not deny writes,
// a pull gate that never blocks, a group that writes to a member nobody
// named.
var formatConfigOnlyKeys = []string{
	domain.WritePolicyKey,
	domain.AllowRedeployLatestKey,
	domain.PullBlockSeverityKey,
	"member_names",
	"writable_member",
}

// misplacedFormatConfigKey returns the first formatConfig key the payload
// carries at the top level, with the value as it was sent.
func misplacedFormatConfigKey(body []byte) (string, json.RawMessage, bool) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return "", nil, false
	}
	for _, key := range formatConfigOnlyKeys {
		if raw, ok := top[key]; ok {
			return key, raw, true
		}
	}
	return "", nil, false
}

// refuseMisplacedFormatConfig answers 400 when the payload carries a
// formatConfig key at the top level, and reports whether it did.
func refuseMisplacedFormatConfig(c *gin.Context, body []byte) bool {
	key, raw, ok := misplacedFormatConfigKey(body)
	if !ok {
		return false
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf(
		"%s belongs in formatConfig, not at the top level: {\"formatConfig\": {%q: %s}}",
		key, key, raw)})
	return true
}

// RepositoryHandler handles repository management endpoints.
type RepositoryHandler struct {
	svc     *service.RepositoryService
	rbacSvc *service.RBACService
}

// NewRepositoryHandler constructs a RepositoryHandler from the repository and RBAC services.
func NewRepositoryHandler(svc *service.RepositoryService, rbacSvc *service.RBACService) *RepositoryHandler {
	return &RepositoryHandler{svc: svc, rbacSvc: rbacSvc}
}

// List handles GET /service/rest/v1/repositories and GET /api/v1/repositories
func (h *RepositoryHandler) List(c *gin.Context) {
	format := c.Query("format")
	repoType := c.Query("type")

	repos, err := h.svc.List(c.Request.Context(), format, repoType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if repos == nil {
		repos = []domain.Repository{}
	}

	userID, _ := c.Get("userID")
	roles, _ := c.Get("roles")
	repos = h.rbacSvc.FilterRepos(c.Request.Context(),
		stringVal(userID), stringSliceVal(roles), repos)

	c.JSON(http.StatusOK, domain.RedactedRepositories(repos))
}

func stringVal(v any) string        { s, _ := v.(string); return s }
func stringSliceVal(v any) []string { s, _ := v.([]string); return s }

// Get handles GET /service/rest/v1/repositories/:name
func (h *RepositoryHandler) Get(c *gin.Context) {
	name := c.Param("name")
	r, err := h.svc.Get(c.Request.Context(), name)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// List and ComponentHandler.GetQuota both filter by RBAC (GetQuota citing
	// #295 explicitly); this sibling get-by-name endpoint didn't, letting any
	// authenticated user read a private repository's full config (format,
	// blob store, remote proxy URL, allowAnonymous) just by knowing or
	// guessing its name.
	userID, _ := c.Get("userID")
	roles, _ := c.Get("roles")
	visible := h.rbacSvc.FilterRepos(c.Request.Context(),
		stringVal(userID), stringSliceVal(roles), []domain.Repository{*r})
	if len(visible) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}
	c.JSON(http.StatusOK, domain.RedactedRepository(*r))
}

// Create handles POST /service/rest/v1/repositories/:format/:type
func (h *RepositoryHandler) Create(c *gin.Context) {
	format := c.Param("format")
	repoType := c.Param("type")

	// The body is read once and decoded twice: into the repository and into a
	// probe for the keys that belong in formatConfig. The decoder drops an
	// unknown top-level key, so a write_policy sent there would be ignored —
	// and a repository that does not deny the writes its caller asked it to
	// deny is worse than one that refuses the payload.
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if refuseMisplacedFormatConfig(c, body) {
		return
	}

	var r domain.Repository
	if err := json.Unmarshal(body, &r); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	r.Format = domain.RepoFormat(format)
	r.Type = domain.RepoType(repoType)

	if err := h.svc.Create(c.Request.Context(), &r); err != nil {
		if isAlreadyExists(err) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if isInvalidInput(err) || isNotFound(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, domain.RedactedRepository(r))
}

// Update handles PUT /service/rest/v1/repositories/:format/:type/:name
func (h *RepositoryHandler) Update(c *gin.Context) {
	name := c.Param("name")

	// The body is read once and decoded twice: into the repository and into a
	// presence probe. "online" and "allowAnonymous" are plain bools, so a body
	// that leaves them out decodes to false — and the service reads false as
	// "take the repository offline" and "make it private". A partial update
	// must not change what it does not mention: attaching a cleanup policy
	// (or a quota) to a repository used to take it offline, and every read of
	// it answered 503 "repository is offline" from then on.
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if refuseMisplacedFormatConfig(c, body) {
		return
	}
	var updates domain.Repository
	if err := json.Unmarshal(body, &updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var presence struct {
		Online         *bool `json:"online"`
		AllowAnonymous *bool `json:"allowAnonymous"`
	}
	if err := json.Unmarshal(body, &presence); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if presence.Online == nil || presence.AllowAnonymous == nil {
		existing, err := h.svc.Get(c.Request.Context(), name)
		if err != nil {
			if isNotFound(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if presence.Online == nil {
			updates.Online = existing.Online
		}
		if presence.AllowAnonymous == nil {
			updates.AllowAnonymous = existing.AllowAnonymous
		}
	}

	r, err := h.svc.Update(c.Request.Context(), name, &updates)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if isInvalidInput(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, domain.RedactedRepository(*r))
}

// Patch handles PATCH /service/rest/v1/repositories/:name — partial update (currently: online toggle only)
func (h *RepositoryHandler) Patch(c *gin.Context) {
	name := c.Param("name")
	var body struct {
		Online bool `json:"online"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	existing, err := h.svc.Get(c.Request.Context(), name)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	r, err := h.svc.Update(c.Request.Context(), name, &domain.Repository{
		Online:         body.Online,
		AllowAnonymous: existing.AllowAnonymous,
	})
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, domain.RedactedRepository(*r))
}

// Delete handles DELETE /service/rest/v1/repositories/:name
func (h *RepositoryHandler) Delete(c *gin.Context) {
	name := c.Param("name")
	if err := h.svc.Delete(c.Request.Context(), name); err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
