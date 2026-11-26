// Package terraform implements a Terraform provider/module registry mirror.
//
// Terraform Registry Protocol v1:
//
//	GET /repository/<repo>/.well-known/terraform.json              → service discovery
//	GET /repository/<repo>/v1/providers/:ns/:type/versions         → list provider versions
//	GET /repository/<repo>/v1/providers/:ns/:type/:ver/download/:os/:arch → provider binary redirect
//	PUT /repository/<repo>/v1/providers/:ns/:type/:ver/upload/:os/:arch  → upload binary (hosted)
//	GET /repository/<repo>/v1/modules/:ns/:name/:provider/versions        → list module versions
//	GET /repository/<repo>/v1/modules/:ns/:name/:provider/:ver/download   → module download redirect
//	PUT /repository/<repo>/v1/modules/:ns/:name/:provider/:ver            → upload module (hosted)
//
// Proxy (mirror) mode: API calls forwarded to registry.terraform.io (or custom remote_url),
// provider binaries cached via repoproxy.ServeGET.
// Hosted mode: provider/module binaries stored in blob store, index served from DB.
package terraform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// platform is one operating system / architecture pair a provider version
// was uploaded for.
type platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// Handler serves the Terraform provider/module registry protocol.
type Handler struct {
	deps formats.Deps
	// signer signs the checksum document of a hosted provider version. It is
	// nil when no key is configured: the registry then serves the checksum
	// document unsigned and says so where the client will see it.
	signer *Signer
	// signerErr is why a configured key could not be loaded. It is kept and
	// served rather than swallowed: a registry that quietly dropped the key
	// would look installed while every provider install failed.
	signerErr error
	// signatures holds the detached signature of a checksum document, keyed by
	// the document's digest. A signature carries its creation time, so
	// signing the same document twice yields two different signatures and the
	// client's verification of the second one fails.
	signatures map[string][]byte
	mu         sync.RWMutex
}

// New creates a Terraform format Handler with the given dependencies.
//
// A configured signing key that cannot be read or parsed is remembered as an
// error rather than dropped: serving an unsigned registry because the key is
// broken would hide the misconfiguration behind a working-looking install.
func New(deps formats.Deps) *Handler {
	h := &Handler{deps: deps, signatures: map[string][]byte{}}
	if deps.TerraformSigningKey != "" {
		signer, err := LoadSigner(deps.TerraformSigningKey)
		if err != nil {
			h.signerErr = err
		} else {
			h.signer = signer
		}
	}
	return h
}

// Name returns the format identifier.
func (h *Handler) Name() string { return "terraform" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	// Service discovery is served locally for all repo types.
	if c.Request.Method == http.MethodGet && p == "/.well-known/terraform.json" {
		h.serveDiscovery(c, repoName)
		return
	}

	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		h.serveProxy(c, repo, repoName, p)
		return
	}

	switch {
	case strings.HasPrefix(p, "/v1/providers/"):
		h.serveHostedProvider(c, repoName, p)
	case strings.HasPrefix(p, "/v1/modules/"):
		h.serveHostedModule(c, repoName, p)
	default:
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown terraform endpoint"})
	}
}

// serveHostedProvider dispatches hosted provider requests.
func (h *Handler) serveHostedProvider(c *gin.Context, repoName, p string) {
	// PUT /v1/providers/<ns>/<type>/<ver>/upload/<os>/<arch>  → upload binary
	if c.Request.Method == http.MethodPut && strings.Contains(p, "/upload/") {
		h.handleProviderUpload(c, repoName, p)
		return
	}
	// GET /v1/providers/<ns>/<type>/<ver>/download/<os>/<arch> → binary download redirect
	if c.Request.Method == http.MethodGet && strings.Contains(p, "/download/") {
		h.handleProviderDownload(c, repoName, p)
		return
	}
	// GET /v1/providers/<ns>/<type>/<ver>/shasums → checksum document
	if c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/shasums") {
		h.handleProviderSums(c, repoName, p)
		return
	}
	// GET /v1/providers/<ns>/<type>/<ver>/shasums.sig → detached signature
	if c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/shasums.sig") {
		h.handleProviderSumsSig(c, repoName, p)
		return
	}
	// GET /v1/providers/<ns>/<type>/versions → list versions
	if c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/versions") {
		h.handleProviderVersions(c, repoName, p)
		return
	}
	// GET /v1/providers/<ns>/<type>/<ver>/<os>_<arch>.zip → serve provider binary
	if c.Request.Method == http.MethodGet && strings.HasSuffix(p, ".zip") {
		h.serveProviderBinary(c, repoName, p)
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "unknown provider endpoint"})
}

