package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// FilterPackumentByAge removes from a packument every version published after
// cutoff, so a proxy with a minimum-package-age policy (#323) never shows a
// client a version younger than the operator allows. The returned applied flag
// reports whether the policy could be evaluated at all: a document carrying no
// usable dates is returned unchanged with applied=false — hiding the whole
// package would be worse than no policy — and the caller logs the gap.
//
// Within a dated document the filter fails closed: a version missing from the
// time map has an unknowable age and is hidden. Dist-tags pointing at a hidden
// version fall back to the newest surviving version (by publish time) — a
// "latest" left dangling would break `npm install pkg` outright, which is
// quarantine of the wrong thing. Malformed input is returned unchanged.
func FilterPackumentByAge(body []byte, cutoff time.Time) (out []byte, applied bool) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, false
	}
	versions, ok := doc["versions"].(map[string]any)
	if !ok {
		return body, false
	}
	times, ok := doc["time"].(map[string]any)
	if !ok {
		return body, false
	}

	// Parse every per-version date once. "created"/"modified" describe the
	// package, not a version.
	published := make(map[string]time.Time, len(times))
	for v, raw := range times {
		if v == "created" || v == "modified" {
			continue
		}
		s, ok := raw.(string)
		if !ok {
			continue
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			published[v] = t
		}
	}
	if len(published) == 0 {
		return body, false
	}

	changed := false
	var newestSurviving string
	for v := range versions {
		t, dated := published[v]
		if !dated || t.After(cutoff) {
			delete(versions, v)
			delete(times, v)
			changed = true
			continue
		}
		if newestSurviving == "" || t.After(published[newestSurviving]) {
			newestSurviving = v
		}
	}

	if tags, ok := doc["dist-tags"].(map[string]any); ok {
		for tag, target := range tags {
			name, _ := target.(string)
			if _, alive := versions[name]; alive {
				continue
			}
			if newestSurviving != "" {
				tags[tag] = newestSurviving
			} else {
				delete(tags, tag)
			}
			changed = true
		}
	}

	if !changed {
		return body, true
	}
	filtered, err := json.Marshal(doc)
	if err != nil {
		return body, true
	}
	return filtered, true
}

// maxPackumentBytes bounds how much upstream metadata the download gate will
// read while deciding a version's age. Real packuments run to a few MB.
const maxPackumentBytes = 32 << 20

// PackumentPublishTimes extracts the version→publish-time map from a raw
// packument. "created"/"modified" describe the package, not a version. An
// empty map means the document carries no usable dates.
func PackumentPublishTimes(body []byte) map[string]time.Time {
	var doc struct {
		Time map[string]string `json:"time"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil
	}
	out := make(map[string]time.Time, len(doc.Time))
	for v, s := range doc.Time {
		if v == "created" || v == "modified" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			out[v] = t
		}
	}
	return out
}

// tarballAgeAllowed is the download half of the minimum-package-age policy: a
// direct tarball URL must not bypass the metadata filter. It answers whether
// the request may proceed; on refusal it has already written the 403.
//
// Within a dated publish history the gate fails closed — a version the history
// does not name (including one published after the cached copy was taken) is
// refused. When no dates are available at all, the gate opens and logs the
// gap, mirroring the metadata filter's hybrid failure mode.
func (h *Handler) tarballAgeAllowed(c *gin.Context, repo *domain.Repository, pkg, version string, minAge time.Duration) bool {
	times := h.packumentTimesFor(c.Request.Context(), repo, pkg)
	if len(times) == 0 {
		log.Printf("nexspence: minimum_package_age not enforceable for %s/%s — no publish dates available", repo.Name, pkg)
		return true
	}
	published, ok := times[version]
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"version %q of %q is not in the package's known publish history; this repository enforces a minimum package age", version, pkg)})
		return false
	}
	if age := time.Since(published); age < minAge {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"%s@%s was published %s ago; this repository enforces a minimum package age of %s",
			pkg, version, age.Round(time.Minute), minAge)})
		return false
	}
	return true
}

// packumentTimesFor returns the publish history for pkg: the proxy's own
// cached packument when present (no upstream round-trip per download — clients
// fetch metadata before tarballs, so the cache is fresh within the metadata
// TTL), the upstream document otherwise.
func (h *Handler) packumentTimesFor(ctx context.Context, repo *domain.Repository, pkg string) map[string]time.Time {
	if asset, err := h.deps.Assets.GetByPath(ctx, repo.Name, "/"+pkg); err == nil && asset != nil {
		if body := h.readCachedBlob(ctx, asset); body != nil {
			if times := PackumentPublishTimes(body); len(times) > 0 {
				return times
			}
		}
	}
	resp, err := repoproxy.FetchUpstreamOnce(ctx, repo, "/"+repoproxy.NPMMetadataPath(pkg), "", nil)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPackumentBytes))
	if err != nil {
		return nil
	}
	return PackumentPublishTimes(body)
}

// readCachedBlob reads a cached asset's bytes from the store that holds them.
// Deliberately not base.FetchArtifact: that path counts a download, and an
// internal policy read must not inflate the packument's statistics.
func (h *Handler) readCachedBlob(ctx context.Context, asset *domain.Asset) []byte {
	store := h.deps.BlobStore
	if asset.BlobStoreID != "" {
		if meta, err := h.deps.Blobs.GetByID(ctx, asset.BlobStoreID); err == nil && meta != nil {
			store = base.PhysicalStore(ctx, h.deps, meta)
		}
	}
	rc, _, err := store.Get(ctx, asset.BlobKey)
	if err != nil {
		return nil
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, maxPackumentBytes))
	if err != nil {
		return nil
	}
	return b
}
