package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRateLimitMiddleware_BlocksOverBurst(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimitMiddleware(nil, 0.0001, 2))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	codes := []int{}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		r.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	assert.Equal(t, http.StatusOK, codes[0])
	assert.Equal(t, http.StatusOK, codes[1])
	assert.Equal(t, http.StatusTooManyRequests, codes[2])
}

func TestRateLimitMiddleware_RetryAfterIsNumeric(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimitMiddleware(nil, 0.0001, 1))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	var throttled *httptest.ResponseRecorder
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "10.0.0.2:1234"
		r.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			throttled = rec
		}
	}
	if assert.NotNil(t, throttled) {
		assert.Regexp(t, `^\d+$`, throttled.Header().Get("Retry-After"))
	}
}

// The bucket belongs to the user the authentication middleware put in the
// context, not to the address the request came from: two users behind one
// address must not exhaust each other's bucket. The router wiring that makes
// the context carry a user ID is covered by the integration test in
// internal/integration/rate_limit_test.go.
func TestRateLimitMiddleware_KeysByUserWhenAuthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Stands in for the authentication middleware: it puts the identity in
	// the context before the limiter runs.
	r.Use(func(c *gin.Context) {
		if u := c.GetHeader("X-Test-User"); u != "" {
			c.Set("userID", u)
		}
		c.Next()
	})
	r.Use(RateLimitMiddleware(nil, 0.0001, 1))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	call := func(userID, addr string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = addr
		if userID != "" {
			req.Header.Set("X-Test-User", userID)
		}
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, http.StatusOK, call("user-a", "10.0.0.3:1234"))
	assert.Equal(t, http.StatusTooManyRequests, call("user-a", "10.0.0.3:1234"),
		"the same user must be limited")
	assert.Equal(t, http.StatusOK, call("user-b", "10.0.0.3:1234"),
		"another user behind the same address must have its own bucket")
	assert.Equal(t, http.StatusTooManyRequests, call("user-a", "10.0.0.9:1234"),
		"the same user from another address shares the bucket")
}
