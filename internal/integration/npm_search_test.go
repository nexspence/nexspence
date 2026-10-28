//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository/postgres"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

func TestNPMSearch_ContentSelectorsAcrossRepositoryTypes(t *testing.T) {
	createHostedRepo(t, "npm", "npm-search-local", `{}`)
	admin := login(t, "admin", "admin123")
	pool := pgtest.Pool(t)
	repo, err := postgres.NewRepositoryRepo(pool).Get(context.Background(), "npm-search-local")
	require.NoError(t, err)
	for _, name := range []string{"@team/allowed", "@private/hidden"} {
		require.NoError(t, postgres.NewComponentRepo(pool).Create(context.Background(), &domain.Component{RepositoryID: repo.ID, Format: "npm", Name: name, Version: "1.0.0"}))
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"objects":[{"package":{"name":"@private/hidden","version":"2.0.0"}},{"package":{"name":"@team/allowed","version":"2.0.0"}}],"total":2,"time":"now"}`)
	}))
	defer upstream.Close()
	createProxyRepo(t, "npm", "npm-search-proxy", upstream.URL)
	response := authReq(t, http.MethodPost, "/service/rest/v1/repositories/npm/group", strings.NewReader(`{"name":"npm-search-group","online":true,"formatConfig":{"member_names":["npm-search-proxy","npm-search-local"]}}`), admin)
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	require.Equal(t, http.StatusCreated, response.StatusCode, string(body))
	t.Cleanup(func() {
		r := authReq(t, http.MethodDelete, "/service/rest/v1/repositories/npm-search-group", nil, admin)
		r.Body.Close()
	})
	for i, name := range []string{"npm-search-local", "npm-search-proxy", "npm-search-group"} {
		t.Run(name, func(t *testing.T) {
			token := selectorUser(t, admin, fmt.Sprintf("search-sel-%d", i), `repository == "`+name+`" && path.startsWith("/@team/")`, "read")
			endpointOnly := selectorUser(t, admin, fmt.Sprintf("search-endpoint-%d", i), `repository == "`+name+`" && path.startsWith("/-/")`, "read")
			for _, suffix := range []string{"/-/v1/search", "/-/v1/search/"} {
				r := authReq(t, http.MethodGet, "/repository/"+name+suffix, nil, endpointOnly)
				raw, _ := io.ReadAll(r.Body)
				r.Body.Close()
				require.Equal(t, http.StatusOK, r.StatusCode, string(raw))
				assert.NotContains(t, string(raw), "hidden")
				assert.NotContains(t, string(raw), "allowed")
			}
			for _, suffix := range []string{"/-//v1/search", "/./-/v1/search"} {
				r := authReq(t, http.MethodGet, "/repository/"+name+suffix+"?size=1&from=0", nil, token)
				r.Body.Close()
				assert.Equal(t, http.StatusBadRequest, r.StatusCode)
			}
			for _, suffix := range []string{"/-/v1/search", "/-/v1/search/"} {
				for _, from := range []int{0, 1} {
					r := authReq(t, http.MethodGet, fmt.Sprintf("/repository/%s%s?size=1&from=%d", name, suffix, from), nil, token)
					raw, _ := io.ReadAll(r.Body)
					r.Body.Close()
					require.Equal(t, http.StatusOK, r.StatusCode, string(raw))
					var doc struct {
						Objects []struct {
							Package struct{ Name, Version string }
						}
						Total int
					}
					require.NoError(t, json.Unmarshal(raw, &doc))
					assert.Equal(t, 1, doc.Total, string(raw))
					assert.NotContains(t, string(raw), "hidden")
					if from == 0 {
						require.Len(t, doc.Objects, 1)
						assert.Equal(t, "@team/allowed", doc.Objects[0].Package.Name)
					} else {
						assert.Empty(t, doc.Objects)
					}
				}

			}
			for _, caller := range []string{"", selectorUser(t, admin, fmt.Sprintf("search-write-%d", i), `repository == "`+name+`"`, "write")} {
				r := authReq(t, http.MethodGet, "/repository/"+name+"/-/v1/search", nil, caller)
				r.Body.Close()
				want := http.StatusForbidden
				if caller == "" {
					want = http.StatusUnauthorized
				}
				assert.Equal(t, want, r.StatusCode)
			}
		})
	}
}

func TestNPMSearch_GroupHEAD(t *testing.T) {
	createHostedRepo(t, "npm", "npm-head-local", `{}`)
	admin := login(t, "admin", "admin123")
	resp := authReq(t, http.MethodPost, "/service/rest/v1/repositories/npm/group", strings.NewReader(`{"name":"npm-head-group","online":true,"formatConfig":{"member_names":["npm-head-local"]}}`), admin)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	t.Cleanup(func() {
		r := authReq(t, http.MethodDelete, "/service/rest/v1/repositories/npm-head-group", nil, admin)
		r.Body.Close()
	})
	resp = authReq(t, http.MethodHead, "/repository/npm-head-group/-/v1/search", nil, admin)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, body)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}
