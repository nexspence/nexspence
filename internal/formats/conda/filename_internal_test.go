package conda

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMetaFromFilename(t *testing.T) {
	for file, want := range map[string][3]string{
		"scikit-learn-1.3.0-py311_0.tar.bz2":       {"scikit-learn", "1.3.0", "py311_0"},
		"python-dateutil-2.8.2-pyhd8ed1ab_0.conda": {"python-dateutil", "2.8.2", "pyhd8ed1ab_0"},
		"numpy-1.26.0-py311h_1.conda":              {"numpy", "1.26.0", "py311h_1"},
		"pkg-1.0.conda":                            {"pkg", "1.0", ""},
		"pkg.conda":                                {"pkg", "", ""},
		"pkg-1.0-.conda":                           {"pkg-1.0-", "", ""},
		"-1.0-0.conda":                             {"-1.0", "0", ""},
	} {
		m := metaFromFilename(file)
		assert.Equal(t, want, [3]string{m.Name, m.Version, m.Build}, file)
	}
}
