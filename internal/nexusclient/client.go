// Package nexusclient is a read-only HTTP client for the Sonatype Nexus OSS 3
// REST API. It is the source side of a Nexus → Nexspence migration: it lists
// what a Nexus instance holds and streams the bytes back, and never writes
// anything to the instance it is pointed at.
package nexusclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nexspence-oss/nexspence/internal/netguard"
)

// maxErrorBody caps how much of a failing response is quoted back in an error.
const maxErrorBody = 512

// Repository is a repository as configured on the source Nexus.
// MemberNames and RemoteURL are only populated when the instance exposes the
// admin repositorySettings endpoint; the basic listing carries neither.
type Repository struct {
	Name        string
	Format      string
	Type        string
	URL         string
	Online      bool
	RemoteURL   string
	MemberNames []string
	// BlobStoreName is informational: Nexspence resolves its own store.
	BlobStoreName string
}

// Asset is one downloadable file belonging to a component.
type Asset struct {
	Path        string
	DownloadURL string
	ContentType string
	SizeBytes   int64
	SHA256      string
	SHA1        string
	MD5         string
}

// Component is one artifact (a coordinate) with its files.
type Component struct {
	Name    string
	Version string
	Group   string
	Format  string
	Assets  []Asset
}

// User is a security user as defined on the source Nexus.
type User struct {
	UserID    string
	FirstName string
	LastName  string
	Email     string
	Source    string
	Status    string
	Roles     []string
}

// Role is a security role, which may nest other roles.
type Role struct {
	ID          string
	Name        string
	Description string
	Privileges  []string
	Roles       []string
	ReadOnly    bool
}

// Privilege is a security privilege. Attrs holds the type-specific fields
// (format, repository, actions, domain, …) that Nexus returns flattened
// alongside the identity fields.
type Privilege struct {
	Name        string
	Description string
	Type        string
	ReadOnly    bool
	Attrs       map[string]any
}

// RoutingRule is a request routing rule (ALLOW or BLOCK plus regex matchers).
type RoutingRule struct {
	Name        string
	Description string
	Mode        string
	Matchers    []string
}

// Client talks to one Nexus instance with one set of credentials.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
}

// New returns a client for baseURL authenticating with the given credentials.
// The default HTTP client is SSRF-guarded, because the URL comes from an
// operator-supplied migration job rather than from configuration.
func New(baseURL, username, password string, timeout time.Duration) *Client {
	return &Client{
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		username: username,
		password: password,
		http:     netguard.Client(timeout),
	}
}

// WithHTTPClient replaces the HTTP client. Intended for tests that need to
// reach loopback servers the SSRF guard blocks.
func (c *Client) WithHTTPClient(h *http.Client) *Client {
	c.http = h
	return c
}

// BaseURL returns the normalised base URL the client was built with.
func (c *Client) BaseURL() string { return c.baseURL }

func (c *Client) do(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, fmt.Errorf("nexus %s: %d %s: %s",
			rawURL, resp.StatusCode, http.StatusText(resp.StatusCode), strings.TrimSpace(string(body)))
	}
	return resp, nil
}

// getJSON fetches path (relative to the base URL) and decodes it into out.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.do(ctx, c.baseURL+path)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return json.NewDecoder(resp.Body).Decode(out)
}

// ── Repositories ────────────────────────────────────────────────────────────

type basicRepoDTO struct {
	Name       string `json:"name"`
	Format     string `json:"format"`
	Type       string `json:"type"`
	URL        string `json:"url"`
	Attributes struct {
		Proxy struct {
			RemoteURL string `json:"remoteUrl"`
		} `json:"proxy"`
	} `json:"attributes"`
}

// ListRepositories returns the repositories visible to the credentials, using
// the endpoint every Nexus 3 exposes. It carries no group membership and no
// online flag — use ListRepositoriesWithConfig when the full config matters.
func (c *Client) ListRepositories(ctx context.Context) ([]Repository, error) {
	var dto []basicRepoDTO
	if err := c.getJSON(ctx, "/service/rest/v1/repositories", &dto); err != nil {
		return nil, err
	}
	out := make([]Repository, 0, len(dto))
	for _, d := range dto {
		out = append(out, Repository{
			Name:      d.Name,
			Format:    d.Format,
			Type:      d.Type,
			URL:       d.URL,
			Online:    true, // not reported here; assume usable
			RemoteURL: d.Attributes.Proxy.RemoteURL,
		})
	}
	return out, nil
}

