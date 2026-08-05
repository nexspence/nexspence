package handlers_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/auth/loginguard"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func testGuard() *loginguard.Guard {
	return loginguard.New(nil, loginguard.Policy{
		Threshold: 2, BaseDelay: time.Second, MaxDelay: time.Minute,
	})
}

func postLogin(r *gin.Engine, username, password string) *httptest.ResponseRecorder {
	body := `{"username":"` + username + `","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login", stringReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func buildLoginRouter(svc *service.UserService, guard *loginguard.Guard) *gin.Engine {
	r := gin.New()
	authH := handlers.NewAuthHandler(svc, zap.NewNop().Sugar()).WithLoginGuard(guard)
	r.POST("/api/v1/login", authH.Login)
	return r
}

// Without a throttle, /api/v1/login is an unmetered credential-guessing surface
// that also burns a bcrypt (cost 12) per attempt.
func TestLogin_RepeatedFailures_Get429WithRetryAfter(t *testing.T) {
	svc := newUserSvc(activeUser("eve", "mypass"))
	r := buildLoginRouter(svc, testGuard())

	// Threshold failures are free, so an honest typo is never throttled; the
	// attempt after that is the first one refused.
	for range 3 {
		require.Equal(t, http.StatusUnauthorized, postLogin(r, "eve", "wrong").Code)
	}

	w := postLogin(r, "eve", "wrong")
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
}

// The response must not distinguish a throttled real account from a throttled
// name that does not exist, or the throttle becomes a user-enumeration oracle.
func TestLogin_ThrottleDoesNotRevealAccountExistence(t *testing.T) {
	svc := newUserSvc(activeUser("eve", "mypass"))
	r := buildLoginRouter(svc, testGuard())

	for range 3 {
		postLogin(r, "eve", "wrong")
		postLogin(r, "ghost", "wrong")
	}

	real := postLogin(r, "eve", "wrong")
	missing := postLogin(r, "ghost", "wrong")
	assert.Equal(t, http.StatusTooManyRequests, real.Code)
	assert.Equal(t, http.StatusTooManyRequests, missing.Code)
	assert.Equal(t, real.Body.String(), missing.Body.String())
}

func TestLogin_SuccessClearsTheCount(t *testing.T) {
	svc := newUserSvc(activeUser("eve", "mypass"))
	guard := testGuard()
	r := buildLoginRouter(svc, guard)

	postLogin(r, "eve", "wrong")
	postLogin(r, "eve", "wrong")
	require.Equal(t, http.StatusOK, postLogin(r, "eve", "mypass").Code)

	// The successful login reset the budget, so a fresh mistake is tolerated.
	assert.Equal(t, http.StatusUnauthorized, postLogin(r, "eve", "wrong").Code)
}

func TestLogin_NoGuard_StillWorks(t *testing.T) {
	svc := newUserSvc(activeUser("eve", "mypass"))
	r := buildLoginRouter(svc, nil)

	for range 5 {
		require.Equal(t, http.StatusUnauthorized, postLogin(r, "eve", "wrong").Code)
	}
}

// ── OptionalAuth: the silent path ─────────────────────────────

func basicReq(username, password string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/open", nil)
	req.Header.Set("Authorization", "Basic "+
		base64.StdEncoding.EncodeToString([]byte(username+":"+password)))
	return req
}

func buildOptionalAuthRouterGuarded(svc *service.UserService, guard *loginguard.Guard, log *zap.SugaredLogger) *gin.Engine {
	r := gin.New()
	r.Use(handlers.OptionalAuth(svc, nil, guard, log))
	r.GET("/open", func(c *gin.Context) {
		username, _ := c.Get("username")
		c.JSON(http.StatusOK, gin.H{"user": username})
	})
	return r
}

// A failed password on /repository/... used to fall through to anonymous with
// no log line at all, so guessing there was invisible to the audit trail while
// still costing a bcrypt per try.
func TestOptionalAuth_BadCredentials_AreLogged(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	svc := newUserSvc(activeUser("dave", "pw"))
	r := buildOptionalAuthRouterGuarded(svc, testGuard(), zap.New(core).Sugar())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, basicReq("dave", "wrong"))

	assert.Equal(t, http.StatusOK, w.Code, "OptionalAuth still falls through to anonymous")
	require.NotEmpty(t, logs.FilterMessageSnippet("authentication failed").All(),
		"a failed credential must leave a trace")
}

func TestOptionalAuth_BadCredentials_CountTowardsTheSameBudget(t *testing.T) {
	guard := testGuard()
	svc := newUserSvc(activeUser("dave", "pw"))
	r := buildOptionalAuthRouterGuarded(svc, guard, zap.NewNop().Sugar())

	for range 3 {
		r.ServeHTTP(httptest.NewRecorder(), basicReq("dave", "wrong"))
	}

	assert.Positive(t, guard.RetryAfter(context.Background(), "dave"),
		"guessing via Basic auth must spend the same budget as /api/v1/login")
}

// Once an account is throttled the middleware must stop hashing passwords for
// it — that bcrypt is the whole cost of the attack.
func TestOptionalAuth_ThrottledAccount_SkipsPasswordCheck(t *testing.T) {
	guard := testGuard()
	svc := newUserSvc(activeUser("dave", "pw"))
	r := buildOptionalAuthRouterGuarded(svc, guard, zap.NewNop().Sugar())

	for range 3 {
		r.ServeHTTP(httptest.NewRecorder(), basicReq("dave", "wrong"))
	}
	require.Positive(t, guard.RetryAfter(context.Background(), "dave"))

	// Correct credentials, but the account is in its penalty window: the request
	// proceeds as anonymous rather than authenticating.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, basicReq("dave", "pw"))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "dave")
}

// ── the same budget applies wherever a password is checked ────

// A throttle that only covers /api/v1/login is evaded by moving the guessing to
// another endpoint that takes the same credentials.
func TestAuthMiddleware_RepeatedBasicFailures_Get429(t *testing.T) {
	guard := testGuard()
	svc := newUserSvc(activeUser("dave", "pw"))

	r := gin.New()
	r.Use(handlers.AuthMiddleware(svc, nil, guard, zap.NewNop().Sugar()))
	r.GET("/open", func(c *gin.Context) { c.Status(http.StatusOK) })

	for range 3 {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, basicReq("dave", "wrong"))
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, basicReq("dave", "wrong"))
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
}

func TestDockerV2Auth_RepeatedBasicFailures_Get429(t *testing.T) {
	guard := testGuard()
	svc := newUserSvc(activeUser("dave", "pw"))

	r := gin.New()
	h := handlers.DockerV2Auth(svc, nil, testutil.NewRepoRepo(), nil, true, guard, zap.NewNop().Sugar())
	r.GET("/v2/", h)

	dockerReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
		req.Header.Set("Authorization", "Basic "+
			base64.StdEncoding.EncodeToString([]byte("dave:wrong")))
		return req
	}

	for range 3 {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, dockerReq())
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, dockerReq())
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestOptionalAuth_GoodCredentials_ClearTheCount(t *testing.T) {
	guard := testGuard()
	svc := newUserSvc(activeUser("dave", "pw"))
	r := buildOptionalAuthRouterGuarded(svc, guard, zap.NewNop().Sugar())

	r.ServeHTTP(httptest.NewRecorder(), basicReq("dave", "wrong"))
	r.ServeHTTP(httptest.NewRecorder(), basicReq("dave", "pw"))

	assert.Zero(t, guard.RetryAfter(context.Background(), "dave"))
}
