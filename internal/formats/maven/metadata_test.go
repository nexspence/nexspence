package maven_test

import (
	"crypto/sha1" //nolint:gosec // maven protocol checksum, not security
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Dynamic maven-metadata.xml generation (#350): Nexus never attaches either
// metadata document to a component, so migrations can't carry them over — and
// without them a real mvn client can't resolve versions or SNAPSHOTs at all.
// Both shapes are generated from stored components on demand, npm-packument
// style; a literal client-uploaded file, when present, still wins.

func putFile(t *testing.T, r http.Handler, repo, p, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repo+p, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "PUT %s: %s", p, w.Body.String())
}

func getPath(r http.Handler, repo, p string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/"+repo+p, nil))
	return w
}

func TestMaven_AggregateMetadata_GeneratedFromStoredVersions(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-meta", "maven2")
	r, _ := setup(repo)

	putFile(t, r, "mvn-meta", "/com/example/widget-app/1.0/widget-app-1.0.jar", "jar-1.0")
	putFile(t, r, "mvn-meta", "/com/example/widget-app/1.1/widget-app-1.1.jar", "jar-1.1")
	putFile(t, r, "mvn-meta", "/com/example/widget-app/2.0-SNAPSHOT/widget-app-2.0-20260830.123456-1.jar", "jar-snap")

	w := getPath(r, "mvn-meta", "/com/example/widget-app/maven-metadata.xml")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, "<groupId>com.example</groupId>")
	assert.Contains(t, body, "<artifactId>widget-app</artifactId>")
	assert.Contains(t, body, "<version>1.0</version>")
	assert.Contains(t, body, "<version>1.1</version>")
	assert.Contains(t, body, "<version>2.0-SNAPSHOT</version>")
	assert.Contains(t, body, "<latest>2.0-SNAPSHOT</latest>")
	assert.Contains(t, body, "<release>1.1</release>", "release must skip SNAPSHOT versions")
}

func TestMaven_AggregateMetadata_NoVersions_404(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-meta-404", "maven2")
	r, _ := setup(repo)

	w := getPath(r, "mvn-meta-404", "/com/example/nothing/maven-metadata.xml")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMaven_PerVersionSnapshotMetadata_Generated(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-snap", "maven2")
	r, _ := setup(repo)

	base := "/com/example/widget-app/2.0-SNAPSHOT/"
	// An older build and the current one — only the latest (timestamp,
	// buildNumber) pair may be reported, with one entry per extension /
	// classifier seen on that build.
	putFile(t, r, "mvn-snap", base+"widget-app-2.0-20260829.101010-2.jar", "old-jar")
	putFile(t, r, "mvn-snap", base+"widget-app-2.0-20260830.123456-3.jar", "new-jar")
	putFile(t, r, "mvn-snap", base+"widget-app-2.0-20260830.123456-3.pom", "new-pom")
	putFile(t, r, "mvn-snap", base+"widget-app-2.0-20260830.123456-3-sources.jar", "new-sources")

	w := getPath(r, "mvn-snap", base+"maven-metadata.xml")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, `modelVersion="1.1.0"`)
	assert.Contains(t, body, "<version>2.0-SNAPSHOT</version>")
	assert.Contains(t, body, "<timestamp>20260830.123456</timestamp>")
	assert.Contains(t, body, "<buildNumber>3</buildNumber>")
	assert.Contains(t, body, "<value>2.0-20260830.123456-3</value>")
	assert.Contains(t, body, "<extension>pom</extension>")
	assert.Contains(t, body, "<classifier>sources</classifier>")
	assert.NotContains(t, body, "20260829.101010", "stale builds must not be reported")
}

func TestMaven_GeneratedMetadataChecksum_MatchesDocument(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-meta-cs", "maven2")
	r, _ := setup(repo)

	putFile(t, r, "mvn-meta-cs", "/com/example/widget-app/1.0/widget-app-1.0.jar", "jar-1.0")

	doc := getPath(r, "mvn-meta-cs", "/com/example/widget-app/maven-metadata.xml")
	require.Equal(t, http.StatusOK, doc.Code)

	cs := getPath(r, "mvn-meta-cs", "/com/example/widget-app/maven-metadata.xml.sha1")
	require.Equal(t, http.StatusOK, cs.Code, cs.Body.String())
	assert.Equal(t, fmt.Sprintf("%x", sha1.Sum(doc.Body.Bytes())), cs.Body.String(), //nolint:gosec
		"sidecar must be computed over the generated document")
}

func TestMaven_ClientUploadedMetadata_TakesPriority(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-meta-lit", "maven2")
	r, _ := setup(repo)

	putFile(t, r, "mvn-meta-lit", "/com/example/widget-app/1.0/widget-app-1.0.jar", "jar-1.0")
	literal := `<?xml version="1.0"?><metadata><groupId>com.example</groupId><artifactId>widget-app</artifactId><versioning><versions><version>9.9</version></versions></versioning></metadata>`
	putFile(t, r, "mvn-meta-lit", "/com/example/widget-app/maven-metadata.xml", literal)

	w := getPath(r, "mvn-meta-lit", "/com/example/widget-app/maven-metadata.xml")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, literal, w.Body.String(), "a real stored metadata file must be served untouched")
}