func (h *Handler) serveProviderBinary(c *gin.Context, repoName, filePath string) {
	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = rc.Close() }()
	c.DataFromReader(http.StatusOK, asset.SizeBytes, "application/zip", rc, nil)
}

func (h *Handler) handleProviderUpload(c *gin.Context, repoName, p string) {
	// p = /v1/providers/<ns>/<type>/<ver>/upload/<os>/<arch>
	rest := strings.TrimPrefix(p, "/v1/providers/")
	parts := strings.Split(rest, "/")
	// parts: [ns, type, ver, "upload", os, arch]
	if len(parts) != 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/providers/<ns>/<type>/<ver>/upload/<os>/<arch>"})
		return
	}
	ns, typ, ver, _, osName, arch := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]
	filePath := providerAssetPath(ns, typ, ver, osName, arch)

	coords := base.Coords{
		Group: ns, Name: typ, Version: ver,
		// The version list answers the platforms a version exists for, and
		// the component is where the upload records them. KeepExtra is left
		// unset on purpose: os and arch describe the file being written, so
		// they are recorded again with it — the keys that survive a redeploy
		// (domain.ListingExtraKeys) belong to apt's suites.
		Extra: map[string]any{"os": osName, "arch": arch},
	}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps, repoName, filePath,
		"application/zip", coords, c.Request.Body, c.Request.ContentLength); err != nil {
		c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"saved": true})
}

// exactComponents returns every stored version of exactly group/name. Modules
// (group "<ns>/<name>") and providers (group "<ns>") share the table, so a
// substring match listed module hashicorp/consul/aws under provider
// hashicorp/aws, and xacme/vpc/aws under acme/vpc/aws (#586).
func (h *Handler) exactComponents(ctx context.Context, repoName, group, name string) ([]domain.Component, error) {
	return base.ExactComponents(ctx, h.deps.Components, domain.SearchParams{
		Repository: repoName, Group: group, Name: name,
	})
}

func (h *Handler) handleProviderVersions(c *gin.Context, repoName, p string) {
	// p = /v1/providers/<ns>/<type>/versions
	rest := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/providers/"), "/versions")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/providers/<ns>/<type>/versions"})
		return
	}
	ns, typ := parts[0], parts[1]

	comps, err := h.exactComponents(c.Request.Context(), repoName, ns, typ)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type version struct {
		Version   string     `json:"version"`
		Protocols []string   `json:"protocols"`
		Platforms []platform `json:"platforms"`
	}

	// The platforms of a version are gathered in a set: the recorded extra
	// names the platform of one upload, while the component's assets name
	// every platform the version was uploaded for.
	platforms := map[string]map[string]platform{}
	for _, comp := range comps {
		set, ok := platforms[comp.Version]
		if !ok {
			set = map[string]platform{}
			platforms[comp.Version] = set
		}
		if osStr, ok := comp.Extra["os"].(string); ok {
			if archStr, ok := comp.Extra["arch"].(string); ok {
				set[osStr+"/"+archStr] = platform{OS: osStr, Arch: archStr}
			}
		}
		for _, pl := range h.componentPlatforms(c.Request.Context(), comp) {
			set[pl.OS+"/"+pl.Arch] = pl
		}
	}

	versionNames := make([]string, 0, len(platforms))
	for ver := range platforms {
		versionNames = append(versionNames, ver)
	}
	sort.Strings(versionNames)

	versions := make([]*version, 0, len(versionNames))
	for _, ver := range versionNames {
		set := platforms[ver]
		list := make([]platform, 0, len(set))
		for _, pl := range set {
			list = append(list, pl)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].OS != list[j].OS {
				return list[i].OS < list[j].OS
			}
			return list[i].Arch < list[j].Arch
		})
		versions = append(versions, &version{
			Version:   ver,
			Protocols: []string{"5.0"},
			Platforms: list,
		})
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

