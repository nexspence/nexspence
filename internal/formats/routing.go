package formats

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// ProxyRuleAllows reports whether a proxy repository answers a read of path
// under its own routing rule (#642). As in Nexus, a rule on a proxy decides
// which paths are fetched from remote_url: a refused path is not served from
// the cache either, and in a group it is a miss, so a hosted member can still
// serve it. Hosted repositories ignore rules, and a group's own rule is
// applied by the group handler.
//
// Evaluation fails closed: a rule that cannot be loaded refuses every path
// (repositories.routing_rule_id is ON DELETE SET NULL, so a lookup error is a
// failing database, not a deleted rule), and so does a path RuleAllows
// refuses.
func ProxyRuleAllows(ctx context.Context, rules repository.RoutingRuleRepo, repo *domain.Repository, method, path string) bool {
	if rules == nil || repo == nil || repo.Type != domain.TypeProxy || repo.RoutingRuleID == nil {
		return true
	}
	if method != http.MethodGet && method != http.MethodHead {
		return true
	}
	rule, err := rules.Get(ctx, *repo.RoutingRuleID)
	if err != nil || rule == nil {
		return false
	}
	return RuleAllows(rule, path)
}

// RuleAllows reports whether rule lets path through. A path with a second
// spelling (see CanonicalPath) is refused rather than judged: no rule can be
// written against every way of spelling the same artifact.
//
// That leaves the trailing slash, which the handler drops and a client may
// need (PyPI's simple index). An ALLOW rule judges the path as requested, so
// ^/simple/requests/ keeps admitting the index page it names. A BLOCK rule
// judges both spellings: /lib.jar/ must not slip past \.jar$ only to be
// served as /lib.jar.
func RuleAllows(rule *domain.RoutingRule, path string) bool {
	if rule == nil {
		return true
	}
	served, err := CanonicalPath(path)
	if err != nil {
		return false
	}
	if !rule.Allows(path) {
		return false
	}
	return rule.Mode == "ALLOW" || rule.Allows(served)
}

// WriteRoutingBlocked answers a read a proxy's routing rule refused. 404, not
// 403: to the client and to a group walking its members, the path is simply
// not in this repository.
func WriteRoutingBlocked(c *gin.Context, repoName string) {
	c.JSON(http.StatusNotFound, gin.H{"error": "path blocked by the routing rule of repository " + repoName})
}
