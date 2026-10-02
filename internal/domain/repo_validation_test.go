package domain_test

import (
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

func TestRepoFormat_Valid(t *testing.T) {
	for _, f := range domain.AllFormats {
		if !f.Valid() {
			t.Errorf("%q is a served format", f)
		}
	}
	for _, f := range []domain.RepoFormat{"", "apk", "foo", "Docker"} {
		if f.Valid() {
			t.Errorf("%q is not a served format", f)
		}
	}
}

func TestRepoType_Valid(t *testing.T) {
	for _, typ := range []domain.RepoType{domain.TypeHosted, domain.TypeProxy, domain.TypeGroup} {
		if !typ.Valid() {
			t.Errorf("%q is a repository type", typ)
		}
	}
	for _, typ := range []domain.RepoType{"", "virtual", "Hosted"} {
		if typ.Valid() {
			t.Errorf("%q is not a repository type", typ)
		}
	}
}

func TestIsAddressableName(t *testing.T) {
	for _, name := range []string{"raw-hosted", "ok.name-1", "My.Repo_2", "a..b", "ünicode"} {
		if !domain.IsAddressableName(name) {
			t.Errorf("%q should be addressable", name)
		}
	}
	for _, name := range []string{".", "..", "a/b", `a\b`, "a?b", "a#b", "a%2", "a b", "a\tb", "a b", "a\x00b", "a\x7fb"} {
		if domain.IsAddressableName(name) {
			t.Errorf("%q should not be addressable", name)
		}
	}
}
