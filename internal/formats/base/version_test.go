package base_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nexspence-oss/nexspence/internal/formats/base"
)

func TestCompareLooseVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0", "1.0", 0},
		{"1.0", "2.0", -1},
		{"2.0", "1.0", 1},
		{"1.10", "1.9", 1},         // numeric, not lexicographic
		{"1.0", "1.0-SNAPSHOT", 1}, // numeric beats qualifier
		{"1.0-alpha", "1.0-beta", -1},
		{"", "1.0", -1},
		{"1.0.1", "1.0", 1}, // longer wins on equal prefix
		{"v1.2.0", "v1.10.0", -1},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, base.CompareLooseVersions(tc.a, tc.b), "%s vs %s", tc.a, tc.b)
	}
}
