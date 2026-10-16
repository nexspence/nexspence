package formats_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

func TestCanonicalPath_ReturnsServedSpelling(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "/",
		"/":                      "/",
		"/a/b.jar":               "/a/b.jar",
		"a/b.jar":                "/a/b.jar",
		"/simple/requests/":      "/simple/requests",
		"/ok/we..ird.txt":        "/ok/we..ird.txt",
		"/.well-known/x.json":    "/.well-known/x.json",
		"/100%.txt":              "/100%.txt",
		"/@types/node":           "/@types/node",
		"/v2/library/alpine/x/":  "/v2/library/alpine/x",
		"/odd%zzname/file.txt":   "/odd%zzname/file.txt",
		"/name%2Bplus/file.txt":  "/name%2Bplus/file.txt",
		"/name%20space/file.txt": "/name%20space/file.txt",
	} {
		got, err := formats.CanonicalPath(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
}

// Every spelling a format handler's path.Clean would rewrite names an artifact
// a second way, and a check judged on the first name does not hold for the
// second. Double-encoded dots matter even though nothing here decodes twice:
// the proxy hands the path to an upstream that decodes it once more.
func TestCanonicalPath_RefusesSecondSpelling(t *testing.T) {
	for _, in := range []string{
		"//private/x",
		"/a//b",
		"/./private/x",
		"/a/.",
		"/a/..",
		"/a/../b",
		"/%2e/x",
		"/%2E%2e/x",
		"/.%2e/x",
		"/%252e%252e/x",
		"/%25252e/x",
		"/%2Fprivate/x",
		"/%2fprivate/x",
		"/a%252Fb/x",
	} {
		_, err := formats.CanonicalPath(in)
		assert.ErrorIs(t, err, formats.ErrNonCanonicalPath, in)
	}
}
