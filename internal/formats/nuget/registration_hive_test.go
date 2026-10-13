package nuget_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/nuget"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Registration hives other than the local /v3/registration/ (#678): nuget.org
// serves registrations under /v3/registration5-gz-semver2/, the hive the
// rewritten service index hands clients. A registration index that links its
// pages must link them on the proxy, in the same hive, or a client following
// them leaves the proxy for api.nuget.org.

const hivePath = "/v3/registration5-gz-semver2/contoso.lib/"

// regLinks is the part of a registration index a client follows.
type regLinks struct {
	ID    string `json:"@id"`
	Items []struct {
		ID     string `json:"@id"`
		Parent string `json:"parent"`
		Items  []struct {
			ID             string `json:"@id"`
			PackageContent string `json:"packageContent"`
			Registration   string `json:"registration"`
		} `json:"items"`
	} `json:"items"`
}

// localPath returns the request path of a link, failing unless the link is on
// the proxy under base.
func localPath(t *testing.T, link, base string) string {
	t.Helper()
	require.True(t, strings.HasPrefix(link, base+"/"), "link %q must be on %q", link, base)
	u, err := url.Parse(link)
	require.NoError(t, err)
	return u.Path
}

func linkedLibPackage() ngPackage {
	pkg := libPackage()
	pkg.pageSize = 2
	return pkg
}