// componentPlatforms are the platforms a component's stored files belong to.
//
// The component of a provider version holds the file of every platform that
// was uploaded for it, so its assets are what names them all — the recorded
// extra names the platform of one upload only.
func (h *Handler) componentPlatforms(ctx context.Context, comp domain.Component) []platform {
	if h.deps.Assets == nil {
		return nil
	}
	assets, err := h.deps.Assets.ListByComponentID(ctx, comp.ID)
	if err != nil {
		return nil
	}
	var out []platform
	for _, asset := range assets {
		osName, arch, ok := providerPlatform(asset.Path)
		if !ok {
			continue
		}
		out = append(out, platform{OS: osName, Arch: arch})
	}
	return out
}

func (h *Handler) handleProviderDownload(c *gin.Context, repoName, p string) {
	// p = /v1/providers/<ns>/<type>/<ver>/download/<os>/<arch>
	rest := strings.TrimPrefix(p, "/v1/providers/")
	parts := strings.Split(rest, "/")
	// parts: [ns, type, ver, "download", os, arch]
	if len(parts) != 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/providers/<ns>/<type>/<ver>/download/<os>/<arch>"})
		return
	}
	ns, typ, ver, _, osName, arch := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]
	filePath := providerAssetPath(ns, typ, ver, osName, arch)

	if h.signerUnavailable(c) {
		return
	}

	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	_ = rc.Close() // only need metadata

	// Root-relative (#821): terraform resolves the download_url against the
	// URL of this very request, so a path without a host lands back on the
	// host the client reached.
	downloadURL := repoPath(repoName) + filePath
	doc := gin.H{
		"os":           osName,
		"arch":         arch,
		"filename":     providerFileName(typ, ver, osName, arch),
		"download_url": downloadURL,
		// The protocol names the digest "shasum"; the client compares the
		// file it fetched against it and refuses the package when it differs.
		"shasum": asset.SHA256,
	}
	if h.signer != nil {
		// The client verifies the checksum document against the signature and
		// the published key: without them it refuses the provider with
		// "authentication signature from unknown issuer".
		sumsURL := h.providerSumsPath(repoName, ns, typ, ver)
		doc["shasums_url"] = sumsURL
		doc["shasums_signature_url"] = sumsURL + ".sig"
		doc["signing_keys"] = gin.H{"gpg_public_keys": []gin.H{{"ascii_armor": h.signer.PublicKey()}}}
	}
	c.JSON(http.StatusOK, doc)
}

// noSigningKeyMessage names the setting an operator has to fill in. The
// client quotes the body of this answer in its own error, so the reason the
// provider cannot be installed reaches the operator instead of stopping at
// the client.
const noSigningKeyMessage = "no Terraform signing key is configured — set terraform.signing_key to an armored OpenPGP private key"

// handleProviderSums serves the checksum document of a provider version.
//
// The client looks its own file up in this document by name, so every
// platform of the version is listed: the file it fetches is the one whose
// digest it compares against.
func (h *Handler) handleProviderSums(c *gin.Context, repoName, p string) {
	ns, typ, ver, ok := providerVersionPath(p, "/shasums")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/providers/<ns>/<type>/<ver>/shasums"})
		return
	}
	if h.signerUnavailable(c) {
		return
	}
	if h.signer == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": noSigningKeyMessage})
		return
	}
	doc, err := h.checksumDocument(c.Request.Context(), repoName, ns, typ, ver)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", doc)
}

// handleProviderSumsSig serves the detached signature of the checksum
// document of a provider version.
func (h *Handler) handleProviderSumsSig(c *gin.Context, repoName, p string) {
	ns, typ, ver, ok := providerVersionPath(p, "/shasums.sig")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/providers/<ns>/<type>/<ver>/shasums.sig"})
		return
	}
	if h.signerUnavailable(c) {
		return
	}
	if h.signer == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": noSigningKeyMessage})
		return
	}
	doc, err := h.checksumDocument(c.Request.Context(), repoName, ns, typ, ver)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	signature, err := h.signature(doc)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// The signature is binary: the client reads it as a detached OpenPGP
	// signature, not as text.
	c.Data(http.StatusOK, "application/octet-stream", signature)
}

