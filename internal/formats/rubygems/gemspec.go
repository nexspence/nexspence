// Package rubygems implements the RubyGems repository protocol: the compact
// index Bundler resolves against (/versions, /info/<gem>, /names), gem
// downloads (/gems/<file>.gem), publish (POST /api/v1/gems) and yank.
package rubygems

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxGemspecBytes bounds the decompressed metadata read: a gemspec is a few KB
// of YAML, and a .gem is attacker-supplied input on the publish path.
const maxGemspecBytes = 4 << 20

// GemDependency is one runtime dependency as the compact index lists it.
type GemDependency struct {
	Name string
	// Requirements joins the version clauses with "&", the compact-index
	// grammar for a multi-clause requirement (">= 1.0&< 2.0").
	Requirements string
}

// GemSpec is the slice of a gemspec the registry needs: coordinates for
// storage, dependencies and interpreter requirements for the compact index.
type GemSpec struct {
	Name     string
	Version  string
	Platform string // "ruby" for pure-ruby gems
	// Dependencies holds the RUNTIME dependencies only, sorted by name —
	// development dependencies never reach a resolver.
	Dependencies     []GemDependency
	RequiredRuby     string
	RequiredRubygems string
}

// Filename is the canonical gem file name: name-version[.platform].gem, with
// the platform suffix omitted for pure-ruby gems.
func (s GemSpec) Filename() string {
	return s.Name + "-" + s.VersionWithPlatform() + ".gem"
}

// VersionWithPlatform is the version as the compact index spells it: a
// platform-specific build carries its platform as a suffix.
func (s GemSpec) VersionWithPlatform() string {
	if s.Platform == "" || s.Platform == "ruby" {
		return s.Version
	}
	return s.Version + "-" + s.Platform
}

// InfoLine renders this version's compact-index /info line:
//
//	VERSION[-PLATFORM] [DEP:REQ[,DEP:REQ…]]|checksum:SHA256[,ruby:REQ][,rubygems:REQ]
func (s GemSpec) InfoLine(sha256sum string) string {
	deps := make([]string, 0, len(s.Dependencies))
	for _, d := range s.Dependencies {
		deps = append(deps, d.Name+":"+d.Requirements)
	}
	var b strings.Builder
	b.WriteString(s.VersionWithPlatform())
	b.WriteByte(' ')
	b.WriteString(strings.Join(deps, ","))
	b.WriteString("|checksum:" + sha256sum)
	if s.RequiredRuby != "" {
		b.WriteString(",ruby:" + s.RequiredRuby)
	}
	if s.RequiredRubygems != "" {
		b.WriteString(",rubygems:" + s.RequiredRubygems)
	}
	return b.String()
}

// gemspecYAML mirrors the slice of Gem::Specification the registry reads. The
// YAML carries ruby-object tags (!ruby/object:Gem::Specification etc.), which
// yaml.v3 happily decodes as plain mappings.
type gemspecYAML struct {
	Name    string `yaml:"name"`
	Version struct {
		Version string `yaml:"version"`
	} `yaml:"version"`
	Platform     string `yaml:"platform"`
	Dependencies []struct {
		Name        string             `yaml:"name"`
		Type        string             `yaml:"type"`
		Requirement gemRequirementYAML `yaml:"requirement"`
	} `yaml:"dependencies"`
	RequiredRubyVersion     gemRequirementYAML `yaml:"required_ruby_version"`
	RequiredRubygemsVersion gemRequirementYAML `yaml:"required_rubygems_version"`
}

// gemRequirementYAML decodes Gem::Requirement: a list of [operator, version]
// pairs, where the version is itself a Gem::Version object.
type gemRequirementYAML struct {
	Requirements [][]yaml.Node `yaml:"requirements"`
}

// join renders the requirement in compact-index form (">= 1.0&< 2.0").
func (r gemRequirementYAML) join() string {
	clauses := make([]string, 0, len(r.Requirements))
	for _, pair := range r.Requirements {
		if len(pair) != 2 {
			continue
		}
		op := pair[0].Value
		var ver struct {
			Version string `yaml:"version"`
		}
		if err := pair[1].Decode(&ver); err != nil || ver.Version == "" {
			continue
		}
		clauses = append(clauses, op+" "+ver.Version)
	}
	return strings.Join(clauses, "&")
}

// ParseGem reads a .gem (a tar holding metadata.gz — the gzipped YAML
// gemspec — beside the data archive) and extracts the spec.
func ParseGem(r io.Reader) (*GemSpec, error) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("not a gem: no metadata.gz entry")
		}
		if err != nil {
			return nil, fmt.Errorf("not a gem: %w", err)
		}
		if hdr.Name != "metadata.gz" && hdr.Name != "./metadata.gz" {
			continue
		}
		gz, err := gzip.NewReader(tr)
		if err != nil {
			return nil, fmt.Errorf("metadata.gz: %w", err)
		}
		raw, err := io.ReadAll(io.LimitReader(gz, maxGemspecBytes))
		if err != nil {
			return nil, fmt.Errorf("metadata.gz: %w", err)
		}
		return parseGemspecYAML(raw)
	}
}

func parseGemspecYAML(raw []byte) (*GemSpec, error) {
	var doc gemspecYAML
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("gemspec metadata: %w", err)
	}
	if doc.Name == "" || doc.Version.Version == "" {
		return nil, errors.New("gemspec metadata: name and version are required")
	}
	spec := &GemSpec{
		Name:             doc.Name,
		Version:          doc.Version.Version,
		Platform:         doc.Platform,
		RequiredRuby:     doc.RequiredRubyVersion.join(),
		RequiredRubygems: doc.RequiredRubygemsVersion.join(),
	}
	for _, d := range doc.Dependencies {
		// The type arrives as the ruby symbol ":runtime".
		if !strings.Contains(d.Type, "runtime") {
			continue
		}
		spec.Dependencies = append(spec.Dependencies, GemDependency{
			Name:         d.Name,
			Requirements: d.Requirement.join(),
		})
	}
	sort.Slice(spec.Dependencies, func(i, j int) bool {
		return spec.Dependencies[i].Name < spec.Dependencies[j].Name
	})
	return spec, nil
}
