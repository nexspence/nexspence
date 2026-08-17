package conan

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/formats/base"
)

// v2Delete answers the Conan v2 deletion routes (#247):
//
//	DELETE /v2/conans/:n/:v/:u/:c                    → remove the recipe, all revisions
//	DELETE /v2/conans/:n/:v/:u/:c/revisions/:rrev    → remove one recipe revision
//
// The client expects 200 on success and raises RecipeNotFoundException on 404,
// so a second delete of the same thing is a 404, not a silent 200. Everything
// stored under the deleted prefix goes — recipe files and package binaries
// alike — through base.DeleteArtifact, so blob refcounts, store usage and the
// artifact.deleted webhook behave exactly as they do for any other format.
// These routes are also what lets a retention job expire Conan revisions the
// way it does for other formats.
//
// Package-granular deletion (…/packages/:pkgID, …/packages/:pkgID/revisions/:prev)
// is not part of #247 and keeps answering 405 rather than being half-guessed
// here. Content a Conan 1 client stored under /files/… is likewise out of
// scope, the same way it is for search: these routes only ever see the
// /v2/conans/… tree, so a retention job built on them expires v2 revisions
// and leaves v1 files where they are.
//
// Write access is the caller's problem to have: RBACMiddleware maps the DELETE
// method to the "delete" action before the format handler runs, and proxy
// repositories never reach this code — RejectMutation refuses the method
// first.
func (h *Handler) v2Delete(c *gin.Context, repoName, p string) {
	segs := strings.Split(strings.TrimPrefix(p, "/v2/conans/"), "/")
	refLevel := len(segs) == 4
	revisionLevel := len(segs) == 6 && segs[4] == "revisions" && segs[5] != ""
	if (!refLevel && !revisionLevel) || segs[0] == "" {
		c.Status(http.StatusMethodNotAllowed)
		return
	}

	prefix := p + "/"
	rel, ok := h.pathsUnder(c, repoName, prefix)
	if !ok {
		return
	}
	if len(rel) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	// Attempt every asset and report the first failure at the end, instead of
	// stopping on it: DeleteArtifact is idempotent, so a client retry after a
	// mid-list error then converges on "gone" instead of chasing a
	// half-deleted revision that revisions/latest still advertise.
	ctx := c.Request.Context()
	var firstErr error
	for f := range rel {
		if err := base.DeleteArtifact(ctx, h.deps, repoName, prefix+f); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": firstErr.Error()})
		return
	}
	// Components whose last asset just went away must not keep showing up in
	// browse and search.
	if err := h.deps.Components.DeleteOrphans(ctx, repoName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusOK)
}
