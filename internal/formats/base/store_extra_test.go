package base_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Storing a file upserts its component. The component's extra survives unless
// the write replaces different bytes at a path of that component (#668).

func storeExtraFile(t *testing.T, ctx context.Context, d extraDeps, path, content string, coords base.Coords) *domain.Asset {
	t.Helper()
	res, err := base.StoreArtifact(ctx, d.Deps, "extra-repo", path, "application/octet-stream",
		coords, strings.NewReader(content), int64(len(content)))
	require.NoError(t, err)
	return res.Asset
}

type extraDeps struct {
	Deps  formats.Deps
	Comps *testutil.ComponentRepo
}

func newExtraDeps() extraDeps {
	d, _, comps, _ := deps(testutil.SimpleRepo("extra-repo", "maven2"))
	return extraDeps{Deps: d, Comps: comps}
}

func componentExtra(t *testing.T, ctx context.Context, d extraDeps, id string) map[string]any {
	t.Helper()
	comp, err := d.Comps.Get(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, comp)
	return comp.Extra
}

var lib = base.Coords{Group: "com.acme", Name: "lib", Version: "1.0"}

func TestStoreArtifact_SecondFileKeepsComponentExtra(t *testing.T) {
	ctx := context.Background()
	d := newExtraDeps()
	jar := storeExtraFile(t, ctx, d, "/com/acme/lib/1.0/lib-1.0.jar", "jar", lib)
	require.NoError(t, d.Comps.UpdateExtra(ctx, jar.ComponentID,
		map[string]any{"scan_result": map[string]any{"status": "ok"}, "k": "v"}))

	pom := storeExtraFile(t, ctx, d, "/com/acme/lib/1.0/lib-1.0.pom", "pom", lib)
	require.Equal(t, jar.ComponentID, pom.ComponentID)
	extra := componentExtra(t, ctx, d, pom.ComponentID)
	assert.Equal(t, "v", extra["k"])
	assert.NotNil(t, extra["scan_result"], "another file of the release does not invalidate its scan")
}

func TestStoreArtifact_SameBytesAgainKeepsComponentExtra(t *testing.T) {
	ctx := context.Background()
	d := newExtraDeps()
	jar := storeExtraFile(t, ctx, d, "/com/acme/lib/1.0/lib-1.0.jar", "jar", lib)
	require.NoError(t, d.Comps.UpdateExtra(ctx, jar.ComponentID, map[string]any{"k": "v"}))

	again := storeExtraFile(t, ctx, d, "/com/acme/lib/1.0/lib-1.0.jar", "jar", lib)
	assert.Equal(t, "v", componentExtra(t, ctx, d, again.ComponentID)["k"])
}

func TestStoreArtifact_ChangedBytesResetComponentExtra(t *testing.T) {
	ctx := context.Background()
	d := newExtraDeps()
	jar := storeExtraFile(t, ctx, d, "/com/acme/lib/1.0/lib-1.0.jar", "jar", lib)
	require.NoError(t, d.Comps.UpdateExtra(ctx, jar.ComponentID,
		map[string]any{"scan_result": map[string]any{"status": "ok"}, "k": "v"}))

	changed := storeExtraFile(t, ctx, d, "/com/acme/lib/1.0/lib-1.0.jar", "rebuilt jar", lib)
	assert.Empty(t, componentExtra(t, ctx, d, changed.ComponentID),
		"metadata about the replaced bytes must not describe the new ones")
}

// Changed bytes at a path recorded under other coordinates: the component the
// write names did not hold those bytes, so its metadata — about its other
// files — stays.
func TestStoreArtifact_ChangedBytesUnderOtherCoordsKeepTargetExtra(t *testing.T) {
	ctx := context.Background()
	d := newExtraDeps()
	v2 := base.Coords{Group: "com.acme", Name: "lib", Version: "2.0"}
	storeExtraFile(t, ctx, d, "/shared/file.bin", "old", lib)
	other := storeExtraFile(t, ctx, d, "/com/acme/lib/2.0/lib-2.0.jar", "jar2", v2)
	require.NoError(t, d.Comps.UpdateExtra(ctx, other.ComponentID, map[string]any{"k": "v2"}))

	storeExtraFile(t, ctx, d, "/shared/file.bin", "new", v2)
	assert.Equal(t, "v2", componentExtra(t, ctx, d, other.ComponentID)["k"])
}

// Deleting a file can leave its component behind with no assets; the next
// store into it is new content, not a sibling, and starts from clean metadata.
func TestStoreArtifact_EmptiedComponentResetsExtra(t *testing.T) {
	ctx := context.Background()
	d := newExtraDeps()
	jar := storeExtraFile(t, ctx, d, "/com/acme/lib/1.0/lib-1.0.jar", "jar", lib)
	require.NoError(t, d.Comps.UpdateExtra(ctx, jar.ComponentID, map[string]any{"k": "v"}))
	require.NoError(t, base.DeleteArtifact(ctx, d.Deps, "extra-repo", "/com/acme/lib/1.0/lib-1.0.jar"))

	rebuilt := storeExtraFile(t, ctx, d, "/com/acme/lib/1.0/lib-1.0.jar", "rebuilt", lib)
	require.Equal(t, jar.ComponentID, rebuilt.ComponentID, "the component outlived its only file")
	assert.Empty(t, componentExtra(t, ctx, d, rebuilt.ComponentID))
}

// An asset whose component cannot be loaded is attributed to nothing; its
// replacement starts from clean metadata rather than keep a guess.
func TestStoreArtifact_UnattributableReplacedPathResetsExtra(t *testing.T) {
	ctx := context.Background()
	d := newExtraDeps()
	jar := storeExtraFile(t, ctx, d, "/com/acme/lib/1.0/lib-1.0.jar", "jar", lib)
	require.NoError(t, d.Comps.UpdateExtra(ctx, jar.ComponentID, map[string]any{"k": "v"}))
	storeExtraFile(t, ctx, d, "/com/acme/lib/1.0/lib-1.0.pom", "pom", lib)
	d.Comps.GetErr = assert.AnError

	changed := storeExtraFile(t, ctx, d, "/com/acme/lib/1.0/lib-1.0.jar", "rebuilt", lib)
	d.Comps.GetErr = nil
	assert.Empty(t, componentExtra(t, ctx, d, changed.ComponentID))
}
