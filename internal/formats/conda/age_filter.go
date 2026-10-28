package conda

// Minimum package age on a Conda proxy (#643, after npm and PyPI in #323 and
// Go): a package build younger than proxy_config.minimum_package_age is left
// out of the repodata the client solves against and its file is refused with
// 403, even when it is already cached.
//
// A build's publish time is the optional `timestamp` of its repodata entry
// (milliseconds since the epoch; old channels wrote seconds, which conda tells
// apart the same way: anything past year 9999 in seconds is milliseconds).
//
// Which documents: repodata.json and repodata_from_packages.json are filtered
// as served. current_repodata.json — the newest build of each package and
// nothing older — is refused with 404 while the policy is on: once its newest
// build is too young it has no older one to offer, and conda falls back to
// repodata.json on a 404 exactly as it does for the compressed variants this
// proxy never serves (see refusedIndexVariant).
//
// Hybrid failure mode, as for npm and PyPI, per package name: an undated build
// of a package whose other builds are dated is hidden and refused (its age is
// unknowable); a package with no dated build cannot be policed, so it is
// served and the gap logged. A file the subdir's repodata.json does not list
// is refused when that document carries dates (a build published after the
// cached table was taken is young by definition), served and logged otherwise.
//
// The download gate cannot re-read repodata per request — conda-forge's runs
// to hundreds of megabytes per subdir — so it keeps one compact table per
// repository and subdir: a 64-bit hash of the package's proxy path → its
// timestamp. The table is filled when the proxy serves that repodata.json and
// otherwise by a streaming read of the upstream document (one entry decoded at
// a time); it is reused for metadata_max_age, and the last table read
// successfully still dates a cached package while upstream is down.

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

const (
	// maxAgeTables bounds the per repository and subdir tables; the least
	// recently used one is dropped past it.
	maxAgeTables = 32
	// ageTableErrorTTL is how long a failed upstream read is remembered, so a
	// burst of downloads against a broken upstream does not queue one read each.
	ageTableErrorTTL = 10 * time.Second
	// ageTableFetchTimeout bounds one streaming read of an upstream repodata.json.
	ageTableFetchTimeout = 5 * time.Minute
	// maxSecondsTimestamp is the largest timestamp conda reads as seconds.
	maxSecondsTimestamp = 253402300799

	// Table values other than a timestamp (milliseconds, > 0).
	ageUndatedRefused int64 = -1 // undated build of a dated package
	ageUndatedAllowed int64 = -2 // package with no dated build at all
)

// ageTablePathBase stands in for this proxy's channel root when a table key is
// derived from an entry's url: rewritePackageURL then yields the same
// "<subdir>/<file>" path the served document sends the client to.
const ageTablePathBase = "http://nexspence.invalid/repository/_/"

// entryMillis reads a repodata timestamp as milliseconds since the epoch.
func entryMillis(v any) (int64, bool) {
	var f float64
	switch t := v.(type) {
	case float64:
		f = t
	case json.Number:
		parsed, err := t.Float64()
		if err != nil {
			return 0, false
		}
		f = parsed
	default:
		return 0, false
	}
	if f <= 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	if f <= maxSecondsTimestamp {
		f *= 1000
	}
	return int64(f), true
}

// nameFromFilename recovers a package name from "<name>-<version>-<build>.<ext>"
// for an entry that does not carry one; names may contain dashes.
func nameFromFilename(filename string) string {
	stem := strings.TrimSuffix(strings.TrimSuffix(filename, ".tar.bz2"), ".conda")
	for range 2 {
		if i := strings.LastIndex(stem, "-"); i > 0 {
			stem = stem[:i]
		}
	}
	return stem
}

// entryName is the package name an entry belongs to.
func entryName(filename string, entry map[string]any) string {
	if n, ok := entry["name"].(string); ok && n != "" {
		return n
	}
	return nameFromFilename(filename)
}

// filterRepodataByAge drops from both package maps of a repodata document the
// builds published after cutoff (milliseconds) and the undated builds of
// dated packages. It returns the names of the packages with no dated build,
// which are kept whole.
func filterRepodataByAge(doc map[string]any, cutoff int64) (undated []string) {
	datedNames := map[string]bool{}
	for _, key := range []string{"packages", "packages.conda"} {
		pkgs, _ := doc[key].(map[string]any)
		for filename, v := range pkgs {
			if entry, ok := v.(map[string]any); ok {
				if _, dated := entryMillis(entry["timestamp"]); dated {
					datedNames[entryName(filename, entry)] = true
				}
			}
		}
	}
	undatedSet := map[string]bool{}
	for _, key := range []string{"packages", "packages.conda"} {
		pkgs, _ := doc[key].(map[string]any)
		for filename, v := range pkgs {
			entry, ok := v.(map[string]any)
			if !ok {
				continue
			}
			name := entryName(filename, entry)
			ms, dated := entryMillis(entry["timestamp"])
			switch {
			case dated && ms > cutoff:
				delete(pkgs, filename)
			case !dated && datedNames[name]:
				delete(pkgs, filename)
			case !dated:
				undatedSet[name] = true
			}
		}
	}
	for name := range undatedSet {
		undated = append(undated, name)
	}
	sort.Strings(undated)
	return undated
}

