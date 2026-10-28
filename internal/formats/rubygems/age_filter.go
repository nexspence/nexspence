package rubygems

// Minimum package age on a RubyGems proxy (#643, after npm and PyPI in #323
// and Go): a version younger than proxy_config.minimum_package_age is left out
// of /info/<gem> and refused on /gems/<file>.gem and its quick gemspec.
//
// Where the dates come from: rubygems.org writes each version's publish time
// into its compact-index line ("created_at:" among the requirements after the
// "|"), so the document Bundler resolves from is also the one that dates it.
// An upstream whose /info lines carry no created_at is asked its JSON API
// (/api/v1/versions/<gem>.json, "created_at" per version and platform), which
// rubygems.org and most mirrors serve on the same host.
//
// /versions is served unchanged. It lists every gem of the registry (tens of
// MB on rubygems.org) with the MD5 of each gem's /info file, and dating every
// gem in it per request is out of the question. Bundler uses that MD5 only to
// skip refetching an /info file whose local copy still matches; a filtered
// /info therefore never matches, and Bundler fetches it again on every
// resolution — which is also how it notices a version that has aged in. The
// version lists in /versions are not what Bundler resolves against.
//
// A filtered /info no longer matches the ETag and Repr-Digest/Digest headers
// upstream sent for the original, and Bundler verifies the body against
// Repr-Digest (MismatchedChecksumError) and older Bundlers against the ETag
// (the MD5 of the body). Both are re-derived from the body actually served.
//
// The legacy Marshal indexes (specs.4.8.gz and friends) and the retired
// dependency API carry no dates and are not worth decoding: under the policy
// they answer 404, as on a hosted repository, and gem/Bundler fall back to
// the compact index.
//
// Hybrid failure mode, as for npm, PyPI and Go: a version without a date (or
// missing from the index) in a gem whose other versions are dated is refused;
// a gem with no dated version at all cannot be policed, so the gap is logged
// and the gem served.

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // compact-index ETag, not security
	"crypto/sha256"
	"encoding/base64"
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
	// maxInfoBytes bounds one /info read; the largest gems on rubygems.org
	// (thousands of platform builds) stay well below 1 MB.
	maxInfoBytes = 16 << 20
	// maxVersionsJSONBytes bounds a JSON API read; rails' runs to ~1 MB.
	maxVersionsJSONBytes = 32 << 20
	// maxAgeEntries bounds each in-memory cache; a full one is cleared.
	maxAgeEntries = 100_000
)

// gemDates is what is known about a gem's versions: every version its /info
// lists, and the publish time of those that are dated.
type gemDates struct {
	found  bool // upstream knows the gem
	listed map[string]bool
	times  map[string]time.Time
}

// apiEntry is one cached JSON API lookup.
type apiEntry struct {
	times   map[string]time.Time
	fetched time.Time
}

// ageCache memoizes publish times per repository, gem and version (immutable
// once known), and JSON API answers and gems found to carry no dates at all
// per repository and gem (for the metadata TTL).
type ageCache struct {
	mu      sync.Mutex
	times   map[string]time.Time
	api     map[string]apiEntry
	undated map[string]time.Time
}

// infoVersion returns the version (with platform) of one /info line.
func infoVersion(line string) string {
	v, _, _ := strings.Cut(line, " ")
	return v
}

// infoCreatedAt returns the created_at requirement of one /info line.
func infoCreatedAt(line string) (time.Time, bool) {
	_, reqs, ok := strings.Cut(line, "|")
	if !ok {
		return time.Time{}, false
	}
	for _, req := range strings.Split(reqs, ",") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(req), "created_at:"); ok {
			t, err := time.Parse(time.RFC3339, v)
			return t, err == nil
		}
	}
	return time.Time{}, false
}

// infoLines splits an /info body into its header (through the "---" line)
// and its version lines, both without their trailing newlines.
func infoLines(body []byte) (header, lines []string) {
	all := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	start := 0
	for i, l := range all {
		if l == "---" {
			start = i + 1
			break
		}
	}
	return all[:start], all[start:]
}

// parseInfoDates reads the versions and created_at times of an /info body.
func parseInfoDates(body []byte) gemDates {
	g := gemDates{found: true, listed: map[string]bool{}, times: map[string]time.Time{}}
	_, lines := infoLines(body)
	for _, l := range lines {
		if l == "" {
			continue
		}
		v := infoVersion(l)
		g.listed[v] = true
		if t, ok := infoCreatedAt(l); ok {
			g.times[v] = t
		}
	}
	return g
}

// apiVersion is one element of /api/v1/versions/<gem>.json.
type apiVersion struct {
	Number    string `json:"number"`
	Platform  string `json:"platform"`
	CreatedAt string `json:"created_at"`
}

