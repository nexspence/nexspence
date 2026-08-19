package oci

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
)

// The catalog endpoint, GET /v2/<repoName>/_catalog.
//
// What it lists: the image names inside <repoName> — "library/ubuntu",
// "charts/nginx" — and not Nexspence repository names. Each Nexspence
// repository is its own registry namespace: a client talking to
// <host>/v2/<repoName>/library/ubuntu sees the repository name
// <repoName>/library/ubuntu, and <repoName> is fixed by the URL it connected
// to. So the set the catalog enumerates is the same set of names
// /v2/<repoName>/<image>/tags/list is addressed by, which is what
// handleTagsList reports back as "name".
//
// What counts as one of those names is a stored manifest, not a stored
// component. Every blob upload registers a component of its own — a layer, and
// an upload abandoned before its manifest — so a catalog read off components
// lists things no client can pull. Two more component-level details would
// corrupt it the same way, and are the reason the query does not go near that
// table: the component search matches names with a substring ILIKE, so
// "charts/nginx" would drag in "charts/nginx-extra", and a manifest pushed by
// tag registers a second digest-alias component, so every image would arrive
// duplicated. Manifest assets have neither problem: the image name is a literal
// segment of the asset path, and the distinct is exact.

func (h *Handler) handleCatalog(c *gin.Context, repoName string) {
	if c.Request.Method != http.MethodGet {
		c.Status(http.StatusMethodNotAllowed)
		return
	}

	// A proxy repository answers from its cache and never forwards. Upstreams
	// almost universally restrict _catalog — Docker Hub, GHCR and ECR all refuse
	// it — so forwarding would turn the endpoint into a 502 for the ordinary
	// case. And an upstream that did answer would have this proxy claim a
	// catalog of images it does not hold: the honest answer for a proxy is what
	// it can serve right now, which is what it has cached. Nothing branches on
	// the repository type below for exactly that reason.
	names, err := h.deps.Assets.ListOCIImageNames(c.Request.Context(), []string{repoName})
	if err != nil {
		dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	// Sorted here rather than in SQL: the ?last= cursor is resolved by comparing
	// Go strings, and a database ORDER BY sorts under the server's collation,
	// which for anything but C orders punctuation differently. A list ordered one
	// way and searched the other makes the cursor skip entries.
	sort.Strings(names)

	params := ParsePageParams(c)
	page, more := Paginate(names, params)
	SetNextLink(c, params, page, more)
	if page == nil {
		// An empty catalog must serialize as [] and not null: a null breaks
		// clients that range over the list.
		page = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"repositories": page})
}
