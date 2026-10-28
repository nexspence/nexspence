package nuget

// Minimum package age on a NuGet proxy (#643, after npm and PyPI in #323 and
// Go): a version younger than proxy_config.minimum_package_age is left out
// of the flat container version list ({id}/index.json, the list dotnet and
// nuget.exe resolve versions from) and of registration pages, and its .nupkg
// and .nuspec are refused with 403.
//
// Publish times come from the upstream registration index of the package
// (catalogEntry.published), in the hive the upstream service index
// advertises as RegistrationsBaseUrl/3.6.0 (SemVer 2.0.0 aware) or the next
// best one. An index may inline its pages or link to them; linked pages are
// fetched. The proxy's own cached copy of the index is used while it is
// fresh, upstream otherwise, and the result is held in memory for the
// metadata TTL.
//
// Unlisted versions: nuget.org replaces "published" with 1900-01-01 when a
// version is unlisted, so the real publish time is gone. Such a version is
// dated by the leaf's commitTimeStamp instead — the catalog commit that last
// touched it, which can never be earlier than the publish — so it passes only
// when even that upper bound is older than the cutoff. Without a
// commitTimeStamp it is undated. The leaf keeps "listed": false, so clients
// still skip it when resolving a range.
//
// Hybrid failure mode, as for npm and PyPI: an undated version among dated
// ones is hidden and refused (its age is unknowable); a package whose
// registration carries no date at all, or an upstream that advertises no
// registration hive, cannot be policed, so the gap is logged and the package
// served. A registration 404 from an upstream that does advertise a hive
// means the package is too new to be indexed yet: its versions are undated
// and hidden.

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

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

const (
	// maxRegistrationBytes bounds one registration index or page read; a
	// nuget.org page of 64 leaves with full catalog entries is ~0.5 MB.
	maxRegistrationBytes = 64 << 20
	// maxServiceIndexBytes bounds the upstream service index read.
	maxServiceIndexBytes = 4 << 20
	// maxFlatIndexBytes bounds an upstream flat container version list read.
	maxFlatIndexBytes = 16 << 20
	// pageFetchParallelism caps concurrent upstream registration page reads.
	pageFetchParallelism = 8
	// maxPackageDates bounds the in-memory date cache; it is cleared when full.
	maxPackageDates = 4096
	// missingVersionRecheck is how long a cached package without the asked
	// version is trusted before upstream is asked again.
	missingVersionRecheck = time.Minute
)

// registrationTypes are the service index resource types that name a
// registration hive, best first: the SemVer 2.0.0 hive also lists versions
// the older hives leave out, which would otherwise read as undated.
var registrationTypes = []string{
	"RegistrationsBaseUrl/3.6.0",
	"RegistrationsBaseUrl/Versioned",
	"RegistrationsBaseUrl/3.4.0",
	"RegistrationsBaseUrl/3.0.0-rc",
	"RegistrationsBaseUrl/3.0.0-beta",
	"RegistrationsBaseUrl",
}

// versionDate is the publish time of one version; known is false when the
// registration carries no usable date for it.
type versionDate struct {
	t     time.Time
	known bool
}

// packageDates is one package's dated versions, keyed by versionKey.
// enforceable is false when the policy cannot apply to the package.
type packageDates struct {
	versions    map[string]versionDate
	enforceable bool
	fetched     time.Time
}

// anyKnown reports whether at least one version is dated.
func (p packageDates) anyKnown() bool {
	for _, d := range p.versions {
		if d.known {
			return true
		}
	}
	return false
}

// registrationBase is a repository's cached registration hive URL; empty
// when the upstream advertises none.
type registrationBase struct {
	url     string
	fetched time.Time
}

// ageCache memoizes registration hives and package dates per repository.
type ageCache struct {
	mu       sync.Mutex
	packages map[string]packageDates
	hives    map[string]registrationBase
}

// regLeaf is the part of a registration leaf the policy reads. catalogEntry
// is an object inside an index or page.
type regLeaf struct {
	CommitTimeStamp string `json:"commitTimeStamp"`
	CatalogEntry    struct {
		Version   string `json:"version"`
		Published string `json:"published"`
	} `json:"catalogEntry"`
}

// regPage is a registration page: inline in an index (items present) or
// linked by @id (items absent), or a page document of its own.
type regPage struct {
	ID    string     `json:"@id"`
	Items *[]regLeaf `json:"items"`
}