// logUndatedPackages reports the packages the policy could not police, one
// line per document rather than one per package.
func logUndatedPackages(repo *domain.Repository, platform string, undated []string) {
	if len(undated) == 0 {
		return
	}
	sample := undated
	if len(sample) > 3 {
		sample = sample[:3]
	}
	log.Printf("nexspence: minimum_package_age not enforceable for %d package(s) in %s/%s (e.g. %s) — upstream repodata entries carry no timestamp",
		len(undated), repo.Name, platform, strings.Join(sample, ", "))
}

// ageTable maps the hash of a package's proxy path ("<subdir>/<file>") to its
// timestamp in milliseconds or one of the ageUndated values. dated reports
// whether any entry carried a timestamp.
type ageTable struct {
	ts    map[uint64]int64
	dated bool
}

func pathKey(rel string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(rel))
	return h.Sum64()
}

// ageTableBuilder collects entries, then resolves the undated ones per name.
type ageTableBuilder struct {
	remoteBase, platform string
	names                map[string]int
	datedNames           []bool
	keys                 []uint64
	nameIDs              []int
	millis               []int64
}

func newAgeTableBuilder(remoteBase, platform string) *ageTableBuilder {
	return &ageTableBuilder{remoteBase: remoteBase, platform: platform, names: map[string]int{}}
}

// add records one entry. Entries whose url the served document hands to the
// client as an upstream URL never reach this proxy and are skipped.
func (b *ageTableBuilder) add(filename, name, rawURL string, ms int64, dated bool) {
	rel := b.platform + "/" + filename
	if rawURL != "" {
		local := rewritePackageURL(rawURL, b.remoteBase, b.platform, ageTablePathBase)
		if !strings.HasPrefix(local, ageTablePathBase) {
			return
		}
		rel = strings.TrimPrefix(local, ageTablePathBase)
	}
	id, ok := b.names[name]
	if !ok {
		id = len(b.datedNames)
		b.names[name] = id
		b.datedNames = append(b.datedNames, false)
	}
	if dated {
		b.datedNames[id] = true
	} else {
		ms = 0
	}
	b.keys = append(b.keys, pathKey(rel))
	b.nameIDs = append(b.nameIDs, id)
	b.millis = append(b.millis, ms)
}

func (b *ageTableBuilder) table() *ageTable {
	t := &ageTable{ts: make(map[uint64]int64, len(b.keys))}
	for i, k := range b.keys {
		ms := b.millis[i]
		switch {
		case ms > 0:
			t.dated = true
		case b.datedNames[b.nameIDs[i]]:
			ms = ageUndatedRefused
		default:
			ms = ageUndatedAllowed
		}
		if _, seen := t.ts[k]; !seen {
			t.ts[k] = ms
		}
	}
	return t
}

// ageTableFromDoc builds the table from a decoded repodata document. Call it
// before the document's urls are rewritten.
func ageTableFromDoc(doc map[string]any, remoteBase, platform string) *ageTable {
	b := newAgeTableBuilder(remoteBase, platform)
	for _, key := range []string{"packages", "packages.conda"} {
		pkgs, _ := doc[key].(map[string]any)
		for filename, v := range pkgs {
			entry, ok := v.(map[string]any)
			if !ok {
				continue
			}
			u, _ := entry["url"].(string)
			ms, dated := entryMillis(entry["timestamp"])
			b.add(filename, entryName(filename, entry), u, ms, dated)
		}
	}
	return b.table()
}

// ageTableFromStream builds the table from a repodata.json body, decoding one
// package entry at a time so a document of hundreds of megabytes never sits
// in memory whole.
func ageTableFromStream(r io.Reader, remoteBase, platform string) (*ageTable, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if err := expectDelim(dec, '{'); err != nil {
		return nil, err
	}
	b := newAgeTableBuilder(remoteBase, platform)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		if key != "packages" && key != "packages.conda" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, err
			}
			continue
		}
		if err := expectDelim(dec, '{'); err != nil {
			return nil, err
		}
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			filename, _ := tok.(string)
			var entry struct {
				Name      string `json:"name"`
				URL       string `json:"url"`
				Timestamp any    `json:"timestamp"`
			}
			if err := dec.Decode(&entry); err != nil {
				return nil, err
			}
			name := entry.Name
			if name == "" {
				name = nameFromFilename(filename)
			}
			ms, dated := entryMillis(entry.Timestamp)
			b.add(filename, name, entry.URL, ms, dated)
		}
		if err := expectDelim(dec, '}'); err != nil {
			return nil, err
		}
	}
	// A body cut short must not pass for a complete document.
	if err := expectDelim(dec, '}'); err != nil {
		return nil, err
	}
	return b.table(), nil
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("repodata: expected %q, got %v", want, tok)
	}
	return nil
}

