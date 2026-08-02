package npm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// distTagsPrefix is the npm CLI's dist-tag API surface:
//
//	GET    /-/package/:pkg/dist-tags        → the tag → version map
//	PUT    /-/package/:pkg/dist-tags/:tag   → body is a bare JSON string version
//	DELETE /-/package/:pkg/dist-tags/:tag   → drop the tag
const distTagsPrefix = "/-/package/"

// parseDistTags splits a dist-tags request path. The package name may be scoped
// and therefore contain a slash, so the segment is located by the "/dist-tags"
// marker rather than by counting path elements.
func parseDistTags(p string) (pkg, tag string, ok bool) {
	if !strings.HasPrefix(p, distTagsPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(p, distTagsPrefix)
	i := strings.Index(rest, "/dist-tags")
	if i <= 0 {
		return "", "", false
	}
	pkg = rest[:i]
	tag = strings.Trim(strings.TrimPrefix(rest[i+len("/dist-tags"):], "/"), "/")
	return pkg, tag, true
}

// sentinelVersion is the pseudo-version a proxy stores its cached packument
// under. It is not a package version and must never be served as one (#131).
const sentinelVersion = "metadata"

// packageComponents returns the components of exactly this package. The search
// filter matches names loosely (ILIKE %name%), so "lib" would otherwise pull in
// "mylib" — the exact match has to happen here.
func (h *Handler) packageComponents(ctx context.Context, repoName, pkgName string) ([]domain.Component, error) {
	page, err := h.deps.Components.Search(ctx, domain.SearchParams{
		Repository: repoName, Name: pkgName, Limit: 200,
	})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Component, 0, len(page.Items))
	for _, comp := range page.Items {
		if comp.Name == pkgName && comp.Version != sentinelVersion {
			out = append(out, comp)
		}
	}
	return out, nil
}

// distTagsOf builds the tag → version map. A package always has a "latest":
// when no version is explicitly tagged, the highest one wins.
func distTagsOf(comps []domain.Component) map[string]string {
	tags := map[string]string{}
	for _, comp := range comps {
		for _, t := range comp.Tags {
			tags[t] = comp.Version
		}
	}
	if _, ok := tags["latest"]; !ok {
		if v := highestVersion(comps); v != "" {
			tags["latest"] = v
		}
	}
	return tags
}

func highestVersion(comps []domain.Component) string {
	latest := ""
	for _, comp := range comps {
		if latest == "" || base.CompareLooseVersions(comp.Version, latest) > 0 {
			latest = comp.Version
		}
	}
	return latest
}

