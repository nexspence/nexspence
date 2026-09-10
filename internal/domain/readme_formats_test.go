package domain_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// The README states a format count and lists the formats in a table, and both
// drifted three formats behind the code before anyone noticed (#438) — the
// claim is only ever read by humans, and a new format's PR touches the docs
// site (which is tested, see site_formats_test.go) but not this file. Same
// guard, applied to the README.

const readmePath = "../../README.md"

func readmeText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	return string(b)
}

// readmeFormatTable returns the rows of the "Supported Package Formats" table,
// each as its display name (the first cell).
func readmeFormatTable(t *testing.T) []string {
	t.Helper()
	text := readmeText(t)
	start := strings.Index(text, "## Supported Package Formats")
	if start < 0 {
		t.Fatal("README has no \"Supported Package Formats\" section")
	}
	section := text[start:]
	if end := strings.Index(section, "\n---"); end > 0 {
		section = section[:end]
	}

	var names []string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		name := strings.TrimSpace(cells[0])
		if name == "Format" || strings.HasPrefix(name, "---") || strings.HasPrefix(name, ":--") {
			continue // header and separator rows
		}
		names = append(names, name)
	}
	return names
}

func TestREADMEFormatCountMatchesCode(t *testing.T) {
	m := regexp.MustCompile(`\*\*(\d+) package formats\*\*`).FindStringSubmatch(readmeText(t))
	if m == nil {
		t.Fatal("README states no package format count")
	}
	got, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("unparsable count %q", m[1])
	}
	if want := len(domain.AllFormats); got != want {
		t.Errorf("README claims %d package formats, code supports %d", got, want)
	}
}

func TestREADMEFormatTableListsEveryFormat(t *testing.T) {
	rows := readmeFormatTable(t)
	if len(rows) != len(domain.AllFormats) {
		t.Errorf("README formats table has %d rows, code supports %d formats", len(rows), len(domain.AllFormats))
	}

	// The table uses display names, so each format is matched on a substring
	// its row is expected to carry — same approach as the docs site's own test.
	labels := map[domain.RepoFormat]string{
		domain.FormatMaven2:    "Maven",
		domain.FormatNPM:       "npm",
		domain.FormatPyPI:      "PyPI",
		domain.FormatDocker:    "Docker",
		domain.FormatOCI:       "OCI",
		domain.FormatGo:        "Go modules",
		domain.FormatNuGet:     "NuGet",
		domain.FormatHelm:      "Helm",
		domain.FormatCargo:     "Cargo",
		domain.FormatApt:       "APT",
		domain.FormatYum:       "Yum",
		domain.FormatConan:     "Conan",
		domain.FormatRaw:       "Raw",
		domain.FormatConda:     "Conda",
		domain.FormatTerraform: "Terraform",
		domain.FormatRubyGems:  "RubyGems",
		domain.FormatCRAN:      "CRAN",
	}
	for _, f := range domain.AllFormats {
		label, ok := labels[f]
		if !ok {
			t.Errorf("format %q has no expected README table label — add one when adding the format", f)
			continue
		}
		found := false
		for _, name := range rows {
			if strings.Contains(name, label) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("format %q (%s) is missing from the README formats table", f, label)
		}
	}
}
