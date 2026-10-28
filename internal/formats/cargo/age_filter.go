package cargo

// Minimum package age on a Cargo proxy (#643, after npm and PyPI in #323 and
// Go): a version younger than proxy_config.minimum_package_age is left out of
// the crate's sparse-index file and refused on its .crate download.
//
// Where the dates come from: index.crates.io writes each version's publish
// time into its index line ("pubtime", RFC 3339), so the file cargo resolves
// from also dates it. A registry whose index lines carry no pubtime is asked
// its web API — the "api" base of the upstream's own config.json, at
// /api/v1/crates/<name>/versions ("created_at" per version), the crates.io
// API most alternative registries mirror. A registry with neither has no
// dates to enforce.
//
// A sparse index has no "latest" pointer — cargo picks from the lines it is
// given — so dropping lines is the whole index half of the policy.
//
// Hybrid failure mode, as for npm, PyPI and Go: a version without a date (or
// missing from the index) in a crate whose other versions are dated is
// refused; a crate with no dated version at all cannot be policed, so the gap
// is logged and the crate served.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

const (
	// maxIndexBytes bounds one sparse-index file read; the crates with the
	// most versions on crates.io stay in the low MBs.
	maxIndexBytes = 32 << 20
	// maxAPIPageBytes bounds one web API page.
	maxAPIPageBytes = 32 << 20
	// maxAPIPages bounds how many next_page links are followed.
	maxAPIPages = 50
	// maxAgeEntries bounds each in-memory cache; a full one is cleared.
	maxAgeEntries = 100_000
)

// crateDates is what is known about a crate's versions: every version its
// index file lists, and the publish time of those that are dated.
type crateDates struct {
	found  bool // upstream knows the crate
	listed map[string]bool
	times  map[string]time.Time
}

// apiEntry is one cached web API lookup.
type apiEntry struct {
	times   map[string]time.Time
	fetched time.Time
}

// ageCache memoizes publish times per repository, crate and version
// (immutable once known), and web API answers and crates found to carry no
// dates at all per repository and crate (for the metadata TTL).
type ageCache struct {
	mu      sync.Mutex
	times   map[string]time.Time
	api     map[string]apiEntry
	undated map[string]time.Time
}

// indexLine is the part of a sparse-index record the policy reads.
type indexLine struct {
	Vers    string `json:"vers"`
	Pubtime string `json:"pubtime"`
}

// parseIndexLine reads one sparse-index line; ok is false for a line that is
// not a record.
func parseIndexLine(line []byte) (vers string, t time.Time, dated, ok bool) {
	var rec indexLine
	if err := json.Unmarshal(line, &rec); err != nil || rec.Vers == "" {
		return "", time.Time{}, false, false
	}
	if rec.Pubtime != "" {
		if pt, err := time.Parse(time.RFC3339, rec.Pubtime); err == nil {
			return rec.Vers, pt, true, true
		}
	}
	return rec.Vers, time.Time{}, false, true
}

// parseIndexDates reads the versions and pubtimes of a sparse-index file.
func parseIndexDates(body []byte) crateDates {
	d := crateDates{found: true, listed: map[string]bool{}, times: map[string]time.Time{}}
	for _, line := range bytes.Split(body, []byte("\n")) {
		vers, t, dated, ok := parseIndexLine(line)
		if !ok {
			continue
		}
		d.listed[vers] = true
		if dated {
			d.times[vers] = t
		}
	}
	return d
}

// apiVersionsPage is one page of /api/v1/crates/<name>/versions.
type apiVersionsPage struct {
	Versions []struct {
		Num       string `json:"num"`
		CreatedAt string `json:"created_at"`
	} `json:"versions"`
	Meta struct {
		NextPage string `json:"next_page"`
	} `json:"meta"`
}

