package npm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/npm"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func searchRouter(d formats.Deps) *gin.Engine {
	r := gin.New()
	h := npm.New(d)
	r.Any("/repository/:repoName/*path", h.ServeHTTP)
	return r
}

func searchGET(r *gin.Engine, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func TestNPMSearch_HostedFiltersLatestPackagesBeforePaging(t *testing.T) {
	repo := testutil.SimpleRepo("host", "npm")
	comps := testutil.NewComponentRepo()
	for _, comp := range []domain.Component{
		{Repository: "host", Format: "npm", Name: "@studio/editor", Version: "1.0.0", Tags: []string{"latest"}, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Extra: map[string]any{"npm_manifest": map[string]any{"description": "Unity editor tools", "keywords": []any{"asset"}}}},
		{Repository: "host", Format: "npm", Name: "@studio/editor", Version: "2.0.0-beta"},
		{Repository: "host", Format: "npm", Name: "other", Version: "1.0.0", Extra: map[string]any{"npm_manifest": map[string]any{"keywords": []any{"unity"}}}},
		{Repository: "host", Format: "npm", Name: "unrelated", Version: "1.0.0"},
		{Repository: "host", Format: "npm", Name: "cached", Version: "metadata"},
		{Repository: "private", Format: "npm", Name: "private", Version: "1.0.0"},
	} {
		comp := comp
		require.NoError(t, comps.Create(context.Background(), &comp))
	}
	d := formats.Deps{Repos: testutil.NewRepoRepo(repo), Components: comps, Assets: testutil.NewAssetRepo(), Blobs: testutil.NewBlobStoreRepo(), BlobStore: testutil.NewBlobStore()}
	r := searchRouter(d)
	w := searchGET(r, "/repository/host/-/v1/search?text=Unity&size=1&from=0")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var doc struct {
		Objects []struct {
			Package struct {
				Name, Version, Description, Date string
				Keywords                         []string
			}
			Score       map[string]any
			SearchScore float64
		}
		Total int
		Time  string
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	require.Len(t, doc.Objects, 1)
	assert.Equal(t, 2, doc.Total)
	assert.Equal(t, "@studio/editor", doc.Objects[0].Package.Name)
	assert.Equal(t, "1.0.0", doc.Objects[0].Package.Version)
	assert.Equal(t, "Unity editor tools", doc.Objects[0].Package.Description)
	assert.Equal(t, "2026-01-02T03:04:05Z", doc.Objects[0].Package.Date)
	assert.Equal(t, []string{"asset"}, doc.Objects[0].Package.Keywords)
	assert.NotEmpty(t, doc.Objects[0].Score)
	assert.NotEmpty(t, doc.Time)
	w = searchGET(r, "/repository/host/-/v1/search?text=unity&size=1&from=1")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	require.Len(t, doc.Objects, 1)
	assert.Equal(t, "other", doc.Objects[0].Package.Name)
	w = searchGET(r, "/repository/host/-/v1/search?text=absent")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	assert.Empty(t, doc.Objects)
	assert.Zero(t, doc.Total)
	assert.Contains(t, w.Body.String(), `"objects":[]`)
}

func TestNPMSearch_ProxyForwardsQueryWithoutCaching(t *testing.T) {
	var queries []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/-/v1/search", r.URL.Path)
		queries = append(queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"objects":[{"package":{"name":"vite","version":"7.0.0"},"score":{"final":0.8},"searchScore":42}],"total":8,"time":"now"}`))
	}))
	defer upstream.Close()
	repo := testutil.SimpleRepo("proxy", "npm")
	repo.Type = domain.TypeProxy
	repo.ProxyConfig = map[string]any{"remote_url": upstream.URL}
	d := formats.Deps{Repos: testutil.NewRepoRepo(repo), Components: testutil.NewComponentRepo(), Assets: testutil.NewAssetRepo(), Blobs: testutil.NewBlobStoreRepo(), BlobStore: testutil.NewBlobStore()}
	r := searchRouter(d)
	const query = "text=vite%20plugin&size=3&from=2&quality=0.65&popularity=0.98&maintenance=0.5"
	for i := 0; i < 2; i++ {
		w := searchGET(r, "/repository/proxy/-/v1/search?"+query)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"objects":[{"package":{"name":"vite","version":"7.0.0"},"score":{"final":0.8},"searchScore":42}],"total":8,"time":"now"}`, w.Body.String())
	}
	assert.Equal(t, []string{query, query}, queries)
	page, err := d.Assets.List(context.Background(), repo.Name, 10, 0)
	require.NoError(t, err)
	assert.Empty(t, page.Items)
}

