package helm_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// #617: index.yaml lists every chart version, past one search page.
func TestHelm_IndexListsEveryVersion(t *testing.T) {
	r := setup(testutil.SimpleRepo("charts-many", "helm"))
	const n = 501
	for i := 0; i < n; i++ {
		require.Equal(t, http.StatusCreated, uploadMultipart(r, "charts-many", fmt.Sprintf("app-1.0.%d.tgz", i), "chart"))
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/charts-many/index.yaml", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, n, strings.Count(w.Body.String(), "version: 1.0."), "every version is listed")
}
