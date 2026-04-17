// Package nuget implements the NuGet v2/v3 repository protocol.
//
// NuGet v3 endpoints (under /repository/:repoName/):
//   GET  /index.json                        → service index (v3)
//   GET  /v3/registration/:id/index.json    → package registration (metadata)
//   GET  /v3/flatcontainer/:id/index.json   → version list
//   GET  /v3/flatcontainer/:id/:ver/:id.:ver.nupkg → download
//
// NuGet v2 endpoints:
//   GET  /FindPackagesById()?id='name'      → OData XML
//   PUT  /v2/package                        → nuget push (multipart)
//   DELETE /v2/packages/:id/:ver            → delete
package nuget

import (
	"encoding/xml"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

type Handler struct{ deps formats.Deps }

func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }
func (h *Handler) Name() string      { return "nuget" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	// Proxy: block mutations, proxy reads through to upstream (e.g. nuget.org)
	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		coords := base.Coords{}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/octet-stream"); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	switch {
	// v3 service index
	case c.Request.Method == http.MethodGet && p == "/index.json":
		h.serveIndex(c, repoName)

	// v3 flat container: version list
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/v3/flatcontainer/") && strings.HasSuffix(p, "/index.json"):
		pkgID := strings.TrimSuffix(strings.TrimPrefix(p, "/v3/flatcontainer/"), "/index.json")
		pkgID = strings.Trim(pkgID, "/")
		h.serveVersionList(c, repoName, pkgID)

	// v3 flat container: download nupkg
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/v3/flatcontainer/") && strings.HasSuffix(p, ".nupkg"):
		h.serveFlatContainerDownload(c, repoName, p)

	// v3 registration index
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/v3/registration/"):
		h.serveRegistration(c, repoName, p)

	// v2 OData query: FindPackagesById()
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/FindPackagesById"):
		pkgID := c.Query("id")
		pkgID = strings.Trim(pkgID, "'")
		h.serveFindPackages(c, repoName, pkgID)

	// v2 push
	case c.Request.Method == http.MethodPut && p == "/v2/package":
		h.handlePush(c, repoName)

	// v2 delete: DELETE /v2/packages/:id/:ver
	case c.Request.Method == http.MethodDelete && strings.HasPrefix(p, "/v2/packages/"):
		rest := strings.TrimPrefix(p, "/v2/packages/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v2/packages/:id/:version"})
			return
		}
		filePath := "/" + parts[0] + "/" + parts[1] + "/" + parts[0] + "." + parts[1] + ".nupkg"
		if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, filePath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) serveIndex(c *gin.Context, repoName string) {
	base2 := h.deps.BaseURL + "/repository/" + repoName
	c.JSON(http.StatusOK, gin.H{
		"version": "3.0.0",
		"resources": []gin.H{
			{"@id": base2 + "/v3/flatcontainer/", "@type": "PackageBaseAddress/3.0.0"},
			{"@id": base2 + "/v3/registration/", "@type": "RegistrationsBaseUrl/3.0.0"},
			{"@id": base2 + "/v2/", "@type": "LegacyGallery/2.0.0"},
		},
	})
}

