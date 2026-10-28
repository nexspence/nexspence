//go:build integration

package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1" //nolint:gosec // apk protocol checksum
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository/postgres"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

func gzipTar(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var tb bytes.Buffer
	tw := tar.NewWriter(&tb)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	var gb bytes.Buffer
	gw := gzip.NewWriter(&gb)
	_, err = gw.Write(tb.Bytes())
	require.NoError(t, err)
	require.NoError(t, gw.Close())
	return gb.Bytes()
}

// apkFor returns an .apk for acme-tool 1.0-r0 built for arch, and the Q1
// checksum of its control segment.
func apkFor(t *testing.T, arch string) ([]byte, string) {
	t.Helper()
	control := gzipTar(t, ".PKGINFO", []byte(fmt.Sprintf(
		"pkgname = acme-tool\npkgver = 1.0-r0\narch = %s\nsize = 4096\ndepend = so:libc.musl-%s.so.1\n", arch, arch)))
	sum := sha1.Sum(control) //nolint:gosec // apk protocol checksum
	return append(control, gzipTar(t, "usr/bin/acme-tool", []byte("binary for "+arch))...),
		"Q1" + base64.StdEncoding.EncodeToString(sum[:])
}

func apkIndexText(t *testing.T, token, p string) string {
	t.Helper()
	code, body := getBody(t, token, p)
	require.Equal(t, http.StatusOK, code)
	gz, err := gzip.NewReader(bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	_, err = tr.Next()
	require.NoError(t, err)
	plain, err := io.ReadAll(tr)
	require.NoError(t, err)
	return string(plain)
}

// #671 over PostgreSQL: the same name and version for two architectures gets
// two index entries, each with its own file's checksum and dependencies.
func TestAlpineHosted_EachArchitectureKeepsItsOwnEntry(t *testing.T) {
	createHostedRepo(t, "alpine", "apk-multi", `{}`)
	token := login(t, "admin", "admin123")
	x86, x86Sum := apkFor(t, "x86_64")
	arm, armSum := apkFor(t, "aarch64")
	sendRaw(t, token, http.MethodPut, "/repository/apk-multi/v3.20/main/x86_64/acme-tool-1.0-r0.apk", "application/octet-stream", x86)
	sendRaw(t, token, http.MethodPut, "/repository/apk-multi/v3.20/main/aarch64/acme-tool-1.0-r0.apk", "application/octet-stream", arm)

	x86Index := apkIndexText(t, token, "/repository/apk-multi/v3.20/main/x86_64/APKINDEX.tar.gz")
	assert.Contains(t, x86Index, "C:"+x86Sum+"\n")
	assert.Contains(t, x86Index, "D:so:libc.musl-x86_64.so.1\n")
	armIndex := apkIndexText(t, token, "/repository/apk-multi/v3.20/main/aarch64/APKINDEX.tar.gz")
	assert.Contains(t, armIndex, "C:"+armSum+"\n")
	assert.Contains(t, armIndex, "D:so:libc.musl-aarch64.so.1\n")
}

// toLegacy rewrites the repository into the pre-#671 shape: every path of
// acme-tool 1.0-r0 in one directory-less component holding sharedSum, and no
// per-directory components.
func toLegacy(t *testing.T, repoName string, paths []string, sharedSum string) string {
	t.Helper()
	ctx := context.Background()
	pool := pgtest.Pool(t)
	comps, assets := postgres.NewComponentRepo(pool), postgres.NewAssetRepo(pool)
	first, err := assets.GetByPath(ctx, repoName, paths[0])
	require.NoError(t, err)
	legacy := &domain.Component{RepositoryID: first.RepositoryID, Format: "alpine", Name: "acme-tool", Version: "1.0-r0",
		Extra: map[string]any{"checksum": sharedSum}}
	require.NoError(t, comps.Create(ctx, legacy))
	for _, p := range paths {
		a, err := assets.GetByPath(ctx, repoName, p)
		require.NoError(t, err)
		require.NoError(t, assets.SetComponent(ctx, a.ID, legacy.ID))
	}
	require.NoError(t, comps.DeleteOrphans(ctx, repoName))
	return legacy.ID
}

func componentOfPath(t *testing.T, repoName, p string) string {
	t.Helper()
	a, err := postgres.NewAssetRepo(pgtest.Pool(t)).GetByPath(context.Background(), repoName, p)
	require.NoError(t, err)
	return a.ComponentID
}

// Pre-#671 data over PostgreSQL: reading the index repairs it even where a
// write-once policy refuses the re-upload, and a re-upload moves the path too.
func TestAlpineHosted_LegacySharedComponentIsRepaired(t *testing.T) {
	createHostedRepo(t, "alpine", "apk-legacy", `{"write_policy":"allow_once"}`)
	createHostedRepo(t, "alpine", "apk-legacy-rw", `{}`)
	token := login(t, "admin", "admin123")
	x86, x86Sum := apkFor(t, "x86_64")
	arm, armSum := apkFor(t, "aarch64")
	paths := []string{"/v3.20/main/x86_64/acme-tool-1.0-r0.apk", "/v3.20/main/aarch64/acme-tool-1.0-r0.apk"}

	// Write-once: repaired on read.
	sendRaw(t, token, http.MethodPut, "/repository/apk-legacy"+paths[0], "application/octet-stream", x86)
	sendRaw(t, token, http.MethodPut, "/repository/apk-legacy"+paths[1], "application/octet-stream", arm)
	legacy := toLegacy(t, "apk-legacy", paths, armSum)
	assert.Contains(t, apkIndexText(t, token, "/repository/apk-legacy/v3.20/main/x86_64/APKINDEX.tar.gz"), "C:"+x86Sum+"\n")
	assert.NotEqual(t, legacy, componentOfPath(t, "apk-legacy", paths[0]), "moved to its own component")
	assert.Contains(t, apkIndexText(t, token, "/repository/apk-legacy/v3.20/main/aarch64/APKINDEX.tar.gz"), "C:"+armSum+"\n")
	got, err := postgres.NewComponentRepo(pgtest.Pool(t)).Get(context.Background(), legacy)
	assert.True(t, err != nil || got == nil, "the emptied shared component is gone")

	// Writable: a re-upload moves the path before any read.
	sendRaw(t, token, http.MethodPut, "/repository/apk-legacy-rw"+paths[0], "application/octet-stream", x86)
	sendRaw(t, token, http.MethodPut, "/repository/apk-legacy-rw"+paths[1], "application/octet-stream", arm)
	legacyRW := toLegacy(t, "apk-legacy-rw", paths, armSum)
	sendRaw(t, token, http.MethodPut, "/repository/apk-legacy-rw"+paths[0], "application/octet-stream", x86)
	assert.NotEqual(t, legacyRW, componentOfPath(t, "apk-legacy-rw", paths[0]),
		"the upsert reports the component the path really has, so the move happens")
}
