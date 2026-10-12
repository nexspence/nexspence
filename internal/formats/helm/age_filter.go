package helm

// Minimum package age on a Helm proxy (#643, after npm and PyPI in #323 and
// Go): a chart version younger than proxy_config.minimum_package_age is left
// out of the served index.yaml and its .tgz (and .tgz.prov) is refused with
// 403, even when the archive is already cached.
//
// A version's publish time is the `created` field of its index.yaml entry.
// Helm has no "latest" pointer — the client picks the highest version the
// index lists — so dropping a version from its chart's list is the whole
// index half; a chart left with no version disappears from entries.
//
// The download gate reads dates from the same per-repository table that
// resolves chart origins (helm.index_cache_ttl, see indexcache.go), so it
// costs no index fetch beyond the one the origin lookup already makes. While
// upstream is unreachable the last table fetched successfully still dates a
// chart: publish times never change.
//
// Hybrid failure mode, as for npm and PyPI, per chart: an undated version of
// a chart whose other versions are dated is hidden and refused (its age is
// unknowable); a chart with no dated version at all cannot be policed, so it
// is served and the gap logged. A download the index does not list at all is
// refused when the index carries dates (a chart published after the cached
// table was taken is young by definition), served and logged otherwise.

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// chartCreated parses an index entry's created field. Helm writes it as a
// quoted RFC 3339 string; an unquoted timestamp decodes to time.Time.
func chartCreated(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, !t.IsZero()
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(t)); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// filterIndexByAge drops the versions published after cutoff from an
// index.yaml document in place. A chart with no dated version is kept whole
// and named in undated; a chart left without versions is removed.
func filterIndexByAge(index map[string]any, cutoff time.Time) (undated []string) {
	entries, ok := index["entries"].(map[string]any)
	if !ok {
		return nil
	}
	for key, raw := range entries {
		charts, ok := raw.([]any)
		if !ok {
			continue
		}
		anyDated := false
		for _, cv := range charts {
			if chart, ok := cv.(map[string]any); ok {
				if _, dated := chartCreated(chart["created"]); dated {
					anyDated = true
					break
				}
			}
		}
		if !anyDated {
			undated = append(undated, key)
			continue
		}
		kept := charts[:0]
		for _, cv := range charts {
			chart, ok := cv.(map[string]any)
			if !ok {
				continue
			}
			if t, dated := chartCreated(chart["created"]); dated && !t.After(cutoff) {
				kept = append(kept, chart)
			}
		}
		if len(kept) == 0 {
			delete(entries, key)
			continue
		}
		entries[key] = kept
	}
	sort.Strings(undated)
	return undated
}

// logUndatedCharts reports the charts the policy could not police, one line
// per index rather than one per chart (an index lists thousands).
func logUndatedCharts(repo *domain.Repository, undated []string) {
	if len(undated) == 0 {
		return
	}
	sample := undated
	if len(sample) > 3 {
		sample = sample[:3]
	}
	log.Printf("nexspence: minimum_package_age not enforceable for %d chart(s) in %s (e.g. %s) — upstream index.yaml entries carry no created date",
		len(undated), repo.Name, strings.Join(sample, ", "))
}

// chartAgeAllowed is the download half of the policy: a chart archive (or its
// .prov) must not bypass the index filter. On refusal the 403 is already
// written.
func (h *Handler) chartAgeAllowed(c *gin.Context, repo *domain.Repository, reqPath string, minAge time.Duration) bool {
	remoteBase, err := repoproxy.RemoteURL(repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return false
	}
	table, err := h.chartTable(c.Request.Context(), repo, remoteBase, true)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return false
	}
	rel := strings.TrimSuffix(strings.TrimPrefix(reqPath, "/"), ".prov")
	o, listed := table[rel]
	if !listed {
		return unlistedChartAllowed(c, repo, table, rel)
	}
	if !o.dated {
		if !o.chartDated {
			log.Printf("nexspence: minimum_package_age not enforceable for %s/%s — upstream index.yaml entries carry no created date", repo.Name, o.name)
			return true
		}
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"%s %s has no created date in the upstream index; this repository enforces a minimum package age", o.name, o.version)})
		return false
	}
	if age := time.Since(o.created); age < minAge {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"%s %s was published %s ago; this repository enforces a minimum package age of %s",
			o.name, o.version, age.Round(time.Minute), minAge)})
		return false
	}
	return true
}

// unlistedChartAllowed decides a download the upstream index does not list:
// refused when the index carries any date, served (and logged) otherwise.
func unlistedChartAllowed(c *gin.Context, repo *domain.Repository, table map[string]chartOrigin, rel string) bool {
	for _, o := range table {
		if o.dated {
			c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
				"%s is not in the upstream index, so its publish time is unknown; this repository enforces a minimum package age", rel)})
			return false
		}
	}
	log.Printf("nexspence: minimum_package_age not enforceable for %s/%s — upstream index.yaml carries no created dates", repo.Name, rel)
	return true
}
