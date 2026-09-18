package base

import (
	"context"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// componentPageSize is the largest page the repository layer will hand out
// (componentRepo.Search clamps Limit to 500).
const componentPageSize = 500

// AllComponents walks every Search page for repoName. Index builders that
// list a whole repository (apt Packages, cran PACKAGES, alpine APKINDEX)
// must not stop at the first page: a repository past the page size would
// otherwise silently serve a short index and tell clients the rest does not
// exist (#443).
func AllComponents(ctx context.Context, comps repository.ComponentRepo, repoName string) ([]domain.Component, error) {
	var out []domain.Component
	for offset := 0; ; offset += componentPageSize {
		page, err := comps.Search(ctx, domain.SearchParams{
			Repository: repoName, Limit: componentPageSize, Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if page.ContinuationToken == nil || len(page.Items) == 0 {
			return out, nil
		}
	}
}
