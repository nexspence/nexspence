package nuget_test

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/nuget"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Minimum package age on a NuGet proxy (#643): versions younger than the
// configured age are left out of the flat container version list and the
// registration pages, and their .nupkg/.nuspec are refused. Publish times
// come from the upstream registration (catalogEntry.published).

const day = 24 * time.Hour

func ago(d time.Duration) string {
	return time.Now().Add(-d).UTC().Format("2006-01-02T15:04:05.999Z07:00")
}

// unlistedMarker is what nuget.org writes as "published" once a version is
// unlisted.
const unlistedMarker = "1900-01-01T00:00:00+00:00"

// ngVersion is one version of the fake upstream. published or commit "" leave
// the field out of the registration leaf.
type ngVersion struct {
	v         string
	published string
	commit    string
	listed    bool
}

// ngPackage is one package of the fake upstream. pageSize > 0 links the
// registration pages instead of inlining them; flat overrides the flat
// container list (default: every version); regStatus answers the
// registration index with that status instead.
type ngPackage struct {
	id        string
	versions  []ngVersion
	pageSize  int
	flat      []string
	regStatus int
}

type ngUpstream struct {
	srv     *httptest.Server
	regHits atomic.Int32
}

// nugetAgeUpstream mimics api.nuget.org: the service index at /v3/index.json,
// the flat container at /v3-flatcontainer/, registrations in the gzip
// SemVer 2.0.0 hive (always gzip-encoded, as nuget.org serves it). noHive
// leaves RegistrationsBaseUrl out of the service index.
func nugetAgeUpstream(t *testing.T, noHive bool, pkgs ...ngPackage) *ngUpstream {
	t.Helper()
	u := &ngUpstream{}
	byID := map[string]ngPackage{}
	for _, p := range pkgs {
		byID[strings.ToLower(p.id)] = p
	}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := u.srv.URL
		hive := base + "/v3/registration5-gz-semver2/"
		p := r.URL.Path
		if p == "/v3/index.json" {
			res := []map[string]string{{"@id": base + "/v3-flatcontainer/", "@type": "PackageBaseAddress/3.0.0"}}
			if !noHive {
				res = append(res,
					map[string]string{"@id": base + "/v3/registration5-semver1/", "@type": "RegistrationsBaseUrl"},
					map[string]string{"@id": hive, "@type": "RegistrationsBaseUrl/3.6.0"})
			}
			writeJSON(w, false, map[string]any{"version": "3.0.0", "resources": res})
			return
		}
		if rest, ok := strings.CutPrefix(p, "/v3-flatcontainer/"); ok {
			segs := strings.Split(rest, "/")
			pkg, ok := byID[segs[0]]
			if !ok {
				http.NotFound(w, r)
				return
			}
			flat := pkg.flat
			if flat == nil {
				for _, v := range pkg.versions {
					flat = append(flat, strings.ToLower(v.v))
				}
			}
			switch {
			case len(segs) == 2 && segs[1] == "index.json":
				writeJSON(w, false, map[string]any{"versions": flat})
			case len(segs) == 3:
				for _, v := range flat {
					if v == segs[1] {
						_, _ = w.Write([]byte("package-bytes"))
						return
					}
				}
				http.NotFound(w, r)
			default:
				http.NotFound(w, r)
			}
			return
		}
		if rest, ok := strings.CutPrefix(p, "/v3/registration5-gz-semver2/"); ok {
			segs := strings.Split(rest, "/")
			pkg, ok := byID[segs[0]]
			if !ok {
				http.NotFound(w, r)
				return
			}
			regURL := hive + segs[0] + "/index.json"
			if len(segs) == 2 && segs[1] == "index.json" {
				u.regHits.Add(1)
				if pkg.regStatus != 0 {
					w.WriteHeader(pkg.regStatus)
					return
				}
				writeJSON(w, true, registrationIndex(base, regURL, pkg))
				return
			}
			if len(segs) == 4 && segs[1] == "page" {
				for _, page := range pagesOf(pkg) {
					if page[0].v == segs[2] && page[len(page)-1].v+".json" == segs[3] {
						doc := pageDoc(base, regURL, pkg, page)
						doc["parent"] = regURL
						writeJSON(w, true, doc)
						return
					}
				}
			}
			http.NotFound(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func writeJSON(w http.ResponseWriter, gz bool, v any) {
	w.Header().Set("Content-Type", "application/json")
	if !gz {
		_ = json.NewEncoder(w).Encode(v)
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	zw := gzip.NewWriter(w)
	_ = json.NewEncoder(zw).Encode(v)
	_ = zw.Close()
}

func pagesOf(pkg ngPackage) [][]ngVersion {
	size := pkg.pageSize
	if size <= 0 {
		size = len(pkg.versions)
	}
	var out [][]ngVersion
	for i := 0; i < len(pkg.versions); i += size {
		out = append(out, pkg.versions[i:min(i+size, len(pkg.versions))])
	}
	return out
}

func leaf(base, regURL string, pkg ngPackage, v ngVersion) map[string]any {
	lid, lv := strings.ToLower(pkg.id), strings.ToLower(v.v)
	content := base + "/v3-flatcontainer/" + lid + "/" + lv + "/" + lid + "." + lv + ".nupkg"
	ce := map[string]any{
		"@id": base + "/v3/catalog0/data/2022.12.08.16.43.03/" + lid + "." + lv + ".json", "@type": "PackageDetails",
		"authors": "Someone", "id": pkg.id, "listed": v.listed, "packageContent": content, "version": v.v,
		"dependencyGroups": []any{map[string]any{"targetFramework": "net8.0"}},
	}
	if v.published != "" {
		ce["published"] = v.published
	}
	l := map[string]any{
		"@id": strings.TrimSuffix(regURL, "index.json") + lv + ".json", "@type": "Package",
		"commitId": "29803214-bbd1-4347-bdbf-bc4906742ca2", "catalogEntry": ce,
		"packageContent": content, "registration": regURL,
	}
	if v.commit != "" {
		l["commitTimeStamp"] = v.commit
	}
	return l
}

func pageDoc(base, regURL string, pkg ngPackage, page []ngVersion) map[string]any {
	items := make([]any, 0, len(page))
	for _, v := range page {
		items = append(items, leaf(base, regURL, pkg, v))
	}
	return map[string]any{
		"@id":   strings.TrimSuffix(regURL, "index.json") + "page/" + page[0].v + "/" + page[len(page)-1].v + ".json",
		"@type": "catalog:CatalogPage", "commitId": "7f406f6d", "commitTimeStamp": ago(30 * day),
		"count": len(page), "items": items, "lower": page[0].v, "upper": page[len(page)-1].v,
	}
}

func registrationIndex(base, regURL string, pkg ngPackage) map[string]any {
	var pages []any
	for _, page := range pagesOf(pkg) {
		doc := pageDoc(base, regURL, pkg, page)
		if pkg.pageSize > 0 {
			delete(doc, "items") // linked: the client fetches the page by @id
		} else {
			doc["@id"] = regURL + "#page/" + page[0].v + "/" + page[len(page)-1].v
		}
		pages = append(pages, doc)
	}
	return map[string]any{
		"@id": regURL, "@type": []string{"catalog:CatalogRoot", "PackageRegistration", "catalog:Permalink"},
		"commitId": "29803214", "commitTimeStamp": ago(day), "count": len(pages), "items": pages,
	}
}

// libPackage: an old release, an old unlisted prerelease (nuget.org's 1900
// marker, dated by an old commit), a freshly unlisted one (recent commit), an
// old release and a young release.
func libPackage() ngPackage {
	return ngPackage{id: "Contoso.Lib", versions: []ngVersion{
		{v: "1.0.0", published: ago(400 * day), commit: ago(300 * day), listed: true},
		{v: "1.1.0-beta1", published: unlistedMarker, commit: ago(300 * day)},
		{v: "1.1.0-beta2", published: unlistedMarker, commit: ago(time.Hour)},
		{v: "1.1.0", published: ago(90 * day), commit: ago(90 * day), listed: true},
		{v: "2.0.0", published: ago(time.Hour), commit: ago(time.Hour), listed: true},
	}}
}

func nugetProxyRepo(name, remote string, minAgeSecs int) *domain.Repository {
	repo := &domain.Repository{
		ID: name, Name: name, Format: "nuget", Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remote},
	}
	if minAgeSecs > 0 {
		repo.ProxyConfig["minimum_package_age"] = minAgeSecs
	}
	return repo
}

func get(r *gin.Engine, url string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

func flatVersions(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var doc struct {
		Versions []string `json:"versions"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	return doc.Versions
}

func agedNuGetProxy(t *testing.T, pkgs ...ngPackage) (*gin.Engine, *ngUpstream) {
	t.Helper()
	up := nugetAgeUpstream(t, false, pkgs...)
	return setup(nugetProxyRepo("ng-aged", up.srv.URL, 7*24*3600)), up
}

const (
	libFlat = "/repository/ng-aged/v3-flatcontainer/contoso.lib/index.json"
	libReg  = "/repository/ng-aged/v3/registration5-gz-semver2/contoso.lib/index.json"
)

func libFile(v, ext string) string {
	if ext == ".nuspec" {
		return "/repository/ng-aged/v3-flatcontainer/contoso.lib/" + v + "/contoso.lib.nuspec"
	}
	return "/repository/ng-aged/v3-flatcontainer/contoso.lib/" + v + "/contoso.lib." + v + ".nupkg"
}

func TestNuGet_AgePolicy_FlatIndexOmitsYoungVersions(t *testing.T) {
	r, _ := agedNuGetProxy(t, libPackage())

	assert.Equal(t, []string{"1.0.0", "1.1.0-beta1", "1.1.0"}, flatVersions(t, get(r, libFlat)),
		"an unlisted version is dated by its commit: an old one stays, a fresh one is hidden")
}

func TestNuGet_AgePolicy_RegistrationIndexIsFiltered(t *testing.T) {
	r, _ := agedNuGetProxy(t, libPackage())

	w := get(r, libReg)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var doc struct {
		Count int `json:"count"`
		Items []struct {
			Count int    `json:"count"`
			Lower string `json:"lower"`
			Upper string `json:"upper"`
			Items []struct {
				CatalogEntry struct {
					Version string `json:"version"`
					Listed  bool   `json:"listed"`
				} `json:"catalogEntry"`
			} `json:"items"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	require.Len(t, doc.Items, 1)
	page := doc.Items[0]
	var versions []string
	for _, l := range page.Items {
		versions = append(versions, l.CatalogEntry.Version)
	}
	assert.Equal(t, []string{"1.0.0", "1.1.0-beta1", "1.1.0"}, versions)
	assert.Equal(t, 3, page.Count)
	assert.Equal(t, "1.0.0", page.Lower)
	assert.Equal(t, "1.1.0", page.Upper, "upper follows the newest surviving leaf")
	assert.False(t, page.Items[1].CatalogEntry.Listed, "an unlisted version stays unlisted")
}

// A client that already knows the version must not bypass the list — even
// when the package is already cached.
func TestNuGet_AgePolicy_YoungVersionFilesAreRefused(t *testing.T) {
	up := nugetAgeUpstream(t, false, libPackage())
	repo := nugetProxyRepo("ng-aged", up.srv.URL, 0)
	r := setup(repo)
	require.Equal(t, http.StatusOK, get(r, libFile("2.0.0", ".nupkg")).Code, "cached before the policy")

	repo.ProxyConfig["minimum_package_age"] = 7 * 24 * 3600
	for _, ext := range []string{".nupkg", ".nuspec"} {
		w := get(r, libFile("2.0.0", ext))
		assert.Equal(t, http.StatusForbidden, w.Code, ext)
		assert.Contains(t, w.Body.String(), "contoso.lib 2.0.0 was published", ext)
		assert.Contains(t, w.Body.String(), "minimum package age of 168h0m0s", ext)

		w = get(r, libFile("1.1.0-beta2", ext))
		assert.Equal(t, http.StatusForbidden, w.Code, "freshly unlisted "+ext)

		for _, v := range []string{"1.0.0", "1.1.0-beta1", "1.1.0"} {
			w = get(r, libFile(v, ext))
			assert.Equal(t, http.StatusOK, w.Code, v+ext+": "+w.Body.String())
		}
	}
}

// Large packages link their registration pages; the dates come from the
// pages, and a page served through the proxy is filtered too.
func TestNuGet_AgePolicy_LinkedRegistrationPages(t *testing.T) {
	pkg := libPackage()
	pkg.pageSize = 2
	r, _ := agedNuGetProxy(t, pkg)

	assert.Equal(t, []string{"1.0.0", "1.1.0-beta1", "1.1.0"}, flatVersions(t, get(r, libFlat)))
	assert.Equal(t, http.StatusForbidden, get(r, libFile("2.0.0", ".nupkg")).Code)
	assert.Equal(t, http.StatusOK, get(r, libFile("1.1.0", ".nupkg")).Code)

	// The index links its pages: nothing inline to filter, served as is.
	w := get(r, libReg)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "/page/2.0.0/2.0.0.json")

	w = get(r, "/repository/ng-aged/v3/registration5-gz-semver2/contoso.lib/page/1.1.0-beta2/1.1.0.json")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var page struct {
		Count  int    `json:"count"`
		Lower  string `json:"lower"`
		Upper  string `json:"upper"`
		Parent string `json:"parent"`
		Items  []any  `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	assert.Equal(t, 1, page.Count)
	assert.Len(t, page.Items, 1)
	assert.Equal(t, "1.1.0", page.Lower)
	assert.Equal(t, "1.1.0", page.Upper)
	assert.NotEmpty(t, page.Parent)

	w = get(r, "/repository/ng-aged/v3/registration5-gz-semver2/contoso.lib/page/2.0.0/2.0.0.json")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	assert.Equal(t, 0, page.Count)
	assert.Empty(t, page.Items)
}

// An inline page left empty is dropped and the index count follows.
func TestNuGet_AgePolicy_EmptiedInlinePageIsDropped(t *testing.T) {
	cutoff := time.Now().Add(-7 * day)
	in := `{"count":2,"items":[
 {"@id":"x#page/1.0.0/1.0.0","count":1,"lower":"1.0.0","upper":"1.0.0","items":[{"commitTimeStamp":"2020-01-02T00:00:00Z","catalogEntry":{"version":"1.0.0","published":"2020-01-01T00:00:00Z"}}]},
 {"@id":"x#page/2.0.0/2.0.0","count":1,"lower":"2.0.0","upper":"2.0.0","items":[{"catalogEntry":{"version":"2.0.0","published":"` + ago(time.Hour) + `"}}]},
 {"@id":"https://x/page/3.0.0/4.0.0.json","count":9,"lower":"3.0.0","upper":"4.0.0"}]}`
	out, applied := nuget.FilterRegistrationByAge([]byte(in), cutoff)
	require.True(t, applied)
	var doc struct {
		Count int `json:"count"`
		Items []struct {
			ID string `json:"@id"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(out, &doc))
	assert.Equal(t, 2, doc.Count)
	require.Len(t, doc.Items, 2)
	assert.Equal(t, "x#page/1.0.0/1.0.0", doc.Items[0].ID)
	assert.Equal(t, "https://x/page/3.0.0/4.0.0.json", doc.Items[1].ID, "a linked page is kept")
}

func TestNuGet_AgePolicy_FilterRegistrationEdgeCases(t *testing.T) {
	cutoff := time.Now().Add(-7 * day)
	for _, in := range []string{`not json`, `{"@id":"leaf","published":"2020-01-01T00:00:00Z"}`, `{"items":[]}`} {
		out, applied := nuget.FilterRegistrationByAge([]byte(in), cutoff)
		assert.True(t, applied, in)
		assert.Equal(t, in, string(out), in)
	}
	// Nothing dated: served whole, reported as a gap.
	in := `{"items":[{"catalogEntry":{"version":"1.0.0"}},{"catalogEntry":{"version":"2.0.0","published":"` + unlistedMarker + `"}}]}`
	out, applied := nuget.FilterRegistrationByAge([]byte(in), cutoff)
	assert.False(t, applied)
	assert.Equal(t, in, string(out))
	// A zone-less timestamp, as some feeds write it, reads as UTC.
	in = `{"items":[{"catalogEntry":{"version":"1.0.0","published":"2020-01-01T00:00:00"}},{"catalogEntry":{"version":"2.0.0","published":"` +
		time.Now().UTC().Format("2006-01-02T15:04:05") + `"}}]}`
	out, applied = nuget.FilterRegistrationByAge([]byte(in), cutoff)
	assert.True(t, applied)
	assert.Contains(t, string(out), `"1.0.0"`)
	assert.NotContains(t, string(out), `"2.0.0"`)
}

// An undated version among dated ones has an unknowable age: hidden and
// refused.
func TestNuGet_AgePolicy_UndatedVersionInDatedPackageIsRefused(t *testing.T) {
	pkg := libPackage()
	pkg.versions = append(pkg.versions, ngVersion{v: "1.0.1", listed: true})
	r, _ := agedNuGetProxy(t, pkg)

	assert.NotContains(t, flatVersions(t, get(r, libFlat)), "1.0.1")
	w := get(r, libFile("1.0.1", ".nupkg"))
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "no publish time")
	assert.NotContains(t, get(r, libReg).Body.String(), `"1.0.1"`)
}

// A version the flat container lists but the registration does not yet know
// is brand new (the registration lags): refused.
func TestNuGet_AgePolicy_NotYetRegisteredVersionIsRefused(t *testing.T) {
	pkg := libPackage()
	pkg.flat = []string{"1.0.0", "1.1.0", "2.0.0", "2.0.1"}
	r, _ := agedNuGetProxy(t, pkg)

	assert.Equal(t, []string{"1.0.0", "1.1.0"}, flatVersions(t, get(r, libFlat)))
	w := get(r, libFile("2.0.1", ".nupkg"))
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
}

// A version upstream does not have stays a 404, not a policy refusal.
func TestNuGet_AgePolicy_UnknownVersionIsNotFound(t *testing.T) {
	r, _ := agedNuGetProxy(t, libPackage())

	w := get(r, libFile("9.9.9", ".nupkg"))
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
}

// A registration without a single date cannot be policed: the package is
// served whole (hybrid failure mode, as for npm and PyPI).
func TestNuGet_AgePolicy_NoDatesAtAllServesPackage(t *testing.T) {
	r, _ := agedNuGetProxy(t, ngPackage{id: "Contoso.Lib", versions: []ngVersion{
		{v: "1.0.0", listed: true}, {v: "2.0.0", listed: true},
	}})

	assert.Equal(t, []string{"1.0.0", "2.0.0"}, flatVersions(t, get(r, libFlat)))
	assert.Equal(t, http.StatusOK, get(r, libFile("2.0.0", ".nupkg")).Code)
	assert.Contains(t, get(r, libReg).Body.String(), `"2.0.0"`)
}

// An upstream without a registration hive has no dates anywhere.
func TestNuGet_AgePolicy_NoRegistrationHiveServesPackage(t *testing.T) {
	up := nugetAgeUpstream(t, true, libPackage())
	r := setup(nugetProxyRepo("ng-aged", up.srv.URL, 7*24*3600))

	assert.Len(t, flatVersions(t, get(r, libFlat)), 5)
	assert.Equal(t, http.StatusOK, get(r, libFile("2.0.0", ".nupkg")).Code)
}

// A registration 404 from an upstream that has a hive: not indexed yet, so
// too new to serve.
func TestNuGet_AgePolicy_UnregisteredPackageIsHidden(t *testing.T) {
	pkg := libPackage()
	pkg.regStatus = http.StatusNotFound
	r, _ := agedNuGetProxy(t, pkg)

	assert.Empty(t, flatVersions(t, get(r, libFlat)))
	assert.Equal(t, http.StatusForbidden, get(r, libFile("1.0.0", ".nupkg")).Code)
}

// A failing registration must not open the gate.
func TestNuGet_AgePolicy_RegistrationErrorFailsClosed(t *testing.T) {
	pkg := libPackage()
	pkg.regStatus = http.StatusInternalServerError
	r, _ := agedNuGetProxy(t, pkg)

	assert.Empty(t, flatVersions(t, get(r, libFlat)))
	assert.Equal(t, http.StatusBadGateway, get(r, libFile("1.0.0", ".nupkg")).Code)
}

// The registration is read once per metadata TTL, and the proxy's own fresh
// copy of it is used instead of upstream.
func TestNuGet_AgePolicy_DatesAreCached(t *testing.T) {
	r, up := agedNuGetProxy(t, libPackage())

	require.Equal(t, http.StatusOK, get(r, libReg).Code) // caches the registration
	require.Equal(t, int32(1), up.regHits.Load())
	for range 3 {
		require.Equal(t, http.StatusOK, get(r, libFile("1.0.0", ".nupkg")).Code)
	}
	flatVersions(t, get(r, libFlat))
	assert.Equal(t, int32(1), up.regHits.Load(), "dates come from the cached registration, then from memory")
}

func TestNuGet_AgePolicy_DisabledByDefault(t *testing.T) {
	up := nugetAgeUpstream(t, false, libPackage())
	r := setup(nugetProxyRepo("ng-aged", up.srv.URL, 0))

	assert.Len(t, flatVersions(t, get(r, libFlat)), 5)
	assert.Equal(t, http.StatusOK, get(r, libFile("2.0.0", ".nupkg")).Code)
	assert.Contains(t, get(r, libReg).Body.String(), `"2.0.0"`)
	assert.Equal(t, int32(1), up.regHits.Load(), "only the client's own registration request reaches upstream")
}

// The legacy local registration layout is still URL-rewritten after the
// filter runs.
func TestNuGet_AgePolicy_LegacyRegistrationPathIsFilteredAndRewritten(t *testing.T) {
	cutoffOld, young := ago(400*day), ago(time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/index.json":
			fmt.Fprint(w, `{"resources":[]}`)
		case "/v3/registration/contoso.lib/index.json":
			fmt.Fprintf(w, `{"count":1,"items":[{"count":2,"lower":"1.0.0","upper":"2.0.0","items":[
 {"@id":"https://api.nuget.org/v3/registration5-gz-semver2/contoso.lib/1.0.0.json","packageContent":"https://api.nuget.org/x.nupkg","catalogEntry":{"id":"Contoso.Lib","version":"1.0.0","published":%q}},
 {"@id":"https://api.nuget.org/v3/registration5-gz-semver2/contoso.lib/2.0.0.json","packageContent":"https://api.nuget.org/y.nupkg","catalogEntry":{"id":"Contoso.Lib","version":"2.0.0","published":%q}}]}]}`, cutoffOld, young)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	r := setup(nugetProxyRepo("ng-legacy", srv.URL, 7*24*3600))

	w := get(r, "/repository/ng-legacy/v3/registration/contoso.lib/index.json")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, "http://localhost:8080/repository/ng-legacy/v3/flatcontainer/contoso.lib/1.0.0/contoso.lib.1.0.0.nupkg")
	assert.NotContains(t, body, "2.0.0")
}

// A group calls its members through their own handlers, so a proxy member's
// policy holds behind the group: its version list is filtered before the
// merge, and its young package is refused.
func TestNuGet_AgePolicy_HoldsBehindGroup(t *testing.T) {
	up := nugetAgeUpstream(t, false, libPackage())
	member := nugetProxyRepo("ng-aged", up.srv.URL, 7*24*3600)
	grp := &domain.Repository{
		ID: "ng-group", Name: "ng-group", Format: "nuget", Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []any{"ng-aged"}},
	}
	repos := testutil.NewRepoRepo(member, grp)
	d := formats.Deps{
		Repos: repos, Blobs: testutil.NewBlobStoreRepo(), Components: testutil.NewComponentRepo(),
		Assets: testutil.NewAssetRepo(), BlobStore: testutil.NewBlobStore(), BaseURL: "http://localhost:8080",
	}
	nh := nuget.New(d)
	gh := group.New(d, map[string]formats.FormatHandler{"nuget": nh})
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		if c.Param("repoName") == "ng-group" {
			gh.ServeHTTP(c)
			return
		}
		nh.ServeHTTP(c)
	})

	assert.Equal(t, []string{"1.0.0", "1.1.0-beta1", "1.1.0"},
		flatVersions(t, get(r, "/repository/ng-group/v3-flatcontainer/contoso.lib/index.json")))
	assert.Equal(t, http.StatusForbidden,
		get(r, "/repository/ng-group/v3-flatcontainer/contoso.lib/2.0.0/contoso.lib.2.0.0.nupkg").Code)
	assert.Equal(t, http.StatusOK,
		get(r, "/repository/ng-group/v3-flatcontainer/contoso.lib/1.1.0/contoso.lib.1.1.0.nupkg").Code)
}
