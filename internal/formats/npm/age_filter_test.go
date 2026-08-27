package npm_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats/npm"
)

// Minimum-package-age filtering of an npm packument (#323): versions published
// after the cutoff disappear from the document a client resolves against.

var ageCutoff = time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)

func packument(t *testing.T, distTags map[string]string, times map[string]string) []byte {
	t.Helper()
	versions := map[string]any{}
	for v := range times {
		if v == "created" || v == "modified" {
			continue
		}
		versions[v] = map[string]any{"name": "pkg", "version": v, "dist": map[string]any{"tarball": "https://reg/pkg/-/pkg-" + v + ".tgz"}}
	}
	doc := map[string]any{"name": "pkg", "dist-tags": distTags, "versions": versions, "time": times}
	b, err := json.Marshal(doc)
	require.NoError(t, err)
	return b
}

func parse(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	require.NoError(t, json.Unmarshal(b, &doc))
	return doc
}

func TestFilterPackumentByAge_HidesYoungVersions(t *testing.T) {
	body := packument(t,
		map[string]string{"latest": "2.0.0"},
		map[string]string{
			"created":  "2026-01-01T00:00:00Z",
			"modified": "2026-08-25T00:00:00Z",
			"1.0.0":    "2026-01-01T00:00:00Z",
			"1.5.0":    "2026-06-01T00:00:00Z",
			"2.0.0":    "2026-08-25T00:00:00Z", // after the cutoff — too young
		})

	out, applied := npm.FilterPackumentByAge(body, ageCutoff)
	require.True(t, applied, "a dated packument must have the policy applied")
	doc := parse(t, out)

	versions := doc["versions"].(map[string]any)
	assert.Contains(t, versions, "1.0.0")
	assert.Contains(t, versions, "1.5.0")
	assert.NotContains(t, versions, "2.0.0", "a version younger than the cutoff must disappear")

	times := doc["time"].(map[string]any)
	assert.NotContains(t, times, "2.0.0", "the time entry goes with the version")
	assert.Contains(t, times, "created")

	// The latest tag pointed at the hidden version: it must fall back to the
	// newest surviving one, or `npm install pkg` breaks entirely.
	tags := doc["dist-tags"].(map[string]any)
	assert.Equal(t, "1.5.0", tags["latest"])
}

func TestFilterPackumentByAge_KeepsIntactWhenNothingIsYoung(t *testing.T) {
	body := packument(t,
		map[string]string{"latest": "1.5.0"},
		map[string]string{"1.0.0": "2026-01-01T00:00:00Z", "1.5.0": "2026-06-01T00:00:00Z"})

	out, applied := npm.FilterPackumentByAge(body, ageCutoff)
	require.True(t, applied)
	doc := parse(t, out)
	assert.Len(t, doc["versions"].(map[string]any), 2)
	assert.Equal(t, "1.5.0", doc["dist-tags"].(map[string]any)["latest"])
}

// Hybrid failure mode: a version listed in a DATED document but absent from
// its time map has an unknowable age — fail closed and hide it.
func TestFilterPackumentByAge_UndatedVersionInDatedDocIsHidden(t *testing.T) {
	body := packument(t,
		map[string]string{"latest": "1.0.0"},
		map[string]string{"1.0.0": "2026-01-01T00:00:00Z"})
	// Splice in a version with no time entry.
	doc := parse(t, body)
	doc["versions"].(map[string]any)["9.9.9"] = map[string]any{"name": "pkg", "version": "9.9.9"}
	spliced, err := json.Marshal(doc)
	require.NoError(t, err)

	out, applied := npm.FilterPackumentByAge(spliced, ageCutoff)
	require.True(t, applied)
	got := parse(t, out)
	assert.NotContains(t, got["versions"].(map[string]any), "9.9.9",
		"an undated version in a dated document fails closed")
}

// Hybrid failure mode: a document with no usable dates at all is served
// unchanged — hiding the whole package would be worse than no policy — and the
// caller is told the policy was NOT applied so it can log the gap.
func TestFilterPackumentByAge_NoDatesAtAll_SkipsPolicy(t *testing.T) {
	doc := map[string]any{
		"name":      "pkg",
		"dist-tags": map[string]string{"latest": "1.0.0"},
		"versions":  map[string]any{"1.0.0": map[string]any{"name": "pkg", "version": "1.0.0"}},
	}
	body, err := json.Marshal(doc)
	require.NoError(t, err)

	out, applied := npm.FilterPackumentByAge(body, ageCutoff)
	assert.False(t, applied, "no dates → policy skipped, caller logs the gap")
	assert.JSONEq(t, string(body), string(out))
}

func TestFilterPackumentByAge_MalformedBodyUnchanged(t *testing.T) {
	body := []byte("not json at all")
	out, applied := npm.FilterPackumentByAge(body, ageCutoff)
	assert.False(t, applied)
	assert.Equal(t, body, out)
}

// Every version young: versions empty, tags dropped — the package looks not
// yet published, which is exactly the quarantine semantic.
func TestFilterPackumentByAge_AllVersionsYoung(t *testing.T) {
	body := packument(t,
		map[string]string{"latest": "2.0.0"},
		map[string]string{"2.0.0": "2026-08-25T00:00:00Z"})

	out, applied := npm.FilterPackumentByAge(body, ageCutoff)
	require.True(t, applied)
	doc := parse(t, out)
	assert.Empty(t, doc["versions"].(map[string]any))
	assert.Empty(t, doc["dist-tags"].(map[string]any))
}