// signerUnavailable answers the request with the reason a configured signing
// key could not be loaded. It reports whether it answered.
func (h *Handler) signerUnavailable(c *gin.Context) bool {
	if h.signerErr == nil {
		return false
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": h.signerErr.Error()})
	return true
}

// checksumDocument is the checksum document of one provider version: one line
// per stored platform, "<sha256>  <filename>", sorted by filename.
//
// The document must be stable for the life of a version: the signature is
// made over these exact bytes, and a client verifies the signature against
// the document it just fetched.
func (h *Handler) checksumDocument(ctx context.Context, repoName, ns, typ, ver string) ([]byte, error) {
	files, err := h.providerVersionFiles(ctx, repoName, ns, typ, ver)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	for _, file := range files {
		fmt.Fprintf(&buf, "%s  %s\n", file.sha256, file.filename)
	}
	return buf.Bytes(), nil
}

// providerFile is one stored platform of a provider version: the protocol's
// file name and the digest of the stored bytes.
type providerFile struct {
	filename string
	sha256   string
}

// providerVersionFiles lists the stored platforms of one provider version,
// sorted by the protocol's file name.
func (h *Handler) providerVersionFiles(ctx context.Context, repoName, ns, typ, ver string) ([]providerFile, error) {
	comps, err := h.exactComponents(ctx, repoName, ns, typ)
	if err != nil {
		return nil, err
	}
	prefix := fmt.Sprintf("/v1/providers/%s/%s/%s/", ns, typ, ver)
	var files []providerFile
	for _, comp := range comps {
		if comp.Version != ver {
			continue
		}
		if h.deps.Assets == nil {
			continue
		}
		assets, err := h.deps.Assets.ListByComponentID(ctx, comp.ID)
		if err != nil {
			return nil, err
		}
		for _, asset := range assets {
			if !strings.HasPrefix(asset.Path, prefix) {
				continue
			}
			osName, arch, ok := providerPlatform(asset.Path)
			if !ok {
				continue
			}
			files = append(files, providerFile{
				filename: providerFileName(typ, ver, osName, arch),
				sha256:   asset.SHA256,
			})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].filename < files[j].filename })
	return files, nil
}

// signature is the detached signature of a checksum document.
//
// The signature is cached per document: an OpenPGP signature carries the
// moment it was made, so signing the same document again yields a different
// signature — and the client verifies the signature against the document it
// fetched, so a fresh signature over the same bytes fails that verification.
func (h *Handler) signature(document []byte) ([]byte, error) {
	digest := sha256.Sum256(document)
	key := hex.EncodeToString(digest[:])

	h.mu.RLock()
	cached, ok := h.signatures[key]
	h.mu.RUnlock()
	if ok {
		return cached, nil
	}
	signature, err := h.signer.SignDetached(document)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.signatures[key] = signature
	h.mu.Unlock()
	return signature, nil
}

// providerVersionPath reads <ns>/<type>/<ver> out of a provider path ending
// in suffix.
func providerVersionPath(p, suffix string) (ns, typ, ver string, ok bool) {
	rest := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/providers/"), suffix)
	parts := strings.Split(rest, "/")
	if len(parts) != 3 {
		return "", "", "", false
	}
	for _, part := range parts {
		if part == "" {
			return "", "", "", false
		}
	}
	return parts[0], parts[1], parts[2], true
}

// providerSumsPath is the root-relative path of the checksum document of a
// provider version.
func (h *Handler) providerSumsPath(repoName, ns, typ, ver string) string {
	return repoPath(repoName) + fmt.Sprintf("/v1/providers/%s/%s/%s/shasums", ns, typ, ver)
}

// providerAssetPath is the path a provider binary is stored under.
func providerAssetPath(ns, typ, ver, osName, arch string) string {
	return fmt.Sprintf("/v1/providers/%s/%s/%s/%s_%s.zip", ns, typ, ver, osName, arch)
}

// providerFileName is the file name the Terraform protocol names a provider
// package by: the client looks the package up in the checksum document under
// exactly this name.
func providerFileName(typ, ver, osName, arch string) string {
	return fmt.Sprintf("terraform-provider-%s_%s_%s_%s.zip", typ, ver, osName, arch)
}