// apiTimes returns the publish times the upstream JSON API gives for a gem,
// keyed as /info names them ("1.0.0", "1.0.0-java"). An upstream without the
// API, or an unusable answer, yields none.
func (h *Handler) apiTimes(ctx context.Context, repo *domain.Repository, gem string) map[string]time.Time {
	key := repo.Name + "\x00" + gem
	h.age.mu.Lock()
	if e, ok := h.age.api[key]; ok && time.Since(e.fetched) < repoproxy.MetadataMaxAge(repo) {
		h.age.mu.Unlock()
		return e.times
	}
	h.age.mu.Unlock()

	times := map[string]time.Time{}
	resp, err := repoproxy.FetchUpstreamOnce(ctx, repo, "/api/v1/versions/"+url.PathEscape(gem)+".json", "", nil)
	if err != nil {
		return times // not cached: the next request asks again
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		var doc []apiVersion
		if json.NewDecoder(io.LimitReader(resp.Body, maxVersionsJSONBytes)).Decode(&doc) == nil {
			for _, v := range doc {
				t, err := time.Parse(time.RFC3339, v.CreatedAt)
				if err != nil || v.Number == "" {
					continue
				}
				key := v.Number
				if v.Platform != "" && v.Platform != "ruby" {
					key += "-" + v.Platform
				}
				times[key] = t
			}
		}
	}

	h.age.mu.Lock()
	if h.age.api == nil || len(h.age.api) >= maxAgeEntries {
		h.age.api = make(map[string]apiEntry)
	}
	h.age.api[key] = apiEntry{times: times, fetched: time.Now()}
	h.age.mu.Unlock()
	return times
}

// withAPIFallback dates an /info that carries no created_at from the JSON API.
func (h *Handler) withAPIFallback(ctx context.Context, repo *domain.Repository, gem string, g gemDates) gemDates {
	if len(g.times) > 0 || !g.found {
		return g
	}
	for v, t := range h.apiTimes(ctx, repo, gem) {
		if g.listed[v] {
			g.times[v] = t
		}
	}
	return g
}

// fetchInfo reads /info/<gem> from upstream. found is false on 404/410.
func fetchInfo(ctx context.Context, repo *domain.Repository, gem string) (body []byte, found bool, err error) {
	resp, err := repoproxy.FetchUpstreamOnce(ctx, repo, "/info/"+gem, "", nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, false, nil
	case resp.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("upstream /info/%s: %s", gem, resp.Status)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxInfoBytes))
	if err != nil {
		return nil, false, err
	}
	return body, true, nil
}

// rememberTime records an immutable publish time.
func (h *Handler) rememberTime(key string, t time.Time) {
	h.age.mu.Lock()
	if h.age.times == nil || len(h.age.times) >= maxAgeEntries {
		h.age.times = make(map[string]time.Time)
	}
	h.age.times[key] = t
	h.age.mu.Unlock()
}

// gemDatesFor dates a gem for the download gate: the proxy's cached /info
// first (a read that does not count a download), upstream when the cached
// copy does not date version — it may predate the version.
func (h *Handler) gemDatesFor(ctx context.Context, repo *domain.Repository, gem, version string) (gemDates, error) {
	if asset, err := h.deps.Assets.GetByPath(ctx, repo.Name, "/info/"+gem); err == nil && asset != nil {
		if body := base.ReadCachedBlob(ctx, h.deps, asset, maxInfoBytes); body != nil {
			if g := parseInfoDates(body); !g.times[version].IsZero() {
				return g, nil
			}
		}
	}
	body, found, err := fetchInfo(ctx, repo, gem)
	if err != nil {
		return gemDates{}, err
	}
	if !found {
		return gemDates{}, nil
	}
	return h.withAPIFallback(ctx, repo, gem, parseInfoDates(body)), nil
}

// versionAgeAllowed is the download half of the policy: a .gem or quick
// gemspec of a version must not bypass /info. On refusal the 403 is already
// written.
func (h *Handler) versionAgeAllowed(c *gin.Context, repo *domain.Repository, gem, version string, minAge time.Duration) bool {
	ctx := c.Request.Context()
	gemKey := repo.Name + "\x00" + gem
	key := gemKey + "\x00" + version
	h.age.mu.Lock()
	t, known := h.age.times[key]
	undatedAt, undated := h.age.undated[gemKey]
	h.age.mu.Unlock()
	if !known && undated && time.Since(undatedAt) < repoproxy.MetadataMaxAge(repo) {
		return true // logged when found undated
	}

	if !known {
		g, err := h.gemDatesFor(ctx, repo, gem, version)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return false
		}
		if !g.found {
			return true // the proxy pipeline answers upstream's 404
		}
		if len(g.times) == 0 {
			log.Printf("nexspence: minimum_package_age not enforceable for %s/%s — upstream has no publish times for it", repo.Name, gem)
			h.age.mu.Lock()
			if h.age.undated == nil || len(h.age.undated) >= maxAgeEntries {
				h.age.undated = make(map[string]time.Time)
			}
			h.age.undated[gemKey] = time.Now()
			h.age.mu.Unlock()
			return true
		}
		if t, known = g.times[version]; !known {
			c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
				"%s-%s has no publish time upstream; this repository enforces a minimum package age", gem, version)})
			return false
		}
		h.rememberTime(key, t)
	}
	if age := time.Since(t); age < minAge {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"%s-%s was published %s ago; this repository enforces a minimum package age of %s",
			gem, version, age.Round(time.Minute), minAge)})
		return false
	}
	return true
}

