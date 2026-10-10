package domain

import (
	"regexp"
	"sync"
)

// compiledMatchers caches compiled matcher regexes keyed by pattern string.
// Allows runs per request on group and proxy repos; recompiling each call
// dominates the actual match, so cache the compiled form.
//
// Entries are never evicted: cardinality is bounded by the number of distinct
// patterns across admin-managed routing rules (operator-bounded, not request-
// bounded). If v2 multi-tenancy ever lets non-admins author matchers, replace
// this with a bounded/LRU cache to avoid unbounded growth.
var compiledMatchers sync.Map // string -> *regexp.Regexp

func compileMatcher(pattern string) (*regexp.Regexp, error) {
	if v, ok := compiledMatchers.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	actual, _ := compiledMatchers.LoadOrStore(pattern, re)
	return actual.(*regexp.Regexp), nil
}

// Allows reports whether path passes the rule. mode=ALLOW: path must match at
// least one matcher; mode=BLOCK: path must match none. An empty matcher list
// therefore blocks everything under ALLOW and allows everything under BLOCK.
// A nil rule allows everything.
func (r *RoutingRule) Allows(path string) bool {
	if r == nil {
		return true
	}
	matched := matchesAny(r.Matchers, path)
	if r.Mode == "ALLOW" {
		return matched
	}
	return !matched
}

func matchesAny(matchers []string, path string) bool {
	for _, m := range matchers {
		re, err := compileMatcher(m)
		if err != nil {
			continue
		}
		if re.MatchString(path) {
			return true
		}
	}
	return false
}
