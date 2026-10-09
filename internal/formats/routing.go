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
// applied by the group handler. A rule that no longer resolves counts as none,
// as it does on a group.
func ProxyRuleAllows(ctx context.Context, rules repository.RoutingRuleRepo, repo *domain.Repository, method, path string) bool {
	if rules == nil || repo == nil || repo.Type != domain.TypeProxy || repo.RoutingRuleID == nil {
		return true
	}
	if method != http.MethodGet && method != http.MethodHead {
		return true
	}
	rule, err := rules.Get(ctx, *repo.RoutingRuleID)
	if err != nil {
		return true
	}
	return rule.Allows(path)
}

// WriteRoutingBlocked answers a read a proxy's routing rule refused. 404, not
// 403: to the client and to a group walking its members, the path is simply
// not in this repository.
func WriteRoutingBlocked(c *gin.Context, repoName string) {
	c.JSON(http.StatusNotFound, gin.H{"error": "path blocked by the routing rule of repository " + repoName})
}
