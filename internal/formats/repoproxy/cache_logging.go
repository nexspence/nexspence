package repoproxy

import (
	"context"

	"github.com/nexspence-oss/nexspence/internal/logger"
)

// LogCacheRefreshError reports a failed freshness timestamp update through the
// configured proxy logger. The upstream revalidation succeeded, so this is a
// local cache warning rather than an upstream proxy-error webhook.
func LogCacheRefreshError(ctx context.Context, repoName, path string, cause error) {
	logger.WithTraceContext(ctx, proxyLogger()).Warnw("cache freshness update failed; serving revalidated cache",
		"repository", repoName, "path", path, "error", cause)
}
