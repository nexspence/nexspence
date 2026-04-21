package handlers

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// BrowseHandler serves Nexspence-native browse APIs.
type BrowseHandler struct {
	repos      repository.RepositoryRepo
	components repository.ComponentRepo
	assets     repository.AssetRepo
	rbac       *service.RBACService
}

func NewBrowseHandler(repos repository.RepositoryRepo, components repository.ComponentRepo, assets repository.AssetRepo, rbac *service.RBACService) *BrowseHandler {
	return &BrowseHandler{repos: repos, components: components, assets: assets, rbac: rbac}
}

// dockerBrowseNode is a Nexus-style folder or leaf in the Docker browse tree.
type dockerBrowseNode struct {
	Kind        string              `json:"kind"` // folder | tag | manifest | blob
	Label       string              `json:"label"`
	Path        string              `json:"path"`
	ImageRef    string              `json:"imageRef,omitempty"`
	Version     string              `json:"version,omitempty"`
	ComponentID string              `json:"componentId,omitempty"`
	Children    []*dockerBrowseNode `json:"children,omitempty"`
}

// DockerTree handles GET /api/v1/browse/repositories/:name/docker-tree
func (h *BrowseHandler) DockerTree(c *gin.Context) {
	repoName := c.Param("name")
	ctx := c.Request.Context()

	repo, err := h.repos.Get(ctx, repoName)
	if err != nil || repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}
	if !strings.EqualFold(string(repo.Format), string(domain.FormatDocker)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repository is not a docker format"})
		return
	}

	repoNames := []string{repoName}
	if repo.Type == domain.TypeGroup {
		repoNames = domain.GroupMemberNames(repo)
		if len(repoNames) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"repository": repoName,
				"format":     "docker",
				"root":       &dockerBrowseNode{Kind: "folder", Label: "/", Path: "/"},
			})
			return
		}
	}

	rows, err := h.components.ListDockerBrowseRows(ctx, repoNames, 3000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("userID")
	roles, _ := c.Get("roles")
	rows = h.rbac.FilterDockerRows(ctx, stringVal(userID), stringSliceVal(roles),
		repoName, repo.AllowAnonymous, rows)

	root := &dockerBrowseNode{Kind: "folder", Label: "/", Path: "/"}
	for _, row := range rows {
		insertDockerBrowseRow(root, row)
	}
	sortBrowseChildren(root)

	c.JSON(http.StatusOK, gin.H{
		"repository": repoName,
		"format":     "docker",
		"root":       root,
	})
}

