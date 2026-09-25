package maven_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func mvnPut(r http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// A `mvn deploy` of a released version twice: the second deploy's jar is
// refused with the Nexus message, while maven-metadata.xml, which every
// deploy rewrites, and the checksum sidecars keep being accepted.
func TestMaven_AllowOnce_RedeployOfReleaseIs400(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-once", "maven2")
	repo.FormatConfig = map[string]any{domain.WritePolicyKey: "allow_once"}
	r, blobStore := setup(repo)
	base0 := "/repository/mvn-once"

	require.Equal(t, http.StatusCreated, mvnPut(r, base0+jarPath, jarBody).Code)
	require.Equal(t, http.StatusCreated, mvnPut(r, base0+"/org/example/mylib/maven-metadata.xml", "<metadata/>").Code)

	w := mvnPut(r, base0+jarPath, "other-bytes")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Repository does not allow updating assets: mvn-once")
	got, err := blobStore.Read(base.BlobKey("mvn-once", jarPath))
	require.NoError(t, err)
	assert.Equal(t, jarBody, got)

	assert.Equal(t, http.StatusCreated, mvnPut(r, base0+"/org/example/mylib/maven-metadata.xml", "<metadata>2</metadata>").Code)
	assert.Equal(t, http.StatusCreated, mvnPut(r, base0+jarPath+".sha1", "abc").Code)

	snap := base0 + "/org/example/mylib/1.1-SNAPSHOT/mylib-1.1-SNAPSHOT.jar"
	assert.Equal(t, http.StatusCreated, mvnPut(r, snap, "s1").Code)
	assert.Equal(t, http.StatusCreated, mvnPut(r, snap, "s2").Code)
}

func TestMaven_Deny_DeployIs400(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-ro", "maven2")
	repo.FormatConfig = map[string]any{domain.WritePolicyKey: "deny"}
	r, _ := setup(repo)

	w := mvnPut(r, "/repository/mvn-ro"+jarPath, jarBody)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Repository is read-only: mvn-ro")
}
