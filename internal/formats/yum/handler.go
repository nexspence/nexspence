// Package yum implements the Yum/DNF RPM repository protocol.
//
// Standard yum repository layout under /repository/:repoName/:
//   GET  /repodata/repomd.xml                  → repository metadata index
//   GET  /repodata/primary.xml[.gz]            → primary packages metadata
//   GET  /repodata/filelists.xml[.gz]          → file lists
//   GET  /repodata/other.xml[.gz]              → changelog data
//   GET  /:path/*.rpm                          → download RPM
//   PUT  /:path/*.rpm                          → upload RPM
//   DELETE /:path/*.rpm                        → delete RPM
package yum

import (
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"fmt"
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
func (h *Handler) Name() string      { return "yum" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	// Proxy: block uploads/deletes, pass reads through to upstream (e.g. dl.fedoraproject.org)
	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		ct := "application/octet-stream"
		if strings.HasSuffix(p, ".xml") {
			ct = "application/xml"
		}
		coords := base.Coords{}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, ct); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	switch {
	// repomd.xml
	case c.Request.Method == http.MethodGet && p == "/repodata/repomd.xml":
		h.serveRepomd(c, repoName)

	// primary.xml (or .gz)
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/repodata/primary"):
		h.servePrimary(c, repoName, p)

	// filelists.xml, other.xml — return empty valid XML for now
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/repodata/"):
		h.serveEmptyMetadata(c, p)

	// Upload RPM
	case c.Request.Method == http.MethodPut && strings.HasSuffix(p, ".rpm"):
		h.handleUpload(c, repoName, p)

	// Delete RPM
	case c.Request.Method == http.MethodDelete && strings.HasSuffix(p, ".rpm"):
		if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, p); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)

	// Download RPM or HEAD
	case (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && strings.HasSuffix(p, ".rpm"):
		h.serveFile(c, repoName, p)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

// repomdXML is the minimal repomd.xml structure
type repomdXML struct {
	XMLName  xml.Name      `xml:"repomd"`
	XMLNS    string        `xml:"xmlns,attr"`
	Revision int64         `xml:"revision"`
	Data     []repomdEntry `xml:"data"`
}
type repomdEntry struct {
	Type     string       `xml:"type,attr"`
	Location repomdLoc    `xml:"location"`
	Checksum repomdCksum  `xml:"checksum"`
	Timestamp int64       `xml:"timestamp"`
}
type repomdLoc struct {
	Href string `xml:"href,attr"`
}
type repomdCksum struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

func (h *Handler) serveRepomd(c *gin.Context, repoName string) {
	base2 := "/repository/" + repoName
	now := time.Now().Unix()
	doc := repomdXML{
		XMLNS:    "http://linux.duke.edu/metadata/repo",
		Revision: now,
		Data: []repomdEntry{
			{
				Type:      "primary",
				Location:  repomdLoc{Href: base2 + "/repodata/primary.xml.gz"},
				Checksum:  repomdCksum{Type: "sha256", Value: ""},
				Timestamp: now,
			},
		},
	}
	out, _ := xml.Marshal(doc)
	c.Data(http.StatusOK, "application/xml; charset=utf-8",
		append([]byte(xml.Header), out...))
}

// primaryXML is the minimal primary.xml structure
type primaryXML struct {
	XMLName  xml.Name      `xml:"metadata"`
	XMLNS    string        `xml:"xmlns,attr"`
	Count    int           `xml:"packages,attr"`
	Packages []rpmPackage  `xml:"package"`
}
type rpmPackage struct {
	Type    string     `xml:"type,attr"`
	Name    string     `xml:"name"`
	Arch    string     `xml:"arch"`
	Version rpmVersion `xml:"version"`
	Size    rpmSize    `xml:"size"`
	Location rpmLoc    `xml:"location"`
}
type rpmVersion struct {
	Epoch string `xml:"epoch,attr"`
	Ver   string `xml:"ver,attr"`
	Rel   string `xml:"rel,attr"`
}
type rpmSize struct {
	Package int64 `xml:"package,attr"`
}
type rpmLoc struct {
	Href string `xml:"href,attr"`
}

func (h *Handler) servePrimary(c *gin.Context, repoName, p string) {
	gzipped := strings.HasSuffix(p, ".gz")

	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Limit: 1000,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	assetPage, err := h.deps.Assets.List(c.Request.Context(), repoName, 1000, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	compMap := map[string]*domain.Component{}
	for i := range page.Items {
		compMap[page.Items[i].ID] = &page.Items[i]
	}

	doc := primaryXML{
		XMLNS: "http://linux.duke.edu/metadata/common",
	}
	for _, a := range assetPage.Items {
		if !strings.HasSuffix(a.Path, ".rpm") {
			continue
		}
		comp := compMap[a.ComponentID]
		if comp == nil {
			continue
		}
		arch := "x86_64"
		filename := path.Base(a.Path)
		// name-version-release.arch.rpm
		parts := strings.Split(strings.TrimSuffix(filename, ".rpm"), ".")
		if len(parts) >= 2 {
			arch = parts[len(parts)-1]
		}
		doc.Packages = append(doc.Packages, rpmPackage{
			Type: "rpm",
			Name: comp.Name,
			Arch: arch,
			Version: rpmVersion{
				Epoch: "0",
				Ver:   comp.Version,
				Rel:   "1",
			},
			Size:     rpmSize{Package: a.SizeBytes},
			Location: rpmLoc{Href: a.Path},
		})
	}
	doc.Count = len(doc.Packages)

	out, _ := xml.Marshal(doc)
	data := append([]byte(xml.Header), out...)

	if gzipped {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		_, _ = gw.Write(data)
		_ = gw.Close()
		c.Data(http.StatusOK, "application/x-gzip", buf.Bytes())
		return
	}
	c.Data(http.StatusOK, "application/xml; charset=utf-8", data)
}

func (h *Handler) serveEmptyMetadata(c *gin.Context, p string) {
	gzipped := strings.HasSuffix(p, ".gz")
	data := []byte(xml.Header + `<metadata xmlns="http://linux.duke.edu/metadata/common" packages="0"/>`)
	if gzipped {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		_, _ = gw.Write(data)
		_ = gw.Close()
		c.Data(http.StatusOK, "application/x-gzip", buf.Bytes())
		return
	}
	c.Data(http.StatusOK, "application/xml; charset=utf-8", data)
}

func (h *Handler) handleUpload(c *gin.Context, repoName, p string) {
	filename := path.Base(p)
	// Parse: name-version-release.arch.rpm
	name := strings.TrimSuffix(filename, ".rpm")
	parts := strings.Split(name, "-")
	pkgName, version := name, "0"
	if len(parts) >= 2 {
		pkgName = strings.Join(parts[:len(parts)-1], "-")
		version = parts[len(parts)-1]
		// strip arch from version: "1.0-1.x86_64" → version="1.0", release="1.x86_64"
		if dot := strings.LastIndex(version, "."); dot > 0 {
			version = version[:dot]
		}
	}

	coords := base.Coords{Name: pkgName, Version: version}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, p, "application/x-rpm",
		coords, c.Request.Body, c.Request.ContentLength); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

func (h *Handler) serveFile(c *gin.Context, repoName, p string) {
	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, p)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer rc.Close()
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
		c.Status(http.StatusOK)
		return
	}
	c.DataFromReader(http.StatusOK, asset.SizeBytes, "application/x-rpm", rc, nil)
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
