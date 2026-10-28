package conda

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
)

// repodataDoc is a subdir's repodata.json. An entry is the package's
// info/index.json with the file's md5, sha256 and size, which is what
// `conda index` publishes (#677).
type repodataDoc struct {
	Info          map[string]string         `json:"info"`
	Packages      map[string]map[string]any `json:"packages"`
	PackagesConda map[string]map[string]any `json:"packages.conda"`
}

// condaIndexKey holds a component's info/index.json. A hosted component is one
// build of one version — a .conda and a .tar.bz2 of it share the document.
const condaIndexKey = "conda_index"

// One repodata request reads at most maxRepairsPerIndex pre-#677 files back
// from storage, for at most repairTimeBudget; the rest are served from what
// their names and old components say, and repaired by the requests that
// follow. Every attempt records a document, so none is read twice.
const (
	maxRepairsPerIndex = 32
	repairTimeBudget   = 10 * time.Second
)

func buildRepodata(ctx context.Context, d formats.Deps, repoName, platform string) (*repodataDoc, error) {
	// Every component of exactly this subdir. Search matches substrings and
	// stops at 500 rows, which listed linux-ppc64le's packages under
	// linux-ppc64 and cut a large subdir short (#586).
	comps, err := base.ExactComponents(ctx, d.Components, domain.SearchParams{
		Repository: repoName,
		Group:      platform,
	})
	if err != nil {
		return nil, fmt.Errorf("conda: list components: %w", err)
	}
	compByID := make(map[string]*domain.Component, len(comps))
	for i := range comps {
		compByID[comps[i].ID] = &comps[i]
	}
	// Every file of the subdir, not one per component: builds of one version
	// and the .conda/.tar.bz2 pair of one build are all installable (#677).
	assets, err := d.Assets.ListByRepoAndPath(ctx, repoName, "/"+platform+"/")
	if err != nil {
		return nil, fmt.Errorf("conda: list files: %w", err)
	}

	doc := &repodataDoc{
		Info:          map[string]string{"subdir": platform},
		Packages:      map[string]map[string]any{},
		PackagesConda: map[string]map[string]any{},
	}
	repo, _ := d.Repos.Get(ctx, repoName)
	repairs, deadline := 0, time.Now().Add(repairTimeBudget)
	for _, asset := range assets {
		filename := path.Base(asset.Path)
		if path.Dir(asset.Path) != "/"+platform || !isPackageFile(filename) {
			continue
		}
		comp := compByID[asset.ComponentID]
		if comp == nil {
			continue
		}
		index, _ := comp.Extra[condaIndexKey].(map[string]any)
		if index == nil {
			// Stored before #677: the component may be shared by several builds
			// and carry another file's metadata. Read this file's own.
			if repo != nil && repairs < maxRepairsPerIndex && time.Now().Before(deadline) {
				repairs++
				index = repairLegacyFile(ctx, d, repo, platform, asset, comp)
			}
			if index == nil {
				index = legacyIndex(comp, filename, platform)
			}
		}
		entry := repodataEntry(index, asset, platform)
		if isCondaFile(filename) {
			doc.PackagesConda[filename] = entry
		} else {
			doc.Packages[filename] = entry
		}
	}
	return doc, nil
}

// repodataEntry is index plus the file's checksums and size, with the fields
// conda and libmamba require present: build_number 0 included (#591), and an
// empty depends list rather than none.
func repodataEntry(index map[string]any, asset domain.Asset, platform string) map[string]any {
	entry := make(map[string]any, len(index)+3)
	for k, v := range index {
		entry[k] = v
	}
	if _, ok := entry["build_number"]; !ok {
		entry["build_number"] = 0
	}
	if _, ok := entry["depends"]; !ok {
		entry["depends"] = []string{}
	}
	if _, ok := entry["subdir"]; !ok {
		entry["subdir"] = platform
	}
	entry["md5"] = asset.MD5
	entry["sha256"] = asset.SHA256
	entry["size"] = asset.SizeBytes
	return entry
}

