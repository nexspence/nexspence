package service

import "testing"

// translateNexusSelectorExpression: Nexus's CSEL dialect → the CEL dialect
// selectors are evaluated under here (#342).
func TestTranslateNexusSelectorExpression(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain CEL untouched",
			`format == "maven2" && path.startsWith("/org/")`,
			`format == "maven2" && path.startsWith("/org/")`},
		{"=~ becomes matches()",
			`path =~ ".*-SNAPSHOT.*"`,
			`path.matches(".*-SNAPSHOT.*")`},
		{"backslash doubled into a valid CEL literal",
			`format == "maven2" && path =~ ".*maven-metadata\.xml.*"`,
			`format == "maven2" && path.matches(".*maven-metadata\\.xml.*")`},
		{"multiple occurrences",
			`path =~ "a\d+" || path =~ "b"`,
			`path.matches("a\\d+") || path.matches("b")`},
		{"no spaces around operator",
			`path=~"x"`,
			`path.matches("x")`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := translateNexusSelectorExpression(tc.in); got != tc.want {
				t.Fatalf("translate(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