// providerPlatform reads the <os>_<arch> of a stored provider binary. A path
// that is not a provider file of this provider is not one.
func providerPlatform(filePath string) (osName, arch string, ok bool) {
	if !strings.HasPrefix(filePath, "/v1/providers/") || !strings.HasSuffix(filePath, ".zip") {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(filePath, "/v1/providers/"), "/")
	if len(parts) != 4 {
		return "", "", false
	}
	name := strings.TrimSuffix(parts[3], ".zip")
	cut := strings.LastIndex(name, "_")
	if cut <= 0 || cut == len(name)-1 {
		return "", "", false
	}
	return name[:cut], name[cut+1:], true
}

// serveHostedModule dispatches hosted module requests.
func (h *Handler) serveHostedModule(c *gin.Context, repoName, p string) {
	// GET /v1/modules/<ns>/<name>/<provider>/versions
	if c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/versions") {
		h.handleModuleVersions(c, repoName, p)
		return
	}
	// GET /v1/modules/<ns>/<name>/<provider>/<ver>/download
	if c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/download") {
		h.handleModuleDownload(c, repoName, p)
		return
	}
	// GET /v1/modules/<ns>/<name>/<provider>/<ver>.tar.gz → serve module archive
	if c.Request.Method == http.MethodGet && strings.HasSuffix(p, ".tar.gz") {
		h.serveModuleArchive(c, repoName, p)
		return
	}
	// PUT /v1/modules/<ns>/<name>/<provider>/<ver>
	if c.Request.Method == http.MethodPut {
		h.handleModuleUpload(c, repoName, p)
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "unknown module endpoint"})
}

func (h *Handler) serveModuleArchive(c *gin.Context, repoName, filePath string) {
	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = rc.Close() }()
	c.DataFromReader(http.StatusOK, asset.SizeBytes, "application/x-tar", rc, nil)
}

func (h *Handler) handleModuleVersions(c *gin.Context, repoName, p string) {
	rest := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/modules/"), "/versions")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/modules/<ns>/<name>/<provider>/versions"})
		return
	}
	ns, name, provider := parts[0], parts[1], parts[2]

	comps, err := h.exactComponents(c.Request.Context(), repoName, ns+"/"+name, provider)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	versions := make([]map[string]string, 0, len(comps))
	for _, comp := range comps {
		versions = append(versions, map[string]string{"version": comp.Version})
	}
	c.JSON(http.StatusOK, gin.H{"modules": []map[string]any{{"versions": versions}}})
}

func (h *Handler) handleModuleUpload(c *gin.Context, repoName, p string) {
	// p = /v1/modules/<ns>/<name>/<provider>/<ver>
	rest := strings.TrimPrefix(p, "/v1/modules/")
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) != 4 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/modules/<ns>/<name>/<provider>/<ver>"})
		return
	}
	ns, name, provider, ver := parts[0], parts[1], parts[2], parts[3]
	filePath := fmt.Sprintf("/v1/modules/%s/%s/%s/%s.tar.gz", ns, name, provider, ver)

	coords := base.Coords{Group: ns + "/" + name, Name: provider, Version: ver}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps, repoName, filePath,
		"application/x-tar", coords, c.Request.Body, c.Request.ContentLength); err != nil {
		c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"saved": true})
}

func (h *Handler) handleModuleDownload(c *gin.Context, repoName, p string) {
	// p = /v1/modules/<ns>/<name>/<provider>/<ver>/download
	rest := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/modules/"), "/download")
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) != 4 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/modules/<ns>/<name>/<provider>/<ver>/download"})
		return
	}
	ns, name, provider, ver := parts[0], parts[1], parts[2], parts[3]
	filePath := fmt.Sprintf("/v1/modules/%s/%s/%s/%s.tar.gz", ns, name, provider, ver)
	// Root-relative (#821): terraform resolves a "/" -prefixed X-Terraform-Get
	// against the URL of this request, so the archive is fetched from the host
	// the client reached.
	downloadURL := repoPath(repoName) + filePath
	c.Header("X-Terraform-Get", downloadURL)
	c.Status(http.StatusNoContent)
}

// serveDiscovery returns the Terraform service discovery document.
func (h *Handler) serveDiscovery(c *gin.Context, repoName string) {
	// Root-relative (#821): terraform-svchost resolves a relative service URL
	// against the discovery document's own URL, so the service base lands on
	// the host the client fetched the document from.
	base := repoPath(repoName)
	c.JSON(http.StatusOK, gin.H{
		"providers.v1": base + "/v1/providers/",
		"modules.v1":   base + "/v1/modules/",
	})
}