func (h *Handler) serveVersionList(c *gin.Context, repoName, pkgID string) {
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Name: strings.ToLower(pkgID), Limit: 200,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	versions := make([]string, 0, len(page.Items))
	for _, comp := range page.Items {
		versions = append(versions, comp.Version)
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

func (h *Handler) serveFlatContainerDownload(c *gin.Context, repoName, p string) {
	// /v3/flatcontainer/:id/:ver/:id.:ver.nupkg
	parts := strings.Split(strings.TrimPrefix(p, "/v3/flatcontainer/"), "/")
	if len(parts) < 3 {
		c.JSON(http.StatusNotFound, gin.H{"error": "invalid nupkg path"})
		return
	}
	pkgID, version := parts[0], parts[1]
	filePath := "/" + pkgID + "/" + version + "/" + pkgID + "." + version + ".nupkg"

	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer rc.Close()
	c.DataFromReader(http.StatusOK, asset.SizeBytes, "application/zip", rc, nil)
}

func (h *Handler) serveRegistration(c *gin.Context, repoName, p string) {
	// /v3/registration/:id/index.json
	rest := strings.TrimPrefix(p, "/v3/registration/")
	pkgID := strings.TrimSuffix(rest, "/index.json")
	pkgID = strings.Trim(pkgID, "/")

	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Name: strings.ToLower(pkgID), Limit: 200,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(page.Items) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "package not found"})
		return
	}

	base2 := h.deps.BaseURL + "/repository/" + repoName
	items := make([]gin.H, 0, len(page.Items))
	for _, comp := range page.Items {
		entryURL := base2 + "/v3/registration/" + pkgID + "/" + comp.Version + ".json"
		items = append(items, gin.H{
			"@id":            entryURL,
			"packageContent": base2 + "/v3/flatcontainer/" + pkgID + "/" + comp.Version + "/" + pkgID + "." + comp.Version + ".nupkg",
			"catalogEntry": gin.H{
				"id":      comp.Name,
				"version": comp.Version,
				"listed":  true,
				"published": comp.CreatedAt.UTC().Format(time.RFC3339),
			},
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"@id":   base2 + "/v3/registration/" + pkgID + "/index.json",
		"count": 1,
		"items": []gin.H{{
			"count": len(items),
			"items": items,
			"lower": page.Items[0].Version,
			"upper": page.Items[len(page.Items)-1].Version,
		}},
	})
}

// OData v2 compatible FindPackagesById response
type feed struct {
	XMLName xml.Name `xml:"feed"`
	XMLNS   string   `xml:"xmlns,attr"`
	Entries []entry  `xml:"entry"`
}
type entry struct {
	XMLName xml.Name `xml:"entry"`
	Title   string   `xml:"title"`
	ID      string   `xml:"id"`
	Content content  `xml:"content"`
}
type content struct {
	Type string `xml:"type,attr"`
	Src  string `xml:"src,attr"`
}

func (h *Handler) serveFindPackages(c *gin.Context, repoName, pkgID string) {
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Name: strings.ToLower(pkgID), Limit: 200,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	base2 := h.deps.BaseURL + "/repository/" + repoName
	f := feed{XMLNS: "http://www.w3.org/2005/Atom"}
	for _, comp := range page.Items {
		f.Entries = append(f.Entries, entry{
			Title: comp.Name + " " + comp.Version,
			ID:    base2 + "/v2/Packages(Id='" + comp.Name + "',Version='" + comp.Version + "')",
			Content: content{
				Type: "application/zip",
				Src:  base2 + "/v3/flatcontainer/" + strings.ToLower(comp.Name) + "/" + comp.Version + "/" + strings.ToLower(comp.Name) + "." + comp.Version + ".nupkg",
			},
		})
	}
	c.Header("Content-Type", "application/atom+xml; charset=utf-8")
	c.XML(http.StatusOK, f)
}

func (h *Handler) handlePush(c *gin.Context, repoName string) {
	if err := c.Request.ParseMultipartForm(64 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	f, fh, err := c.Request.FormFile("package")
	if err != nil {
		// some clients use "file" as field name
		f, fh, err = c.Request.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing package file"})
			return
		}
	}
	defer f.Close()

	// Filename: id.version.nupkg
	filename := fh.Filename
	name2 := strings.TrimSuffix(filename, ".nupkg")
	// Split on last dot to get version (id may contain dots)
	lastDot := strings.LastIndex(name2, ".")
	pkgID, version := name2, "0.0.0"
	if lastDot > 0 {
		pkgID = name2[:lastDot]
		version = name2[lastDot+1:]
	}
	pkgID = strings.ToLower(pkgID)
	filePath := "/" + pkgID + "/" + version + "/" + pkgID + "." + version + ".nupkg"

	coords := base.Coords{Name: pkgID, Version: version}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, filePath, "application/zip", coords, f, fh.Size); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}

