package formats_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func TestProxyRuleAllows(t *testing.T) {
	ctx := context.Background()
	rules := testutil.NewRoutingRuleRepo()
	_ = rules.Create(ctx, &domain.RoutingRule{ID: "rr", Mode: "BLOCK", Matchers: []string{`^/com/acme/`}})
	id, missing := "rr", "gone"
	proxy := &domain.Repository{Name: "central", Type: domain.TypeProxy, RoutingRuleID: &id}
	hosted := &domain.Repository{Name: "internal", Type: domain.TypeHosted, RoutingRuleID: &id}
	dangling := &domain.Repository{Name: "p2", Type: domain.TypeProxy, RoutingRuleID: &missing}
	plain := &domain.Repository{Name: "p3", Type: domain.TypeProxy}
	const blocked = "/com/acme/lib/1.0/lib-1.0.jar"

	assert.False(t, formats.ProxyRuleAllows(ctx, rules, proxy, http.MethodGet, blocked))
	assert.False(t, formats.ProxyRuleAllows(ctx, rules, proxy, http.MethodHead, blocked))
	assert.True(t, formats.ProxyRuleAllows(ctx, rules, proxy, http.MethodGet, "/org/x/1/x-1.jar"))
	assert.True(t, formats.ProxyRuleAllows(ctx, rules, proxy, http.MethodPut, blocked), "only reads are routed")
	assert.True(t, formats.ProxyRuleAllows(ctx, rules, hosted, http.MethodGet, blocked), "hosted ignores rules")
	assert.False(t, formats.ProxyRuleAllows(ctx, rules, dangling, http.MethodGet, blocked), "a rule that cannot be loaded refuses")
	assert.True(t, formats.ProxyRuleAllows(ctx, rules, plain, http.MethodGet, blocked))
	assert.True(t, formats.ProxyRuleAllows(ctx, nil, proxy, http.MethodGet, blocked), "no rule repo wired")
}

// AGENTS.md: filter evaluation fails closed. A rule lookup that errors is not
// a proxy without a rule — every path would reach the upstream.
func TestProxyRuleAllows_RuleLookupFails_Refuses(t *testing.T) {
	ctx := context.Background()
	rules := testutil.NewRoutingRuleRepo()
	_ = rules.Create(ctx, &domain.RoutingRule{ID: "rr", Mode: "BLOCK", Matchers: []string{`^/com/acme/`}})
	rules.Err = errors.New("database unavailable")
	id := "rr"
	proxy := &domain.Repository{Name: "central", Type: domain.TypeProxy, RoutingRuleID: &id}

	assert.False(t, formats.ProxyRuleAllows(ctx, rules, proxy, http.MethodGet, "/org/x/1/x-1.jar"))
}

// The handler serves path.Clean of the path, so the rule judges that string,
// and a spelling the handler would rewrite is refused rather than judged.
func TestProxyRuleAllows_NonCanonicalSpelling_Refuses(t *testing.T) {
	ctx := context.Background()
	rules := testutil.NewRoutingRuleRepo()
	_ = rules.Create(ctx, &domain.RoutingRule{ID: "rr", Mode: "BLOCK", Matchers: []string{`^/private/`, `\.jar$`}})
	id := "rr"
	proxy := &domain.Repository{Name: "central", Type: domain.TypeProxy, RoutingRuleID: &id}

	for _, p := range []string{
		"//private/x", "/./private/x", "/%2e/private/x", "/%2Fprivate/x", "/a/%2e%2e/private/x",
		"/lib.jar/", // served as /lib.jar
	} {
		assert.False(t, formats.ProxyRuleAllows(ctx, rules, proxy, http.MethodGet, p), p)
	}
	assert.True(t, formats.ProxyRuleAllows(ctx, rules, proxy, http.MethodGet, "/public/x/"))
}
