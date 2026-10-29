package apt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func (e *ctlEnv) send(method, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/repository/"+e.repo.Name+path, bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

// packagesOf lists "Package Version" of each stanza in a suite's index.
func (e *ctlEnv) packagesOf(t *testing.T, suite, arch string) []string {
	t.Helper()
	var out []string
	for _, stanza := range strings.Split(e.get(t, "/dists/"+suite+"/main/binary-"+arch+"/Packages"), "\n\n") {
		var pkg, ver string
		for _, line := range strings.Split(stanza, "\n") {
			if v, ok := strings.CutPrefix(line, "Package: "); ok {
				pkg = v
			}
			if v, ok := strings.CutPrefix(line, "Version: "); ok {
				ver = v
			}
		}
		if pkg != "" {
			out = append(out, pkg+" "+ver)
		}
	}
	return out
}

func (e *ctlEnv) poolCount(t *testing.T) int {
	t.Helper()
	assets, err := e.assets.ListByRepoAndPath(t.Context(), e.repo.Name, "/pool/")
	require.NoError(t, err)
	return len(assets)
}

// envWith builds a handler over e's stores with Components replaced.
func (e *ctlEnv) envWith(comps repository.ComponentRepo) *gin.Engine {
	d := e.deps
	d.Components = comps
	h := New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func putVia(r *gin.Engine, url string, body io.Reader, size int64) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, url, body)
	req.ContentLength = size
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Each suite lists its own debs; a deb shared by two suites is stored once;
// a deb uploaded without a suite is listed in every suite.
func TestSuites_EachSuiteListsItsOwnDebs(t *testing.T) {
	e := newCtlEnv(t, "lines", domain.WritePolicyAllowOnce)
	shared := debFor(t, "acme-toolchain", "1.0", "amd64", "", "toolchain")

	require.Equal(t, http.StatusCreated, e.put("/pool/main/acme_1.4_all.deb?suite=acme-1.4", debFor(t, "acme", "1.4", "all", "", "a")).Code)
	require.Equal(t, http.StatusCreated, e.put("/pool/main/acme_1.5_all.deb?suite=acme-1.5", debFor(t, "acme", "1.5", "all", "", "b")).Code)
	require.Equal(t, http.StatusCreated, e.put("/pool/main/acme-toolchain_1.0_amd64.deb?suite=acme-1.4", shared).Code)
	// The same file again for another line: it joins, nothing is stored, and
	// the write-once policy has nothing to refuse.
	require.Equal(t, http.StatusOK, e.put("/pool/main/acme-toolchain_1.0_amd64.deb?suite=acme-1.5", shared).Code)
	// Suites are added through the file's own path, the one the uploader's
	// permissions were checked against.
	w := e.put("/pool/main/a/acme-toolchain/copy.deb?suite=acme-1.6", shared)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "/pool/main/acme-toolchain_1.0_amd64.deb")
	require.Equal(t, http.StatusCreated, e.put("/pool/main/acme-common_1.0_all.deb", debFor(t, "acme-common", "1.0", "all", "", "c")).Code)

	assert.Equal(t, 4, e.poolCount(t), "the shared deb is stored once")
	assert.ElementsMatch(t, []string{"acme 1.4", "acme-toolchain 1.0", "acme-common 1.0"}, e.packagesOf(t, "acme-1.4", "amd64"))
	assert.ElementsMatch(t, []string{"acme 1.5", "acme-toolchain 1.0", "acme-common 1.0"}, e.packagesOf(t, "acme-1.5", "amd64"))
	assert.ElementsMatch(t, []string{"acme-common 1.0"}, e.packagesOf(t, "acme-1.6", "amd64"))
	assert.ElementsMatch(t, []string{"acme-common 1.0"}, e.packagesOf(t, "stable", "amd64"), "a suite nobody named lists the suite-less debs")
	assert.ElementsMatch(t, []string{"acme 1.4", "acme-common 1.0"}, e.packagesOf(t, "acme-1.4", "all"))
}

// A repository that never names a suite serves one index under any name.
func TestSuites_RepositoryWithoutSuitesIsUnchanged(t *testing.T) {
	e := newCtlEnv(t, "plain", "")
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_1.0_amd64.deb", debFor(t, "foo", "1.0", "amd64", "", "x")).Code)
	for _, suite := range []string{"stable", "jammy", "anything"} {
		assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, suite, "amd64"), suite)
	}
}

