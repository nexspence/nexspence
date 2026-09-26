package helm

// Upstream index caching: the download path needs one fact out of index.yaml —
// the origin URL of the chart being pulled. Fetching and parsing the whole
// document per tarball request made the first pull through a group cost one
// index download per proxy member, and an index like Bitnami's is tens of
// megabytes.
//
// So the origin lookup goes through a per-repository cache of rewritten local
// path → origin URL (the same path fetchAndRewriteHelmIndex emits) and
// refetches only once the copy is older than helm.index_cache_ttl (5 minutes
// by default, 0 to fetch per lookup). Artifactory caches upstream metadata for 2
// hours by default and Nexus for 24; minutes here is the conservative end of
// that. The client-facing /index.yaml route stays live: the catalog a client
// searches must not lag behind upstream, and only the URL table is small enough
// to keep for several repositories at once.

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// originTableLocalBase is a dummy prefix rewriteChartURL needs so the cache can
// recover the proxy-relative path it would emit into the served index.
const originTableLocalBase = "http://nexspence.invalid/repository/_/"

const (
	// chartIndexErrorTTL is how long a failed fetch is remembered, so a burst of
	// pulls against a broken upstream does not queue one 30s attempt per request.
	// It is capped by the configured TTL, which an operator may set below it.
	chartIndexErrorTTL = 10 * time.Second

	// maxCachedIndexes bounds the cache; the least recently used repository is
	// dropped past it.
	maxCachedIndexes = 64
)

// chartOrigin is one index.yaml entry as the download path needs it: the
// rewritten local path is the map key; name/version file the cached component.
type chartOrigin struct {
	name, version, url string
}

// chartIndex is one repository's slot. Its lock is held across the upstream
// fetch, so concurrent pulls (helm dependency update asks for several charts at
// once) share one download instead of one each.
type chartIndex struct {
	mu     sync.Mutex
	urls   map[string]chartOrigin // rewritten local path → origin; never mutated once published
	urlsAt time.Time
	err    error
	errAt  time.Time

	lastUsed time.Time // guarded by chartIndexCache.mu
}

type chartIndexCache struct {
	mu      sync.Mutex
	entries map[string]*chartIndex
}

func newChartIndexCache() *chartIndexCache {
	return &chartIndexCache{entries: map[string]*chartIndex{}}
}

// slot returns the cache slot for key, evicting the least recently used one when
// the cache is full. A slot evicted mid-fetch simply loses its result: the next
// request gets a fresh slot and fetches again.
func (c *chartIndexCache) slot(key string) *chartIndex {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if e, ok := c.entries[key]; ok {
		e.lastUsed = now
		return e
	}
	for len(c.entries) >= maxCachedIndexes {
		oldestKey := ""
		for k, e := range c.entries {
			if oldestKey == "" || e.lastUsed.Before(c.entries[oldestKey].lastUsed) {
				oldestKey = k
			}
		}
		delete(c.entries, oldestKey)
	}
	e := &chartIndex{lastUsed: now}
	c.entries[key] = e
	return e
}

// chartURLs returns repo's upstream chart URL table, fetching index.yaml when the
// cached copy is missing or stale. The returned map is shared and read-only.
func (h *Handler) chartURLs(ctx context.Context, repo *domain.Repository, remoteBase string) (map[string]chartOrigin, error) {
	ttl := h.deps.HelmIndexCacheTTL
	if ttl <= 0 {
		index, err := fetchHelmIndexDoc(ctx, repo, remoteBase)
		if err != nil {
			return nil, err
		}
		return chartOriginTable(index, remoteBase), nil
	}

	e := h.indexes.slot(repo.ID + "\x00" + remoteBase)
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now()
	if e.urls != nil && now.Sub(e.urlsAt) < ttl {
		return e.urls, nil
	}
	if e.err != nil && now.Sub(e.errAt) < min(chartIndexErrorTTL, ttl) {
		return nil, e.err
	}

	// The first requester's cancel must not poison the slot: other pulls still
	// need this index. The fetch itself is still bounded by fetchHelmIndexDoc's
	// timeout. A failure that is just "the client hung up" is not cached.
	index, err := fetchHelmIndexDoc(context.WithoutCancel(ctx), repo, remoteBase)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		e.urls, e.err, e.errAt = nil, err, time.Now()
		return nil, err
	}
	e.urls, e.urlsAt, e.err = chartOriginTable(index, remoteBase), time.Now(), nil
	return e.urls, nil
}

// chartOriginTable flattens an index.yaml document to rewritten local path →
// origin. The key is whatever rewriteChartURL would put in the served index, so
// a same-host charts/widget.tgz and an off-host mint of widget-1.0.0.tgz both
// round-trip. Entries without a usable URL, or that rewriteChartURL would hand
// to the client raw, are left out. The first entry for a path wins.
func chartOriginTable(index map[string]any, remoteBase string) map[string]chartOrigin {
	entries, _ := index["entries"].(map[string]any)
	table := make(map[string]chartOrigin, len(entries))
	for key, raw := range entries {
		charts, ok := raw.([]any)
		if !ok {
			continue
		}
		for _, cv := range charts {
			chart, ok := cv.(map[string]any)
			if !ok {
				continue
			}
			name := key
			if n := asString(chart["name"]); n != "" {
				name = n
			}
			version := asString(chart["version"])
			urls, _ := chart["urls"].([]any)
			if len(urls) == 0 || version == "" {
				continue
			}
			first, _ := urls[0].(string)
			if first == "" {
				continue
			}
			origin := resolvedUpstreamChartURL(first, remoteBase)
			if origin == "" {
				continue
			}
			rewritten := rewriteChartURL(first, remoteBase, originTableLocalBase, canonicalChartFile(chart, key))
			if !strings.HasPrefix(rewritten, originTableLocalBase) {
				continue
			}
			rel := strings.TrimPrefix(rewritten, originTableLocalBase)
			if rel == "" {
				continue
			}
			if _, seen := table[rel]; !seen {
				table[rel] = chartOrigin{name: name, version: version, url: origin}
			}
		}
	}
	return table
}
