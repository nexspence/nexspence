package conda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
)

type pkgEntry struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Build       string   `json:"build,omitempty"`
	BuildNumber int      `json:"build_number,omitempty"`
	Depends     []string `json:"depends,omitempty"`
	MD5         string   `json:"md5,omitempty"`
	SHA256      string   `json:"sha256,omitempty"`
	Size        int64    `json:"size,omitempty"`
	Subdir      string   `json:"subdir,omitempty"`
}

type repodataDoc struct {
	Info          map[string]string   `json:"info"`
	Packages      map[string]pkgEntry `json:"packages"`
	PackagesConda map[string]pkgEntry `json:"packages.conda"`
}

func buildRepodata(ctx context.Context, d formats.Deps, repoName, platform string) (*repodataDoc, error) {
	page, err := d.Components.Search(ctx, domain.SearchParams{
		Repository: repoName,
		Group:      platform,
		Limit:      5000,
	})
	if err != nil {
		return nil, fmt.Errorf("conda: list components: %w", err)
	}

	doc := &repodataDoc{
		Info:          map[string]string{"subdir": platform},
		Packages:      map[string]pkgEntry{},
		PackagesConda: map[string]pkgEntry{},
	}

	prefix := "/" + platform + "/"
	for _, comp := range page.Items {
		assets, err := d.Assets.ListByComponentID(ctx, comp.ID)
		if err != nil || len(assets) == 0 {
			continue
		}
		asset := assets[0]

		filename := asset.Path
		if len(filename) > len(prefix) && filename[:len(prefix)] == prefix {
			filename = filename[len(prefix):]
		}

		entry := pkgEntry{
			Name:    comp.Name,
			Version: comp.Version,
			Subdir:  platform,
			MD5:     asset.MD5,
			SHA256:  asset.SHA256,
			Size:    asset.SizeBytes,
		}
		if v, ok := comp.Extra["build"].(string); ok {
			entry.Build = v
		}
		if v, ok := comp.Extra["build_number"].(float64); ok {
			entry.BuildNumber = int(v)
		}
		if deps, ok := comp.Extra["depends"].([]any); ok {
			for _, dep := range deps {
				if s, ok := dep.(string); ok {
					entry.Depends = append(entry.Depends, s)
				}
			}
		}

		if isCondaFile(filename) {
			doc.PackagesConda[filename] = entry
		} else {
			doc.Packages[filename] = entry
		}
	}
	return doc, nil
}

func isCondaFile(filename string) bool {
	return len(filename) > 6 && filename[len(filename)-6:] == ".conda"
}

func (h *Handler) serveIndex(c *gin.Context, repoName, platform string) {
	doc, err := buildRepodata(c.Request.Context(), h.deps, repoName, platform)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	data, err := json.Marshal(doc)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json", data)
}