// One identity is one file across the repository: other content under it is
// refused whichever suite it is meant for.
func TestSuites_OtherContentUnderAnIdentityIsRefused(t *testing.T) {
	e := newCtlEnv(t, "lines-409", domain.WritePolicyAllowOnce)
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_1.0_amd64.deb?suite=one", debFor(t, "foo", "1.0", "amd64", "", "first")).Code)

	w := e.put("/pool/main/rebuilt/foo_1.0_amd64.deb?suite=two", debFor(t, "foo", "1.0", "amd64", "", "second"))
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "/pool/main/foo_1.0_amd64.deb")
	// At the same path it is a redeploy, which write-once refuses.
	assert.NotEqual(t, http.StatusCreated, e.put("/pool/main/foo_1.0_amd64.deb?suite=two", debFor(t, "foo", "1.0", "amd64", "", "second")).Code)

	assert.Equal(t, 1, e.poolCount(t))
	assert.Empty(t, e.packagesOf(t, "two", "amd64"))
}

// A redeploy with other content, where the write policy allows it, replaces
// the file in every suite it is in and adds the new one.
func TestSuites_RedeployKeepsTheSuites(t *testing.T) {
	e := newCtlEnv(t, "lines-redeploy", domain.WritePolicyAllow)
	p := "/pool/main/foo_1.0_amd64.deb"
	require.Equal(t, http.StatusCreated, e.put(p+"?suite=one", debFor(t, "foo", "1.0", "amd64", "", "first")).Code)
	second := debFor(t, "foo", "1.0", "amd64", "", "second")
	require.Equal(t, http.StatusCreated, e.put(p+"?suite=two", second).Code)

	sum := fmt.Sprintf("SHA256: %x\n", sha256.Sum256(second))
	assert.Contains(t, e.get(t, "/dists/one/main/binary-amd64/Packages"), sum)
	assert.Contains(t, e.get(t, "/dists/two/main/binary-amd64/Packages"), sum)
	assert.Equal(t, string(second), e.get(t, p))
}

// A redeploy that names no suite keeps the file's suites: it must not fall
// back to "every suite".
func TestSuites_PlainRedeployKeepsTheSuites(t *testing.T) {
	e := newCtlEnv(t, "lines-plain-redeploy", domain.WritePolicyAllow)
	p := "/pool/main/foo_1.0_amd64.deb"
	require.Equal(t, http.StatusCreated, e.put(p+"?suite=one", debFor(t, "foo", "1.0", "amd64", "", "first")).Code)
	require.Equal(t, http.StatusCreated, e.put(p, debFor(t, "foo", "1.0", "amd64", "", "second")).Code)
	assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, "one", "amd64"))
	assert.Empty(t, e.packagesOf(t, "two", "amd64"))

	// Out of every suite, a redeploy keeps it out.
	require.Equal(t, http.StatusNoContent, e.send(http.MethodDelete, p+"?suite=one", nil).Code)
	require.Equal(t, http.StatusCreated, e.put(p, debFor(t, "foo", "1.0", "amd64", "", "third")).Code)
	assert.Empty(t, e.packagesOf(t, "one", "amd64"))
	assert.Empty(t, e.packagesOf(t, "stable", "amd64"))
}

// Release describes the suite's own indexes: its architectures and the
// checksums of exactly what dists/<suite>/ serves.
func TestSuites_ReleaseDescribesTheSuite(t *testing.T) {
	e := newCtlEnv(t, "lines-release", "")
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_1.0_amd64.deb?suite=one", debFor(t, "foo", "1.0", "amd64", "", "x")).Code)
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_1.0_arm64.deb?suite=two", debFor(t, "foo", "1.0", "arm64", "", "y")).Code)

	for suite, arch := range map[string]string{"one": "amd64", "two": "arm64"} {
		release := e.get(t, "/dists/"+suite+"/Release")
		assert.Contains(t, release, "Suite: "+suite+"\n")
		assert.Contains(t, release, "Architectures: "+arch+" all\n", suite)
		for _, rel := range []string{"main/binary-" + arch + "/Packages", "main/binary-all/Packages"} {
			body := e.get(t, "/dists/"+suite+"/"+rel)
			assert.Contains(t, release, fmt.Sprintf(" %x %d %s\n", sha256.Sum256([]byte(body)), len(body), rel), suite+" vouches for "+rel)
		}
	}
}

