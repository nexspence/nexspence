package nexusclient_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/nexusclient"
)

// newClient points a client at srv and lets it dial loopback, which the
// SSRF-guarded default client refuses.
func newClient(t *testing.T, srv *httptest.Server) *nexusclient.Client {
	t.Helper()
	return nexusclient.New(srv.URL, "admin", "s3cret", 5*time.Second).
		WithHTTPClient(srv.Client())
}

func TestListRepositories_ParsesBasicListing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/service/rest/v1/repositories", r.URL.Path)
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "admin", user)
		assert.Equal(t, "s3cret", pass)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{"name":"maven-central","format":"maven2","type":"proxy","url":"http://n/repository/maven-central",
			 "attributes":{"proxy":{"remoteUrl":"https://repo1.maven.org/maven2"}}},
			{"name":"raw-hosted","format":"raw","type":"hosted","url":"http://n/repository/raw-hosted"}
		]`)
	}))
	defer srv.Close()

	repos, err := newClient(t, srv).ListRepositories(context.Background())
	require.NoError(t, err)
	require.Len(t, repos, 2)
	assert.Equal(t, "maven-central", repos[0].Name)
	assert.Equal(t, "maven2", repos[0].Format)
	assert.Equal(t, "proxy", repos[0].Type)
	assert.Equal(t, "https://repo1.maven.org/maven2", repos[0].RemoteURL)
	assert.Equal(t, "raw-hosted", repos[1].Name)
	assert.Equal(t, "hosted", repos[1].Type)
}

func TestListRepositories_SurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "bad credentials")
	}))
	defer srv.Close()

	_, err := newClient(t, srv).ListRepositories(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestListRepositoriesWithConfig_UsesRepositorySettings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/service/rest/v1/repositorySettings", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{"name":"maven-releases","format":"maven2","type":"hosted","online":true,
			 "storage":{"blobStoreName":"default"}},
			{"name":"maven-public","format":"maven2","type":"group","online":true,
			 "group":{"memberNames":["maven-releases","maven-central"]}},
			{"name":"maven-central","format":"maven2","type":"proxy","online":false,
			 "proxy":{"remoteUrl":"https://repo1.maven.org/maven2"}}
		]`)
	}))
	defer srv.Close()

	repos, err := newClient(t, srv).ListRepositoriesWithConfig(context.Background())
	require.NoError(t, err)
	require.Len(t, repos, 3)
	assert.True(t, repos[0].Online)
	assert.Equal(t, []string{"maven-releases", "maven-central"}, repos[1].MemberNames)
	assert.Equal(t, "https://repo1.maven.org/maven2", repos[2].RemoteURL)
	assert.False(t, repos[2].Online)
}

func TestListRepositoriesWithConfig_FallsBackWhenSettingsForbidden(t *testing.T) {
	var settingsHits, basicHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/service/rest/v1/repositorySettings":
			settingsHits++
			w.WriteHeader(http.StatusForbidden)
		case "/service/rest/v1/repositories":
			basicHits++
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[{"name":"raw-hosted","format":"raw","type":"hosted"}]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	repos, err := newClient(t, srv).ListRepositoriesWithConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, settingsHits)
	assert.Equal(t, 1, basicHits)
	require.Len(t, repos, 1)
	assert.Equal(t, "raw-hosted", repos[0].Name)
	// The basic listing carries no online flag; a repository is assumed usable.
	assert.True(t, repos[0].Online)
}

func TestListComponents_FollowsContinuationToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/service/rest/v1/components", r.URL.Path)
		require.Equal(t, "raw-hosted", r.URL.Query().Get("repository"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("continuationToken") == "" {
			_, _ = io.WriteString(w, `{"items":[
				{"name":"a.txt","version":null,"group":"/","format":"raw","assets":[
					{"path":"a.txt","downloadUrl":"http://n/repository/raw-hosted/a.txt",
					 "contentType":"text/plain","fileSize":3,"checksum":{"sha256":"aa"}}]}
			],"continuationToken":"tok2"}`)
			return
		}
		require.Equal(t, "tok2", r.URL.Query().Get("continuationToken"))
		_, _ = io.WriteString(w, `{"items":[
			{"name":"b.txt","version":null,"group":"/","format":"raw","assets":[
				{"path":"b.txt","downloadUrl":"http://n/repository/raw-hosted/b.txt","fileSize":4}]}
		],"continuationToken":null}`)
	}))
	defer srv.Close()

	c := newClient(t, srv)
	first, tok, err := c.ListComponents(context.Background(), "raw-hosted", "")
	require.NoError(t, err)
	require.Len(t, first, 1)
	assert.Equal(t, "a.txt", first[0].Name)
	require.Len(t, first[0].Assets, 1)
	assert.Equal(t, "a.txt", first[0].Assets[0].Path)
	assert.Equal(t, "text/plain", first[0].Assets[0].ContentType)
	assert.Equal(t, int64(3), first[0].Assets[0].SizeBytes)
	assert.Equal(t, "aa", first[0].Assets[0].SHA256)
	require.Equal(t, "tok2", tok)

	second, tok, err := c.ListComponents(context.Background(), "raw-hosted", tok)
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, "b.txt", second[0].Name)
	assert.Empty(t, tok)
}

func TestDownloadAsset_StreamsBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "admin", user)
		_, _ = io.WriteString(w, "hello")
	}))
	defer srv.Close()

	rc, err := newClient(t, srv).DownloadAsset(context.Background(), srv.URL+"/repository/raw-hosted/a.txt")
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(body))
}