func TestNPMSearch_ProxyFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		status, want int
	}{
		{"upstream error", `{"error":"upstream unavailable"}`, 503, 503},
		{"malformed response", `<html>not a registry</html>`, 200, 502},
		{"missing search objects", `{}`, 200, 502},
		{"missing total", `{"objects":[{"package":{"name":"a"}}]}`, 200, 502},
		{"null total", `{"objects":[{"package":{"name":"a"}}],"total":null}`, 200, 502},
		{"contradictory total", `{"objects":[{"package":{"name":"a"}}],"total":0}`, 200, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			repo := testutil.SimpleRepo("proxy", "npm")
			repo.Type = domain.TypeProxy
			repo.ProxyConfig = map[string]any{"remote_url": upstream.URL}
			w := searchGET(searchRouter(formats.Deps{Repos: testutil.NewRepoRepo(repo), Components: testutil.NewComponentRepo()}), "/repository/proxy/-/v1/search?text=a")
			require.Equal(t, tc.want, w.Code, w.Body.String())
			if tc.status != 200 {
				assert.Equal(t, tc.body, w.Body.String())
			}
		})
	}
}

func TestNPMSearch_GroupMergesAllPagesWithHostedPrecedence(t *testing.T) {
	var offsets []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offsets = append(offsets, r.URL.Query().Get("from"))
		assert.Equal(t, "250", r.URL.Query().Get("size"))
		assert.Equal(t, "studio", r.URL.Query().Get("text"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("from") == "2" {
			_, _ = w.Write([]byte(`{"objects":[{"package":{"name":"studio-z","version":"3.0.0"}}],"total":3,"time":"now"}`))
			return
		}
		_, _ = w.Write([]byte(`{"objects":[{"package":{"name":"studio-a","version":"99.0.0"}},{"package":{"name":"studio-b","version":"2.0.0"}}],"total":3,"time":"now"}`))
	}))
	defer upstream.Close()
	hosted := testutil.SimpleRepo("local", "npm")
	proxy := testutil.SimpleRepo("remote", "npm")
	proxy.Type = domain.TypeProxy
	proxy.ProxyConfig = map[string]any{"remote_url": upstream.URL}
	groupRepo := testutil.SimpleRepo("all", "npm")
	groupRepo.Type = domain.TypeGroup
	groupRepo.FormatConfig = map[string]any{"member_names": []any{"remote", "local"}}
	comps := testutil.NewComponentRepo()
	require.NoError(t, comps.Create(context.Background(), &domain.Component{Repository: "local", Format: "npm", Name: "studio-a", Version: "1.0.0"}))
	d := formats.Deps{Repos: testutil.NewRepoRepo(hosted, proxy, groupRepo), Components: comps, Assets: testutil.NewAssetRepo(), BlobStore: testutil.NewBlobStore(), Blobs: testutil.NewBlobStoreRepo()}
	h := npm.New(d)
	g := group.New(d, map[string]formats.FormatHandler{"npm": h})
	r := gin.New()
	r.Any("/repository/:repoName/*path", g.ServeHTTP)
	w := searchGET(r, "/repository/all/-/v1/search?text=studio&size=1&from=2")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var doc struct {
		Objects []struct {
			Package struct{ Name, Version string }
		}
		Total int
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	require.Len(t, doc.Objects, 1)
	assert.Equal(t, 3, doc.Total)
	assert.Equal(t, "studio-z", doc.Objects[0].Package.Name)
	assert.Equal(t, []string{"0", "2"}, offsets)
	w = searchGET(r, "/repository/all/-/v1/search?text=studio&size=1")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	require.Len(t, doc.Objects, 1)
	assert.Equal(t, "studio-a", doc.Objects[0].Package.Name)
	assert.Equal(t, "1.0.0", doc.Objects[0].Package.Version)
}

func TestNPMSearch_GroupDoesNotHideFailuresOrInvalidPaging(t *testing.T) {
	for _, tc := range []struct {
		name, query, body string
		status, want      int
	}{
		{"upstream unavailable", "?text=a", `{"error":"unavailable"}`, 503, 503},
		{"malformed object", "?text=a", `{"objects":[{"package":{}}],"total":1}`, 200, 502},
		{"negative page", "?size=-1", `{"objects":[],"total":0}`, 200, 400},
		{"overflow page", "?from=999999999999999999999999", `{"objects":[],"total":0}`, 200, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer up.Close()
			hosted := testutil.SimpleRepo("local", "npm")
			proxy := testutil.SimpleRepo("remote", "npm")
			proxy.Type = domain.TypeProxy
			proxy.ProxyConfig = map[string]any{"remote_url": up.URL}
			g := testutil.SimpleRepo("g", "npm")
			g.Type = domain.TypeGroup
			g.FormatConfig = map[string]any{"member_names": []string{"local", "remote"}}
			d := formats.Deps{Repos: testutil.NewRepoRepo(hosted, proxy, g), Components: testutil.NewComponentRepo()}
			handler := group.New(d, map[string]formats.FormatHandler{"npm": npm.New(d)})
			r := gin.New()
			r.Any("/repository/:repoName/*path", handler.ServeHTTP)
			w := searchGET(r, "/repository/g/-/v1/search"+tc.query)
			assert.Equal(t, tc.want, w.Code, w.Body.String())
		})
	}
}

