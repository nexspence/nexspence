package group_test

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io"
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
	"github.com/nexspence-oss/nexspence/internal/formats/yum"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// repomdDoc is enough of repomd.xml to check what it advertises.
type repomdDoc struct {
	Data []struct {
		Type     string `xml:"type,attr"`
		Location struct {
			Href string `xml:"href,attr"`
		} `xml:"location"`
		Checksum struct {
			Value string `xml:",chardata"`
		} `xml:"checksum"`
		OpenChecksum struct {
			Value string `xml:",chardata"`
		} `xml:"open-checksum"`
		Size     int64 `xml:"size"`
		OpenSize int64 `xml:"open-size"`
	} `xml:"data"`
}

func buildYumGroupEngine(t *testing.T, groupName string, memberNames ...string) *gin.Engine {
	t.Helper()

	repos := make([]*domain.Repository, 0, len(memberNames)+1)
	ms := make([]interface{}, len(memberNames))
	for i, name := range memberNames {
		repos = append(repos, testutil.SimpleRepo(name, "yum"))
		ms[i] = name
	}
	repos = append(repos, &domain.Repository{
		ID: "repo-" + groupName, Name: groupName, Format: "yum",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": ms},
	})

	repoRepo := testutil.NewRepoRepo(repos...)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	yumH := yum.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"yum": yumH})

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		yumH.ServeHTTP(c)
	})
	return r
}

func putRpmTo(t *testing.T, r *gin.Engine, repoName, filename string) {
	t.Helper()
	body := "rpm-payload-" + filename
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+"/Packages/"+filename, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "upload %s: %s", filename, w.Body.String())
}

// dnf verifies every repodata document against repomd.xml before using it, so a
// group's repomd has to describe the merged documents the group serves — not a
// single member's copy of them.
func TestGroupMerge_YumRepomdDescribesTheMergedRepodata(t *testing.T) {
	r := buildYumGroupEngine(t, "yg", "y1", "y2")
	putRpmTo(t, r, "y1", "curl-8.0.0-1.x86_64.rpm")
	putRpmTo(t, r, "y2", "vim-9.0-1.x86_64.rpm")

	w := get(r, "/repository/yg/repodata/repomd.xml")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var repomd repomdDoc
	require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &repomd))
	require.Len(t, repomd.Data, 3, "primary, filelists and other are all advertised")

	for _, d := range repomd.Data {
		gz := get(r, "/repository/yg/"+d.Location.Href)
		require.Equal(t, http.StatusOK, gz.Code, d.Location.Href)
		gzBody := gz.Body.Bytes()

		assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(gzBody)), d.Checksum.Value,
			"%s checksum must match the %s the group serves", d.Type, d.Location.Href)
		assert.Equal(t, int64(len(gzBody)), d.Size, "%s size", d.Type)

		plainPath := strings.TrimSuffix(d.Location.Href, ".gz")
		plain := get(r, "/repository/yg/"+plainPath)
		require.Equal(t, http.StatusOK, plain.Code, plainPath)
		plainBody := plain.Body.Bytes()

		assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(plainBody)), d.OpenChecksum.Value,
			"%s open-checksum must match the %s the group serves", d.Type, plainPath)
		assert.Equal(t, int64(len(plainBody)), d.OpenSize, "%s open-size", d.Type)

		// The gzip flavor is the same document, not a separately merged one.
		zr, err := gzip.NewReader(strings.NewReader(string(gzBody)))
		require.NoError(t, err)
		unzipped, err := io.ReadAll(zr)
		require.NoError(t, err)
		assert.Equal(t, string(plainBody), string(unzipped))
	}
}

// filelists/other are aggregated indexes like primary: a group that serves one
// member's copy hides every other member's packages from `dnf provides` and
// changelog queries.
func TestGroupMerge_YumFilelistsAndOtherCoverEveryMember(t *testing.T) {
	r := buildYumGroupEngine(t, "yg2", "y3", "y4")
	putRpmTo(t, r, "y3", "curl-8.0.0-1.x86_64.rpm")
	putRpmTo(t, r, "y4", "vim-9.0-1.x86_64.rpm")

	for _, doc := range []string{"primary", "filelists", "other"} {
		body := get(r, "/repository/yg2/repodata/"+doc+".xml").Body.String()
		assert.Contains(t, body, "curl", "%s.xml lists the first member's package", doc)
		assert.Contains(t, body, "vim", "%s.xml lists the second member's package", doc)
		assert.Contains(t, body, `packages="2"`, "%s.xml counts both", doc)
	}
}

// The same package in two members is one package in the group.
func TestGroupMerge_YumDedupsPackagesAcrossMembers(t *testing.T) {
	r := buildYumGroupEngine(t, "yg3", "y5", "y6")
	putRpmTo(t, r, "y5", "curl-8.0.0-1.x86_64.rpm")
	putRpmTo(t, r, "y6", "curl-8.0.0-1.x86_64.rpm")

	primary := get(r, "/repository/yg3/repodata/primary.xml").Body.String()
	assert.Equal(t, 1, strings.Count(primary, "<name>curl</name>"))
	assert.Contains(t, primary, `packages="1"`)
}
