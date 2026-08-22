package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestCORS_AllowlistReflectsKnownOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(corsMiddleware([]string{"https://ui.example.com"}))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://ui.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://ui.example.com" {
		t.Fatalf("ACAO = %q, want %q", got, "https://ui.example.com")
	}
}

func TestCORS_PreflightAllowedOriginCarriesACAO(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(corsMiddleware([]string{"https://ui.example.com"}))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://ui.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://ui.example.com" {
		t.Fatalf("ACAO = %q, want %q", got, "https://ui.example.com")
	}
}

func TestCORS_UnknownOriginNotReflected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(corsMiddleware([]string{"https://ui.example.com"}))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO = %q, want empty", got)
	}
}

// An empty allowlist used to mean "wildcard". That let any website read
// responses from an internal instance: the API authenticates by Authorization
// header and serves anonymous repositories, so a page on evil.com could fetch
// an internal artifact from inside the corporate network and read the body.
// Empty now means no CORS header at all.
func TestCORS_EmptyAllowlistSendsNoACAO(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(corsMiddleware(nil))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO = %q, want empty", got)
	}
}

// The wildcard stays available, but has to be asked for.
func TestCORS_ExplicitWildcardStillWorks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(corsMiddleware([]string{"*"}))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://anything.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("ACAO = %q, want %q", got, "*")
	}
}

// A preflight against an instance with no allowlist must not be told the
// request is permitted.
func TestCORS_PreflightWithoutAllowlistSendsNoACAO(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(corsMiddleware(nil))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO = %q, want empty", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(securityHeaders(DefaultCSP))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	cases := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for h, want := range cases {
		if got := w.Header().Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
}

// The per-request summary line and the request's trace must share ids (#321).
// requestLogger stays FIRST in the chain (it times everything and still sees
// aborted CORS preflights), so it cannot read the span from the request
// context — otelgin restores that to the pre-span value on unwind. The stash
// middleware inside otelgin's scope is what carries the ids across.
func TestRequestLogger_CarriesTraceIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prev := otel.GetTracerProvider()
	defer otel.SetTracerProvider(prev)
	exp := tracetest.NewInMemoryExporter()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp), sdktrace.WithSampler(sdktrace.AlwaysSample())))

	core, logs := observer.New(zap.InfoLevel)
	log := zap.New(core).Sugar()

	r := gin.New()
	r.Use(requestLogger(log))
	r.Use(otelgin.Middleware("nexspence-test"))
	r.Use(traceLogStash())
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	require.Equal(t, 1, logs.Len())
	fields := logs.All()[0].ContextMap()
	spans := exp.GetSpans()
	require.NotEmpty(t, spans)
	assert.Equal(t, spans[0].SpanContext.TraceID().String(), fields["trace_id"],
		"the log line and the exported span must agree on the trace id")
	assert.Equal(t, spans[0].SpanContext.SpanID().String(), fields["span_id"])
}

// Without tracing middleware the summary line simply has no trace fields —
// and, unchanged from before, requestLogger first in the chain still logs
// requests that later middlewares abort.
func TestRequestLogger_NoTracing_NoTraceFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zap.InfoLevel)
	log := zap.New(core).Sugar()

	r := gin.New()
	r.Use(requestLogger(log))
	r.Use(corsMiddleware([]string{"https://app.example.com"}))
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	// An OPTIONS preflight aborts inside corsMiddleware; the summary line must
	// still be written (the reason requestLogger was not moved after otelgin).
	req := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	req.Header.Set("Origin", "https://app.example.com")
	r.ServeHTTP(httptest.NewRecorder(), req)

	require.Equal(t, 1, logs.Len())
	entry := logs.All()[0].ContextMap()
	assert.NotContains(t, entry, "trace_id")
	assert.Equal(t, int64(http.StatusNoContent), entry["status"])
}
