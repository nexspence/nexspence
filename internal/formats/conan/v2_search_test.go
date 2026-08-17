package conan_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func searchResults(t *testing.T, r http.Handler, url string) []string {
	t.Helper()
	w := v2Get(r, url)
	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Results []string `json:"results"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Results
}

func TestConan_V2Search_PatternsAndRefShapes(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-search", "conan")
	r := setup(repo)

	// One ref with the "_" placeholders, one with a real user/channel, and a
	// v1-file-API upload. The v1 one must NOT surface: every other v2 route
	// 404s for it, and a search hit the follow-up calls cannot resolve makes
	// version-range resolution pick it and abort the install.
	require.Equal(t, http.StatusCreated,
		v2Put(r, "/repository/cv2-search/v2/conans/boost/1.83.0/_/_/revisions/r1/files/conanfile.py", "a"))
	require.Equal(t, http.StatusCreated,
		v2Put(r, "/repository/cv2-search/v2/conans/zlib/1.3/team/stable/revisions/r1/files/conanfile.py", "b"))
	require.Equal(t, http.StatusCreated,
		v2Put(r, "/repository/cv2-search/files/openssl/3.2/_/_/0/export/conanfile.py", "c"))

	all := searchResults(t, r, "/repository/cv2-search/v2/conans/search")
	assert.ElementsMatch(t, []string{"boost/1.83.0", "zlib/1.3@team/stable"}, all)

	// `*` crosses "/" — fnmatch semantics, not path.Match.
	assert.Equal(t, []string{"boost/1.83.0"},
		searchResults(t, r, "/repository/cv2-search/v2/conans/search?q=boost*"))

	// Case folds by default; ignorecase=False turns folding off.
	assert.Equal(t, []string{"boost/1.83.0"},
		searchResults(t, r, "/repository/cv2-search/v2/conans/search?q=BOOST*"))
	assert.Empty(t,
		searchResults(t, r, "/repository/cv2-search/v2/conans/search?q=BOOST*&ignorecase=False"))

	// A pattern over user/channel matches the "@user/channel" rendering.
	assert.Equal(t, []string{"zlib/1.3@team/stable"},
		searchResults(t, r, "/repository/cv2-search/v2/conans/search?q=*@team/*"))

	// No match and an empty repository are both an empty list, not null.
	assert.Empty(t, searchResults(t, r, "/repository/cv2-search/v2/conans/search?q=nothing*"))
	empty := setup(testutil.SimpleRepo("cv2-search-empty", "conan"))
	w := v2Get(empty, "/repository/cv2-search-empty/v2/conans/search")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"results":[]`)
}

func TestConan_V2Search_Packages_RevisionLevel(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-psearch", "conan")
	r := setup(repo)
	base := "/repository/cv2-psearch/v2/conans/boost/1.83.0/_/_/revisions/r1"
	info := "[settings]\nos=Linux\narch=x86_64\n"

	require.Equal(t, http.StatusCreated, v2Put(r, base+"/files/conanfile.py", "recipe"))
	require.Equal(t, http.StatusCreated,
		v2Put(r, base+"/packages/pkgA/revisions/p1/files/conaninfo.txt", info))
	require.Equal(t, http.StatusCreated,
		v2Put(r, base+"/packages/pkgA/revisions/p1/files/conan_package.tgz", "bin"))

	// list_only → keys only, empty values.
	w := v2Get(r, base+"/search?list_only=True")
	require.Equal(t, http.StatusOK, w.Code)
	var listOnly map[string]map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listOnly))
	require.Contains(t, listOnly, "pkgA")
	assert.Empty(t, listOnly["pkgA"])

	// Full search → the raw conaninfo.txt under "content"; the client parses
	// it itself.
	w2 := v2Get(r, base+"/search?list_only=False")
	require.Equal(t, http.StatusOK, w2.Code)
	var full map[string]map[string]string
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &full))
	assert.Equal(t, info, full["pkgA"]["content"])
}

