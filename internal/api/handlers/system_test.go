package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ── fake authenticators ───────────────────────────────────────
// SystemHandler only calls TestConnection on LDAP/OIDC and MetadataXML on SAML;
// the rest of each interface is stubbed to satisfy the type.

type fakeLDAP struct{ err error }

func (f fakeLDAP) Authenticate(_ context.Context, _, _ string) (*auth.LDAPUser, error) {
	return nil, nil
}
func (f fakeLDAP) TestConnection(_ context.Context) error { return f.err }

type fakeOIDC struct{ err error }

func (f fakeOIDC) AuthCodeURL(_, _, _ string) string { return "" }
func (f fakeOIDC) ExchangeAndVerify(_ context.Context, _, _, _ string) (*auth.OIDCClaims, string, error) {
	return nil, "", nil
}
func (f fakeOIDC) TestConnection(_ context.Context) error { return f.err }
func (f fakeOIDC) EndSessionEndpoint() string             { return "" }

type fakeSAML struct {
	xml []byte
	err error
}

func (f fakeSAML) MetadataXML() ([]byte, error)             { return f.xml, f.err }
func (f fakeSAML) AuthnRequestURL(_ string) (string, error) { return "", nil }
func (f fakeSAML) ParseResponse(_ *http.Request) (*auth.SAMLClaims, error) {
	return nil, nil
}
func (f fakeSAML) SignRelayState(returnTo string) string      { return returnTo }
func (f fakeSAML) VerifyRelayState(rs string) (string, error) { return rs, nil }

// newUnreachablePool returns a lazily-initialized pgxpool pointing at an address
// that is never reachable. pgxpool.New does not dial until first use, so this
// never blocks at construction; the Ping in checkPostgres returns an error
// quickly (within the handler's 5s timeout), exercising the "error" branch.
func newUnreachablePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(),
		"postgres://nope:nope@127.0.0.1:1/nope?connect_timeout=1")
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// mountSystem builds a SystemHandler over the given config + optional
// authenticators and mounts GET /api/v1/system/services. Storage local base
// path defaults to a real temp dir so checkStorage reports "ok".
func mountSystem(t *testing.T, cfg *config.Config, ldap auth.LDAPAuthenticator, oidc auth.OIDCAuthenticator, saml auth.SAMLAuthenticator, blobs *testutil.BlobStoreRepo) *gin.Engine {
	t.Helper()
	if cfg.Storage.DefaultType == "" {
		cfg.Storage.DefaultType = "local"
	}
	if cfg.Storage.DefaultType == "local" && cfg.Storage.Local.BasePath == "" {
		cfg.Storage.Local.BasePath = t.TempDir()
	}
	h := handlers.NewSystemHandler(cfg, newUnreachablePool(t), ldap, oidc)
	if saml != nil {
		h = h.WithSAML(saml)
	}
	if blobs != nil {
		h = h.WithBlobStores(blobs)
	}
	r := gin.New()
	r.GET("/api/v1/system/services", h.Services)
	return r
}

func parseServices(t *testing.T, body []byte) []handlers.ServiceStatus {
	t.Helper()
	var out []handlers.ServiceStatus
	require.NoError(t, json.Unmarshal(body, &out))
	return out
}

func findService(svcs []handlers.ServiceStatus, name string) (handlers.ServiceStatus, bool) {
	for _, s := range svcs {
		if s.Name == name {
			return s, true
		}
	}
	return handlers.ServiceStatus{}, false
}

// TestSystem_Services_LocalStorage_AllSSODisabled covers the baseline path:
// postgres (error, unreachable), local storage (ok), all SSO providers absent
// and disabled, docker connector disabled, redis disabled.
func TestSystem_Services_LocalStorage_AllSSODisabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Database.DSN = "postgres://u@127.0.0.1:1/db"
	r := mountSystem(t, cfg, nil, nil, nil, nil)

	rec := do(t, r, http.MethodGet, "/api/v1/system/services", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	svcs := parseServices(t, rec.Body.Bytes())

	pg, ok := findService(svcs, "PostgreSQL")
	require.True(t, ok)
	assert.Equal(t, "error", pg.Status) // unreachable pool

	st, ok := findService(svcs, "Local Storage")
	require.True(t, ok)
	assert.Equal(t, "ok", st.Status)

	dsc, ok := findService(svcs, "Docker Subdomain Connector")
	require.True(t, ok)
	assert.Equal(t, "disabled", dsc.Status)

	redis, ok := findService(svcs, "Redis")
	require.True(t, ok)
	assert.Equal(t, "disabled", redis.Status)
}