// PathTree handles GET /api/v1/browse/repositories/:name/path-tree
// Returns unique directory-level path prefixes from assets in the repository.
// Optional query param: q (substring filter, case-insensitive).
// For Docker repos the /blobs/ and /manifests/ storage prefixes are stripped so
// callers see image-namespace paths (e.g. /da/bas/python/) that match the
// dockerpath used by RBACMiddleware — suitable as content-selector path prefixes.
func (h *BrowseHandler) PathTree(c *gin.Context) {
	repoName := c.Param("name")
	q := c.Query("q")
	ctx := c.Request.Context()

	repo, err := h.repos.Get(ctx, repoName)
	if err != nil || repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}

	var paths []string
	if strings.EqualFold(string(repo.Format), "docker") {
		raw, err := h.assets.ListRawAssetPaths(ctx, repoName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		paths = dockerImageDirs(raw, q)
	} else {
		var err error
		paths, err = h.assets.ListPathsByRepo(ctx, repoName, q)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if paths == nil {
		paths = []string{}
	}

	userID, _ := c.Get("userID")
	roles, _ := c.Get("roles")
	paths = h.rbac.FilterPaths(ctx, stringVal(userID), stringSliceVal(roles),
		repoName, repo.AllowAnonymous, paths)

	c.JSON(http.StatusOK, gin.H{"paths": paths})
}

// dockerImageDirs extracts unique image-namespace directory paths from raw Docker
// asset paths (/blobs/da/bas/python/sha256:… , /manifests/da/bas/python/latest).
//
// Result: /da/, /da/bas/, /da/bas/python/ — all ancestor levels for every image.
// Selecting /da/bas/ in the content-selector picker produces
// path.startsWith("/da/bas/") which matches ALL dockerpath requests for images
// under that namespace (blobs, manifests, tags/list).
func dockerImageDirs(rawAssetPaths []string, q string) []string {
	seen := make(map[string]struct{})

	for _, raw := range rawAssetPaths {
		var rest string
		switch {
		case strings.HasPrefix(raw, "/blobs/"):
			rest = strings.TrimPrefix(raw, "/blobs/")
		case strings.HasPrefix(raw, "/manifests/"):
			rest = strings.TrimPrefix(raw, "/manifests/")
		default:
			continue
		}
		// rest = "da/bas/python/sha256:abc" — strip the last segment to get image name.
		slashIdx := strings.LastIndex(rest, "/")
		if slashIdx < 0 {
			continue
		}
		imageName := rest[:slashIdx] // "da/bas/python"
		if imageName == "" {
			continue
		}

		// Add /da/, /da/bas/, /da/bas/python/ — build incrementally, no double slashes.
		segs := strings.Split(imageName, "/")
		cur := ""
		for _, seg := range segs {
			if seg == "" {
				continue
			}
			cur += seg + "/"        // e.g. "da/" → "da/bas/" → "da/bas/python/"
			p := "/" + cur          // e.g. "/da/" → "/da/bas/" → "/da/bas/python/"
			if q == "" || strings.Contains(strings.ToLower(p), strings.ToLower(q)) {
				seen[p] = struct{}{}
			}
		}
	}

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dockerBrowseCategory(version, samplePath string) string {
	p := samplePath
	if strings.Contains(p, "/blobs/") {
		return "Blobs"
	}
	if strings.Contains(p, "/manifests/") {
		if strings.HasPrefix(version, "sha256:") || strings.HasPrefix(version, "sha512:") {
			return "Manifests"
		}
		return "Tags"
	}
	if strings.HasPrefix(version, "sha256:") || strings.HasPrefix(version, "sha512:") {
		return "Manifests"
	}
	return "Tags"
}

func browseJoin(base, seg string) string {
	if base == "" || base == "/" {
		return "/" + seg
	}
	return strings.TrimRight(base, "/") + "/" + seg
}

func insertDockerBrowseRow(root *dockerBrowseNode, row domain.DockerBrowseRow) {
	image := strings.Trim(row.ImageName, "/")
	if image == "" {
		return
	}
	parts := strings.Split(image, "/")
	cur := root
	curPath := "/"
	for _, seg := range parts {
		if seg == "" {
			continue
		}
		curPath = browseJoin(curPath, seg)
		cur = cur.getOrCreateFolder(seg, curPath)
	}

	cat := dockerBrowseCategory(row.Version, row.SamplePath)
	catPath := browseJoin(cur.Path, cat)
	catNode := cur.getOrCreateFolder(cat, catPath)

	leafKind := "tag"
	switch cat {
	case "Manifests":
		leafKind = "manifest"
	case "Blobs":
		leafKind = "blob"
	}
	leafPath := browseJoin(catNode.Path, row.Version)
	for _, ex := range catNode.Children {
		if ex.Path == leafPath {
			return
		}
	}
	leaf := &dockerBrowseNode{
		Kind:        leafKind,
		Label:       row.Version,
		Path:        leafPath,
		ImageRef:    image,
		Version:     row.Version,
		ComponentID: row.ComponentID,
	}
	catNode.Children = append(catNode.Children, leaf)
}

func (n *dockerBrowseNode) getOrCreateFolder(label, nodePath string) *dockerBrowseNode {
	for _, ch := range n.Children {
		if ch.Kind == "folder" && ch.Label == label {
			return ch
		}
	}
	ch := &dockerBrowseNode{Kind: "folder", Label: label, Path: nodePath, Children: []*dockerBrowseNode{}}
	n.Children = append(n.Children, ch)
	return ch
}

func sortBrowseChildren(n *dockerBrowseNode) {
	sort.Slice(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.Kind != b.Kind {
			return a.Kind == "folder" && b.Kind != "folder"
		}
		return strings.ToLower(a.Label) < strings.ToLower(b.Label)
	})
	for _, ch := range n.Children {
		sortBrowseChildren(ch)
	}
}

// DeleteByPath handles DELETE /api/v1/browse/repositories/:name/path
// Query param: path=<prefix> (required). Deletes all assets whose path starts with
// the prefix, then removes orphan components. Blobs are cleaned by the GC scheduler.
func (h *BrowseHandler) DeleteByPath(c *gin.Context) {
	repoName := c.Param("name")
	pathPrefix := c.Query("path")
	if pathPrefix == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path query param required"})
		return
	}

	ctx := c.Request.Context()
	assets, err := h.assets.ListByRepoAndPath(ctx, repoName, pathPrefix)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, a := range assets {
		if err := h.assets.Delete(ctx, a.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if err := h.components.DeleteOrphans(ctx, repoName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
