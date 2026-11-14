package group_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A member that fails after its headers were written leaves a body shorter
// than the length it announced. The member runs against a recorder, not a
// network stream, so nothing closes a connection — the short body would be
// relayed as a complete one, and the client would wait for bytes that never
// arrive.

// truncatingHandler announces a length it does not deliver.
type truncatingHandler struct{}

func (truncatingHandler) Name() string { return "raw" }

func (truncatingHandler) ServeHTTP(c *gin.Context) {
	c.Header("Content-Length", "1024")
	c.Status(http.StatusOK)
	_, _ = c.Writer.Write([]byte("short"))
}

func buildTruncatingEngine(repos ...*domain.Repository) *gin.Engine {
	repoRepo := testutil.NewRepoRepo(repos...)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	groupH := group.New(d, map[string]formats.FormatHandler{"raw": truncatingHandler{}})
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { groupH.ServeHTTP(c) })
	return r
}

func TestGroup_TruncatedMemberResponseIsNotRelayed(t *testing.T) {
	m1 := testutil.SimpleRepo("trunc1", "raw")
	grp := makeGroupRepo("truncgrp", "trunc1")
	r := buildTruncatingEngine(m1, grp)

	req := httptest.NewRequest(http.MethodGet, "/repository/truncgrp/artifact.bin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code, "a short member response is a failure, not an artifact")
	assert.Empty(t, w.Header().Get("Content-Length"), "the length the member announced must not be relayed")
	assert.NotContains(t, w.Body.String(), "short")
}

func TestGroup_CompleteMemberResponseIsRelayed(t *testing.T) {
	// The guard must not fire on a member that delivers what it announced.
	m1 := testutil.SimpleRepo("whole1", "raw")
	grp := makeGroupRepo("wholegrp", "whole1")
	r := buildEngine(m1, grp)

	require := assert.New(t)
	require.Equal(http.StatusCreated, put(r, "whole1", "/artifact.bin", "complete"))

	req := httptest.NewRequest(http.MethodGet, "/repository/wholegrp/artifact.bin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(http.StatusOK, w.Code)
	require.Equal("complete", w.Body.String())
}