// apiTimes returns the publish times the upstream web API gives for a crate.
// An upstream without an "api" base, or an unusable answer, yields none.
func (h *Handler) apiTimes(ctx context.Context, repo *domain.Repository, name string) map[string]time.Time {
	key := repo.Name + "\x00" + name
	h.age.mu.Lock()
	if e, ok := h.age.api[key]; ok && time.Since(e.fetched) < repoproxy.MetadataMaxAge(repo) {
		h.age.mu.Unlock()
		return e.times
	}
	h.age.mu.Unlock()

	times := map[string]time.Time{}
	api := h.upstreamAPIBase(ctx, repo)
	if api == "" {
		return times
	}
	// The API is the upstream's own declared host, like dl (#347): the
	// repository's credentials may go there too.
	ctx = repoproxy.WithTrustedAuthBases(ctx, api)
	endpoint := strings.TrimRight(api, "/") + "/api/v1/crates/" + url.PathEscape(name) + "/versions"
	query := ""
	for range maxAPIPages {
		page, ok, err := fetchAPIPage(ctx, repo, endpoint, query)
		if err != nil {
			return times // not cached: the next request asks again
		}
		if !ok {
			break
		}
		for _, v := range page.Versions {
			if t, err := time.Parse(time.RFC3339, v.CreatedAt); err == nil && v.Num != "" {
				times[v.Num] = t
			}
		}
		next := strings.TrimPrefix(page.Meta.NextPage, "?")
		if next == "" || next == query {
			break
		}
		query = next
	}

	h.age.mu.Lock()
	if h.age.api == nil || len(h.age.api) >= maxAgeEntries {
		h.age.api = make(map[string]apiEntry)
	}
	h.age.api[key] = apiEntry{times: times, fetched: time.Now()}
	h.age.mu.Unlock()
	return times
}

// fetchAPIPage GETs one web API page. ok is false for a non-200 or
// unparsable answer: that is no dates, not a failure.
func fetchAPIPage(ctx context.Context, repo *domain.Repository, endpoint, query string) (page apiVersionsPage, ok bool, err error) {
	resp, err := repoproxy.FetchUpstreamOnce(ctx, repo, endpoint, query, nil)
	if err != nil {
		return page, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return page, false, nil
	}
	if json.NewDecoder(io.LimitReader(resp.Body, maxAPIPageBytes)).Decode(&page) != nil {
		return page, false, nil
	}
	return page, true, nil
}

// withAPIFallback dates an index file that carries no pubtime from the web
// API.
func (h *Handler) withAPIFallback(ctx context.Context, repo *domain.Repository, name string, d crateDates) crateDates {
	if len(d.times) > 0 || !d.found {
		return d
	}
	for v, t := range h.apiTimes(ctx, repo, name) {
		if d.listed[v] {
			d.times[v] = t
		}
	}
	return d
}

// indexPath is the local sparse-index path of a crate (names are lowercased
// in index paths).
func indexPath(name string) string {
	lower := strings.ToLower(name)
	return "/index/" + cargoPrefix(lower) + "/" + lower
}

// fetchIndex reads a crate's sparse-index file from upstream. found is false
// on 404/410 (and 451, which index.crates.io answers for a removed crate).
func fetchIndex(ctx context.Context, repo *domain.Repository, name string) (body []byte, found bool, err error) {
	resp, err := repoproxy.FetchUpstreamOnce(ctx, repo, repoproxy.CargoIndexUpstreamPath(indexPath(name)), "", nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone ||
		resp.StatusCode == http.StatusUnavailableForLegalReasons:
		return nil, false, nil
	case resp.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("upstream index for %s: %s", name, resp.Status)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes))
	if err != nil {
		return nil, false, err
	}
	return body, true, nil
}

// crateDatesFor dates a crate for the download gate: the proxy's cached index
// file first (a read that does not count a download), upstream when the
// cached copy does not date version — it may predate the version.
func (h *Handler) crateDatesFor(ctx context.Context, repo *domain.Repository, name, version string) (crateDates, error) {
	if asset, err := h.deps.Assets.GetByPath(ctx, repo.Name, indexPath(name)); err == nil && asset != nil {
		if body := base.ReadCachedBlob(ctx, h.deps, asset, maxIndexBytes); body != nil {
			if d := parseIndexDates(body); !d.times[version].IsZero() {
				return d, nil
			}
		}
	}
	body, found, err := fetchIndex(ctx, repo, name)
	if err != nil {
		return crateDates{}, err
	}
	if !found {
		return crateDates{}, nil
	}
	return h.withAPIFallback(ctx, repo, strings.ToLower(name), parseIndexDates(body)), nil
}