// regIndex is a registration index.
type regIndex struct {
	Items []regPage `json:"items"`
}

// parseNuGetTime parses a registration timestamp. nuget.org writes RFC 3339
// with a fraction; some feeds leave the zone out, which is read as UTC.
func parseNuGetTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02T15:04:05.999999999", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// leafDate dates one registration leaf from its published time, or — for an
// unlisted version, whose published is the 1900 marker — from its
// commitTimeStamp, an upper bound of the publish time.
func leafDate(published, commitTimeStamp string) versionDate {
	t, ok := parseNuGetTime(published)
	if ok && t.Year() > 1900 {
		return versionDate{t: t, known: true}
	}
	if ct, ok := parseNuGetTime(commitTimeStamp); ok && ct.Year() > 1900 {
		return versionDate{t: ct, known: true}
	}
	return versionDate{}
}

// fetchBody GETs an upstream URL and returns its body on 200; found is
// false on 404/410.
func fetchBody(ctx context.Context, repo *domain.Repository, upstreamURL string, limit int64) (body []byte, found bool, err error) {
	resp, err := repoproxy.FetchUpstreamOnce(ctx, repo, upstreamURL, "", nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, false, nil
	case resp.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("upstream %s: %s", upstreamURL, resp.Status)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, false, err
	}
	return body, true, nil
}

// registrationHive returns the registration base URL (with a trailing
// slash) the upstream service index advertises, empty when it has none.
func (h *Handler) registrationHive(ctx context.Context, repo *domain.Repository, origin string) (string, error) {
	h.age.mu.Lock()
	if e, ok := h.age.hives[repo.Name]; ok && time.Since(e.fetched) < repoproxy.MetadataMaxAge(repo) {
		h.age.mu.Unlock()
		return e.url, nil
	}
	h.age.mu.Unlock()

	body, found, err := fetchBody(ctx, repo, origin+"/v3/index.json", maxServiceIndexBytes)
	if err != nil {
		return "", err
	}
	hive := ""
	if found {
		var idx struct {
			Resources []struct {
				ID   string `json:"@id"`
				Type string `json:"@type"`
			} `json:"resources"`
		}
		if err := json.Unmarshal(body, &idx); err != nil {
			return "", fmt.Errorf("upstream service index: %w", err)
		}
	pick:
		for _, want := range registrationTypes {
			for _, r := range idx.Resources {
				if r.Type == want && r.ID != "" {
					hive = strings.TrimRight(r.ID, "/") + "/"
					break pick
				}
			}
		}
	}

	h.age.mu.Lock()
	if h.age.hives == nil {
		h.age.hives = make(map[string]registrationBase)
	}
	h.age.hives[repo.Name] = registrationBase{url: hive, fetched: time.Now()}
	h.age.mu.Unlock()
	return hive, nil
}

// cachedRegistration returns the proxy's own copy of an upstream
// registration document when it is fresh, else nil. The copy is cached
// under the request path, which is the upstream URL less the origin.
func (h *Handler) cachedRegistration(ctx context.Context, repo *domain.Repository, origin, upstreamURL string) []byte {
	local, ok := strings.CutPrefix(upstreamURL, origin)
	if !ok || !strings.HasPrefix(local, "/") {
		return nil
	}
	asset, err := h.deps.Assets.GetByPath(ctx, repo.Name, local)
	if err != nil || asset == nil || time.Since(asset.LastModified) > repoproxy.MetadataMaxAge(repo) {
		return nil
	}
	return base.ReadCachedBlob(ctx, h.deps, asset, maxRegistrationBytes)
}