// A suite's Release Date never goes back while the repository changes: apt
// keeps the Release it has when a new one is not newer, so a client would
// keep listing a deb that left the suite.
func TestSuites_ReleaseDateFollowsSuiteChanges(t *testing.T) {
	at := func(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }
	deb := func(modified time.Time, suites []any, changed time.Time) suiteDeb {
		extra := map[string]any{}
		if suites != nil {
			extra[debSuitesKey] = suites
		}
		if !changed.IsZero() {
			extra[debSuitesChangedKey] = changed.Format(time.RFC3339Nano)
		}
		return suiteDeb{asset: domain.Asset{LastModified: modified}, comp: &domain.Component{Extra: extra}}
	}
	older := deb(at(1), []any{"x"}, at(1))
	newer := deb(at(5), []any{"x"}, at(5))
	all := []suiteDeb{older, newer}
	assert.Equal(t, at(5), suiteDate(all, inSuiteDebs(all, "x"), time.Time{}))

	// The newer deb leaves x: x's Date moves on to when it left.
	left := deb(at(5), []any{}, at(9))
	all = []suiteDeb{older, left}
	assert.Equal(t, at(9), suiteDate(all, inSuiteDebs(all, "x"), time.Time{}))

	// An older deb joining x moves its Date too.
	all = []suiteDeb{deb(at(1), []any{"x"}, at(12)), newer}
	assert.Equal(t, at(12), suiteDate(all, inSuiteDebs(all, "x"), time.Time{}))

	// A deletion from the repository moves it too (#728).
	assert.Equal(t, at(20), suiteDate(all, inSuiteDebs(all, "x"), at(20)))

	// A suite with no deb takes the repository's newest, not the wall clock.
	all = []suiteDeb{deb(at(3), nil, time.Time{})}
	assert.Equal(t, at(3), suiteDate(all, nil, time.Time{}))
}

// Uploading the same file for a suite it is already in changes nothing, so
// the suite's Release stays byte-identical.
func TestSuites_RepeatedJoinChangesNothing(t *testing.T) {
	e := newCtlEnv(t, "lines-repeat", domain.WritePolicyAllowOnce)
	deb := debFor(t, "foo", "1.0", "amd64", "", "x")
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_1.0_amd64.deb?suite=one", deb).Code)
	before := e.get(t, "/dists/one/Release")
	require.Equal(t, http.StatusOK, e.put("/pool/main/foo_1.0_amd64.deb?suite=one", deb).Code)
	assert.Equal(t, before, e.get(t, "/dists/one/Release"))
}

// DELETE with a suite takes the file out of that suite only.
func TestSuites_LeaveASuite(t *testing.T) {
	e := newCtlEnv(t, "lines-leave", "")
	p := "/pool/main/foo_1.0_amd64.deb"
	deb := debFor(t, "foo", "1.0", "amd64", "", "x")
	require.Equal(t, http.StatusCreated, e.put(p+"?suite=one&suite=two", deb).Code)

	assert.Equal(t, http.StatusNoContent, e.send(http.MethodDelete, p+"?suite=one", nil).Code)
	assert.Empty(t, e.packagesOf(t, "one", "amd64"))
	assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, "two", "amd64"))
	assert.Equal(t, string(deb), e.get(t, p), "the file stays")
	assert.Equal(t, http.StatusNotFound, e.send(http.MethodDelete, p+"?suite=one", nil).Code)

	// Out of its last suite it is listed nowhere, not everywhere.
	assert.Equal(t, http.StatusNoContent, e.send(http.MethodDelete, p+"?suite=two", nil).Code)
	assert.Empty(t, e.packagesOf(t, "two", "amd64"))
	assert.Empty(t, e.packagesOf(t, "stable", "amd64"))

	// A suite-less file is in every suite; leaving one is refused.
	require.Equal(t, http.StatusCreated, e.put("/pool/main/bar_1.0_amd64.deb", debFor(t, "bar", "1.0", "amd64", "", "y")).Code)
	assert.Equal(t, http.StatusConflict, e.send(http.MethodDelete, "/pool/main/bar_1.0_amd64.deb?suite=one", nil).Code)
	assert.Equal(t, http.StatusNotFound, e.send(http.MethodDelete, "/pool/main/none_1.0_amd64.deb?suite=one", nil).Code)
}