// versionAgeAllowed is the download half of the policy: a .crate must not
// bypass the index. On refusal the 403 is already written.
func (h *Handler) versionAgeAllowed(c *gin.Context, repo *domain.Repository, name, version string, minAge time.Duration) bool {
	ctx := c.Request.Context()
	crateKey := repo.Name + "\x00" + strings.ToLower(name)
	key := crateKey + "\x00" + version
	h.age.mu.Lock()
	t, known := h.age.times[key]
	undatedAt, undated := h.age.undated[crateKey]
	h.age.mu.Unlock()
	if !known && undated && time.Since(undatedAt) < repoproxy.MetadataMaxAge(repo) {
		return true // logged when found undated
	}

	if !known {
		d, err := h.crateDatesFor(ctx, repo, name, version)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return false
		}
		if !d.found {
			return true // the proxy pipeline answers upstream's 404
		}
		if len(d.times) == 0 {
			log.Printf("nexspence: minimum_package_age not enforceable for %s/%s — upstream has no publish times for it", repo.Name, name)
			h.age.mu.Lock()
			if h.age.undated == nil || len(h.age.undated) >= maxAgeEntries {
				h.age.undated = make(map[string]time.Time)
			}
			h.age.undated[crateKey] = time.Now()
			h.age.mu.Unlock()
			return true
		}
		if t, known = d.times[version]; !known {
			c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
				"%s %s has no publish time upstream; this repository enforces a minimum package age", name, version)})
			return false
		}
		h.age.mu.Lock()
		if h.age.times == nil || len(h.age.times) >= maxAgeEntries {
			h.age.times = make(map[string]time.Time)
		}
		h.age.times[key] = t
		h.age.mu.Unlock()
	}
	if age := time.Since(t); age < minAge {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"%s %s was published %s ago; this repository enforces a minimum package age of %s",
			name, version, age.Round(time.Minute), minAge)})
		return false
	}
	return true
}

// filterIndexRewrite returns the rewrite that drops too-young versions from
// a sparse-index file. Kept lines are copied byte for byte.
func (h *Handler) filterIndexRewrite(c *gin.Context, repo *domain.Repository, name string, minAge time.Duration) func([]byte) []byte {
	return func(body []byte) []byte {
		d := h.withAPIFallback(c.Request.Context(), repo, name, parseIndexDates(body))
		if len(d.listed) == 0 {
			return body
		}
		if len(d.times) == 0 {
			log.Printf("nexspence: minimum_package_age not enforceable for %s/%s — upstream has no publish times for it", repo.Name, name)
			return body
		}
		cutoff := time.Now().Add(-minAge)
		var out bytes.Buffer
		for _, line := range bytes.Split(body, []byte("\n")) {
			vers, _, _, ok := parseIndexLine(line)
			if !ok {
				continue
			}
			if t, dated := d.times[vers]; dated && !t.After(cutoff) {
				out.Write(line)
				out.WriteByte('\n')
			}
		}
		if bytes.Equal(out.Bytes(), body) {
			return body
		}
		// The ETag and checksums upstream sent (or the cache derived)
		// describe the original; cargo replays the ETag as If-None-Match.
		sum := sha256.Sum256(out.Bytes())
		c.Header("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
		for _, k := range []string{"X-Checksum-SHA256", "X-Checksum-SHA1", "X-Checksum-MD5", "Content-Length"} {
			c.Writer.Header().Del(k)
		}
		return out.Bytes()
	}
}

// applyAgePolicy enforces minimum_package_age on a proxy GET. It reports done
// when it has answered the request itself (a refusal); otherwise it may set
// rewrite to filter a sparse-index file.
func (h *Handler) applyAgePolicy(c *gin.Context, repo *domain.Repository, p string, minAge time.Duration, rewrite *func([]byte) []byte) (done bool) {
	if rest, ok := strings.CutPrefix(p, "/index/"); ok && rest != "config.json" {
		parts := strings.Split(rest, "/")
		if name := parts[len(parts)-1]; name != "" {
			*rewrite = h.filterIndexRewrite(c, repo, strings.ToLower(name), minAge)
		}
		return false
	}
	if name, version, ok := parseDownloadPath(p); ok {
		return !h.versionAgeAllowed(c, repo, name, version, minAge)
	}
	return false
}
