package npm

// Version manifests (#131). `npm publish` sends the full package.json of the
// version inside the packument; without it the registry document we serve back
// carries no dependency lists, and resolvers that trust the registry document
// over the tarball (pnpm, yarn) install a package whose deps are missing.
// The manifest is kept on the component row and replayed on every GET.

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"path"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// extraManifestKey is the component.Extra key holding the published package.json.
const extraManifestKey = "npm_manifest"

// maxManifestBytes caps how much of a tarball entry is parsed as package.json.
// Real manifests are a few kB; the limit keeps a crafted archive from becoming
// a memory problem.
const maxManifestBytes = 4 << 20

// manifestOf returns the stored package.json of a published version, or nil.
func manifestOf(comp domain.Component) map[string]any {
	m, _ := comp.Extra[extraManifestKey].(map[string]any)
	return m
}

// versionDocument builds one entry of the packument "versions" map: the
// published manifest with the fields this registry owns (name, version and the
// tarball URL pointing at this instance) written over it.
func versionDocument(manifest map[string]any, pkgName, version, tarball string) map[string]any {
	doc := map[string]any{}
	for k, v := range manifest {
		doc[k] = v
	}
	doc["name"] = pkgName
	doc["version"] = version

	dist := map[string]any{}
	if d, ok := doc["dist"].(map[string]any); ok {
		for k, v := range d {
			dist[k] = v
		}
	}
	dist["tarball"] = tarball
	doc["dist"] = dist
	return doc
}

// withDistShasum records the SHA-1 of the stored tarball in the manifest's dist
// block when the client did not send one. Clients refuse to install a version
// whose document carries no checksum at all.
func withDistShasum(manifest map[string]any, sha1Hex string) map[string]any {
	if manifest == nil || sha1Hex == "" {
		return manifest
	}
	dist, ok := manifest["dist"].(map[string]any)
	if !ok {
		dist = map[string]any{}
		manifest["dist"] = dist
	}
	if s, ok := dist["shasum"].(string); !ok || s == "" {
		dist["shasum"] = sha1Hex
	}
	return manifest
}

// manifestFromPublish pulls the package.json of the version being published out
// of the packument body.
func manifestFromPublish(doc map[string]json.RawMessage, version string) map[string]any {
	raw, ok := doc["versions"]
	if !ok {
		return nil
	}
	var versions map[string]json.RawMessage
	if json.Unmarshal(raw, &versions) != nil {
		return nil
	}
	vraw, ok := versions[version]
	if !ok {
		return nil
	}
	var manifest map[string]any
	if json.Unmarshal(vraw, &manifest) != nil {
		return nil
	}
	return manifest
}

// manifestFromTarball reads package.json out of a stored npm tarball. It is the
// fallback for versions published before the manifest was persisted: the file
// inside the archive is the same document the client sent. Returns nil for
// anything that is not a readable npm tarball.
func manifestFromTarball(r io.Reader) map[string]any {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err != nil {
			return nil
		}
		if !isRootPackageJSON(hdr.Name) {
			continue
		}
		var manifest map[string]any
		if json.NewDecoder(io.LimitReader(tr, maxManifestBytes)).Decode(&manifest) != nil {
			return nil
		}
		return manifest
	}
}

// isRootPackageJSON matches the manifest npm puts at the root of the archive
// ("package/package.json"), and not the package.json of a bundled dependency.
func isRootPackageJSON(name string) bool {
	clean := strings.TrimPrefix(path.Clean(strings.TrimPrefix(name, "./")), "/")
	return path.Base(clean) == "package.json" && strings.Count(clean, "/") == 1
}
