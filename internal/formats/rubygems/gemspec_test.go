package rubygems

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildGem assembles a minimal but structurally faithful .gem: a tar holding
// metadata.gz (the gzipped YAML gemspec) and data.tar.gz.
func buildGem(t *testing.T, specYAML string) []byte {
	t.Helper()
	var meta bytes.Buffer
	gz := gzip.NewWriter(&meta)
	_, err := gz.Write([]byte(specYAML))
	require.NoError(t, err)
	require.NoError(t, gz.Close())

	var data bytes.Buffer
	dgz := gzip.NewWriter(&data)
	inner := tar.NewWriter(dgz)
	require.NoError(t, inner.WriteHeader(&tar.Header{Name: "lib/x.rb", Mode: 0o644, Size: 4}))
	_, _ = inner.Write([]byte("# x\n"))
	require.NoError(t, inner.Close())
	require.NoError(t, dgz.Close())

	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for _, f := range []struct {
		name string
		body []byte
	}{
		{"metadata.gz", meta.Bytes()},
		{"data.tar.gz", data.Bytes()},
	} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body))}))
		_, err := tw.Write(f.body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return out.Bytes()
}

const specYAML = `--- !ruby/object:Gem::Specification
name: demo-gem
version: !ruby/object:Gem::Version
  version: 1.2.0
platform: ruby
dependencies:
- !ruby/object:Gem::Dependency
  name: rack
  requirement: !ruby/object:Gem::Requirement
    requirements:
    - - ">="
      - !ruby/object:Gem::Version
        version: '2.0'
  type: :runtime
- !ruby/object:Gem::Dependency
  name: rspec
  requirement: !ruby/object:Gem::Requirement
    requirements:
    - - "~>"
      - !ruby/object:Gem::Version
        version: '3.0'
  type: :development
- !ruby/object:Gem::Dependency
  name: multi-req
  requirement: !ruby/object:Gem::Requirement
    requirements:
    - - ">="
      - !ruby/object:Gem::Version
        version: '1.0'
    - - "<"
      - !ruby/object:Gem::Version
        version: '2.0'
  type: :runtime
required_ruby_version: !ruby/object:Gem::Requirement
  requirements:
  - - ">="
    - !ruby/object:Gem::Version
      version: '2.6'
required_rubygems_version: !ruby/object:Gem::Requirement
  requirements:
  - - ">="
    - !ruby/object:Gem::Version
      version: '0'
`

func TestParseGem_ExtractsSpec(t *testing.T) {
	gem := buildGem(t, specYAML)
	spec, err := ParseGem(bytes.NewReader(gem))
	require.NoError(t, err)

	assert.Equal(t, "demo-gem", spec.Name)
	assert.Equal(t, "1.2.0", spec.Version)
	assert.Equal(t, "ruby", spec.Platform)
	assert.Equal(t, "demo-gem-1.2.0.gem", spec.Filename())
	assert.Equal(t, "1.2.0", spec.VersionWithPlatform())

	// Only runtime dependencies matter to a resolver (rspec is :development and
	// absent), sorted by name; requirements with several clauses join with "&"
	// per the compact-index grammar.
	require.Len(t, spec.Dependencies, 2)
	assert.Equal(t, "multi-req", spec.Dependencies[0].Name)
	assert.Equal(t, ">= 1.0&< 2.0", spec.Dependencies[0].Requirements)
	assert.Equal(t, "rack", spec.Dependencies[1].Name)
	assert.Equal(t, ">= 2.0", spec.Dependencies[1].Requirements)

	assert.Equal(t, ">= 2.6", spec.RequiredRuby)
	assert.Equal(t, ">= 0", spec.RequiredRubygems)
}

func TestParseGem_PlatformSpecificFilename(t *testing.T) {
	yaml := `--- !ruby/object:Gem::Specification
name: native-gem
version: !ruby/object:Gem::Version
  version: 2.0.0
platform: x86_64-linux
dependencies: []
`
	spec, err := ParseGem(bytes.NewReader(buildGem(t, yaml)))
	require.NoError(t, err)
	assert.Equal(t, "native-gem-2.0.0-x86_64-linux.gem", spec.Filename())
	assert.Equal(t, "2.0.0-x86_64-linux", spec.VersionWithPlatform())
}

func TestParseGem_RejectsGarbage(t *testing.T) {
	_, err := ParseGem(bytes.NewReader([]byte("not a gem at all")))
	assert.Error(t, err)
}

// The compact-index /info line for one version: deps, then checksum and the
// interpreter requirements after the pipe.
func TestInfoLine(t *testing.T) {
	spec, err := ParseGem(bytes.NewReader(buildGem(t, specYAML)))
	require.NoError(t, err)
	line := spec.InfoLine("abc123")
	assert.Equal(t,
		"1.2.0 multi-req:>= 1.0&< 2.0,rack:>= 2.0|checksum:abc123,ruby:>= 2.6,rubygems:>= 0",
		line, "deps sorted by name; empty requirement fields omitted")
}
