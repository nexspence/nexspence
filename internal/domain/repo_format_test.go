package domain

import "testing"

func TestRepoFormat_IsOCIRegistry(t *testing.T) {
	cases := []struct {
		format RepoFormat
		want   bool
	}{
		{FormatDocker, true},
		{FormatOCI, true},
		{FormatHelm, false},
		{FormatRaw, false},
		{FormatMaven2, false},
		{"", false},
		// The stored value is the canonical lowercase label; anything else is not
		// a format this codebase writes, and the check stays strict.
		{"Docker", false},
		{"OCI", false},
	}
	for _, tc := range cases {
		if got := tc.format.IsOCIRegistry(); got != tc.want {
			t.Errorf("RepoFormat(%q).IsOCIRegistry() = %v, want %v", tc.format, got, tc.want)
		}
	}
}