// A suite-less file named for a suite is recorded under it, and from then on
// is listed only in its recorded suites.
func TestSuites_NamingASuiteRecordsIt(t *testing.T) {
	e := newCtlEnv(t, "lines-record", domain.WritePolicyAllowOnce)
	deb := debFor(t, "foo", "1.0", "amd64", "", "x")
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_1.0_amd64.deb", deb).Code)
	require.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, "anything", "amd64"))

	require.Equal(t, http.StatusOK, e.put("/pool/main/foo_1.0_amd64.deb?suite=one", deb).Code)
	assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, "one", "amd64"))
	assert.Empty(t, e.packagesOf(t, "anything", "amd64"))
}

// Suite names are one dists/ segment; anything else is refused before a byte
// is stored, and an empty or malformed suite on DELETE never deletes the file.
func TestSuites_InvalidNamesAreRefused(t *testing.T) {
	e := newCtlEnv(t, "lines-names", "")
	deb := debFor(t, "foo", "1.0", "amd64", "", "x")
	for _, q := range []string{"suite=", "suite=a/b", "suite=..", "suite=-x", "suite=a%20b"} {
		assert.Equal(t, http.StatusBadRequest, e.put("/pool/main/foo_1.0_amd64.deb?"+q, deb).Code, q)
	}
	assert.Equal(t, 0, e.poolCount(t))

	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_1.0_amd64.deb?suite=one", deb).Code)
	for _, q := range []string{"suite=", "suite=a/b", "suite=one&suite=two"} {
		assert.Equal(t, http.StatusBadRequest, e.send(http.MethodDelete, "/pool/main/foo_1.0_amd64.deb?"+q, nil).Code, q)
	}
	assert.Equal(t, 1, e.poolCount(t), "the file is still there")
	for _, ok := range []string{"stable", "bookworm-backports", "acme-1.5.1", "1.0~rc1", "a+b_c"} {
		_, err := requestSuites([]string{ok})
		assert.NoError(t, err, ok)
	}
}

// A deb stored before #637 shares a group-less component with the other
// architectures of its version. Joining a suite files it under its own
// component first, so the other architectures keep their suites.
func TestSuites_JoiningMovesALegacyDebToItsOwnComponent(t *testing.T) {
	e := newCtlEnv(t, "lines-legacy", domain.WritePolicyAllowOnce)
	amd := debFor(t, "foo", "1.0", "amd64", "", "amd")
	arm := debFor(t, "foo", "1.0", "arm64", "", "arm")
	for p, body := range map[string][]byte{"/pool/main/foo_1.0_amd64.deb": amd, "/pool/main/foo_1.0_arm64.deb": arm} {
		_, err := base.StoreArtifact(t.Context(), e.deps, e.repo.Name, p, "application/vnd.debian.binary-package",
			base.Coords{Name: "foo", Version: "1.0"}, bytes.NewReader(body), int64(len(body)))
		require.NoError(t, err)
	}

	require.Equal(t, http.StatusOK, e.put("/pool/main/foo_1.0_amd64.deb?suite=one", amd).Code)

	assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, "one", "amd64"))
	assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, "other", "arm64"), "arm64 is still suite-less")
	assert.Empty(t, e.packagesOf(t, "other", "amd64"))
	assert.Contains(t, e.get(t, "/dists/one/main/binary-amd64/Packages"), "Maintainer:", "the full stanza from its control file")
	assert.Equal(t, 2, e.poolCount(t))
}

