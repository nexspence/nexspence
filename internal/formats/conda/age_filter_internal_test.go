package conda

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEntryMillis(t *testing.T) {
	ms, ok := entryMillis(float64(1700000000000))
	assert.True(t, ok)
	assert.Equal(t, int64(1700000000000), ms)
	ms, ok = entryMillis(float64(1700000000)) // seconds, as old channels wrote
	assert.True(t, ok)
	assert.Equal(t, int64(1700000000000), ms)
	ms, ok = entryMillis(json.Number("1700000000000"))
	assert.True(t, ok)
	assert.Equal(t, int64(1700000000000), ms)
	for _, v := range []any{nil, "1700000000000", json.Number("x"), float64(0), float64(-5), math.NaN(), math.Inf(1)} {
		_, ok := entryMillis(v)
		assert.False(t, ok, "%v", v)
	}
}

func TestNameFromFilename(t *testing.T) {
	assert.Equal(t, "python-dateutil", nameFromFilename("python-dateutil-2.9.0-pyhd8ed1ab_0.conda"))
	assert.Equal(t, "numpy", nameFromFilename("numpy-1.26.4-py312h8753938_0.tar.bz2"))
	assert.Equal(t, "odd", nameFromFilename("odd.conda"))
	assert.Equal(t, "pkg", entryName("pkg-1-0.conda", map[string]any{}))
}

// The streaming reader decodes the same table as the parsed document, skips
// fields it does not need and rejects a body that is not repodata.
func TestAgeTableFromStream(t *testing.T) {
	body := `{"info":{"subdir":"noarch"},"packages":{"a-1-0.tar.bz2":{"name":"a","timestamp":1700000000000},` +
		`"b-1-0.tar.bz2":{"version":"1"}},"packages.conda":{"a-2-0.conda":{"name":"a"}},"removed":["x"],"repodata_version":1}`
	tbl, err := ageTableFromStream(strings.NewReader(body), "https://example.invalid/chan", "noarch")
	require.NoError(t, err)
	assert.True(t, tbl.dated)
	assert.Equal(t, int64(1700000000000), tbl.ts[pathKey("noarch/a-1-0.tar.bz2")])
	assert.Equal(t, ageUndatedRefused, tbl.ts[pathKey("noarch/a-2-0.conda")])
	assert.Equal(t, ageUndatedAllowed, tbl.ts[pathKey("noarch/b-1-0.tar.bz2")])

	for _, bad := range []string{``, `[]`, `{"packages":[]}`, `{"packages":{"a":`, `{"info":`, `{"packages":{"a":{}}`, `{"packages":{1`} {
		_, err := ageTableFromStream(strings.NewReader(bad), "https://example.invalid/chan", "noarch")
		assert.Error(t, err, bad)
	}
}

// An entry whose url leaves the channel is fetched by the client directly and
// never reaches the gate.
func TestAgeTable_OffChannelURLSkipped(t *testing.T) {
	doc := map[string]any{"packages": map[string]any{
		"a-1-0.tar.bz2": map[string]any{"name": "a", "timestamp": float64(1700000000000), "url": "https://cdn.invalid/a-1-0.tar.bz2"},
		"junk":          "not-an-entry",
	}}
	tbl := ageTableFromDoc(doc, "https://example.invalid/chan", "noarch")
	assert.Empty(t, tbl.ts)
	assert.Empty(t, filterRepodataByAge(doc, 0))
}

func TestAgeTables_EvictsLeastRecentlyUsed(t *testing.T) {
	var a ageTables
	first := a.slot("k0")
	for i := 1; i <= maxAgeTables; i++ {
		a.slot(fmt.Sprintf("k%d", i))
	}
	assert.Len(t, a.slots, maxAgeTables)
	assert.NotSame(t, first, a.slot("k0"), "the oldest slot was dropped")
}
