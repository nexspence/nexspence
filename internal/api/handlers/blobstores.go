package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// BlobStoreHandler handles blob store management endpoints.
type BlobStoreHandler struct {
	repo      repository.BlobStoreRepo
	repos     repository.RepositoryRepo
	assets    repository.AssetRepo
	gcSvc     *service.BlobGCService
	blobStore storage.BlobStore
}

func NewBlobStoreHandler(repo repository.BlobStoreRepo) *BlobStoreHandler {
	return &BlobStoreHandler{repo: repo}
}

func (h *BlobStoreHandler) WithGC(svc *service.BlobGCService) *BlobStoreHandler {
	h.gcSvc = svc
	return h
}

func (h *BlobStoreHandler) WithBlobStore(bs storage.BlobStore) *BlobStoreHandler {
	h.blobStore = bs
	return h
}

// WithUsageDeps wires the repository and asset repos used by the Usage endpoint.
func (h *BlobStoreHandler) WithUsageDeps(repos repository.RepositoryRepo, assets repository.AssetRepo) *BlobStoreHandler {
	h.repos = repos
	h.assets = assets
	return h
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

// PresignGet handles GET /api/v1/blobstores/:name/presign
// Query params: key=<blobKey>, ttl=<seconds> (default 3600).
// Returns a presigned download URL (S3 stores only).
func (h *BlobStoreHandler) PresignGet(c *gin.Context) {
	ps, ok := h.blobStore.(storage.PresignableStore)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "presigned URLs are only supported for S3 blob stores"})
		return
	}
	key := c.Query("key")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key query parameter is required"})
		return
	}
	ttlSec := int64(3600)
	if v := c.Query("ttl"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ttl must be a positive integer (seconds)"})
			return
		}
		ttlSec = n
	}
	url, err := ps.PresignGetURL(c.Request.Context(), key, time.Duration(ttlSec)*time.Second)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": url, "ttl_seconds": ttlSec})
}

// PresignPut handles POST /api/v1/blobstores/:name/presign
// Body JSON: {"key": "<blobKey>", "ttl": <seconds>}
// Returns a presigned upload URL (S3 stores only).
func (h *BlobStoreHandler) PresignPut(c *gin.Context) {
	ps, ok := h.blobStore.(storage.PresignableStore)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "presigned URLs are only supported for S3 blob stores"})
		return
	}
	var req struct {
		Key string `json:"key"`
		TTL int64  `json:"ttl"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}
	ttlSec := req.TTL
	if ttlSec <= 0 {
		ttlSec = 3600
	}
	url, err := ps.PresignPutURL(c.Request.Context(), req.Key, time.Duration(ttlSec)*time.Second)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": url, "ttl_seconds": ttlSec})
}

// ConfigureLifecycle handles PUT /api/v1/blobstores/:name/lifecycle
// Body JSON: {"expiration_days": 30}  (0 = remove all rules)
// S3 stores only.
func (h *BlobStoreHandler) ConfigureLifecycle(c *gin.Context) {
	ps, ok := h.blobStore.(storage.PresignableStore)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lifecycle configuration is only supported for S3 blob stores"})
		return
	}
	var req struct {
		ExpirationDays int32 `json:"expiration_days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ExpirationDays < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expiration_days must be >= 0 (0 removes all rules)"})
		return
	}
	if err := ps.ConfigureLifecycle(c.Request.Context(), req.ExpirationDays); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"expiration_days": req.ExpirationDays})
}

// LinkedRepoInfo is one row in the Usage response: a repository that uses the blob store.
type LinkedRepoInfo struct {
	Name       string `json:"name"`
	Format     string `json:"format"`
	Type       string `json:"type"`
	BytesUsed  int64  `json:"bytesUsed"`
}

// Usage handles GET /api/v1/blob-stores/:name/usage
// Returns the store details plus the repositories that use it and per-repo byte counts.
func (h *BlobStoreHandler) Usage(c *gin.Context) {
	if h.repos == nil || h.assets == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage deps not configured"})
		return
	}
	name := c.Param("name")
	ctx := c.Request.Context()

	bs, err := h.repo.Get(ctx, name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if bs == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "blob store not found"})
		return
	}

	linked, err := h.repos.ListByBlobStoreID(ctx, bs.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	info := make([]LinkedRepoInfo, 0, len(linked))
	var total int64
	for _, r := range linked {
		used, _ := h.assets.SumSizeByRepo(ctx, r.Name)
		info = append(info, LinkedRepoInfo{
			Name:      r.Name,
			Format:    string(r.Format),
			Type:      string(r.Type),
			BytesUsed: used,
		})
		total += used
	}

	resp := gin.H{
		"store":              bs,
		"linkedRepositories": info,
		"totalAssetBytes":    total,
	}
	if bs.QuotaBytes != nil {
		resp["quotaRemaining"] = *bs.QuotaBytes - bs.UsedBytes
	}
	c.JSON(http.StatusOK, resp)
}

// Compact handles POST /api/v1/blobstores/:name/compact
// Query param ?dry_run=true reports orphans without deleting them.
func (h *BlobStoreHandler) Compact(c *gin.Context) {
	if h.gcSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "GC service not configured"})
		return
	}
	dryRun := c.Query("dry_run") == "true"
	result, err := h.gcSvc.Compact(c.Request.Context(), dryRun)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}
