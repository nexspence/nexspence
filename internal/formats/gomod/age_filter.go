package gomod

// Minimum package age on a Go proxy (#643, after npm and PyPI in #323): a
// version younger than proxy_config.minimum_package_age is left out of
// @v/list and @latest and refused on .info/.mod/.zip.
//
// A version's publish time is the Time field of its upstream .info document;
// @v/list itself carries only names. Times are immutable, so a known one is
// cached for the life of the process; a version whose .info has no Time is
// re-asked after the metadata TTL.
//
// Hybrid failure mode, as for npm and PyPI: a version without a Time in a
// module whose other versions have one is refused (its age is unknowable);
// a module where no version has a Time cannot be policed, so the gap is
// logged and the module served.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/mod/module"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

const (
	// maxInfoBytes bounds one upstream .info or @latest read; the document is
	// two short fields.
	maxInfoBytes = 64 << 10
	// maxListBytes bounds an upstream @v/list read.
	maxListBytes = 4 << 20
	// infoFetchParallelism caps concurrent upstream .info requests while a
	// version list is dated.
	infoFetchParallelism = 8
	// maxVersionTimes bounds the in-memory time cache; it is cleared when full.
	maxVersionTimes = 100_000
)

// versionTime is one cached .info lookup: found reports whether upstream has
// the version at all, known whether its document carried a Time.
type versionTime struct {
	t       time.Time
	found   bool
	known   bool
	fetched time.Time
}

// ageCache memoizes upstream .info times per repository, module and version.
type ageCache struct {
	mu    sync.Mutex
	times map[string]versionTime
}

// infoDoc is the shape of a GOPROXY .info and @latest document.
type infoDoc struct {
	Version string `json:"Version"`
	Time    string `json:"Time"`
}

// infoTime parses an .info body; ok is false when the body has no usable Time.
func infoTime(body []byte) (infoDoc, time.Time, bool) {
	var doc infoDoc
	if err := json.Unmarshal(body, &doc); err != nil || doc.Time == "" {
		return doc, time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, doc.Time)
	if err != nil {
		return doc, time.Time{}, false
	}
	return doc, t, true
}

// fetchSmall GETs an upstream document and returns its body on 200. found is
// false on 404/410, which GOPROXY uses for "no such version or module".
func fetchSmall(ctx context.Context, repo *domain.Repository, upstreamPath string, limit int64) (body []byte, found bool, err error) {
	resp, err := repoproxy.FetchUpstreamOnce(ctx, repo, upstreamPath, "", nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, false, nil
	case resp.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("upstream %s: %s", upstreamPath, resp.Status)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, false, err
	}
	return body, true, nil
}

// versionTime returns the publish time of version from the upstream .info.
// modPrefix is the escaped module path as it appears in the request ("/" +
// module, before "/@v/"). An error is not cached: the next request asks again.
func (h *Handler) versionTime(ctx context.Context, repo *domain.Repository, modPrefix, version string) (versionTime, error) {
	key := repo.Name + "\x00" + modPrefix + "\x00" + version
	h.age.mu.Lock()
	if e, ok := h.age.times[key]; ok && (e.known || time.Since(e.fetched) < repoproxy.MetadataMaxAge(repo)) {
		h.age.mu.Unlock()
		return e, nil
	}
	h.age.mu.Unlock()

	escaped, err := module.EscapeVersion(version)
	if err != nil {
		return versionTime{}, err
	}
	infoPath := modPrefix + "/@v/" + escaped + ".info"
	e := versionTime{fetched: time.Now()}
	// A cached .info is the upstream document, and it never changes.
	if asset, err := h.deps.Assets.GetByPath(ctx, repo.Name, infoPath); err == nil && asset != nil {
		if body := base.ReadCachedBlob(ctx, h.deps, asset, maxInfoBytes); body != nil {
			_, e.t, e.known = infoTime(body)
			e.found = e.known
		}
	}
	if !e.known {
		body, found, err := fetchSmall(ctx, repo, infoPath, maxInfoBytes)
		if err != nil {
			return versionTime{}, err
		}
		e.found = found
		if found {
			_, e.t, e.known = infoTime(body)
		}
	}

	h.age.mu.Lock()
	if h.age.times == nil || len(h.age.times) >= maxVersionTimes {
		h.age.times = make(map[string]versionTime)
	}
	h.age.times[key] = e
	h.age.mu.Unlock()
	return e, nil
}

// datedVersions looks up the time of every version, infoFetchParallelism at
// a time. A version whose lookup failed is reported as unknown.
func (h *Handler) datedVersions(ctx context.Context, repo *domain.Repository, modPrefix string, versions []string) map[string]versionTime {
	out := make(map[string]versionTime, len(versions))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, infoFetchParallelism)
	for _, v := range versions {
		wg.Add(1)
		sem <- struct{}{}
		go func(v string) {
			defer func() { <-sem; wg.Done() }()
			e, err := h.versionTime(ctx, repo, modPrefix, v)
			if err != nil {
				e = versionTime{}
			}
			mu.Lock()
			out[v] = e
			mu.Unlock()
		}(v)
	}
	wg.Wait()
	return out
}

