package cargo

import "strings"

// Component extra keys holding what the sparse index serves for a version.
const (
	extraDeps        = "deps"
	extraFeatures    = "features"
	extraFeatures2   = "features2"
	extraLinks       = "links"
	extraRustVersion = "rust_version"
)

// publishMeta is the JSON half of a cargo publish request, as far as the index
// needs it (https://doc.rust-lang.org/cargo/reference/registry-web-api.html#publish).
type publishMeta struct {
	Name        string              `json:"name"`
	Version     string              `json:"vers"`
	Deps        []publishDep        `json:"deps"`
	Features    map[string][]string `json:"features"`
	Links       *string             `json:"links"`
	RustVersion *string             `json:"rust_version"`
}

// publishDep is a dependency as cargo publish sends it.
type publishDep struct {
	Name               string   `json:"name"`
	VersionReq         string   `json:"version_req"`
	Features           []string `json:"features"`
	Optional           bool     `json:"optional"`
	DefaultFeatures    bool     `json:"default_features"`
	Target             *string  `json:"target"`
	Kind               string   `json:"kind"`
	Registry           *string  `json:"registry"`
	ExplicitNameInToml *string  `json:"explicit_name_in_toml"`
}

// indexExtra is the metadata stored on the version's component. Every key is
// written, so a re-published version cannot keep values from before.
func (m publishMeta) indexExtra() map[string]any {
	features, features2 := splitFeatures(m.Features)
	extra := map[string]any{
		extraDeps:        indexDeps(m.Deps),
		extraFeatures:    features,
		extraFeatures2:   nil,
		extraLinks:       nil,
		extraRustVersion: nil,
	}
	if len(features2) > 0 {
		extra[extraFeatures2] = features2
	}
	if m.Links != nil && *m.Links != "" {
		extra[extraLinks] = *m.Links
	}
	if m.RustVersion != nil && *m.RustVersion != "" {
		extra[extraRustVersion] = *m.RustVersion
	}
	return extra
}

// indexDeps converts publish dependencies to index records
// (https://doc.rust-lang.org/cargo/reference/registry-index.html#json-schema).
// A dependency renamed in Cargo.toml arrives with name = the real crate and
// explicit_name_in_toml = the rename; the index names it by the rename and
// points at the real crate with package.
func indexDeps(deps []publishDep) []map[string]any {
	out := make([]map[string]any, 0, len(deps))
	for _, d := range deps {
		features := d.Features
		if features == nil {
			features = []string{}
		}
		kind := d.Kind
		if kind == "" {
			kind = "normal"
		}
		rec := map[string]any{
			"name":             d.Name,
			"req":              d.VersionReq,
			"features":         features,
			"optional":         d.Optional,
			"default_features": d.DefaultFeatures,
			"target":           d.Target,
			"kind":             kind,
			"registry":         d.Registry,
		}
		if d.ExplicitNameInToml != nil && *d.ExplicitNameInToml != "" && *d.ExplicitNameInToml != d.Name {
			rec["name"] = *d.ExplicitNameInToml
			rec["package"] = d.Name
		}
		out = append(out, rec)
	}
	return out
}

// splitFeatures moves features that use the newer syntax — "dep:name" or
// "name?/feature" — to features2, which index records of schema v2 carry:
// cargo versions that predate the syntax must not see them.
func splitFeatures(all map[string][]string) (features, features2 map[string][]string) {
	features = map[string][]string{}
	features2 = map[string][]string{}
	for name, values := range all {
		if values == nil {
			values = []string{}
		}
		v2 := false
		for _, v := range values {
			if strings.HasPrefix(v, "dep:") || strings.Contains(v, "?/") {
				v2 = true
				break
			}
		}
		if v2 {
			features2[name] = values
		} else {
			features[name] = values
		}
	}
	return features, features2
}