type settingsRepoDTO struct {
	Name    string `json:"name"`
	Format  string `json:"format"`
	Type    string `json:"type"`
	URL     string `json:"url"`
	Online  bool   `json:"online"`
	Storage struct {
		BlobStoreName string `json:"blobStoreName"`
	} `json:"storage"`
	Group struct {
		MemberNames []string `json:"memberNames"`
	} `json:"group"`
	Proxy struct {
		RemoteURL string `json:"remoteUrl"`
	} `json:"proxy"`
}

// ListRepositoriesWithConfig returns repositories including group membership
// and proxy remote URLs, read from the admin repositorySettings endpoint.
// Instances that do not expose it — older versions, or credentials without
// admin rights — fall back to the basic listing, which still names every
// repository but cannot describe how the groups are wired.
func (c *Client) ListRepositoriesWithConfig(ctx context.Context) ([]Repository, error) {
	var dto []settingsRepoDTO
	err := c.getJSON(ctx, "/service/rest/v1/repositorySettings", &dto)
	if err != nil {
		return c.ListRepositories(ctx)
	}
	out := make([]Repository, 0, len(dto))
	for _, d := range dto {
		out = append(out, Repository{
			Name:          d.Name,
			Format:        d.Format,
			Type:          d.Type,
			URL:           d.URL,
			Online:        d.Online,
			RemoteURL:     d.Proxy.RemoteURL,
			MemberNames:   d.Group.MemberNames,
			BlobStoreName: d.Storage.BlobStoreName,
		})
	}
	return out, nil
}

// ── Components and assets ───────────────────────────────────────────────────

type componentPageDTO struct {
	Items []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Group   string `json:"group"`
		Format  string `json:"format"`
		Assets  []struct {
			Path        string `json:"path"`
			DownloadURL string `json:"downloadUrl"`
			ContentType string `json:"contentType"`
			FileSize    int64  `json:"fileSize"`
			Checksum    struct {
				SHA256 string `json:"sha256"`
				SHA1   string `json:"sha1"`
				MD5    string `json:"md5"`
			} `json:"checksum"`
		} `json:"assets"`
	} `json:"items"`
	ContinuationToken string `json:"continuationToken"`
}

// ListComponents returns one page of components in repo. Pass the token
// returned by the previous call to get the next page; an empty returned token
// means the listing is exhausted.
func (c *Client) ListComponents(ctx context.Context, repo, continuationToken string) ([]Component, string, error) {
	reqURL := c.baseURL + "/service/rest/v1/components?repository=" + url.QueryEscape(repo)
	if continuationToken != "" {
		reqURL += "&continuationToken=" + url.QueryEscape(continuationToken)
	}
	resp, err := c.do(ctx, reqURL)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var page componentPageDTO
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, "", err
	}
	out := make([]Component, 0, len(page.Items))
	for _, it := range page.Items {
		comp := Component{Name: it.Name, Version: it.Version, Group: it.Group, Format: it.Format}
		for _, a := range it.Assets {
			comp.Assets = append(comp.Assets, Asset{
				Path:        a.Path,
				DownloadURL: a.DownloadURL,
				ContentType: a.ContentType,
				SizeBytes:   a.FileSize,
				SHA256:      a.Checksum.SHA256,
				SHA1:        a.Checksum.SHA1,
				MD5:         a.Checksum.MD5,
			})
		}
		out = append(out, comp)
	}
	return out, page.ContinuationToken, nil
}

