package handlers

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
)

type rawBrowseNode struct {
	Kind        string           `json:"kind"` // "folder" | "file"
	Label       string           `json:"label"`
	Path        string           `json:"path"`
	Size        int64            `json:"size,omitempty"`
	SHA256      string           `json:"sha256,omitempty"`
	ContentType string           `json:"contentType,omitempty"`
	UpdatedAt   string           `json:"updatedAt,omitempty"` // RFC3339
	ComponentID string           `json:"componentId,omitempty"`
	Children    []*rawBrowseNode `json:"children,omitempty"`
}

// RawTree handles GET /api/v1/browse/repositories/:name/raw-tree
func (h *BrowseHandler) RawTree(c *gin.Context) {
	repoName := c.Param("name")
	ctx := c.Request.Context()

	repo, err := h.repos.Get(ctx, repoName)
	if err != nil || repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}
	if !strings.EqualFold(string(repo.Format), string(domain.FormatRaw)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repository is not a raw format"})
		return
	}

	repoNames := []string{repoName}
	if repo.Type == domain.TypeGroup {
		repoNames = domain.GroupMemberNames(repo)
		if len(repoNames) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"repository": repoName,
				"format":     "raw",
				"root":       &rawBrowseNode{Kind: "folder", Label: "/", Path: "/", Children: []*rawBrowseNode{}},
			})
			return
		}
	}

	rows, err := h.assets.ListRawBrowseAssets(ctx, repoNames)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("userID")
	roles, _ := c.Get("roles")

	paths := make([]string, len(rows))
	for i, r := range rows {
		paths[i] = r.Path
	}
	allowed := h.rbac.FilterPaths(ctx, stringVal(userID), stringSliceVal(roles),
		repoName, repo.AllowAnonymous, paths)
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, p := range allowed {
		allowedSet[p] = struct{}{}
	}
	filtered := rows[:0]
	for _, r := range rows {
		if _, ok := allowedSet[r.Path]; ok {
			filtered = append(filtered, r)
		}
	}

	root := buildRawTree(filtered)

	c.JSON(http.StatusOK, gin.H{
		"repository": repoName,
		"format":     "raw",
		"root":       root,
	})
}

func buildRawTree(assets []domain.RawBrowseAsset) *rawBrowseNode {
	root := &rawBrowseNode{Kind: "folder", Label: "/", Path: "/", Children: []*rawBrowseNode{}}
	for _, a := range assets {
		insertRawAsset(root, a)
	}
	sortRawChildren(root)
	return root
}

func insertRawAsset(root *rawBrowseNode, a domain.RawBrowseAsset) {
	p := strings.TrimLeft(a.Path, "/")
	if p == "" {
		return
	}
	segs := strings.Split(p, "/")
	cur := root
	curPath := "/"
	for i, seg := range segs {
		if seg == "" {
			continue
		}
		if curPath == "/" {
			curPath = "/" + seg
		} else {
			curPath = curPath + "/" + seg
		}
		if i == len(segs)-1 {
			// file leaf
			leaf := &rawBrowseNode{
				Kind:        "file",
				Label:       seg,
				Path:        curPath,
				Size:        a.SizeBytes,
				SHA256:      a.SHA256,
				ContentType: a.ContentType,
				ComponentID: a.ComponentID,
			}
			if !a.UpdatedAt.IsZero() {
				leaf.UpdatedAt = a.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
			}
			cur.Children = append(cur.Children, leaf)
		} else {
			cur = rawGetOrCreateFolder(cur, seg, curPath)
		}
	}
}

func rawGetOrCreateFolder(n *rawBrowseNode, label, nodePath string) *rawBrowseNode {
	for _, ch := range n.Children {
		if ch.Kind == "folder" && ch.Label == label {
			return ch
		}
	}
	ch := &rawBrowseNode{Kind: "folder", Label: label, Path: nodePath, Children: []*rawBrowseNode{}}
	n.Children = append(n.Children, ch)
	return ch
}

func sortRawChildren(n *rawBrowseNode) {
	sort.Slice(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.Kind != b.Kind {
			return a.Kind == "folder" && b.Kind != "folder"
		}
		return strings.ToLower(a.Label) < strings.ToLower(b.Label)
	})
	for _, ch := range n.Children {
		sortRawChildren(ch)
	}
}
