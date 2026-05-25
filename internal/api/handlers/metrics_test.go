package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/metrics"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestPrometheusHandler_NoToken_Returns401(t *testing.T) {
	r := gin.New()
	authSvc := auth.NewService(testSecret, 24, bcryptCostTest)
	uSvc := service.NewUserService(testutil.NewUserRepo(), testutil.NewRoleRepo(), authSvc, zap.NewNop().Sugar())
	tSvc := service.NewTokenService(testutil.NewUserTokenRepo(), testutil.NewUserRepo())
	r.GET("/metrics", handlers.AuthMiddleware(uSvc, tSvc),
		gin.WrapH(promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{})))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPrometheusHandler_ValidToken_Returns200(t *testing.T) {
	user := activeUser("admin", "pass")
	authSvc := auth.NewService(testSecret, 24, bcryptCostTest)
	uSvc := service.NewUserService(testutil.NewUserRepo(user), testutil.NewRoleRepo(), authSvc, zap.NewNop().Sugar())
	tSvc := service.NewTokenService(testutil.NewUserTokenRepo(), testutil.NewUserRepo(user))
	r := gin.New()
	r.GET("/metrics", handlers.AuthMiddleware(uSvc, tSvc),
		gin.WrapH(promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{})))

	tok := bearerToken(newUserSvc(user), "admin")
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")
}

func TestHistoryHandler_EmptyBuffer_ReturnsEmptyArray(t *testing.T) {
	metrics.History = &metrics.RingBuffer{}
	r := gin.New()
	r.GET("/api/v1/metrics/history", handlers.HistoryHandler())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/metrics/history", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "[]", w.Body.String())
}

func TestHistoryHandler_WithData_ReturnsPoints(t *testing.T) {
	metrics.History = &metrics.RingBuffer{}
	metrics.History.Add(metrics.DataPoint{Timestamp: 1000, RequestsTotal: 42})
	r := gin.New()
	r.GET("/api/v1/metrics/history", handlers.HistoryHandler())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/metrics/history", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"timestamp":1000`)
}

func TestRecordRequest_DoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		metrics.RecordRequest("DELETE", "4xx", 10*time.Millisecond)
	})
}
