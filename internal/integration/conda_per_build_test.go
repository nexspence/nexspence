//go:build integration

package integration

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository/postgres"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// condaPkg builds a .conda whose info-*.tar.zst member holds index.
func condaPkg(t *testing.T, stem string, index map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(index)
	require.NoError(t, err)
	var tb bytes.Buffer
	tw := tar.NewWriter(&tb)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "info/index.json", Mode: 0o644, Size: int64(len(raw))}))
	_, err = tw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	var zb bytes.Buffer
	enc, err := zstd.NewWriter(&zb)
	require.NoError(t, err)
	_, err = enc.Write(tb.Bytes())
	require.NoError(t, err)
	require.NoError(t, enc.Close())
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	w, err := zw.Create("info-" + stem + ".tar.zst")
	require.NoError(t, err)
	_, err = w.Write(zb.Bytes())
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return out.Bytes()
}

func acmeIndex(build, py string) map[string]any {
	return map[string]any{"name": "acme-lib", "version": "1.0", "build": build, "build_number": 0,
		"subdir": "linux-64", "depends": []string{"python " + py + ".*"}}
}

func condaRepodata(t *testing.T, token, repo string) map[string]map[string]any {
	t.Helper()
	code, body := getBody(t, token, "/repository/"+repo+"/linux-64/repodata.json")
	require.Equal(t, http.StatusOK, code)
	var doc struct {
		PackagesConda map[string]map[string]any `json:"packages.conda"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &doc))
	return doc.PackagesConda
}

// #677 over PostgreSQL: builds of one version each get their own entry, and
// files stored before the change are repaired when the index is read.
func TestCondaHosted_EveryBuildListedAndLegacyRepaired(t *testing.T) {
	createHostedRepo(t, "conda", "conda-builds", `{}`)
	token := login(t, "admin", "admin123")
	files := map[string][]byte{
		"/linux-64/acme-lib-1.0-py310_0.conda": condaPkg(t, "acme-lib-1.0-py310_0", acmeIndex("py310_0", "3.10")),
		"/linux-64/acme-lib-1.0-py311_0.conda": condaPkg(t, "acme-lib-1.0-py311_0", acmeIndex("py311_0", "3.11")),
	}
	for p, body := range files {
		sendRaw(t, token, http.MethodPut, "/repository/conda-builds"+p, "application/zip", body)
	}
	pkgs := condaRepodata(t, token, "conda-builds")
	require.Len(t, pkgs, 2)
	assert.Equal(t, []any{"python 3.10.*"}, pkgs["acme-lib-1.0-py310_0.conda"]["depends"])
	assert.Equal(t, []any{"python 3.11.*"}, pkgs["acme-lib-1.0-py311_0.conda"]["depends"])

	// Rewrite into the pre-#677 shape: one component per version holding both
	// files and the last upload's metadata.
	ctx := context.Background()
	pool := pgtest.Pool(t)
	comps, assets := postgres.NewComponentRepo(pool), postgres.NewAssetRepo(pool)
	first, err := assets.GetByPath(ctx, "conda-builds", "/linux-64/acme-lib-1.0-py310_0.conda")
	require.NoError(t, err)
	legacy := &domain.Component{RepositoryID: first.RepositoryID, Format: "conda", Group: "linux-64", Name: "acme-lib", Version: "1.0",
		Extra: map[string]any{"build": "py311_0", "build_number": 0, "depends": []string{"python 3.11.*"}}}
	require.NoError(t, comps.Create(ctx, legacy))
	for p := range files {
		a, err := assets.GetByPath(ctx, "conda-builds", p)
		require.NoError(t, err)
		require.NoError(t, assets.SetComponent(ctx, a.ID, legacy.ID))
	}
	require.NoError(t, comps.DeleteOrphans(ctx, "conda-builds"))

	pkgs = condaRepodata(t, token, "conda-builds")
	require.Len(t, pkgs, 2)
	assert.Equal(t, "py310_0", pkgs["acme-lib-1.0-py310_0.conda"]["build"])
	assert.Equal(t, []any{"python 3.10.*"}, pkgs["acme-lib-1.0-py310_0.conda"]["depends"], "read from the file itself")
	got, err := comps.Get(ctx, legacy.ID)
	assert.True(t, err != nil || got == nil, "the emptied shared component is gone")
}
