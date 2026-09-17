package service

import (
	"testing"

	"github.com/nexspence-oss/nexspence/internal/nexusclient"
)

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

func TestRepoAllowSet(t *testing.T) {
	if repoAllowSet(nil) != nil {
		t.Fatal("nil names means every repository")
	}
	if repoAllowSet([]string{}) != nil {
		t.Fatal("empty names means every repository")
	}
	if repoAllowSet([]string{" ", ""}) != nil {
		t.Fatal("whitespace-only names means every repository")
	}
	got := repoAllowSet([]string{" npm-hosted ", "pypi-hosted", "npm-hosted"})
	if len(got) != 2 {
		t.Fatalf("got %d names, want 2: %v", len(got), got)
	}
	if !repoAllowed(got, "npm-hosted") || !repoAllowed(got, "pypi-hosted") {
		t.Fatalf("missing expected names: %v", got)
	}
	if repoAllowed(got, "raw-hosted") {
		t.Fatal("raw-hosted is not on the allowlist")
	}
	if !repoAllowed(nil, "anything") {
		t.Fatal("nil allowlist admits every name")
	}
}

func TestExpandRepoAllowSet(t *testing.T) {
	source := []nexusclient.Repository{
		{Name: "raw-hosted", Type: "hosted"},
		{Name: "raw-proxy", Type: "proxy"},
		{Name: "inner", Type: "group", MemberNames: []string{"raw-hosted"}},
		{Name: "outer", Type: "group", MemberNames: []string{"inner", "raw-proxy"}},
	}
	if expandRepoAllowSet(nil, source) != nil {
		t.Fatal("nil allowlist stays nil")
	}
	got := expandRepoAllowSet(repoAllowSet([]string{"outer"}), source)
	for _, want := range []string{"outer", "inner", "raw-hosted", "raw-proxy"} {
		if !repoAllowed(got, want) {
			t.Fatalf("missing %s after group expansion: %v", want, got)
		}
	}
	hostedOnly := expandRepoAllowSet(repoAllowSet([]string{"raw-hosted"}), source)
	if len(hostedOnly) != 1 || !repoAllowed(hostedOnly, "raw-hosted") {
		t.Fatalf("hosted-only allowlist must not grow: %v", hostedOnly)
	}
}