// TestSystem_Services_S3Storage covers the checkStorage S3 branch.
func TestSystem_Services_S3Storage(t *testing.T) {
	cfg := &config.Config{}
	cfg.Storage.DefaultType = "s3"
	cfg.Storage.S3.Bucket = "mybucket"
	cfg.Storage.S3.Region = "us-east-1"
	cfg.Storage.S3.Endpoint = "https://s3.example.com"
	r := mountSystem(t, cfg, nil, nil, nil, nil)

	rec := do(t, r, http.MethodGet, "/api/v1/system/services", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	svcs := parseServices(t, rec.Body.Bytes())

	st, ok := findService(svcs, "S3 Storage")
	require.True(t, ok)
	assert.Equal(t, "ok", st.Status)
	assert.Contains(t, st.Detail, "mybucket")
	assert.Contains(t, st.Detail, "s3.example.com")
}

// TestSystem_Services_LocalStorageMissing covers the checkStorage error branch
// when the local base path does not exist.
func TestSystem_Services_LocalStorageMissing(t *testing.T) {
	cfg := &config.Config{}
	cfg.Storage.DefaultType = "local"
	cfg.Storage.Local.BasePath = "/nonexistent/path/that/should/not/exist/nxs-test"
	// Bypass mountSystem's temp-dir defaulting by constructing directly.
	h := handlers.NewSystemHandler(cfg, newUnreachablePool(t), nil, nil)
	r := gin.New()
	r.GET("/api/v1/system/services", h.Services)

	rec := do(t, r, http.MethodGet, "/api/v1/system/services", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	svcs := parseServices(t, rec.Body.Bytes())

	st, ok := findService(svcs, "Local Storage")
	require.True(t, ok)
	assert.Equal(t, "error", st.Status)
}

// TestSystem_Services_SSOWithAuthenticators exercises checkLDAP/checkOIDC/checkSAML
// when the authenticators are present.
func TestSystem_Services_SSOWithAuthenticators(t *testing.T) {
	cfg := &config.Config{}
	cfg.LDAP.Host = "ldap.example.com"
	cfg.LDAP.Port = 389
	cfg.OIDC.DisplayName = "Keycloak"
	cfg.OIDC.Issuer = "https://idp.example.com"
	cfg.SAML.DisplayName = "Okta"

	ldap := fakeLDAP{err: nil}
	oidc := fakeOIDC{err: nil}
	saml := fakeSAML{xml: []byte("<EntityDescriptor/>")}
	r := mountSystem(t, cfg, ldap, oidc, saml, nil)

	rec := do(t, r, http.MethodGet, "/api/v1/system/services", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	svcs := parseServices(t, rec.Body.Bytes())

	l, ok := findService(svcs, "LDAP")
	require.True(t, ok)
	assert.Equal(t, "ok", l.Status)

	o, ok := findService(svcs, "OIDC · Keycloak")
	require.True(t, ok)
	assert.Equal(t, "ok", o.Status)

	s, ok := findService(svcs, "SAML · Okta")
	require.True(t, ok)
	assert.Equal(t, "ok", s.Status)
}

// TestSystem_Services_SSOAuthenticatorErrors covers the error branches of
// checkLDAP/checkOIDC/checkSAML.
func TestSystem_Services_SSOAuthenticatorErrors(t *testing.T) {
	cfg := &config.Config{}

	ldap := fakeLDAP{err: assertErr("ldap down")}
	oidc := fakeOIDC{err: assertErr("oidc down")}
	saml := fakeSAML{err: assertErr("no metadata")}
	r := mountSystem(t, cfg, ldap, oidc, saml, nil)

	rec := do(t, r, http.MethodGet, "/api/v1/system/services", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	svcs := parseServices(t, rec.Body.Bytes())

	l, ok := findService(svcs, "LDAP")
	require.True(t, ok)
	assert.Equal(t, "error", l.Status)

	o, ok := findService(svcs, "OIDC")
	require.True(t, ok)
	assert.Equal(t, "error", o.Status)

	s, ok := findService(svcs, "SAML")
	require.True(t, ok)
	assert.Equal(t, "error", s.Status)
}

// TestSystem_Services_SSODisabledViaConfig covers the disabled() fallback when
// no authenticator is wired but config marks the provider enabled.
func TestSystem_Services_SSODisabledViaConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.LDAP.Enabled = true
	cfg.OIDC.Enabled = true
	cfg.SAML.Enabled = true
	r := mountSystem(t, cfg, nil, nil, nil, nil)

	rec := do(t, r, http.MethodGet, "/api/v1/system/services", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	svcs := parseServices(t, rec.Body.Bytes())

	for _, name := range []string{"LDAP", "OIDC", "SAML"} {
		s, ok := findService(svcs, name)
		require.True(t, ok, "service %s missing", name)
		assert.Equal(t, "disabled", s.Status)
	}
}

// TestSystem_Services_DockerConnectorEnabled covers the enabled-with-base-domain
// and enabled-without-base-domain (warn) branches.
func TestSystem_Services_DockerConnectorEnabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Docker.SubdomainConnector.Enabled = true
	cfg.Docker.SubdomainConnector.BaseDomain = "docker.example.com"
	r := mountSystem(t, cfg, nil, nil, nil, nil)

	rec := do(t, r, http.MethodGet, "/api/v1/system/services", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	svcs := parseServices(t, rec.Body.Bytes())

	dsc, ok := findService(svcs, "Docker Subdomain Connector")
	require.True(t, ok)
	assert.Equal(t, "ok", dsc.Status)
	assert.Contains(t, dsc.Detail, "docker.example.com")
}

func TestSystem_Services_DockerConnectorEnabledNoBaseDomain(t *testing.T) {
	cfg := &config.Config{}
	cfg.Docker.SubdomainConnector.Enabled = true
	r := mountSystem(t, cfg, nil, nil, nil, nil)

	rec := do(t, r, http.MethodGet, "/api/v1/system/services", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	svcs := parseServices(t, rec.Body.Bytes())

	dsc, ok := findService(svcs, "Docker Subdomain Connector")
	require.True(t, ok)
	assert.Equal(t, "warn", dsc.Status)
}

// TestSystem_Services_RedisEnabled covers the redis error branch (unreachable addr).
func TestSystem_Services_RedisEnabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Redis.Enabled = true
	cfg.Redis.Addr = "127.0.0.1:1" // unreachable
	r := mountSystem(t, cfg, nil, nil, nil, nil)

	rec := do(t, r, http.MethodGet, "/api/v1/system/services", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	svcs := parseServices(t, rec.Body.Bytes())

	redis, ok := findService(svcs, "Redis")
	require.True(t, ok)
	assert.Equal(t, "error", redis.Status)
}

// TestSystem_Services_S3BlobStoreProbe covers the per-S3-endpoint check added
// from the blob_stores table. The endpoint is unreachable so the probe returns
// "error" — this exercises checkS3Endpoint and the endpoint-grouping loop.
func TestSystem_Services_S3BlobStoreProbe(t *testing.T) {
	cfg := &config.Config{}
	s3store := &domain.BlobStore{
		ID:   "00000000-0000-0000-0000-0000000000ff",
		Name: "s3-primary",
		Type: "s3",
		Config: map[string]any{
			"endpoint":          "https://127.0.0.1:1",
			"bucket":            "artifacts",
			"region":            "us-east-1",
			"access_key_id":     "x",
			"secret_access_key": "y",
		},
	}
	// Include a local store too — it must be ignored by the s3-only loop.
	localStore := &domain.BlobStore{ID: "1", Name: "local", Type: "local"}
	blobs := testutil.NewBlobStoreRepo(s3store, localStore)
	r := mountSystem(t, cfg, nil, nil, nil, blobs)

	rec := do(t, r, http.MethodGet, "/api/v1/system/services", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	svcs := parseServices(t, rec.Body.Bytes())

	s, ok := findService(svcs, "S3 · https://127.0.0.1:1")
	require.True(t, ok, "expected an S3 endpoint check; got %v", svcs)
	assert.Equal(t, "error", s.Status)
	assert.Contains(t, s.Detail, "s3-primary")
	assert.Contains(t, s.Detail, "artifacts")

	// The local blob store must NOT produce its own S3 endpoint check.
	_, hasLocalS3 := findService(svcs, "S3 · ")
	assert.False(t, hasLocalS3)
}

// assertErr is a tiny error helper to avoid importing errors in many spots.
func assertErr(msg string) error { return &simpleErr{msg} }

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }
