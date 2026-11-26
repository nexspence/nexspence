package base_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// The path a handler passes already starts with a slash, so joining it to the
// repository name with one more wrote "yum-hosted//Packages/…" — a path that
// is not the one the client asked for, and that a reader has to work out
// before they can trust it.
func TestFetchArtifact_NotFoundNamesThePathWithOneSlash(t *testing.T) {
	repo := testutil.SimpleRepo("yum-hosted", "yum")
	d, _, _, _ := deps(repo)

	_, _, err := base.FetchArtifact(context.Background(), d, "yum-hosted", "/Packages/n/nosuch-1.0-1.x86_64.rpm")
	require.Error(t, err)
	assert.Equal(t, "not found: yum-hosted/Packages/n/nosuch-1.0-1.x86_64.rpm", err.Error())
	assert.NotContains(t, err.Error(), "//")
}

// A path without a leading slash is joined with one.
func TestFetchArtifact_NotFoundJoinsAPathWithoutALeadingSlash(t *testing.T) {
	repo := testutil.SimpleRepo("raw-hosted", "raw")
	d, _, _, _ := deps(repo)

	_, _, err := base.FetchArtifact(context.Background(), d, "raw-hosted", "file.txt")
	require.Error(t, err)
	assert.Equal(t, "not found: raw-hosted/file.txt", err.Error())
}

// A repository name that ends with a slash does not produce a doubled one.
func TestFetchArtifact_NotFoundJoinsARepositoryNameWithATrailingSlash(t *testing.T) {
	repo := testutil.SimpleRepo("raw-hosted", "raw")
	d, _, _, _ := deps(repo)

	_, _, err := base.FetchArtifact(context.Background(), d, "raw-hosted/", "/file.txt")
	require.Error(t, err)
	assert.Equal(t, "not found: raw-hosted/file.txt", err.Error())
}
