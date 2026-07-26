package group_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/maven"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// End-to-end через реальный maven handler: metadata из двух hosted членов
// мержится, latest пересчитывается, checksum считается от merged-документа.
func TestGroupMerge_MavenEndToEnd(t *testing.T) {
	m1 := testutil.SimpleRepo("mv1", "maven2")
	m2 := testutil.SimpleRepo("mv2", "maven2")
	g := &domain.Repository{
		ID: "repo-mvg", Name: "mvg", Format: "maven2",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []interface{}{"mv1", "mv2"}},
	}

	repoRepo := testutil.NewRepoRepo(m1, m2, g)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	mavenH := maven.New(d)
	registry := map[string]formats.FormatHandler{"maven2": mavenH}
	groupH := group.New(d, registry)

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		mavenH.ServeHTTP(c)
	})

	put := func(repoName, p, body string) {
		req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+p, strings.NewReader(body))
		req.ContentLength = int64(len(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Contains(t, []int{http.StatusCreated, http.StatusOK}, w.Code)
	}

	meta := func(versions ...string) string {
		s := "<metadata><groupId>com.x</groupId><artifactId>lib</artifactId><versioning><versions>"
		for _, v := range versions {
			s += "<version>" + v + "</version>"
		}
		return s + "</versions></versioning></metadata>"
	}
	put("mv1", "/com/x/lib/maven-metadata.xml", meta("1.0"))
	put("mv2", "/com/x/lib/maven-metadata.xml", meta("2.0"))

	w := get(r, "/repository/mvg/com/x/lib/maven-metadata.xml")
	assert.Equal(t, http.StatusOK, w.Code)
	out := w.Body.String()
	assert.Contains(t, out, "<version>1.0</version>")
	assert.Contains(t, out, "<version>2.0</version>")
	assert.Contains(t, out, "<latest>2.0</latest>")
	assert.Equal(t, "mv1,mv2", w.Header().Get("X-Nexspence-Source"))

	// Checksum of the MERGED doc, not any member's copy (40 hex chars).
	ws := get(r, "/repository/mvg/com/x/lib/maven-metadata.xml.sha1")
	assert.Equal(t, http.StatusOK, ws.Code)
	assert.Len(t, strings.TrimSpace(ws.Body.String()), 40)
}