func TestDownloadAsset_ErrorsOnNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := newClient(t, srv).DownloadAsset(context.Background(), srv.URL+"/missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestListUsers_ParsesSecurityUsers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/service/rest/v1/security/users", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{"userId":"jdoe","firstName":"Jane","lastName":"Doe","emailAddress":"j@example.com",
			 "source":"default","status":"active","roles":["nx-admin"]},
			{"userId":"ldapuser","firstName":"L","lastName":"U","emailAddress":"l@example.com",
			 "source":"LDAP","status":"disabled","roles":[]}
		]`)
	}))
	defer srv.Close()

	users, err := newClient(t, srv).ListUsers(context.Background())
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, "jdoe", users[0].UserID)
	assert.Equal(t, "Jane", users[0].FirstName)
	assert.Equal(t, "j@example.com", users[0].Email)
	assert.Equal(t, "default", users[0].Source)
	assert.Equal(t, "active", users[0].Status)
	assert.Equal(t, []string{"nx-admin"}, users[0].Roles)
	assert.Equal(t, "LDAP", users[1].Source)
}

func TestListRoles_ParsesNestedRoles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/service/rest/v1/security/roles", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{"id":"dev","name":"dev","description":"Developers","readOnly":false,
			 "privileges":["nx-repository-view-maven2-*-read"],"roles":["base"]},
			{"id":"base","name":"base","description":"","readOnly":false,"privileges":[],"roles":[]}
		]`)
	}))
	defer srv.Close()

	roles, err := newClient(t, srv).ListRoles(context.Background())
	require.NoError(t, err)
	require.Len(t, roles, 2)
	assert.Equal(t, "dev", roles[0].ID)
	assert.Equal(t, "Developers", roles[0].Description)
	assert.Equal(t, []string{"base"}, roles[0].Roles)
	assert.Equal(t, []string{"nx-repository-view-maven2-*-read"}, roles[0].Privileges)
}

func TestListPrivileges_KeepsTypeSpecificAttributes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/service/rest/v1/security/privileges", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{"type":"repository-view","name":"view-maven","description":"d","readOnly":false,
			 "format":"maven2","repository":"maven-central","actions":["READ","BROWSE"]},
			{"type":"application","name":"nx-all","description":"","readOnly":true,
			 "domain":"*","actions":["*"]}
		]`)
	}))
	defer srv.Close()

	privs, err := newClient(t, srv).ListPrivileges(context.Background())
	require.NoError(t, err)
	require.Len(t, privs, 2)
	assert.Equal(t, "view-maven", privs[0].Name)
	assert.Equal(t, "repository-view", privs[0].Type)
	assert.False(t, privs[0].ReadOnly)
	assert.Equal(t, "maven2", privs[0].Attrs["format"])
	assert.Equal(t, "maven-central", privs[0].Attrs["repository"])
	assert.True(t, privs[1].ReadOnly)
	// Identity fields are not duplicated into the attribute bag.
	assert.NotContains(t, privs[0].Attrs, "name")
	assert.NotContains(t, privs[0].Attrs, "type")
}

func TestListRoutingRules_ParsesMatchers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/service/rest/v1/routing-rules", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"name":"block-com","description":"no com","mode":"BLOCK",
			"matchers":["^/com/.*"]}]`)
	}))
	defer srv.Close()

	rules, err := newClient(t, srv).ListRoutingRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "block-com", rules[0].Name)
	assert.Equal(t, "BLOCK", rules[0].Mode)
	assert.Equal(t, []string{"^/com/.*"}, rules[0].Matchers)
}

func TestNew_TrimsTrailingSlashFromBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()

	c := nexusclient.New(srv.URL+"/", "admin", "s3cret", time.Second).WithHTTPClient(srv.Client())
	_, err := c.ListRepositories(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "/service/rest/v1/repositories", gotPath)
}

// ── OCI registry surface ────────────────────────────────────────────────────

func TestRepositoryURL_ComposesTheContentPath(t *testing.T) {
	c := nexusclient.New("https://nexus.example.com/", "admin", "pw", time.Second)
	assert.Equal(t,
		"https://nexus.example.com/repository/docker-hosted/v2/demo/alpine/blobs/sha256:abc",
		c.RepositoryURL("docker-hosted", "/v2/demo/alpine/blobs/sha256:abc"))
	// A path without its leading slash composes to the same URL.
	assert.Equal(t,
		"https://nexus.example.com/repository/docker-hosted/v2/demo/alpine/manifests/3.20",
		c.RepositoryURL("docker-hosted", "v2/demo/alpine/manifests/3.20"))
}

func TestDownloadManifest_AsksForEveryManifestMediaType(t *testing.T) {
	var gotAccept, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = io.WriteString(w, `{"schemaVersion":2}`)
	}))
	defer srv.Close()

	body, contentType, err := newClient(t, srv).DownloadManifest(
		context.Background(), "docker-hosted", "demo/alpine", "3.20")
	require.NoError(t, err)
	assert.Equal(t, `{"schemaVersion":2}`, string(body))
	assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", contentType)
	assert.Equal(t, "/repository/docker-hosted/v2/demo/alpine/manifests/3.20", gotPath)

	// Without these, a registry is free to hand back a different variant than
	// the one whose digest the client is about to record.
	for _, want := range []string{
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.oci.image.index.v1+json",
	} {
		assert.Contains(t, gotAccept, want)
	}
}

func TestDownloadManifest_SurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, _, err := newClient(t, srv).DownloadManifest(context.Background(), "r", "img", "tag")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}