func (h *Handler) handleDistTags(c *gin.Context, repoName, pkgName, tag string) {
	ctx := c.Request.Context()
	repo, err := h.deps.Repos.Get(ctx, repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if repoproxy.RejectMutation(c, repo) {
		return
	}

	switch c.Request.Method {
	case http.MethodGet:
		comps, err := h.packageComponents(ctx, repoName, pkgName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if len(comps) == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
			return
		}
		c.JSON(http.StatusOK, distTagsOf(comps))

	case http.MethodPut:
		if tag == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "tag required"})
			return
		}
		// The body is a bare JSON string, e.g. "1.0.0" — not an object.
		var version string
		if err := c.ShouldBindJSON(&version); err != nil || version == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "body must be a version string"})
			return
		}
		if err := h.setDistTag(ctx, repoName, pkgName, tag, version); err != nil {
			writeTagError(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"ok": true})

	case http.MethodDelete:
		if tag == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "tag required"})
			return
		}
		if err := h.removeDistTag(ctx, repoName, pkgName, tag); err != nil {
			writeTagError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

// errNoSuchTagTarget marks "the thing you asked me to tag/untag is not here",
// which the npm client expects as a 404 rather than a 500.
type errNoSuchTagTarget struct{ msg string }

func (e errNoSuchTagTarget) Error() string { return e.msg }

func writeTagError(c *gin.Context, err error) {
	var notFound errNoSuchTagTarget
	if errorsAs(err, &notFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": notFound.msg})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

// errorsAs is errors.As specialised to errNoSuchTagTarget, kept local so the
// rest of the file reads without an extra import alias.
func errorsAs(err error, target *errNoSuchTagTarget) bool {
	if e, ok := err.(errNoSuchTagTarget); ok { //nolint:errorlint // no wrapping in this package
		*target = e
		return true
	}
	return false
}

// setDistTag points tag at version, moving it off whatever version held it.
func (h *Handler) setDistTag(ctx context.Context, repoName, pkgName, tag, version string) error {
	comps, err := h.packageComponents(ctx, repoName, pkgName)
	if err != nil {
		return err
	}
	found := false
	for _, comp := range comps {
		if comp.Version == version {
			found = true
		}
	}
	if !found {
		return errNoSuchTagTarget{msg: pkgName + "@" + version + " not found"}
	}
	for _, comp := range comps {
		want := withoutTag(comp.Tags, tag)
		if comp.Version == version {
			want = append(want, tag)
		}
		if sameTags(comp.Tags, want) {
			continue
		}
		if err := h.deps.Components.SetTags(ctx, comp.ID, want); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) removeDistTag(ctx context.Context, repoName, pkgName, tag string) error {
	comps, err := h.packageComponents(ctx, repoName, pkgName)
	if err != nil {
		return err
	}
	removed := false
	for _, comp := range comps {
		want := withoutTag(comp.Tags, tag)
		if sameTags(comp.Tags, want) {
			continue
		}
		if err := h.deps.Components.SetTags(ctx, comp.ID, want); err != nil {
			return err
		}
		removed = true
	}
	if !removed {
		return errNoSuchTagTarget{msg: "tag " + tag + " not found"}
	}
	return nil
}

func withoutTag(tags []string, tag string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t != tag {
			out = append(out, t)
		}
	}
	return out
}

func sameTags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ─── unpublish ──────────────────────────────────────────────────

// splitRev strips npm's "/-rev/:rev" suffix. The revision itself is
// meaningless here: we do not implement optimistic concurrency.
func splitRev(p string) (string, bool) {
	i := strings.Index(p, "/-rev/")
	if i < 0 {
		return p, false
	}
	return p[:i], true
}

// handleUnpublish serves every DELETE the npm client makes: a single tarball
// (with or without a -rev suffix) or a whole package. Deleting something that
// is not there is a 404 — reporting "ok" while nothing happened is what made
// unpublish look successful (#101).
func (h *Handler) handleUnpublish(c *gin.Context, repoName, p string) {
	ctx := c.Request.Context()
	target, _ := splitRev(p)

	if strings.Contains(target, "/-/") {
		if err := h.deleteAsset(ctx, repoName, target); err != nil {
			writeTagError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}

	pkgName := strings.Trim(strings.TrimPrefix(target, "/"), "/")
	comps, err := h.packageComponents(ctx, repoName, pkgName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(comps) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}
	for i := range comps {
		if err := h.unpublishComponent(ctx, repoName, comps[i]); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// deleteAsset removes one stored file and, when it was the last file of its
// component, the now-empty component row.
func (h *Handler) deleteAsset(ctx context.Context, repoName, assetPath string) error {
	asset, err := h.deps.Assets.GetByPath(ctx, repoName, assetPath)
	if err != nil || asset == nil {
		return errNoSuchTagTarget{msg: "not found: " + assetPath}
	}
	componentID := asset.ComponentID
	if err := base.DeleteArtifact(ctx, h.deps, repoName, assetPath); err != nil {
		return err
	}
	if componentID == "" {
		return nil
	}
	remaining, err := h.deps.Assets.ListByComponentID(ctx, componentID)
	if err == nil && len(remaining) == 0 {
		_ = h.deps.Components.Delete(ctx, componentID)
	}
	return nil
}

// unpublishComponent removes one published version: its files first, then the
// component row itself.
func (h *Handler) unpublishComponent(ctx context.Context, repoName string, comp domain.Component) error {
	assets, err := h.deps.Assets.ListByComponentID(ctx, comp.ID)
	if err != nil {
		return err
	}
	for _, a := range assets {
		if err := base.DeleteArtifact(ctx, h.deps, repoName, a.Path); err != nil {
			return err
		}
	}
	return h.deps.Components.Delete(ctx, comp.ID)
}

// handleRevPut serves `PUT /:pkg/-rev/:rev`, which the npm client sends as the
// first half of `npm unpublish <pkg>@<version>`: the body is the packument with
// that version removed. Versions missing from the body are unpublished.
// A body carrying attachments is an ordinary publish.
func (h *Handler) handleRevPut(c *gin.Context, repoName, pkgPath string) {
	ctx := c.Request.Context()
	pkgName := strings.Trim(strings.TrimPrefix(pkgPath, "/"), "/")

	var doc map[string]json.RawMessage
	if err := c.ShouldBindJSON(&doc); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if raw, ok := doc["_attachments"]; ok {
		var attachments map[string]json.RawMessage
		if json.Unmarshal(raw, &attachments) == nil && len(attachments) > 0 {
			h.publishDoc(c, repoName, pkgName, doc)
			return
		}
	}

	keep := map[string]bool{}
	if raw, ok := doc["versions"]; ok {
		var versions map[string]json.RawMessage
		_ = json.Unmarshal(raw, &versions)
		for v := range versions {
			keep[v] = true
		}
	}

	comps, err := h.packageComponents(ctx, repoName, pkgName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(comps) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}
	for i := range comps {
		if keep[comps[i].Version] {
			continue
		}
		if err := h.unpublishComponent(ctx, repoName, comps[i]); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