func TestNPMSearch_GroupBoundsCombinedUpstreamPages(t *testing.T) {
	// Two individually valid pages must not grow the accumulated search without
	// bound. The response should fail rather than return a partial catalog.
	padding := strings.Repeat("x", 17<<20)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"objects": []any{map[string]any{"package": map[string]any{"name": "package-" + r.URL.Query().Get("from"), "description": padding}}}, "total": 2})
	}))
	defer up.Close()
	proxy := testutil.SimpleRepo("remote", "npm")
	proxy.Type = domain.TypeProxy
	proxy.ProxyConfig = map[string]any{"remote_url": up.URL}
	g := testutil.SimpleRepo("g", "npm")
	g.Type = domain.TypeGroup
	g.FormatConfig = map[string]any{"member_names": []string{"remote"}}
	d := formats.Deps{Repos: testutil.NewRepoRepo(proxy, g)}
	handler := group.New(d, map[string]formats.FormatHandler{"npm": npm.New(d)})
	r := gin.New()
	r.Any("/repository/:repoName/*path", handler.ServeHTTP)
	w := searchGET(r, "/repository/g/-/v1/search?text=package")
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

type malformedPackument struct{ *npm.Handler }

func (h malformedPackument) ServeHTTP(c *gin.Context) {
	c.Data(http.StatusOK, "application/json", []byte("not-json"))
}

func TestNPMSearch_StrictSearchDoesNotChangePackumentFallback(t *testing.T) {
	member := testutil.SimpleRepo("local", "npm")
	g := testutil.SimpleRepo("g", "npm")
	g.Type = domain.TypeGroup
	g.FormatConfig = map[string]any{"member_names": []string{"local"}}
	d := formats.Deps{Repos: testutil.NewRepoRepo(member, g)}
	h := group.New(d, map[string]formats.FormatHandler{"npm": malformedPackument{npm.New(d)}})
	r := gin.New()
	r.Any("/repository/:repoName/*path", h.ServeHTTP)
	w := searchGET(r, "/repository/g/pkg")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "not-json", w.Body.String())
}

func TestNPMSearch_HostedMatchesTextValues(t *testing.T) {
	repo := testutil.SimpleRepo("host", "npm")
	comps := testutil.NewComponentRepo()
	require.NoError(t, comps.Create(context.Background(), &domain.Component{Repository: "host", Format: "npm", Name: "editor", Version: "1.0.0", Extra: map[string]any{"npm_manifest": map[string]any{"description": "salt & pepper <editor>"}}}))
	r := searchRouter(formats.Deps{Repos: testutil.NewRepoRepo(repo), Components: comps})
	for _, tc := range []struct {
		query string
		want  int
	}{{"null", 0}, {"%26", 1}, {"%3Ceditor%3E", 1}} {
		w := searchGET(r, "/repository/host/-/v1/search?text="+tc.query)
		require.Equal(t, http.StatusOK, w.Code)
		var doc struct{ Total int }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
		assert.Equal(t, tc.want, doc.Total)
	}
}

func TestNPMSearch_HostedMaintainersAreUsableByNPMCLI(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"absent", nil, `[]`},
		{"invalid type", "invalid", `[]`},
		{"publisher names", []any{map[string]any{"name": "publisher", "email": "publisher@example.com"}}, `[{"username":"publisher","email":"publisher@example.com"}]`},
		{"registry usernames", []any{map[string]any{"username": "maintainer"}}, `[{"username":"maintainer"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := testutil.SimpleRepo("host", "npm")
			comps := testutil.NewComponentRepo()
			require.NoError(t, comps.Create(context.Background(), &domain.Component{Repository: "host", Format: "npm", Name: "editor", Version: "1.0.0", Extra: map[string]any{"npm_manifest": map[string]any{"maintainers": tc.value}}}))
			w := searchGET(searchRouter(formats.Deps{Repos: testutil.NewRepoRepo(repo), Components: comps}), "/repository/host/-/v1/search")
			require.Equal(t, http.StatusOK, w.Code)
			var doc struct {
				Objects []struct {
					Package struct{ Maintainers json.RawMessage }
				}
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
			require.Len(t, doc.Objects, 1)
			assert.JSONEq(t, tc.want, string(doc.Objects[0].Package.Maintainers))
		})
	}
}