// filterInfoRewrite returns the rewrite that drops too-young versions from an
// /info/<gem> body and re-derives the integrity headers of what is served.
func (h *Handler) filterInfoRewrite(c *gin.Context, repo *domain.Repository, gem string, minAge time.Duration) func([]byte) []byte {
	return func(body []byte) []byte {
		g := h.withAPIFallback(c.Request.Context(), repo, gem, parseInfoDates(body))
		if len(g.listed) == 0 {
			return body
		}
		if len(g.times) == 0 {
			log.Printf("nexspence: minimum_package_age not enforceable for %s/%s — upstream has no publish times for it", repo.Name, gem)
			return body
		}
		cutoff := time.Now().Add(-minAge)
		header, lines := infoLines(body)
		var b bytes.Buffer
		for _, l := range header {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		for _, l := range lines {
			if l == "" {
				continue
			}
			if t, ok := g.times[infoVersion(l)]; ok && !t.After(cutoff) {
				b.WriteString(l)
				b.WriteByte('\n')
			}
		}
		out := b.Bytes()
		if bytes.Equal(out, body) {
			return body
		}
		setBodyIntegrity(c, out)
		return out
	}
}

// setBodyIntegrity replaces the headers that describe the upstream original
// with ones for body: the ETag rubygems.org uses (the quoted MD5, which
// Bundler before 2.5 compares against the body) and Repr-Digest/Digest
// (sha-256, which current Bundler verifies). The checksum headers of the
// cached original are dropped.
func setBodyIntegrity(c *gin.Context, body []byte) {
	sum := md5.Sum(body) //nolint:gosec // protocol ETag, not security
	c.Header("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	sha := sha256.Sum256(body)
	digest := "sha-256=:" + base64.StdEncoding.EncodeToString(sha[:]) + ":"
	c.Header("Repr-Digest", digest)
	c.Header("Digest", digest)
	for _, k := range []string{"X-Checksum-SHA256", "X-Checksum-SHA1", "X-Checksum-MD5", "Content-Length"} {
		c.Writer.Header().Del(k)
	}
}

// legacyIndex reports the Marshal-era endpoints that list versions without
// dates.
func legacyIndex(p string) bool {
	switch p {
	case "/specs.4.8.gz", "/latest_specs.4.8.gz", "/prerelease_specs.4.8.gz",
		"/specs.4.8", "/latest_specs.4.8", "/prerelease_specs.4.8",
		"/api/v1/dependencies", "/api/v1/dependencies.json":
		return true
	}
	return false
}

// gemFileVersion returns the gem and version (with platform) a .gem or quick
// gemspec path names.
func gemFileVersion(p string) (gem, version string, ok bool) {
	if file, found := strings.CutPrefix(p, "/gems/"); found && !strings.Contains(file, "/") {
		return parseGemFilename(file)
	}
	if file, found := strings.CutPrefix(p, "/quick/Marshal.4.8/"); found && !strings.Contains(file, "/") {
		if stem, found := strings.CutSuffix(file, ".gemspec.rz"); found {
			return parseGemFilename(stem + ".gem")
		}
	}
	return "", "", false
}

// applyAgePolicy enforces minimum_package_age on a proxy GET. It reports
// done when it has answered the request itself (a refusal or a legacy index);
// otherwise it may set rewrite to filter an /info body.
func (h *Handler) applyAgePolicy(c *gin.Context, repo *domain.Repository, p string, minAge time.Duration, rewrite *func([]byte) []byte) (done bool) {
	if legacyIndex(p) {
		c.Status(http.StatusNotFound)
		return true
	}
	if gem, ok := strings.CutPrefix(p, "/info/"); ok && gem != "" && !strings.Contains(gem, "/") {
		*rewrite = h.filterInfoRewrite(c, repo, gem, minAge)
		return false
	}
	if gem, version, ok := gemFileVersion(p); ok {
		return !h.versionAgeAllowed(c, repo, gem, version, minAge)
	}
	return false
}