// A body without a control file has no architecture to file it under, so it
// cannot take a suite; without one it is stored as before.
func TestSuites_NonDebBodyCannotTakeASuite(t *testing.T) {
	e := newCtlEnv(t, "lines-raw", "")
	assert.Equal(t, http.StatusBadRequest, e.put("/pool/main/foo_1.0_amd64.deb?suite=one", []byte("not an ar archive")).Code)
	assert.Equal(t, 0, e.poolCount(t))
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_1.0_amd64.deb", []byte("not an ar archive")).Code)
}

// Suites are part of what a read-only repository publishes: they do not change.
func TestSuites_ReadOnlyRepositoryRefusesSuiteChanges(t *testing.T) {
	e := newCtlEnv(t, "lines-ro", domain.WritePolicyDeny)
	p := "/pool/main/foo_1.0_amd64.deb"
	deb := debFor(t, "foo", "1.0", "amd64", "", "x")
	_, ctl, err := readDebHead(bytes.NewReader(deb))
	require.NoError(t, err)
	_, err = base.StoreArtifact(base.WithoutWritePolicy(t.Context()), e.deps, e.repo.Name, p, "application/vnd.debian.binary-package",
		base.Coords{Group: "amd64", Name: "foo", Version: "1.0", Extra: uploadExtra(ctl, []string{"one"}, "upload:seed")},
		bytes.NewReader(deb), int64(len(deb)))
	require.NoError(t, err)

	// Refused as any write to it is (base.HTTPStatusForError).
	assert.Equal(t, http.StatusBadRequest, e.put(p+"?suite=two", deb).Code)
	assert.Equal(t, http.StatusBadRequest, e.send(http.MethodDelete, p+"?suite=one", nil).Code)
	assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, "one", "amd64"))
	assert.Empty(t, e.packagesOf(t, "two", "amd64"))
}

// The suite is echoed into the signed Release, so anything that is not a
// suite name — a line break above all — is no suite of this repository.
func TestSuites_MalformedSuiteOnReadIs404(t *testing.T) {
	e := newCtlEnv(t, "lines-read", "")
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_1.0_amd64.deb", debFor(t, "foo", "1.0", "amd64", "", "x")).Code)
	for _, dist := range []string{"x%0AValid-Until:%20Thu,%2001%20Jan%202099", "x%20y", "-x"} {
		for _, file := range []string{"Release", "InRelease", "Release.gpg", "main/binary-amd64/Packages"} {
			assert.Equal(t, http.StatusNotFound, e.send(http.MethodGet, "/dists/"+dist+"/"+file, nil).Code, dist+"/"+file)
		}
		_, merged := New(e.deps).GroupIndexSourcePath("/dists/" + strings.ReplaceAll(dist, "%0A", "\n") + "/Release")
		assert.False(t, merged, "a group does not render it either")
	}
	_, merged := New(e.deps).GroupIndexSourcePath("/dists/x\nValid-Until: y/InRelease")
	assert.False(t, merged)
	_, merged = New(e.deps).GroupIndexSourcePath("/dists/acme-1.5/InRelease")
	assert.True(t, merged)
}

// failingExtra refuses every UpdateExtra, as a database failing between the
// store and the handler's metadata writes would.
type failingExtra struct{ *testutil.ComponentRepo }

func (failingExtra) UpdateExtra(context.Context, string, map[string]any) error {
	return errors.New("database unavailable")
}

// A new deb's suites are recorded with its component, before it can be listed:
// even when every later write fails, it is never served in another suite.
func TestSuites_NewDebIsNeverListedOutsideItsSuites(t *testing.T) {
	e := newCtlEnv(t, "lines-atomic", "")
	r := e.envWith(failingExtra{e.comps})
	deb := debFor(t, "foo", "1.0", "amd64", "", "x")
	putVia(r, "/repository/lines-atomic/pool/main/foo_1.0_amd64.deb?suite=one", bytes.NewReader(deb), int64(len(deb)))

	require.Equal(t, 1, e.poolCount(t), "the file was stored")
	assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, "one", "amd64"))
	assert.Empty(t, e.packagesOf(t, "two", "amd64"))
	assert.Contains(t, e.get(t, "/dists/one/main/binary-amd64/Packages"), "Maintainer:", "with its control stanza")
}

// losingFirstUpload resets every new component right after it is created, as
// another instance's first upload of the same identity at another path does
// when its upsert lands before this upload's file: it finds no file there yet.
type losingFirstUpload struct{ *testutil.ComponentRepo }