// indexFromMeta is the info/index.json a package is published with: its own
// when it had one, else what its filename says.
func indexFromMeta(m *PkgMeta, platform string) map[string]any {
	if m.Index != nil {
		return m.Index
	}
	depends := m.Depends
	if depends == nil {
		depends = []string{}
	}
	return map[string]any{
		"name": m.Name, "version": m.Version, "build": m.Build,
		"build_number": m.BuildNumber, "depends": depends, "subdir": platform,
	}
}

// legacyIndex describes a pre-#677 file without reading it: name, version and
// build from its filename, and the old component's build number and
// dependencies only when they were recorded for this very build.
func legacyIndex(comp *domain.Component, filename, platform string) map[string]any {
	m := metaFromFilename(filename)
	if b, _ := comp.Extra["build"].(string); b != "" && b == m.Build {
		if n, ok := comp.Extra["build_number"].(float64); ok {
			m.BuildNumber = int(n)
		} else if n, ok := comp.Extra["build_number"].(int); ok {
			m.BuildNumber = n
		}
		m.Depends = append(m.Depends, extraStrings(comp.Extra["depends"])...)
	}
	return indexFromMeta(m, platform)
}

// extraStrings reads a string list from Component.Extra, which holds []string
// in memory and []any after a JSONB round trip.
func extraStrings(v any) []string {
	switch vv := v.(type) {
	case []string:
		return vv
	case []any:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// repairLegacyFile reads a pre-#677 file's info/index.json from storage, moves
// the file to the component of its own build and records the document there,
// so the read happens once. A file whose metadata cannot be read is recorded
// as legacyIndex describes it, for the same reason. Concurrent requests
// repairing the same file wait for each other and reuse the result. It
// returns nil when the path no longer holds the file it was asked about.
func repairLegacyFile(ctx context.Context, d formats.Deps, repo *domain.Repository, platform string, asset domain.Asset, legacy *domain.Component) map[string]any {
	unlock := base.LockLocal("conda-index-repair:" + repo.Name + ":" + asset.Path)
	defer unlock()
	cur, err := d.Assets.GetByPath(ctx, repo.Name, asset.Path)
	if err != nil || cur == nil {
		return nil
	}
	if cur.ComponentID != asset.ComponentID || cur.SHA256 != asset.SHA256 {
		// Repaired by a request that held the lock first, or replaced by an
		// upload: whatever its component records now describes it.
		if comp, err := d.Components.Get(ctx, cur.ComponentID); err == nil && comp != nil {
			index, _ := comp.Extra[condaIndexKey].(map[string]any)
			return index
		}
		return nil
	}

	filename := path.Base(asset.Path)
	var index map[string]any
	meta, err := readStoredMeta(ctx, d, cur)
	if err != nil || meta == nil {
		if err != nil {
			log.Printf("nexspence: conda %s%s: index repair falls back to the filename: %v", repo.Name, asset.Path, err)
		}
		meta = metaFromFilename(filename)
		index = legacyIndex(legacy, filename, platform)
	} else {
		index = indexFromMeta(meta, platform)
	}
	if err := placeFile(ctx, d, repo, asset.Path, cur.SHA256, componentCoords(platform, meta), index); err != nil {
		log.Printf("nexspence: conda %s%s: move to its build's component: %v", repo.Name, asset.Path, err)
	}
	return index
}

// readStoredMeta reads a stored package's metadata straight from its blob
// store — not through FetchArtifact, which would count the read as a download
// and keep the file from download-age cleanup. A .tar.bz2 is parsed as a
// stream; a .conda is a zip, read from its end, so it is spooled first.
func readStoredMeta(ctx context.Context, d formats.Deps, asset *domain.Asset) (*PkgMeta, error) {
	store, err := base.AssetPhysicalStore(ctx, d, asset)
	if err != nil {
		return nil, err
	}
	rc, _, err := store.Get(ctx, asset.BlobKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	filename := path.Base(asset.Path)
	if !isCondaFile(filename) {
		return parseTarBz2(rc)
	}
	tmp, size, err := spool(rc)
	if err != nil {
		return nil, err
	}
	defer cleanupSpool(tmp)
	return ParseMeta(filename, tmp, size)
}

func isCondaFile(filename string) bool {
	return strings.HasSuffix(filename, ".conda")
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
