package helm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

// An unquoted created decodes to time.Time; both spellings are dates.
func TestChartCreated_Spellings(t *testing.T) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte("a: 2024-01-02T03:04:05Z\nb: \"2024-01-02T03:04:05.123456789Z\"\nc: soon\n"), &doc); err != nil {
		t.Fatal(err)
	}
	ta, ok := chartCreated(doc["a"])
	assert.True(t, ok)
	assert.Equal(t, 2024, ta.Year())
	_, ok = chartCreated(doc["b"])
	assert.True(t, ok)
	_, ok = chartCreated(doc["c"])
	assert.False(t, ok)
	_, ok = chartCreated(nil)
	assert.False(t, ok)
	_, ok = chartCreated(time.Time{})
	assert.False(t, ok)
}

// A document without usable entries is left alone.
func TestFilterIndexByAge_OddShapes(t *testing.T) {
	assert.Nil(t, filterIndexByAge(map[string]any{"apiVersion": "v1"}, time.Now()))
	doc := map[string]any{"entries": map[string]any{"weird": "not-a-list", "mixed": []any{"x", map[string]any{"created": "2001-01-01T00:00:00Z"}}}}
	assert.Empty(t, filterIndexByAge(doc, time.Now()))
	entries := doc["entries"].(map[string]any)
	assert.Equal(t, "not-a-list", entries["weird"])
	assert.Len(t, entries["mixed"], 1)
}
