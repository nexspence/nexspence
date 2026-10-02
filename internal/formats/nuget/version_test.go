package nuget_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nexspence-oss/nexspence/internal/formats/nuget"
)

func TestNormalizeVersion(t *testing.T) {
	for in, want := range map[string]string{
		"1.0.0":             "1.0.0",
		"1.0":               "1.0.0",
		"1":                 "1.0.0",
		"1.0.0.0":           "1.0.0",
		"1.0.0.1":           "1.0.0.1",
		"1.02.0.0-Beta+abc": "1.2.0-Beta",
		"01.002.0003":       "1.2.3",
		"1.0.0-Beta":        "1.0.0-Beta",
		"1.0.0-rc.1+build":  "1.0.0-rc.1",
		"2.0-preview-2":     "2.0.0-preview-2",
		"not.a.version":     "not.a.version",
		"1.2.3.4.5":         "1.2.3.4.5",
		"":                  "",
	} {
		assert.Equal(t, want, nuget.NormalizeVersion(in), in)
	}
}