// anyKnown reports whether at least one version has a Time.
func anyKnown(times map[string]versionTime) bool {
	for _, e := range times {
		if e.known {
			return true
		}
	}
	return false
}

// splitList returns the non-empty lines of an @v/list body.
func splitList(body []byte) []string {
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		if v := strings.TrimSpace(line); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// oldEnough keeps the versions published at or before cutoff, in list order.
// When no version has a Time the list is returned whole and ok is false.
func oldEnough(versions []string, times map[string]versionTime, cutoff time.Time) (kept []string, ok bool) {
	if !anyKnown(times) {
		return versions, false
	}
	for _, v := range versions {
		if e := times[v]; e.known && !e.t.After(cutoff) {
			kept = append(kept, v)
		}
	}
	return kept, true
}

// filterListRewrite returns the rewrite that drops too-young versions from an
// @v/list body.
func (h *Handler) filterListRewrite(ctx context.Context, repo *domain.Repository, modPrefix string, minAge time.Duration) func([]byte) []byte {
	return func(body []byte) []byte {
		versions := splitList(body)
		if len(versions) == 0 {
			return body
		}
		kept, ok := oldEnough(versions, h.datedVersions(ctx, repo, modPrefix, versions), time.Now().Add(-minAge))
		if !ok {
			log.Printf("nexspence: minimum_package_age not enforceable for %s%s — upstream .info documents carry no Time", repo.Name, modPrefix)
			return body
		}
		if len(kept) == 0 {
			return []byte{}
		}
		return []byte(strings.Join(kept, "\n") + "\n")
	}
}

// serveLatestByAge answers @latest with the newest version old enough to
// serve. The upstream @latest is served as is when it is old enough (it may
// be a pseudo-version of a module without tags, which @v/list never names);
// otherwise the newest qualifying version from @v/list answers, and a module
// with none answers 404, which the go command reads as "no version".
func (h *Handler) serveLatestByAge(c *gin.Context, repo *domain.Repository, modPrefix string, minAge time.Duration) {
	ctx := c.Request.Context()
	cutoff := time.Now().Add(-minAge)

	latestBody, latestFound, err := fetchSmall(ctx, repo, modPrefix+"/@latest", maxInfoBytes)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	latestDoc, latestT, latestDated := infoTime(latestBody)
	if latestFound && latestDated && !latestT.After(cutoff) {
		c.Data(http.StatusOK, "application/json", latestBody)
		return
	}

	listBody, _, err := fetchSmall(ctx, repo, modPrefix+"/@v/list", maxListBytes)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	versions := splitList(listBody)
	times := h.datedVersions(ctx, repo, modPrefix, versions)
	if !latestDated && !anyKnown(times) {
		// Nothing upstream is dated: the policy cannot apply.
		if latestFound {
			log.Printf("nexspence: minimum_package_age not enforceable for %s%s — upstream .info documents carry no Time", repo.Name, modPrefix)
			c.Data(http.StatusOK, "application/json", latestBody)
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "no versions found"})
		return
	}
	kept, _ := oldEnough(versions, times, cutoff)
	comps := make([]domain.Component, 0, len(kept))
	for _, v := range kept {
		comps = append(comps, domain.Component{Version: v})
	}
	best, ok := latestVersion(comps)
	if !ok {
		msg := "no version is old enough; this repository enforces a minimum package age of " + minAge.String()
		if latestDoc.Version != "" {
			msg = latestDoc.Version + " and every older version are younger than this repository's minimum package age of " + minAge.String()
		}
		c.JSON(http.StatusNotFound, gin.H{"error": msg})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"Version": best.Version,
		"Time":    times[best.Version].t.UTC().Format(time.RFC3339),
	})
}

// versionAgeAllowed is the download half of the policy: .info, .mod and .zip
// of a version must not bypass the list. On refusal the 403 is already
// written.
func (h *Handler) versionAgeAllowed(c *gin.Context, repo *domain.Repository, modPrefix, version string, minAge time.Duration) bool {
	ctx := c.Request.Context()
	e, err := h.versionTime(ctx, repo, modPrefix, version)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return false
	}
	if !e.found {
		return true // the proxy pipeline answers upstream's 404
	}
	if !e.known {
		// Undated: refused when the module's other versions are dated.
		listBody, _, err := fetchSmall(ctx, repo, modPrefix+"/@v/list", maxListBytes)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return false
		}
		if !anyKnown(h.datedVersions(ctx, repo, modPrefix, splitList(listBody))) {
			log.Printf("nexspence: minimum_package_age not enforceable for %s%s@%s — upstream .info documents carry no Time", repo.Name, modPrefix, version)
			return true
		}
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"%s has no publish time upstream; this repository enforces a minimum package age", version)})
		return false
	}
	if age := time.Since(e.t); age < minAge {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"%s was published %s ago; this repository enforces a minimum package age of %s",
			version, age.Round(time.Minute), minAge)})
		return false
	}
	return true
}