func (l losingFirstUpload) Create(ctx context.Context, c *domain.Component) error {
	if err := l.ComponentRepo.Create(ctx, c); err != nil {
		return err
	}
	// The loser's upsert replaces the whole extra with its own start.
	return l.UpdateExtra(ctx, c.ID, map[string]any{
		debSuitesKey: []string{"other"}, debSuitesByKey: "upload:00:/pool/main/rebuilt/foo_1.0_amd64.deb",
		debControlKey: "Package: foo\nVersion: 1.0\nArchitecture: amd64\nDescription: the loser's\n",
	})
}

// The winner of two first uploads states its own suites and stanza, whatever
// the loser reset the shared component to before giving way.
func TestSuites_NewFileStatesItsSuitesOverALosersReset(t *testing.T) {
	e := newCtlEnv(t, "lines-race", "")
	r := e.envWith(losingFirstUpload{e.comps})
	deb := debFor(t, "foo", "1.0", "amd64", "", "x")
	require.Equal(t, http.StatusCreated, putVia(r, "/repository/lines-race/pool/main/foo_1.0_amd64.deb?suite=one", bytes.NewReader(deb), int64(len(deb))).Code)
	assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, "one", "amd64"))
	assert.Empty(t, e.packagesOf(t, "other", "amd64"))
	assert.NotContains(t, e.get(t, "/dists/one/main/binary-amd64/Packages"), "the loser's")

	// Without a suite, the winner is in every suite again.
	bar := debFor(t, "bar", "1.0", "amd64", "", "y")
	require.Equal(t, http.StatusCreated, putVia(r, "/repository/lines-race/pool/main/bar_1.0_amd64.deb", bytes.NewReader(bar), int64(len(bar))).Code)
	assert.Contains(t, e.packagesOf(t, "anything", "amd64"), "bar 1.0")
}

// changeSuitesAtEOF changes a component's suites when the body has been read,
// as another instance's join or leave landing while a redeploy streams.
type changeSuitesAtEOF struct {
	r    io.Reader
	done func()
}

func (c *changeSuitesAtEOF) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if err == io.EOF && c.done != nil {
		c.done()
		c.done = nil
	}
	return n, err
}

// A redeploy does not write back suites it read before its body streamed:
// the upsert keeps what the component says when the file is registered.
func TestSuites_RedeployDoesNotUndoAConcurrentSuiteChange(t *testing.T) {
	e := newCtlEnv(t, "lines-concurrent", domain.WritePolicyAllow)
	p := "/pool/main/foo_1.0_amd64.deb"
	require.Equal(t, http.StatusCreated, e.put(p+"?suite=one", debFor(t, "foo", "1.0", "amd64", "", "first")).Code)
	a, err := e.assets.GetByPath(t.Context(), e.repo.Name, p)
	require.NoError(t, err)

	second := debFor(t, "foo", "1.0", "amd64", "", "second")
	body := &changeSuitesAtEOF{r: bytes.NewReader(second), done: func() {
		require.NoError(t, e.comps.UpdateExtra(t.Context(), a.ComponentID, suitesExtra([]string{"two"}, suitesByChange)))
	}}
	require.Equal(t, http.StatusCreated, putVia(e.r, "/repository/lines-concurrent"+p, body, int64(len(second))).Code)

	assert.Empty(t, e.packagesOf(t, "one", "amd64"), "the concurrent leave stands")
	assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, "two", "amd64"), "and so does the join")
	assert.Equal(t, string(second), e.get(t, p))
}

