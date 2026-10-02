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

// ExactComponents walks every Search page of an exact lookup: p.Group and
// p.Name match whole values. A protocol handler serving one package's index
// must use it rather than a single substring-matched page, or it lists other
// packages whose names contain this one and drops its own versions past the
// first page (#586). Limit and Offset in p are ignored.
func ExactComponents(ctx context.Context, comps repository.ComponentRepo, p domain.SearchParams) ([]domain.Component, error) {
	p.Exact = true
	p.Limit = componentPageSize
	var out []domain.Component
	for offset := 0; ; offset += componentPageSize {
		p.Offset = offset
		page, err := comps.Search(ctx, p)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if page.ContinuationToken == nil || len(page.Items) == 0 {
			return out, nil
		}
	}
}