func TestNuGet_Proxy_SemVer2HiveLinksStayOnProxy(t *testing.T) {
	up := nugetAgeUpstream(t, false, linkedLibPackage())
	r := setup(nugetProxyRepo("ng-hive", up.srv.URL, 0))
	const local = "http://localhost:8080/repository/ng-hive"

	w := get(r, "/repository/ng-hive"+hivePath+"index.json")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), up.srv.URL+"/v3/registration",
		"no link into a registration hive may point at the upstream")
	var idx regLinks
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &idx))
	assert.Equal(t, local+hivePath+"index.json", idx.ID)
	require.Len(t, idx.Items, 3)
	for _, page := range idx.Items {
		assert.Empty(t, page.Items, "pages are linked, not inlined")
		assert.True(t, strings.HasPrefix(page.ID, local+hivePath+"page/"),
			"page link %q keeps the hive, re-rooted on the proxy", page.ID)
	}

	// Following a page link goes through the proxy and lands on the page.
	w = get(r, localPath(t, idx.Items[1].ID, local))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), up.srv.URL+"/v3/registration")
	var page struct {
		ID     string `json:"@id"`
		Parent string `json:"parent"`
		Items  []struct {
			ID             string `json:"@id"`
			PackageContent string `json:"packageContent"`
			Registration   string `json:"registration"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	assert.Equal(t, idx.Items[1].ID, page.ID)
	assert.Equal(t, local+hivePath+"index.json", page.Parent)
	require.Len(t, page.Items, 2)
	assert.Equal(t, local+hivePath+"1.1.0-beta2.json", page.Items[0].ID)
	assert.Equal(t, local+hivePath+"index.json", page.Items[0].Registration)
	assert.True(t, strings.HasPrefix(page.Items[0].PackageContent, local+"/"), page.Items[0].PackageContent)
}

// The minimum-age filter applies to the pages a client reaches through the
// rewritten links.
func TestNuGet_Proxy_SemVer2HiveLinkedPagesAreAgeFiltered(t *testing.T) {
	r, up := agedNuGetProxy(t, linkedLibPackage())
	const local = "http://localhost:8080/repository/ng-aged"

	w := get(r, libReg)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), up.srv.URL+"/v3/registration")
	var idx regLinks
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &idx))
	require.Len(t, idx.Items, 3)

	young := idx.Items[2].ID
	require.Equal(t, local+hivePath+"page/2.0.0/2.0.0.json", young)
	w = get(r, localPath(t, young, local))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var page struct {
		Count int   `json:"count"`
		Items []any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	assert.Equal(t, 0, page.Count, "the young version is filtered out of the linked page")
	assert.Empty(t, page.Items)
}

// groupEngine serves the named group through the group handler and every
// other repository through the NuGet handler.
func groupEngine(groupName string, repos ...*domain.Repository) *gin.Engine {
	d := formats.Deps{
		Repos: testutil.NewRepoRepo(repos...), Blobs: testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(), Assets: testutil.NewAssetRepo(),
		BlobStore: testutil.NewBlobStore(), BaseURL: "http://localhost:8080",
	}
	nh := nuget.New(d)
	gh := group.New(d, map[string]formats.FormatHandler{"nuget": nh})
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		if c.Param("repoName") == groupName {
			gh.ServeHTTP(c)
			return
		}
		nh.ServeHTTP(c)
	})
	return r
}

// Behind a group the links stay on the group, as the group's service index
// does — also with a hosted member, which is asked first and must answer a
// hive it does not serve with a miss, not a 405 that ends the fan-out.
func TestNuGet_Group_SemVer2HiveLinksStayOnGroup(t *testing.T) {
	for _, withHosted := range []bool{false, true} {
		name := "proxy only"
		if withHosted {
			name = "hosted and proxy"
		}
		t.Run(name, func(t *testing.T) {
			up := nugetAgeUpstream(t, false, linkedLibPackage())
			member := nugetProxyRepo("ng-aged", up.srv.URL, 7*24*3600)
			members := []any{"ng-aged"}
			repos := []*domain.Repository{member}
			if withHosted {
				hosted := testutil.SimpleRepo("ng-hosted", "nuget")
				repos = append(repos, hosted)
				members = append([]any{"ng-hosted"}, members...)
			}
			grp := &domain.Repository{
				ID: "ng-group", Name: "ng-group", Format: "nuget", Type: domain.TypeGroup, Online: true,
				FormatConfig: map[string]any{"member_names": members},
			}
			r := groupEngine("ng-group", append(repos, grp)...)
			const local = "http://localhost:8080/repository/ng-group"

			w := get(r, "/repository/ng-group"+hivePath+"index.json")
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.NotContains(t, w.Body.String(), up.srv.URL+"/v3/registration")
			assert.NotContains(t, w.Body.String(), "/repository/ng-aged/",
				"links served through the group stay on the group")
			var idx regLinks
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &idx))
			require.Len(t, idx.Items, 3)
			for _, page := range idx.Items {
				assert.True(t, strings.HasPrefix(page.ID, local+hivePath+"page/"), page.ID)
			}

			w = get(r, localPath(t, idx.Items[1].ID, local))
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), local+hivePath+"1.1.0.json")

			w = get(r, localPath(t, idx.Items[2].ID, local))
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var page struct {
				Count int `json:"count"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
			assert.Equal(t, 0, page.Count, "the member's age filter holds behind the group")
		})
	}
}

// A package id that merely starts with "registration" is not a registration
// hive: its flat container list and files keep the flat container's policy
// (and are not buffered through the registration rewrite).
func TestNuGet_Proxy_RegistrationLikePackageIDIsNotAHive(t *testing.T) {
	pkg := libPackage()
	pkg.id = "RegistrationKit"
	r, _ := agedNuGetProxy(t, pkg)
	const flat = "/repository/ng-aged/v3-flatcontainer/registrationkit/"

	assert.Equal(t, []string{"1.0.0", "1.1.0-beta1", "1.1.0"}, flatVersions(t, get(r, flat+"index.json")),
		"the young version is filtered out of the flat container list")
	assert.Equal(t, http.StatusForbidden, get(r, flat+"2.0.0/registrationkit.2.0.0.nupkg").Code)
	assert.Equal(t, http.StatusForbidden, get(r, flat+"2.0.0/registrationkit.nuspec").Code)
	assert.Equal(t, http.StatusOK, get(r, flat+"1.1.0/registrationkit.1.1.0.nupkg").Code)
}
