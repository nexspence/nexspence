package oci_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// imageFixture lays out manifests and blobs of image "app" in repo "r" and
// serves manifest bytes from memory.
type imageFixture struct {
	assets *testutil.AssetRepo
	bodies map[string][]byte // path → manifest bytes
}

func newImageFixture() *imageFixture {
	return &imageFixture{assets: testutil.NewAssetRepo(), bodies: map[string][]byte{}}
}

func (f *imageFixture) manifest(t *testing.T, ref, sha, body string) {
	t.Helper()
	p := "/manifests/app/" + ref
	require.NoError(t, f.assets.Create(context.Background(), &domain.Asset{
		Repository: "r", Path: p, SHA256: sha, BlobKey: "k" + p,
	}))
	f.bodies[p] = []byte(body)
}

func (f *imageFixture) blob(t *testing.T, digest string) {
	t.Helper()
	p := "/blobs/app/" + digest
	require.NoError(t, f.assets.Create(context.Background(), &domain.Asset{Repository: "r", Path: p, BlobKey: "k" + p}))
}

func (f *imageFixture) read(_ context.Context, a *domain.Asset) ([]byte, error) {
	b, ok := f.bodies[a.Path]
	if !ok {
		return nil, errors.New("no bytes")
	}
	return b, nil
}

func image(config string, layers ...string) string {
	s := fmt.Sprintf(`{"config":{"digest":%q},"layers":[`, config)
	for i, l := range layers {
		if i > 0 {
			s += ","
		}
		s += fmt.Sprintf(`{"digest":%q}`, l)
	}
	return s + "]}"
}

func (f *imageFixture) plan(t *testing.T, released map[string]string) []string {
	t.Helper()
	rel := map[string][]byte{}
	for sha, body := range released {
		rel[sha] = []byte(body)
	}
	paths, err := oci.PlanImageRelease(context.Background(), f.assets, f.read, "r", "app", rel)
	require.NoError(t, err)
	return paths
}

// v1 expired; v2 shares the base layer. v1's alias, config and own layer go,
// the base stays.
func TestPlanImageRelease_KeepsSharedLayers(t *testing.T) {
	f := newImageFixture()
	v1, v2 := image("sha256:c1", "sha256:base", "sha256:l1"), image("sha256:c2", "sha256:base", "sha256:l2")
	f.manifest(t, "sha256:m1", "m1", v1)
	f.manifest(t, "sha256:m2", "m2", v2)
	f.manifest(t, "v2", "m2", v2)
	for _, b := range []string{"sha256:c1", "sha256:c2", "sha256:base", "sha256:l1", "sha256:l2"} {
		f.blob(t, b)
	}
	assert.Equal(t, []string{
		"/manifests/app/sha256:m1",
		"/blobs/app/sha256:c1", "/blobs/app/sha256:l1",
	}, f.plan(t, map[string]string{"m1": v1}))
}

// Another tag on the same manifest keeps its alias and blobs (#595).
func TestPlanImageRelease_AliasOfAnotherTagStays(t *testing.T) {
	f := newImageFixture()
	body := image("sha256:c", "sha256:l")
	f.manifest(t, "sha256:m", "m", body)
	f.manifest(t, "latest", "m", body)
	f.blob(t, "sha256:c")
	f.blob(t, "sha256:l")
	assert.Empty(t, f.plan(t, map[string]string{"m": body}))
}

// An expired multi-arch tag releases its index alias, then each child no
// other index names, with their blobs.
func TestPlanImageRelease_IndexReleasesItsChildren(t *testing.T) {
	f := newImageFixture()
	amd, arm := image("sha256:ca", "sha256:la"), image("sha256:cb", "sha256:lb")
	idx1 := `{"manifests":[{"digest":"sha256:amd"},{"digest":"sha256:arm"}]}`
	idx2 := `{"manifests":[{"digest":"sha256:arm"}]}`
	f.manifest(t, "sha256:idx1", "idx1", idx1)
	f.manifest(t, "sha256:amd", "amd", amd)
	f.manifest(t, "sha256:arm", "arm", arm)
	f.manifest(t, "sha256:idx2", "idx2", idx2)
	f.manifest(t, "arm-only", "idx2", idx2)
	for _, b := range []string{"sha256:ca", "sha256:la", "sha256:cb", "sha256:lb"} {
		f.blob(t, b)
	}
	assert.Equal(t, []string{
		"/manifests/app/sha256:amd", "/manifests/app/sha256:idx1",
		"/blobs/app/sha256:ca", "/blobs/app/sha256:la",
	}, f.plan(t, map[string]string{"idx1": idx1}))
}

// A signature naming the manifest as its subject keeps it.
func TestPlanImageRelease_ReferrerSubjectStays(t *testing.T) {
	f := newImageFixture()
	body := image("sha256:c", "sha256:l")
	f.manifest(t, "sha256:m", "m", body)
	f.manifest(t, "sha256:sig", "sig", `{"config":{"digest":"sha256:sc"},"layers":[],"subject":{"digest":"sha256:m"}}`)
	f.blob(t, "sha256:c")
	f.blob(t, "sha256:l")
	assert.Empty(t, f.plan(t, map[string]string{"m": body}))
}

// A manifest that cannot be read leaves nothing safe to drop.
func TestPlanImageRelease_UnreadableManifestIsAnError(t *testing.T) {
	f := newImageFixture()
	f.manifest(t, "sha256:m", "m", "{}")
	delete(f.bodies, "/manifests/app/sha256:m")
	_, err := oci.PlanImageRelease(context.Background(), f.assets, f.read, "r", "app", map[string][]byte{"x": []byte("{}")})
	assert.Error(t, err)
}