// DownloadAsset streams an asset from its absolute download URL. The caller
// closes the returned reader.
func (c *Client) DownloadAsset(ctx context.Context, downloadURL string) (io.ReadCloser, error) {
	resp, err := c.do(ctx, downloadURL)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// ── OCI registry surface ────────────────────────────────────────────────────

// manifestAccept lists every manifest media type worth asking for. A registry
// that is not told what the caller understands is free to convert, and would
// hand back a document whose digest is not the one being recorded.
const manifestAccept = "application/vnd.docker.distribution.manifest.v2+json," +
	"application/vnd.docker.distribution.manifest.list.v2+json," +
	"application/vnd.oci.image.manifest.v1+json," +
	"application/vnd.oci.image.index.v1+json"

// maxManifestBytes caps a manifest read. The OCI spec caps manifests at 4 MiB;
// reading one byte past it is what makes an oversized document visible instead
// of silently truncated.
const maxManifestBytes = 4 << 20

// RepositoryURL returns the absolute URL of a path inside a repository's own
// content surface — the same URL a docker or maven client would fetch.
func (c *Client) RepositoryURL(repo, path string) string {
	return c.baseURL + "/repository/" + repo + "/" + strings.TrimPrefix(path, "/")
}

// DownloadManifest fetches an image manifest by tag or digest and returns its
// bytes along with the media type the registry served it as.
//
// Manifests are read whole rather than streamed because the migration has to
// look inside one: a manifest names the blobs that must come across with it,
// and Nexus does not list those blobs under the image they belong to.
func (c *Client) DownloadManifest(ctx context.Context, repo, image, reference string) ([]byte, string, error) {
	rawURL := c.RepositoryURL(repo, "/v2/"+image+"/manifests/"+reference)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	req.Header.Set("Accept", manifestAccept)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, "", fmt.Errorf("nexus %s: %d %s: %s",
			rawURL, resp.StatusCode, http.StatusText(resp.StatusCode), strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > maxManifestBytes {
		return nil, "", fmt.Errorf("nexus %s: manifest exceeds the 4MiB limit", rawURL)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// DownloadBlob streams a registry blob by digest from the image's own path.
// Nexus stores blobs content-addressed and lists them under a placeholder image
// name, but serves them under whichever image references them.
func (c *Client) DownloadBlob(ctx context.Context, repo, image, digest string) (io.ReadCloser, error) {
	return c.DownloadAsset(ctx, c.RepositoryURL(repo, "/v2/"+image+"/blobs/"+digest))
}

// ── Security ────────────────────────────────────────────────────────────────

type userDTO struct {
	UserID    string   `json:"userId"`
	FirstName string   `json:"firstName"`
	LastName  string   `json:"lastName"`
	Email     string   `json:"emailAddress"`
	Source    string   `json:"source"`
	Status    string   `json:"status"`
	Roles     []string `json:"roles"`
}

// ListUsers returns every security user. Requires admin credentials; Nexus OSS
// returns the full list unpaginated.
func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	var dto []userDTO
	if err := c.getJSON(ctx, "/service/rest/v1/security/users", &dto); err != nil {
		return nil, err
	}
	out := make([]User, 0, len(dto))
	for _, d := range dto {
		out = append(out, User(d))
	}
	return out, nil
}

type roleDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Privileges  []string `json:"privileges"`
	Roles       []string `json:"roles"`
	ReadOnly    bool     `json:"readOnly"`
}

// ListRoles returns every security role, including its nested role references.
func (c *Client) ListRoles(ctx context.Context) ([]Role, error) {
	var dto []roleDTO
	if err := c.getJSON(ctx, "/service/rest/v1/security/roles", &dto); err != nil {
		return nil, err
	}
	out := make([]Role, 0, len(dto))
	for _, d := range dto {
		out = append(out, Role(d))
	}
	return out, nil
}

// privilegeIdentityFields are decoded into the struct proper; everything else
// Nexus returns is type-specific and kept in Attrs.
var privilegeIdentityFields = map[string]bool{
	"name": true, "description": true, "type": true, "readOnly": true,
}

// ListPrivileges returns every security privilege with its type-specific
// attributes preserved.
func (c *Client) ListPrivileges(ctx context.Context) ([]Privilege, error) {
	var raw []map[string]any
	if err := c.getJSON(ctx, "/service/rest/v1/security/privileges", &raw); err != nil {
		return nil, err
	}
	out := make([]Privilege, 0, len(raw))
	for _, m := range raw {
		p := Privilege{
			Name:        stringField(m, "name"),
			Description: stringField(m, "description"),
			Type:        stringField(m, "type"),
			Attrs:       map[string]any{},
		}
		if b, ok := m["readOnly"].(bool); ok {
			p.ReadOnly = b
		}
		for k, v := range m {
			if !privilegeIdentityFields[k] {
				p.Attrs[k] = v
			}
		}
		out = append(out, p)
	}
	return out, nil
}

type routingRuleDTO struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Mode        string   `json:"mode"`
	Matchers    []string `json:"matchers"`
}

// ListRoutingRules returns every routing rule defined on the instance.
func (c *Client) ListRoutingRules(ctx context.Context) ([]RoutingRule, error) {
	var dto []routingRuleDTO
	if err := c.getJSON(ctx, "/service/rest/v1/routing-rules", &dto); err != nil {
		return nil, err
	}
	out := make([]RoutingRule, 0, len(dto))
	for _, d := range dto {
		out = append(out, RoutingRule(d))
	}
	return out, nil
}

func stringField(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}