func TestConan_V2Search_Packages_RefLevelResolvesLatestRevision(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-psearch-ref", "conan")
	r := setup(repo)
	ref := "/repository/cv2-psearch-ref/v2/conans/boost/1.83.0/_/_"

	// Older revision carries pkgOld, newer carries pkgNew — the ref-level
	// search must answer for the newest revision only.
	require.Equal(t, http.StatusCreated,
		v2Put(r, ref+"/revisions/r1/packages/pkgOld/revisions/p1/files/conaninfo.txt", "[settings]\n"))
	require.Equal(t, http.StatusCreated,
		v2Put(r, ref+"/revisions/r2/packages/pkgNew/revisions/p1/files/conaninfo.txt", "[settings]\n"))

	w := v2Get(r, ref+"/search?list_only=True")
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Contains(t, got, "pkgNew")
	assert.NotContains(t, got, "pkgOld")
}

func TestConan_V2Search_Packages_RecipeOnlyIsEmptyObjectAndUnknownIs404(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-psearch-empty", "conan")
	r := setup(repo)
	base := "/repository/cv2-psearch-empty/v2/conans/nxtest/1.0/_/_/revisions/r1"
	require.Equal(t, http.StatusCreated, v2Put(r, base+"/files/conanfile.py", "recipe"))

	// A recipe-only revision answers an empty object — the download flow
	// treats that as "no binaries", not as an error.
	w := v2Get(r, base+"/search?list_only=False")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "{}", w.Body.String())

	w2 := v2Get(r, "/repository/cv2-psearch-empty/v2/conans/nope/1.0/_/_/search")
	assert.Equal(t, http.StatusNotFound, w2.Code)
	w3 := v2Get(r, base[:len(base)-2]+"rX/search")
	assert.Equal(t, http.StatusNotFound, w3.Code)
}

func TestConan_V2Search_BadPattern400(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-search-bad", "conan")
	r := setup(repo)
	w := v2Get(r, "/repository/cv2-search-bad/v2/conans/search?q=%5Bz-a%5D")
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// The pattern length is capped — compiling it is the only CPU an
	// unauthenticated caller can make this handler spend.
	long := strings.Repeat("*a", 200)
	w2 := v2Get(r, "/repository/cv2-search-bad/v2/conans/search?q="+long)
	assert.Equal(t, http.StatusBadRequest, w2.Code)
}

func TestConan_V2Search_ClassWithBackslashIsLiteral(t *testing.T) {
	// fnmatch treats a backslash inside [] as a literal; unescaped it would
	// swallow the closing "]" in the regexp and turn into a compile error.
	repo := testutil.SimpleRepo("cv2-search-cls", "conan")
	r := setup(repo)
	require.Equal(t, http.StatusCreated,
		v2Put(r, "/repository/cv2-search-cls/v2/conans/boost/1.83.0/_/_/revisions/r1/files/conanfile.py", "a"))

	assert.Equal(t, []string{"boost/1.83.0"},
		searchResults(t, r, "/repository/cv2-search-cls/v2/conans/search?q="+url.QueryEscape(`[ab\]oost*`)))

	// A leading "^" is a literal caret in fnmatch (negation is "!"), so
	// "[^b]" matches "b" — it must not negate.
	assert.Equal(t, []string{"boost/1.83.0"},
		searchResults(t, r, "/repository/cv2-search-cls/v2/conans/search?q="+url.QueryEscape(`[^b]oost*`)))
}

func TestConan_V2Search_ProxyRepoServesFromLocalCache(t *testing.T) {
	// The generic proxy GET would drop ?q= on the upstream fetch and cache
	// the first answer forever — search on a proxy answers from the local
	// cache instead of forwarding.
	repo := testutil.SimpleRepo("cv2-search-proxy", "conan")
	repo.Type = domain.TypeProxy
	r := setup(repo)

	w := v2Get(r, "/repository/cv2-search-proxy/v2/conans/search?q=*")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"results":[]`)
}
