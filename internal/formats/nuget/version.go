package nuget

import (
	"strconv"
	"strings"
)

// NormalizeVersion returns the normalized form of a NuGet version
// (https://learn.microsoft.com/nuget/concepts/package-versioning#normalized-version-numbers):
// build metadata dropped, the numeric part padded to three parts, a zero
// fourth part dropped and leading zeros stripped — "1.0" is "1.0.0",
// "1.02.0.0-Beta+abc" is "1.2.0-Beta". The release label keeps its case.
// A version whose numeric part is not 1–4 dotted integers is returned as is.
func NormalizeVersion(v string) string {
	core, _, _ := strings.Cut(v, "+")
	nums, label, hasLabel := strings.Cut(core, "-")
	parts := strings.Split(nums, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return v
	}
	out := make([]string, 0, 4)
	for _, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil || p == "" {
			return v
		}
		out = append(out, strconv.FormatUint(n, 10))
	}
	for len(out) < 3 {
		out = append(out, "0")
	}
	if len(out) == 4 && out[3] == "0" {
		out = out[:3]
	}
	norm := strings.Join(out, ".")
	if hasLabel {
		norm += "-" + label
	}
	return norm
}

// versionKey is the form one NuGet version has in paths and lists: normalized
// and lowercased, as the flat container serves it. Versions compare
// case-insensitively, so 1.0.0-Beta and 1.0.0-BETA are one version.
func versionKey(v string) string {
	return strings.ToLower(NormalizeVersion(v))
}

// nupkgPath is where push stores a package: the flat-container download path
// without its prefix, built from the lowercased id and version key.
func nupkgPath(id, version string) string {
	id, version = strings.ToLower(id), versionKey(version)
	return "/" + id + "/" + version + "/" + id + "." + version + ".nupkg"
}