func (h *Handler) serveProxy(c *gin.Context, repo *domain.Repository, repoName, p string) {
	remoteBase, err := repoproxy.RemoteURL(repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Provider binary downloads: cache in blob store.
	// Pattern: /v1/providers/<ns>/<type>/<ver>/download/<os>/<arch>
	if strings.HasPrefix(p, "/v1/providers/") && strings.Contains(p, "/download/") {
		coords := base.Coords{Name: p}
		// Provider binaries are immutable per version — never revalidate.
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/zip", 0); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}
	// Module source archives (actual .tar.gz, not the /download redirect or /versions JSON):
	if strings.HasPrefix(p, "/v1/modules/") && strings.HasSuffix(p, ".tar.gz") {
		coords := base.Coords{Name: p}
		// Module source archives are immutable per version — never revalidate.
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/x-tar", 0); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	// All other API calls (versions, download redirect): proxy JSON response.
	// Through JoinURL, not pasted on: it refuses a path that decodes to a
	// ".." segment at the upstream.
	upstreamURL, err := repoproxy.JoinURL(remoteBase, p)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, upstreamURL, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Header.Set("Accept", "application/json")

	repoproxy.SetUpstreamAuth(req, repo)
	resp, err := repoproxy.ClientFor(repo).Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream: " + err.Error()})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// The download endpoints answer 204 with the archive URL in the
	// X-Terraform-Get header, and the client follows that URL to fetch the
	// package. The header is read before the status: a 204 answer would
	// otherwise be copied to the client as an empty body and the archive
	// would be lost.
	if xGet := resp.Header.Get("X-Terraform-Get"); xGet != "" {
		if resp.StatusCode == http.StatusNoContent {
			// The archive must come from this instance: the client resolves
			// the header against the request that carried it, so a host in
			// the value would send the client straight to the upstream
			// instead of through this mirror.
			xGet = localTerraformGet(xGet, remoteBase, repoPath(repoName))
		}
		c.Header("X-Terraform-Get", xGet)
		c.Status(http.StatusNoContent)
		return
	}

	if resp.StatusCode != http.StatusOK {
		c.Status(resp.StatusCode)
		_, _ = io.Copy(c.Writer, resp.Body)
		return
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "parse upstream JSON: " + err.Error()})
		return
	}

	// Root-relative (#821): terraform resolves the rewritten download_url
	// against the URL of the request that carried it.
	localBase := repoPath(repoName)
	rewriteTerraformURLs(body, localBase)

	c.JSON(http.StatusOK, body)
}

// localTerraformGet re-roots an upstream archive URL at this instance.
//
// The result is root-relative, like every other URL this format hands a
// client: it names no host, so it lands on the host the client reached.
//
// Only an archive this instance serves is re-rooted. A module source is
// usually a git URL — the public registry answers
// `git::https://github.com/<owner>/<repo>?ref=<sha>` — and the client
// fetches that itself; it is not an archive of this instance and re-rooting
// it would send the client to a path nothing serves.
func localTerraformGet(xGet, remoteBase, localBase string) string {
	parsed, err := url.Parse(xGet)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return xGet // not an absolute URL: there is no host to replace
	}
	base, err := url.Parse(remoteBase)
	if err != nil {
		return xGet
	}
	// The upstream's own base path is not part of the path this instance
	// serves: the proxy puts it back when it forwards the request.
	p := strings.TrimPrefix(parsed.Path, strings.TrimSuffix(base.Path, "/"))
	if !strings.HasPrefix(p, "/v1/modules/") || !strings.HasSuffix(p, ".tar.gz") {
		return xGet // not an archive of this instance: the client fetches it itself
	}
	local := localBase + p
	if parsed.RawQuery != "" {
		local += "?" + parsed.RawQuery
	}
	return local
}

// rewriteTerraformURLs rewrites download_url fields so provider binary fetches
// route through Nexspence and get cached.
func rewriteTerraformURLs(body map[string]any, localBase string) {
	if u, ok := body["download_url"].(string); ok {
		if parsed, err := url.Parse(u); err == nil {
			body["download_url"] = localBase + parsed.Path
		}
	}
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
