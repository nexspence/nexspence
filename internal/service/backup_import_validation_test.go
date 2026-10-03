package service_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// singleRepoArchive is a repository export holding only repository.json.
func singleRepoArchive(t *testing.T, repo domain.Repository) *bytes.Buffer {
	t.Helper()
	data, err := json.Marshal(repo)
	require.NoError(t, err)
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "repository.json", Mode: 0o600, Size: int64(len(data))}))
	_, err = tw.Write(data)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return &buf
}

// #619: an imported repository passes the same checks as one created
// through the API — name, format and type.
func TestBackup_ImportRepo_ValidatesTheRepository(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		repo   domain.Repository
		target string
	}{
		{"unaddressable archived name", domain.Repository{Name: "a#b", Format: domain.FormatRaw, Type: domain.TypeHosted}, ""},
		{"unaddressable target name", domain.Repository{Name: "ok", Format: domain.FormatRaw, Type: domain.TypeHosted}, "a/b"},
		{"unknown format", domain.Repository{Name: "x", Format: "apk", Type: domain.TypeHosted}, ""},
		{"unknown type", domain.Repository{Name: "x", Format: domain.FormatRaw, Type: "virtual"}, ""},
		{"docker grammar", domain.Repository{Name: "Upper", Format: domain.FormatDocker, Type: domain.TypeHosted}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repos := testutil.NewRepoRepo()
			svc := buildBackupSvc()
			svc.Repos = repos
			_, err := svc.ImportRepo(ctx, singleRepoArchive(t, tc.repo), tc.target, "skip")
			require.Error(t, err)
			assert.True(t, errors.Is(err, service.ErrInvalidInput), "%v", err)
			for _, n := range []string{tc.repo.Name, tc.target} {
				if n != "" {
					got, _ := repos.Get(ctx, n)
					assert.Nil(t, got, "nothing is created: %s", n)
				}
			}
		})
	}
}

// A full restore skips such a repository and reports it, keeping the rest.
func TestBackup_Restore_SkipsInvalidRepository(t *testing.T) {
	ctx := context.Background()
	src := buildBackupSvc(testutil.SimpleRepo("good", "raw"), testutil.SimpleRepo("bad#name", "raw"))
	var buf bytes.Buffer
	require.NoError(t, src.Export(ctx, &buf))

	dst := buildBackupSvc()
	stats, err := dst.Restore(ctx, &buf)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Repos)
	require.Len(t, stats.Failures, 1)
	assert.Equal(t, "bad#name", stats.Failures[0].Name)
	_, err = dst.Repos.Get(ctx, "bad#name")
	assert.Error(t, err)
}
