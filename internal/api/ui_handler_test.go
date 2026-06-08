package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func newUITestFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":      {Data: []byte("<!doctype html><title>SPA</title>")},
		"assets/app.js":   {Data: []byte("console.log('hi')")},
		"assets/logo.svg": {Data: []byte("<svg></svg>")},
	}
}

func doUIGet(t *testing.T, h gin.HandlerFunc, target string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	h(c)
	return w
}

func TestUIHandler_ServesIndexAtRoot(t *testing.T) {
	w := doUIGet(t, uiHandler(newUITestFS()), "/")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "SPA") {
		t.Fatalf("body = %q, want index.html content", w.Body.String())
	}
}

func TestUIHandler_ServesRealAsset(t *testing.T) {
	w := doUIGet(t, uiHandler(newUITestFS()), "/assets/app.js")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "console.log") {
		t.Fatalf("body = %q, want app.js content", w.Body.String())
	}
}

func TestUIHandler_SPAFallbackForUnknownRoute(t *testing.T) {
	w := doUIGet(t, uiHandler(newUITestFS()), "/repos/some/deep/spa/route")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "SPA") {
		t.Fatalf("unknown route should fall back to index.html, got %q", w.Body.String())
	}
}

func TestUIHandler_NoDirectoryListing(t *testing.T) {
	// "assets" is a directory with no index.html — must fall back to index.html,
	// never expose a listing of app.js/logo.svg.
	w := doUIGet(t, uiHandler(newUITestFS()), "/assets")
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "SPA") {
		t.Fatalf("directory request should serve index fallback, got status=%d body=%q", w.Code, body)
	}
	if strings.Contains(body, "app.js") || strings.Contains(body, "logo.svg") {
		t.Fatalf("directory listing leaked: %q", body)
	}
}
