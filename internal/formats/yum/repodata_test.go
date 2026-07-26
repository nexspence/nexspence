package yum_test

import (
	"bytes"
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

	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// repomdDoc mirrors the served repomd.xml for assertions.
type repomdDoc struct {
	Data []struct {
		Type     string `xml:"type,attr"`
		Location struct {
			Href string `xml:"href,attr"`
		} `xml:"location"`
		Checksum struct {
			Type  string `xml:"type,attr"`
			Value string `xml:",chardata"`
		} `xml:"checksum"`
		OpenChecksum struct {
			Value string `xml:",chardata"`
		} `xml:"open-checksum"`
	} `xml:"data"`
}

func fetch(t *testing.T, r *gin.Engine, url string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, url)
	return w
}

func setupWithRPM(t *testing.T, repoName string) *gin.Engine {
	t.Helper()
	repo := testutil.SimpleRepo(repoName, "yum")
	r := setup(repo)
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+"/pool/curl-8.0.0-1.x86_64.rpm",
		strings.NewReader("rpm-bytes"))
	req.ContentLength = int64(len("rpm-bytes"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	return r
}

// #102: dnf verifies the primary checksum advertised in repomd.xml against
// the actual primary.xml.gz bytes — an empty checksum makes it reject the repo.
func TestYum_Repomd_RealPrimaryChecksum(t *testing.T) {
	r := setupWithRPM(t, "rpms-ck")

	var doc repomdDoc
	require.NoError(t, xml.Unmarshal(fetch(t, r, "/repository/rpms-ck/repodata/repomd.xml").Body.Bytes(), &doc))

	var primary *struct {
		Type     string `xml:"type,attr"`
		Location struct {
			Href string `xml:"href,attr"`
		} `xml:"location"`
		Checksum struct {
			Type  string `xml:"type,attr"`
			Value string `xml:",chardata"`
		} `xml:"checksum"`
		OpenChecksum struct {
			Value string `xml:",chardata"`
		} `xml:"open-checksum"`
	}
	for i := range doc.Data {
		if doc.Data[i].Type == "primary" {
			primary = &doc.Data[i]
		}
	}
	require.NotNil(t, primary, "repomd must declare primary")

	// Relative href — resolves against the repo (or group) base URL.
	assert.Equal(t, "repodata/primary.xml.gz", primary.Location.Href)

	gz := fetch(t, r, "/repository/rpms-ck/repodata/primary.xml.gz").Body.Bytes()
	assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(gz)), strings.TrimSpace(primary.Checksum.Value),
		"repomd checksum must match the served primary.xml.gz")

	zr, err := gzip.NewReader(bytes.NewReader(gz))
	require.NoError(t, err)
	plain, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(plain)), strings.TrimSpace(primary.OpenChecksum.Value),
		"open-checksum must match the uncompressed primary.xml")
}

func TestYum_Repomd_DeclaresFilelistsAndOther(t *testing.T) {
	r := setupWithRPM(t, "rpms-fl")

	var doc repomdDoc
	require.NoError(t, xml.Unmarshal(fetch(t, r, "/repository/rpms-fl/repodata/repomd.xml").Body.Bytes(), &doc))

	types := map[string]string{}
	for _, d := range doc.Data {
		types[d.Type] = strings.TrimSpace(d.Checksum.Value)
	}
	require.Contains(t, types, "filelists")
	require.Contains(t, types, "other")
	assert.Len(t, types["filelists"], 64, "real sha256")
	assert.Len(t, types["other"], 64)

	// The declared docs are actually served and valid XML with per-package entries.
	fl := fetch(t, r, "/repository/rpms-fl/repodata/filelists.xml").Body.String()
	assert.Contains(t, fl, `name="curl"`)
	oth := fetch(t, r, "/repository/rpms-fl/repodata/other.xml").Body.String()
	assert.Contains(t, oth, `name="curl"`)
}

func TestYum_Primary_RelativeHrefAndPkgid(t *testing.T) {
	r := setupWithRPM(t, "rpms-href")

	primary := fetch(t, r, "/repository/rpms-href/repodata/primary.xml").Body.String()
	assert.Contains(t, primary, `href="pool/curl-8.0.0-1.x86_64.rpm"`,
		"location href must be relative to the repo root (no leading slash)")
	assert.Contains(t, primary, `pkgid="YES"`, "primary must carry the package checksum for dnf pkgid matching")
}
