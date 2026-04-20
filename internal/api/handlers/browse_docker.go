package handlers

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// BrowseHandler serves Nexspence-native browse APIs.
type BrowseHandler struct {
	repos      repository.RepositoryRepo
	components repository.ComponentRepo
	assets     repository.AssetRepo
}

func NewBrowseHandler(repos repository.RepositoryRepo, components repository.ComponentRepo, assets repository.AssetRepo) *BrowseHandler {
	return &BrowseHandler{repos: repos, components: components, assets: assets}
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
func (h *BrowseHandler) PathTree(c *gin.Context) {
	repoName := c.Param("name")
	q := c.Query("q")
	ctx := c.Request.Context()

	repo, err := h.repos.Get(ctx, repoName)
	if err != nil || repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}

	paths, err := h.assets.ListPathsByRepo(ctx, repoName, q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if paths == nil {
		paths = []string{}
	}

	c.JSON(http.StatusOK, gin.H{"paths": paths})
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
