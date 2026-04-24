package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/api"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { gin.SetMode(gin.TestMode) }

func buildAuditRouter(auditRepo *testutil.AuditRepo) *gin.Engine {
	r := gin.New()
	r.Use(api.AuditMiddleware(auditRepo))

	r.POST("/service/rest/v1/repositories", func(c *gin.Context) {
		c.JSON(http.StatusCreated, gin.H{"ok": true})
	})
	r.DELETE("/service/rest/v1/repositories/myrepo", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	r.GET("/service/rest/v1/repositories", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"items": []any{}})
	})
	r.PUT("/service/rest/v1/security/users/alice", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	r.POST("/service/rest/v1/repositories/unknown", func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})
	return r
}

// waitForAudit pauses briefly so the goroutine in AuditMiddleware can write.
func waitForAudit() { time.Sleep(20 * time.Millisecond) }

func TestAuditMiddleware_POST_Creates_Event(t *testing.T) {
	repo := testutil.NewAuditRepo()
	r := buildAuditRouter(repo)

	req := httptest.NewRequest(http.MethodPost, "/service/rest/v1/repositories", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	waitForAudit()

	require.Len(t, repo.Events, 1)
	e := repo.Events[0]
	assert.Equal(t, "CREATE", e.Action)
	assert.Equal(t, "REPOSITORY", e.Domain)
	assert.Equal(t, "success", e.Result)
}

func TestAuditMiddleware_DELETE_Creates_Event(t *testing.T) {
	repo := testutil.NewAuditRepo()
	r := buildAuditRouter(repo)

	req := httptest.NewRequest(http.MethodDelete, "/service/rest/v1/repositories/myrepo", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	waitForAudit()

	require.Len(t, repo.Events, 1)
	assert.Equal(t, "DELETE", repo.Events[0].Action)
}

func TestAuditMiddleware_GET_NotAudited(t *testing.T) {
	repo := testutil.NewAuditRepo()
	r := buildAuditRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/service/rest/v1/repositories", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	waitForAudit()

	assert.Empty(t, repo.Events, "GET requests should not be audited")
}

func TestAuditMiddleware_SecurityDomain(t *testing.T) {
	repo := testutil.NewAuditRepo()
	r := buildAuditRouter(repo)

	req := httptest.NewRequest(http.MethodPut, "/service/rest/v1/security/users/alice", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	waitForAudit()

	require.Len(t, repo.Events, 1)
	e := repo.Events[0]
	assert.Equal(t, "SECURITY", e.Domain)
	assert.Equal(t, "UPDATE", e.Action)
}

func TestAuditMiddleware_FailedRequest_ResultDenied(t *testing.T) {
	repo := testutil.NewAuditRepo()
	r := buildAuditRouter(repo)

	req := httptest.NewRequest(http.MethodPost, "/service/rest/v1/repositories/unknown", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	waitForAudit()

	require.Len(t, repo.Events, 1)
	assert.Equal(t, "denied", repo.Events[0].Result)
}

func TestAuditMiddleware_Username_FromContext(t *testing.T) {
	repo := testutil.NewAuditRepo()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("username", "bob")
		c.Set("userID", "uid-bob")
		c.Next()
	})
	r.Use(api.AuditMiddleware(repo))
	r.DELETE("/service/rest/v1/repositories/x", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodDelete, "/service/rest/v1/repositories/x", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	waitForAudit()

	require.Len(t, repo.Events, 1)
	assert.Equal(t, "bob", repo.Events[0].Username)
}

func TestAuditMiddleware_Repository_CapturesPath(t *testing.T) {
	repo := testutil.NewAuditRepo()
	r := gin.New()
	r.Use(api.AuditMiddleware(repo))
	r.PUT("/repository/:repoName/*path", func(c *gin.Context) {
		c.Status(http.StatusCreated)
	})

	req := httptest.NewRequest(http.MethodPut, "/repository/maven-hosted/com/example/foo/1.0/foo-1.0.jar", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	waitForAudit()

	require.Len(t, repo.Events, 1)
	e := repo.Events[0]
	assert.Equal(t, "REPOSITORY", e.Domain)
	assert.Equal(t, "ARTIFACT", e.EntityType)
	assert.Equal(t, "maven-hosted", e.EntityName)
	require.NotNil(t, e.Context)
	assert.Equal(t, "com/example/foo/1.0/foo-1.0.jar", e.Context["path"])
}

func TestAuditMiddleware_DockerV2_CapturesManifestRef(t *testing.T) {
	repo := testutil.NewAuditRepo()
	r := gin.New()
	r.Use(api.AuditMiddleware(repo))
	r.PUT("/v2/:repoName/manifests/:ref", func(c *gin.Context) {
		c.Status(http.StatusCreated)
	})

	req := httptest.NewRequest(http.MethodPut, "/v2/myrepo/manifests/v1", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	waitForAudit()

	require.Len(t, repo.Events, 1)
	assert.Equal(t, "manifests/v1", repo.Events[0].Context["path"])
}

func TestAuditMiddleware_Webhooks_PrefixIsAudited(t *testing.T) {
	repo := testutil.NewAuditRepo()
	r := gin.New()
	r.Use(api.AuditMiddleware(repo))
	r.POST("/api/v1/webhooks", func(c *gin.Context) {
		c.Status(http.StatusCreated)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	waitForAudit()

	require.Len(t, repo.Events, 1)
	assert.Equal(t, "SYSTEM", repo.Events[0].Domain)
	assert.Equal(t, "WEBHOOK", repo.Events[0].EntityType)
}

func TestAuditMiddleware_Roles_PrefixIsAudited(t *testing.T) {
	repo := testutil.NewAuditRepo()
	r := gin.New()
	r.Use(api.AuditMiddleware(repo))
	r.POST("/service/rest/v1/security/roles", func(c *gin.Context) {
		c.Status(http.StatusCreated)
	})

	req := httptest.NewRequest(http.MethodPost, "/service/rest/v1/security/roles", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	waitForAudit()

	require.Len(t, repo.Events, 1)
	assert.Equal(t, "SECURITY", repo.Events[0].Domain)
	assert.Equal(t, "ROLE", repo.Events[0].EntityType)
}

func TestAuditMiddleware_OIDCCallback_WritesLoginEvent(t *testing.T) {
	repo := testutil.NewAuditRepo()
	r := gin.New()
	// Pre-middleware: simulate OIDCHandler.Callback setting audit hooks.
	r.Use(func(c *gin.Context) {
		c.Set("username", "alice")
		c.Set("userID", "u1")
		c.Set("audit_source", "oidc")
		c.Next()
	})
	r.Use(api.AuditMiddleware(repo))
	r.GET("/api/v1/auth/oidc/callback", func(c *gin.Context) { c.Status(http.StatusFound) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?code=x&state=s", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	waitForAudit()

	require.Len(t, repo.Events, 1)
	ev := repo.Events[0]
	assert.Equal(t, "LOGIN", ev.Action)
	assert.Equal(t, "alice", ev.Username)
	assert.Equal(t, "SECURITY", ev.Domain)
	assert.Equal(t, "oidc", ev.Context["source"])
}

func TestAuditMiddleware_NonOIDC_GET_NotAudited(t *testing.T) {
	repo := testutil.NewAuditRepo()
	r := buildAuditRouter(repo)
	// Plain GET on repositories list — must NOT create audit event.
	req := httptest.NewRequest(http.MethodGet, "/service/rest/v1/repositories", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
	waitForAudit()
	assert.Empty(t, repo.Events, "GET requests outside OIDC callback must not be audited")
}

func TestAuditMiddleware_RemoteIP_NonEmpty(t *testing.T) {
	repo := testutil.NewAuditRepo()
	r := gin.New()
	r.Use(api.AuditMiddleware(repo))
	r.POST("/service/rest/v1/repositories", func(c *gin.Context) {
		c.Status(http.StatusCreated)
	})

	req := httptest.NewRequest(http.MethodPost, "/service/rest/v1/repositories", nil)
	req.RemoteAddr = "10.1.2.3:12345"
	r.ServeHTTP(httptest.NewRecorder(), req)
	waitForAudit()

	require.Len(t, repo.Events, 1)
	assert.NotEmpty(t, repo.Events[0].RemoteIP, "RemoteIP must be captured from c.ClientIP()")
}