// packageDates returns the dated versions of package id. want, when set, is
// a version the caller needs: a cached entry without it is re-read from
// upstream once missingVersionRecheck has passed, since a just-published
// version reaches the registration after the flat container.
func (h *Handler) packageDates(ctx context.Context, repo *domain.Repository, id, want string) (packageDates, error) {
	id = strings.ToLower(id)
	key := repo.Name + "\x00" + id
	force := false
	h.age.mu.Lock()
	if e, ok := h.age.packages[key]; ok && time.Since(e.fetched) < repoproxy.MetadataMaxAge(repo) {
		_, has := e.versions[versionKey(want)]
		if want == "" || has || !e.enforceable || time.Since(e.fetched) < missingVersionRecheck {
			h.age.mu.Unlock()
			return e, nil
		}
		force = true
	}
	h.age.mu.Unlock()

	origin := nugetRemoteOrigin(remoteURLOf(repo))
	hive, err := h.registrationHive(ctx, repo, origin)
	if err != nil {
		return packageDates{}, err
	}
	out := packageDates{versions: map[string]versionDate{}, fetched: time.Now()}
	if hive != "" {
		out.enforceable = true
		indexURL := hive + id + "/index.json"
		var body []byte
		if !force {
			body = h.cachedRegistration(ctx, repo, origin, indexURL)
		}
		found := body != nil
		if !found {
			if body, found, err = fetchBody(ctx, repo, indexURL, maxRegistrationBytes); err != nil {
				return packageDates{}, err
			}
		}
		if found {
			if err := h.readRegistration(ctx, repo, body, out.versions); err != nil {
				return packageDates{}, err
			}
			// A registration without a single date cannot be policed; a
			// missing one (404) means "not indexed yet" and stays enforced.
			out.enforceable = out.anyKnown()
		}
	}

	h.age.mu.Lock()
	if h.age.packages == nil || len(h.age.packages) >= maxPackageDates {
		h.age.packages = make(map[string]packageDates)
	}
	h.age.packages[key] = out
	h.age.mu.Unlock()
	return out, nil
}

// readRegistration collects the dated versions of a registration index into
// dates, fetching the pages it links rather than inlines.
func (h *Handler) readRegistration(ctx context.Context, repo *domain.Repository, body []byte, dates map[string]versionDate) error {
	var idx regIndex
	if err := json.Unmarshal(body, &idx); err != nil {
		return fmt.Errorf("upstream registration: %w", err)
	}
	var linked []string
	for _, page := range idx.Items {
		if page.Items == nil {
			if page.ID != "" {
				linked = append(linked, page.ID)
			}
			continue
		}
		addLeaves(dates, *page.Items)
	}
	if len(linked) == 0 {
		return nil
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	sem := make(chan struct{}, pageFetchParallelism)
	for _, pageURL := range linked {
		wg.Add(1)
		sem <- struct{}{}
		go func(pageURL string) {
			defer func() { <-sem; wg.Done() }()
			leaves, err := fetchPage(ctx, repo, pageURL)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			addLeaves(dates, leaves)
		}(pageURL)
	}
	wg.Wait()
	return firstErr
}

// fetchPage reads the leaves of one linked registration page.
func fetchPage(ctx context.Context, repo *domain.Repository, pageURL string) ([]regLeaf, error) {
	body, found, err := fetchBody(ctx, repo, pageURL, maxRegistrationBytes)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("upstream registration page %s: not found", pageURL)
	}
	var page regPage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("upstream registration page: %w", err)
	}
	if page.Items == nil {
		return nil, nil
	}
	return *page.Items, nil
}

// addLeaves records each leaf's date under its version key.
func addLeaves(dates map[string]versionDate, leaves []regLeaf) {
	for _, l := range leaves {
		if l.CatalogEntry.Version == "" {
			continue
		}
		dates[versionKey(l.CatalogEntry.Version)] = leafDate(l.CatalogEntry.Published, l.CommitTimeStamp)
	}
}

// filterFlatIndexRewrite returns the rewrite that drops too-young versions
// from a flat container version list. A body without a "versions" array
// (not a version list after all) passes unchanged.
func (h *Handler) filterFlatIndexRewrite(ctx context.Context, repo *domain.Repository, id string, minAge time.Duration) func([]byte) []byte {
	return func(body []byte) []byte {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(body, &doc); err != nil {
			return body
		}
		var versions []string
		if raw, ok := doc["versions"]; !ok || json.Unmarshal(raw, &versions) != nil {
			return body
		}
		if len(versions) == 0 {
			return body
		}
		kept := []string{}
		dates, err := h.packageDates(ctx, repo, id, "")
		switch {
		case err != nil:
			// The gate must not open on a transient upstream failure: no
			// version is offered until the dates can be read.
			log.Printf("nexspence: minimum_package_age: dating %s/%s failed, hiding its versions: %v", repo.Name, id, err)
		case !dates.enforceable:
			log.Printf("nexspence: minimum_package_age not enforceable for %s/%s — upstream registration carries no publish times", repo.Name, id)
			return body
		default:
			cutoff := time.Now().Add(-minAge)
			for _, v := range versions {
				if d := dates.versions[versionKey(v)]; d.known && !d.t.After(cutoff) {
					kept = append(kept, v)
				}
			}
			if len(kept) == len(versions) {
				return body
			}
		}
		raw, err := json.Marshal(kept)
		if err != nil {
			return body
		}
		doc["versions"] = raw
		out, err := json.Marshal(doc)
		if err != nil {
			return body
		}
		return out
	}
}

// FilterRegistrationByAge removes every leaf published after cutoff from a
// registration index or page document. In an index, inline pages are
// filtered, a page left empty is dropped and the counts and lower/upper
// bounds follow; linked pages are filtered when they are fetched. Within a
// dated document an undated leaf is dropped too. applied is false when the
// document has leaves but no dated one — the caller logs the gap and serves
// it. Malformed input is returned unchanged.
func FilterRegistrationByAge(body []byte, cutoff time.Time) (out []byte, applied bool) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, true
	}
	items, ok := doc["items"].([]any)
	if !ok {
		return body, true
	}

	// The pages whose leaves are inline, by position in items: the document
	// itself when it is a page (-1), else the index's inline pages.
	pageDoc := isLeafList(items)
	pages := map[int]map[string]any{}
	if pageDoc {
		pages[-1] = doc
	} else {
		for i, it := range items {
			if page, ok := it.(map[string]any); ok {
				if _, inline := page["items"].([]any); inline {
					pages[i] = page
				}
			}
		}
	}
	leaves, dated := 0, 0
	for _, page := range pages {
		inner, _ := page["items"].([]any)
		for _, it := range inner {
			leaves++
			if judgeLeaf(it).known {
				dated++
			}
		}
	}
	if dated == 0 {
		return body, leaves == 0
	}

	changed := false
	emptied := map[int]bool{}
	for i, page := range pages {
		if filterPage(page, cutoff) {
			changed = true
			if inner, _ := page["items"].([]any); len(inner) == 0 && !pageDoc {
				emptied[i] = true
			}
		}
	}
	if !changed {
		return body, true
	}
	if len(emptied) > 0 {
		kept := make([]any, 0, len(items)-len(emptied))
		for i, it := range items {
			if !emptied[i] {
				kept = append(kept, it)
			}
		}
		doc["items"] = kept
		doc["count"] = len(kept)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return body, true
	}
	return b, true
}

// isLeafList reports whether items are registration leaves (they carry a
// catalogEntry) rather than pages.
func isLeafList(items []any) bool {
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			_, leaf := m["catalogEntry"]
			return leaf
		}
	}
	return false
}

// leafVersion returns a decoded registration leaf's catalogEntry.version.
func leafVersion(leaf any) string {
	m, _ := leaf.(map[string]any)
	ce, _ := m["catalogEntry"].(map[string]any)
	v, _ := ce["version"].(string)
	return v
}

// judgeLeaf dates a decoded registration leaf.
func judgeLeaf(leaf any) versionDate {
	m, _ := leaf.(map[string]any)
	ce, _ := m["catalogEntry"].(map[string]any)
	published, _ := ce["published"].(string)
	commit, _ := m["commitTimeStamp"].(string)
	return leafDate(published, commit)
}

// filterPage drops the too-young and undated leaves of one page in place
// and fixes its count and bounds. It reports whether it changed the page.
func filterPage(page map[string]any, cutoff time.Time) bool {
	items, _ := page["items"].([]any)
	kept := make([]any, 0, len(items))
	var lower, upper string
	for _, it := range items {
		if d := judgeLeaf(it); !d.known || d.t.After(cutoff) {
			continue
		}
		kept = append(kept, it)
		v := NormalizeVersion(leafVersion(it))
		if lower == "" {
			lower = v
		}
		upper = v
	}
	if len(kept) == len(items) {
		return false
	}
	page["items"] = kept
	page["count"] = len(kept)
	if len(kept) > 0 {
		page["lower"], page["upper"] = lower, upper
	}
	return true
}

// filterRegistrationRewrite returns the rewrite that applies
// FilterRegistrationByAge to a served registration document.
func filterRegistrationRewrite(repo *domain.Repository, p string, minAge time.Duration) func([]byte) []byte {
	return func(body []byte) []byte {
		out, applied := FilterRegistrationByAge(body, time.Now().Add(-minAge))
		if !applied {
			log.Printf("nexspence: minimum_package_age not enforceable for %s%s — upstream registration carries no publish times", repo.Name, p)
		}
		return out
	}
}

// versionAgeAllowed is the download half of the policy: the .nupkg and
// .nuspec of a version must not bypass the version list. flatIndex is the
// package's flat container version list path. On refusal the response is
// already written.
func (h *Handler) versionAgeAllowed(c *gin.Context, repo *domain.Repository, id, version, flatIndex string, minAge time.Duration) bool {
	ctx := c.Request.Context()
	dates, err := h.packageDates(ctx, repo, id, version)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return false
	}
	if !dates.enforceable {
		log.Printf("nexspence: minimum_package_age not enforceable for %s/%s %s — upstream registration carries no publish times", repo.Name, id, version)
		return true
	}
	d, ok := dates.versions[versionKey(version)]
	if !ok {
		// Not in the registration: either upstream has no such version (the
		// proxy pipeline answers its 404) or it is too new to be indexed.
		listed, err := upstreamLists(ctx, repo, flatIndex, version)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return false
		}
		if !listed {
			return true
		}
	}
	if !d.known {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"%s %s has no publish time upstream; this repository enforces a minimum package age", id, version)})
		return false
	}
	if age := time.Since(d.t); age < minAge {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"%s %s was published %s ago; this repository enforces a minimum package age of %s",
			id, version, age.Round(time.Minute), minAge)})
		return false
	}
	return true
}

// upstreamLists reports whether the upstream flat container version list
// names version. It is read fresh: a cached list may predate the version.
func upstreamLists(ctx context.Context, repo *domain.Repository, flatIndex, version string) (bool, error) {
	origin := nugetRemoteOrigin(remoteURLOf(repo))
	body, found, err := fetchBody(ctx, repo, origin+flatIndex, maxFlatIndexBytes)
	if err != nil || !found {
		return false, err
	}
	var doc struct {
		Versions []string `json:"versions"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return false, fmt.Errorf("upstream version list: %w", err)
	}
	want := versionKey(version)
	for _, v := range doc.Versions {
		if versionKey(v) == want {
			return true, nil
		}
	}
	return false, nil
}

// isRegistrationPath reports whether a proxied path is a registration
// document: it lies inside a registration hive, as rerootRegistrationID
// recognizes upstream URLs.
func isRegistrationPath(p string) bool {
	return registrationHiveSegment(p) >= 0
}

// registrationHiveSegment returns the index of the segment of path p that
// names a registration hive (registration, registration5-gz-semver2,
// registrations2, ...), or -1. Every registration document lies at least two
// segments below its hive ({hive}/{id}/index.json, {hive}/{id}/{version}.json,
// {hive}/{id}/page/{lower}/{upper}.json), while a package id that merely
// starts with "registration" sits right above the flat container's
// index.json or names a package file. Without that distinction such a
// package's version list and files escaped the flat container's age policy.
func registrationHiveSegment(p string) int {
	if strings.HasSuffix(p, ".nupkg") || strings.HasSuffix(p, ".nuspec") {
		return -1
	}
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i := 0; i+2 < len(segs); i++ {
		if strings.HasPrefix(segs[i], "registration") {
			return i
		}
	}
	return -1
}

// applyAgePolicy enforces minimum_package_age on a proxy GET. It reports
// done when it has answered the request itself (a refusal); otherwise it
// may wrap rewrite to filter a version list or registration document.
func (h *Handler) applyAgePolicy(c *gin.Context, repo *domain.Repository, p string, minAge time.Duration, rewrite *func([]byte) []byte) (done bool) {
	if isRegistrationPath(p) {
		filter := filterRegistrationRewrite(repo, p, minAge)
		if next := *rewrite; next != nil {
			*rewrite = func(b []byte) []byte { return next(filter(b)) }
		} else {
			*rewrite = filter
		}
		return false
	}
	segs := strings.Split(strings.Trim(p, "/"), "/")
	n := len(segs)
	// {base}/{id}/{version}/{id}.{version}.nupkg and {base}/{id}/{version}/{id}.nuspec
	if n >= 3 && (strings.HasSuffix(p, ".nupkg") || strings.HasSuffix(p, ".nuspec")) {
		id, version := segs[n-3], segs[n-2]
		flatIndex := "/" + strings.Join(segs[:n-2], "/") + "/index.json"
		return !h.versionAgeAllowed(c, repo, id, version, flatIndex, minAge)
	}
	// {base}/{id}/index.json — the flat container version list.
	if n >= 2 && segs[n-1] == "index.json" {
		*rewrite = h.filterFlatIndexRewrite(c.Request.Context(), repo, segs[n-2], minAge)
	}
	return false
}
