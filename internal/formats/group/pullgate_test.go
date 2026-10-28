package group_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Pull gate through a group (#641): the gate of whichever member answers holds
// inside the group, and a group's own gate applies to every member — so the
// same image cannot be pulled around a gate by asking a group instead.

func withPullGate(r *domain.Repository, severity string) *domain.Repository {
	if r.FormatConfig == nil {
		r.FormatConfig = map[string]any{}
	}
	r.FormatConfig[domain.PullBlockSeverityKey] = severity
	return r
}

type gateGroup struct {
	r      *gin.Engine
	assets *testutil.AssetRepo
	scans  *testutil.ScanResultRepo
}

func newGateGroup(repos ...*domain.Repository) *gateGroup {
	g := &gateGroup{assets: testutil.NewAssetRepo(), scans: testutil.NewScanResultRepo()}
	g.r = ociGroupEngineWithDeps(func(d *formats.Deps) {
		d.Assets = g.assets
		d.ScanResults = g.scans
	}, repos...)
	return g
}

func (g *gateGroup) scan(t *testing.T, repoName, imageName, reference string, s domain.ScanSummary) {
	t.Helper()
	a, err := g.assets.GetByPath(context.Background(), repoName, "/manifests/"+imageName+"/"+reference)
	require.NoError(t, err)
	require.NoError(t, g.scans.Insert(context.Background(), &domain.ScanResultRow{
		ComponentID: a.ComponentID, Scanner: "trivy", Status: domain.ScanStatusOK,
		Critical: s.Critical, High: s.High, Medium: s.Medium, ScannedAt: time.Now(),
	}))
}

func (g *gateGroup) pull(method, repoName, reference string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	g.r.ServeHTTP(w, httptest.NewRequest(method, "/repository/"+repoName+"/v2/img/manifests/"+reference, nil))
	return w
}

// The gated member answers first and its refusal is relayed — the group does
// not fall through to an ungated member holding the same image.
func TestGroupPullGate_MemberRefusalIsNotBypassed(t *testing.T) {
	g := newGateGroup(ociGroup("grp", "gated", "open"),
		withPullGate(hostedOCI("gated"), "high"), hostedOCI("open"))
	pushOCIManifest(t, g.r, "gated", "img", "1.0", ociImageManifest)
	pushOCIManifest(t, g.r, "open", "img", "1.0", ociImageManifest)
	g.scan(t, "gated", "img", "1.0", domain.ScanSummary{Critical: 1})

	w := g.pull(http.MethodGet, "grp", "1.0")
	assert.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"DENIED"`)
	assert.Equal(t, "gated", w.Header().Get("X-Nexspence-Source"))

	h := g.pull(http.MethodHead, "grp", "1.0")
	assert.Equal(t, http.StatusForbidden, h.Code)
	assert.Empty(t, h.Body.String())
}

// A group's gate applies to its members even when they carry none; the
// members addressed directly keep today's behavior.
func TestGroupPullGate_GroupGateCoversMembers(t *testing.T) {
	g := newGateGroup(withPullGate(ociGroup("grp", "m1"), "medium"), hostedOCI("m1"))
	pushOCIManifest(t, g.r, "m1", "img", "1.0", ociImageManifest)

	// Unscanned on a hosted member: refused through the group, served direct.
	w := g.pull(http.MethodGet, "grp", "1.0")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "no finished vulnerability scan")
	assert.Equal(t, http.StatusOK, g.pull(http.MethodGet, "m1", "1.0").Code)

	g.scan(t, "m1", "img", "1.0", domain.ScanSummary{Medium: 2})
	w = g.pull(http.MethodGet, "grp", "1.0")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), `repository \"grp\"`, "the refusal names the repository whose gate applied")
	assert.Equal(t, http.StatusOK, g.pull(http.MethodGet, "m1", "1.0").Code)
}

// The stricter of the group's and the member's thresholds applies.
func TestGroupPullGate_StricterThresholdWins(t *testing.T) {
	g := newGateGroup(withPullGate(ociGroup("grp", "m1"), "critical"), withPullGate(hostedOCI("m1"), "medium"))
	pushOCIManifest(t, g.r, "m1", "img", "1.0", ociImageManifest)
	g.scan(t, "m1", "img", "1.0", domain.ScanSummary{High: 1})

	w := g.pull(http.MethodGet, "grp", "1.0")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), `repository \"m1\"`)
}

// Through a proxy member, an image nobody scanned yet is fetched: the first
// pull is what brings it in.
func TestGroupPullGate_ProxyMemberServesUnscanned(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/img/manifests/1.0" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write([]byte(ociImageManifest))
	}))
	defer upstream.Close()
	g := newGateGroup(withPullGate(ociGroup("grp", "px"), "high"), proxyOCI("px", upstream.URL))

	w := g.pull(http.MethodGet, "grp", "1.0")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	g.scan(t, "px", "img", "1.0", domain.ScanSummary{Critical: 1})
	assert.Equal(t, http.StatusForbidden, g.pull(http.MethodGet, "grp", "1.0").Code)
}
