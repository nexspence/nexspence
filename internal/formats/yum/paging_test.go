package yum_test

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// #617: primary.xml lists every package, past one search page (500
// components) and past 1000 assets.
func TestYum_PrimaryListsEveryPackage(t *testing.T) {
	r := setup(testutil.SimpleRepo("rpms-many", "yum"))
	const n = 501
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodPut,
			fmt.Sprintf("/repository/rpms-many/pool/pkg%03d-1.0.0-1.x86_64.rpm", i), strings.NewReader("rpm"))
		req.ContentLength = 3
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusCreated, w.Code)
	}
	zr, err := gzip.NewReader(bytes.NewReader(fetch(t, r, "/repository/rpms-many/repodata/primary.xml.gz").Body.Bytes()))
	require.NoError(t, err)
	primary, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Equal(t, n, strings.Count(string(primary), "<package "), "every package is listed")
	assert.Contains(t, string(primary), "pkg500-1.0.0-1.x86_64.rpm")
}