// ageSlot is one repository and subdir's table. Its lock is held across the
// upstream read, so concurrent downloads share one.
type ageSlot struct {
	mu    sync.Mutex
	table *ageTable
	at    time.Time
	good  *ageTable // last table read successfully; outlives a failed refetch
	err   error
	errAt time.Time

	lastUsed time.Time // guarded by ageTables.mu
}

type ageTables struct {
	mu    sync.Mutex
	slots map[string]*ageSlot
}

func (a *ageTables) slot(key string) *ageSlot {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if a.slots == nil {
		a.slots = map[string]*ageSlot{}
	}
	if s, ok := a.slots[key]; ok {
		s.lastUsed = now
		return s
	}
	for len(a.slots) >= maxAgeTables {
		oldest := ""
		for k, s := range a.slots {
			if oldest == "" || s.lastUsed.Before(a.slots[oldest].lastUsed) {
				oldest = k
			}
		}
		delete(a.slots, oldest)
	}
	s := &ageSlot{lastUsed: now}
	a.slots[key] = s
	return s
}

func ageSlotKey(repo *domain.Repository, remoteBase, platform string) string {
	return repo.ID + "\x00" + remoteBase + "\x00" + platform
}

// storeAgeTable records a table built from a repodata.json the proxy has just
// served, so the downloads that follow need no read of their own.
func (h *Handler) storeAgeTable(repo *domain.Repository, remoteBase, platform string, t *ageTable) {
	s := h.ages.slot(ageSlotKey(repo, remoteBase, platform))
	s.mu.Lock()
	s.table, s.at, s.good, s.err = t, time.Now(), t, nil
	s.mu.Unlock()
}

// subdirAgeTable returns repo's table for platform, reading upstream
// repodata.json when the cached one is missing or older than metadata_max_age.
// A failed read answers the last good table when there is one.
func (h *Handler) subdirAgeTable(ctx context.Context, repo *domain.Repository, remoteBase, platform string) (*ageTable, error) {
	s := h.ages.slot(ageSlotKey(repo, remoteBase, platform))
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if s.table != nil && now.Sub(s.at) < repoproxy.MetadataMaxAge(repo) {
		return s.table, nil
	}
	if s.err != nil && now.Sub(s.errAt) < ageTableErrorTTL {
		if s.good != nil {
			return s.good, nil
		}
		return nil, s.err
	}

	// Detached from the requester: other downloads wait on this read.
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ageTableFetchTimeout)
	defer cancel()
	t, err := fetchAgeTable(fetchCtx, repo, remoteBase, platform)
	if err != nil {
		s.table, s.err, s.errAt = nil, err, time.Now()
		if s.good != nil {
			return s.good, nil
		}
		return nil, err
	}
	s.table, s.at, s.good, s.err = t, time.Now(), t, nil
	return t, nil
}

func fetchAgeTable(ctx context.Context, repo *domain.Repository, remoteBase, platform string) (*ageTable, error) {
	resp, err := repoproxy.FetchUpstreamOnce(ctx, repo, "/"+platform+"/repodata.json", "", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream %s/repodata.json: %s", platform, resp.Status)
	}
	t, err := ageTableFromStream(resp.Body, remoteBase, platform)
	if err != nil {
		return nil, fmt.Errorf("parse upstream %s/repodata.json: %w", platform, err)
	}
	return t, nil
}

// isPackageFile reports whether filename is a conda package archive.
func isPackageFile(filename string) bool {
	return strings.HasSuffix(filename, ".conda") || strings.HasSuffix(filename, ".tar.bz2")
}

// packageAgeAllowed is the download half of the policy: a package file must
// not bypass the repodata filter. p is the request path ("/<subdir>/<file>").
// On refusal the response is already written.
func (h *Handler) packageAgeAllowed(c *gin.Context, repo *domain.Repository, p, platform string, minAge time.Duration) bool {
	remoteBase, err := repoproxy.RemoteURL(repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return false
	}
	t, err := h.subdirAgeTable(c.Request.Context(), repo, remoteBase, platform)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return false
	}
	rel := strings.TrimPrefix(p, "/")
	ms, listed := t.ts[pathKey(rel)]
	switch {
	case !listed && t.dated:
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"%s is not in the upstream repodata.json, so its publish time is unknown; this repository enforces a minimum package age", rel)})
		return false
	case !listed, ms == ageUndatedAllowed:
		log.Printf("nexspence: minimum_package_age not enforceable for %s/%s — upstream repodata entries carry no timestamp", repo.Name, rel)
		return true
	case ms == ageUndatedRefused:
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"%s has no timestamp in the upstream repodata.json; this repository enforces a minimum package age", rel)})
		return false
	}
	if age := time.Since(time.UnixMilli(ms)); age < minAge {
		c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf(
			"%s was published %s ago; this repository enforces a minimum package age of %s",
			rel, age.Round(time.Minute), minAge)})
		return false
	}
	return true
}
