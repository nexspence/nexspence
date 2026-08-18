package handlers

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// DockerCatalog serves GET /v2/_catalog — the instance-level catalog (#261).
//
// Before this route existed, the path fell into the /v2/:repoName dispatch as
// a repository literally named "_catalog", and the failed lookup denied even
// an nx-admin with a bare 403 — the admin bypass lives in CanAccessRepo,
// which a missing repository never reaches.
//
// Entries are "<repository>/<image>" — the exact names this registry's /v2/
// surface serves, so every catalog entry is something the caller could pass
// to docker pull. The per-repository catalog (GET /v2/<repoName>/_catalog)
// keeps answering with bare image names, because there the namespace prefix
// is fixed by the URL.
//
// Visibility follows per-repository RBAC: the caller sees images only from
// docker/oci repositories they may read, so an anonymous caller sees exactly
// the allow_anonymous ones and a catalog probe leaks nothing about private
// repositories. Pagination (?n=/?last=) matches the other list endpoints.
func DockerCatalog(
	repos repository.RepositoryRepo,
	rbac *service.RBACService,
	assets repository.AssetRepo,
) gin.HandlerFunc {
	internalError := func(c *gin.Context) {
		// The registry error shape, and no internals: this route answers
		// unauthenticated callers, and the request log already carries the
		// real error via the 500 status.
		c.JSON(http.StatusInternalServerError, gin.H{
			"errors": []gin.H{{"code": "UNKNOWN", "message": "internal error"}},
		})
	}

	return func(c *gin.Context) {
		c.Header("Docker-Distribution-API-Version", "registry/2.0")
		ctx := c.Request.Context()

		all, err := repos.List(ctx, "", "")
		if err != nil {
			internalError(c)
			return
		}
		registries := make([]domain.Repository, 0, len(all))
		for _, r := range all {
			if r.Format.IsOCIRegistry() && r.Online {
				registries = append(registries, r)
			}
		}

		userID := c.GetString("userID")
		roles, _ := c.Get("roles")
		rolesSlice, _ := roles.([]string)
		readable := rbac.FilterRepos(ctx, userID, rolesSlice, registries)

		// A credential-less caller who can see nothing gets the challenge the
		// sibling /v2/ surfaces give, not a 200 that reads as "the registry is
		// empty" — a client holding credentials should be told to present them.
		if userID == "" && len(readable) == 0 {
			c.Header("WWW-Authenticate", `Basic realm="Nexspence"`)
			c.JSON(http.StatusUnauthorized, gin.H{
				"errors": []gin.H{{"code": "UNAUTHORIZED", "message": "authentication required"}},
			})
			return
		}

		// One query per readable registry rather than one for the union:
		// ListOCIImageNames does not say which repository a name came from,
		// and the entries need the repository prefix to be pullable. Each
		// repository's names then pass the caller's content selectors —
		// FilterRepos alone ignores path clauses, and image names are exactly
		// what a path-scoped selector is configured to hide.
		var entries []string
		for i := range readable {
			r := &readable[i]
			names, err := assets.ListOCIImageNames(ctx, []string{r.Name})
			if err != nil {
				internalError(c)
				return
			}
			for _, n := range rbac.FilterOCIImageNames(ctx, userID, rolesSlice, r, names) {
				entries = append(entries, r.Name+"/"+n)
			}
		}
		// Sorted in Go, not SQL, for the same reason the per-repo catalog is:
		// the ?last= cursor compares Go strings, and a collation-ordered list
		// searched byte-wise makes the cursor skip entries.
		sort.Strings(entries)

		params := oci.ParsePageParams(c)
		page, more := oci.Paginate(entries, params)
		oci.SetNextLink(c, params, page, more)
		if page == nil {
			page = []string{}
		}
		c.JSON(http.StatusOK, gin.H{"repositories": page})
	}
}
