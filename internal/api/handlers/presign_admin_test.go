package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
)

// The presign handlers take an arbitrary blob key and hand back a URL for it,
// bypassing repository RBAC entirely. They are not mounted today; the gate goes
// in now so that mounting them later cannot silently open every blob in the
// store to any authenticated user.
func presignRouter() *gin.Engine {
	h := handlers.NewBlobStoreHandler(nil)
	r := gin.New()
	// Deliberately mounted without AdminRequired, the mistake being guarded against.
	r.GET("/presign", withRoles(nil), h.PresignGet)
	r.POST("/presign", withRoles(nil), h.PresignPut)
	r.POST("/lifecycle", withRoles(nil), h.ConfigureLifecycle)
	r.GET("/presign-admin", withRoles([]string{"nx-admin"}), h.PresignGet)
	return r
}

func withRoles(roles []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if roles != nil {
			c.Set("roles", roles)
		}
		c.Next()
	}
}

func TestPresign_NonAdmin_IsForbidden(t *testing.T) {
	r := presignRouter()

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/presign?key=any/blob"},
		{http.MethodPost, "/presign"},
		{http.MethodPost, "/lifecycle"},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"key":"any/blob"}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code, "%s %s", tc.method, tc.path)
	}
}

func TestPresign_Admin_GetsPastTheGate(t *testing.T) {
	r := presignRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/presign-admin?key=any/blob", nil))

	// No S3 store is configured here, so the handler refuses for that reason —
	// what matters is that it is no longer the admin gate turning it away.
	assert.NotEqual(t, http.StatusForbidden, w.Code)
}