// A redeploy naming a suite reads ahead at most the stored file's size; a
// larger body, or one of the same size with other bytes, is stored whole.
func TestSuites_RedeployWithASuiteStoresTheWholeBody(t *testing.T) {
	e := newCtlEnv(t, "lines-spool", domain.WritePolicyAllow)
	p := "/pool/main/foo_1.0_amd64.deb"
	first := debFor(t, "foo", "1.0", "amd64", "", "first")
	require.Equal(t, http.StatusCreated, e.put(p+"?suite=one", first).Code)

	larger := debFor(t, "foo", "1.0", "amd64", "", strings.Repeat("larger payload ", 4096))
	// Undeclared size: the read-ahead is all that bounds it.
	require.Equal(t, http.StatusCreated, putVia(e.r, "/repository/lines-spool"+p+"?suite=two", bytes.NewReader(larger), -1).Code)
	assert.Equal(t, string(larger), e.get(t, p))

	// Same size, other bytes: one byte of the data member changed.
	same := append([]byte{}, larger...)
	same[len(same)-10] ^= 0xff
	require.Equal(t, http.StatusCreated, putVia(e.r, "/repository/lines-spool"+p+"?suite=three", bytes.NewReader(same), int64(len(same))).Code)
	assert.Equal(t, string(same), e.get(t, p))
	for _, suite := range []string{"one", "two", "three"} {
		assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, suite, "amd64"), suite)
	}
}

// joinAfterStore lands another instance's join of the new file (same bytes,
// same path, another suite) right after the file is stored, before the
// upload settles its own suites.
type joinAfterStore struct {
	*testutil.ComponentRepo
	joined bool
}

func (j *joinAfterStore) UpdateExtra(ctx context.Context, id string, extra map[string]any) error {
	if _, control := extra[debControlKey]; control && !j.joined {
		j.joined = true
		if err := j.ComponentRepo.UpdateExtra(ctx, id, suitesExtra([]string{"one", "two"}, suitesByChange)); err != nil {
			return err
		}
	}
	return j.ComponentRepo.UpdateExtra(ctx, id, extra)
}

// A new file settling its suites does not undo a join another client was
// already told succeeded.
func TestSuites_NewFileKeepsAJoinThatLandedAfterIt(t *testing.T) {
	e := newCtlEnv(t, "lines-late-join", "")
	r := e.envWith(&joinAfterStore{ComponentRepo: e.comps})
	deb := debFor(t, "foo", "1.0", "amd64", "", "x")
	require.Equal(t, http.StatusCreated, putVia(r, "/repository/lines-late-join/pool/main/foo_1.0_amd64.deb?suite=one", bytes.NewReader(deb), int64(len(deb))).Code)
	assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, "one", "amd64"))
	assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, "two", "amd64"), "the join stands")
	assert.Empty(t, e.packagesOf(t, "three", "amd64"))
}

// The keys a redeploy keeps, and promotion leaves behind, are this package's
// suite keys.
func TestSuites_ListingKeysAreTheSuiteKeys(t *testing.T) {
	assert.ElementsMatch(t, []string{debSuitesKey, debSuitesChangedKey, debSuitesByKey}, domain.ListingExtraKeys)
}

// sameFileElsewhere lands another instance's first upload of the same file to
// the same path, naming another suite, right after this one's component is
// created: both saw the path free, and both were acknowledged.
type sameFileElsewhere struct{ *testutil.ComponentRepo }

func (s sameFileElsewhere) Create(ctx context.Context, c *domain.Component) error {
	if err := s.ComponentRepo.Create(ctx, c); err != nil {
		return err
	}
	return s.UpdateExtra(ctx, c.ID, suitesExtra([]string{"two"}, "upload:ff:/pool/main/foo_1.0_amd64.deb"))
}

// Two uploads of one path settle on the union of their suites, as a join
// would on one instance; neither overwrites the other.
func TestSuites_ConcurrentUploadsOfOnePathUnionTheirSuites(t *testing.T) {
	e := newCtlEnv(t, "lines-same-path", "")
	r := e.envWith(sameFileElsewhere{e.comps})
	deb := debFor(t, "foo", "1.0", "amd64", "", "x")
	require.Equal(t, http.StatusCreated, putVia(r, "/repository/lines-same-path/pool/main/foo_1.0_amd64.deb?suite=one", bytes.NewReader(deb), int64(len(deb))).Code)
	assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, "one", "amd64"))
	assert.Equal(t, []string{"foo 1.0"}, e.packagesOf(t, "two", "amd64"))
}

func TestSuites_TokenNamesItsPath(t *testing.T) {
	assert.Equal(t, "/pool/main/a:b.deb", tokenPath(newUploadToken("/pool/main/a:b.deb")))
	assert.Empty(t, tokenPath(suitesByChange))
	assert.Empty(t, tokenPath(""))
}
