# terraform-provider-nexspence v0.1.0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Terraform provider managing Nexspence repositories, blob stores, and RBAC objects via the Nexus-compat REST API, published on registry.terraform.io as `nexspence/nexspence`.

**Architecture:** New public repo `nexspence/terraform-provider-nexspence`. `terraform-plugin-framework` provider over a thin hand-written REST client (`internal/client/`). Unit tests run the client against `httptest.Server`; acceptance tests (`terraform-plugin-testing`, `TF_ACC=1`) run against a live Nexspence from docker compose.

**Tech Stack:** Go ≥1.24, terraform-plugin-framework, terraform-plugin-framework-validators, terraform-plugin-testing, goreleaser (GPG-signed, registry manifest).

**Spec:** `docs/superpowers/specs/2026-06-06-terraform-provider-design.md` (in nexspence-core).

---

## Verified API contracts (from nexspence-core source — do not re-derive)

All paths Nexus-compat, all mutations require the `nx-admin` role. Auth: `Authorization: Bearer nxs_<token>` or `Basic user:pass`. Errors: `{"error": "<message>"}`. 404 on missing resources.

| Object | Endpoints | Notes |
|---|---|---|
| Repository | `GET /service/rest/v1/repositories`; `GET /repositories/:name`; `POST /repositories/:format/:type`; `PUT /repositories/:format/:type/:name`; `DELETE /repositories/:name` | JSON: `name`, `format`, `type`, `blobStoreId` (**ID**, not name), `online`, `allowAnonymous`, `description`, `quotaBytes`, `formatConfig` (group: `{member_names: [...], writable_member: "..."}`), `proxyConfig` (`{remote_url: "..."}` — only key supported), `cleanupPolicyIds`, computed `id`, `url` |
| Blob store | `GET /service/rest/v1/blobstores`; `GET /blobstores/:name`; `POST /blobstores/:type`; `PUT /blobstores/:type/:name`; `DELETE /blobstores/:name` | JSON: `id`, `name`, `type` (`local`\|`s3`), `config`, `quotaBytes`, computed `usedBytes`. local config: `{path}`. s3 config: `{bucket, region, endpoint, access_key, secret_key, force_path_style}` |
| User | `GET/POST /service/rest/v1/security/users`; `GET/PUT/DELETE /security/users/:userId`; `PUT /security/users/:userId/roles` body `{"roleIds": [...]}`; `PUT /security/users/:userId/change-password` body `{"newPassword": "..."}` (admin: no oldPassword) | JSON: `userId` (=username), `emailAddress`, `firstName`, `lastName`, `status` (`active`\|`disabled`), `source`, `roles`. **Asymmetry:** GET returns role **names**; the roles endpoint takes role **IDs**. PUT user ignores `roles` — use the dedicated roles endpoint. Create takes `password` |
| Role | `GET/POST /service/rest/v1/security/roles`; `PUT/DELETE /security/roles/:id` | JSON: `id` (UUID), `name`, `description`, `privileges` (privilege **IDs**, read and write), `readOnly`. **No GET-by-id** — read = list + match |
| Privilege | `GET/POST /service/rest/v1/security/privileges`; `GET/PUT/DELETE /security/privileges/:id` | JSON: `id`, `name`, `description`, `type` (always send `repository-content-selector`), `contentSelectorId` (**ID**) |
| Content selector | `GET/POST /service/rest/v1/security/content-selectors`; `GET/PUT/DELETE /security/content-selectors/:id` | JSON: `id`, `name`, `description`, `expression` (CEL) |

Formats enum (14): `maven2 npm pypi docker go nuget raw apt yum helm cargo conan conda terraform`.

Acceptance stack: image `ghcr.io/nexspence/nexspence:latest`, port `8081`, env `NEXSPENCE_DATABASE_DSN`, `NEXSPENCE_AUTH_JWT_SECRET`, `NEXSPENCE_HTTP_ADDR`; bootstrap admin `admin`/`admin123`. Blob store `default` is seeded.

---

### Task 1: Repository scaffold

**Files:**
- Create: `~/WORKING/AI/terraform-provider-nexspence/` — `go.mod`, `main.go`, `internal/provider/provider.go`, `Makefile`, `.gitignore`, `LICENSE`

- [ ] **Step 1: Init repo and module**

```bash
mkdir -p ~/WORKING/AI/terraform-provider-nexspence && cd ~/WORKING/AI/terraform-provider-nexspence
git init -b main
go mod init github.com/nexspence/terraform-provider-nexspence
go get github.com/hashicorp/terraform-plugin-framework@latest
go get github.com/hashicorp/terraform-plugin-framework-validators@latest
printf 'dist/\n.terraform/\n*.tfstate*\n' > .gitignore
```

Copy `LICENSE` (AGPLv3) from nexspence-core: `cp ~/WORKING/AI/nexspence-core/LICENSE .`

> **Manual user step (flag it, don't block):** create the empty **public** GitHub repo `nexspence/terraform-provider-nexspence` in the org UI, then `git remote add origin git@github.com:nexspence/terraform-provider-nexspence.git`. No `gh` CLI on this machine.

- [ ] **Step 2: Write `main.go`**

```go
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/nexspence/terraform-provider-nexspence/internal/provider"
)

// version is set by goreleaser via -ldflags.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run with Terraform plugin debugger support")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/nexspence/nexspence",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 3: Write minimal `internal/provider/provider.go`** (resources added in later tasks)

```go
package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// New returns the provider factory used by main and by acceptance tests.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &nexspenceProvider{version: version}
	}
}

type nexspenceProvider struct {
	version string
}

func (p *nexspenceProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "nexspence"
	resp.Version = p.version
}

func (p *nexspenceProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage Nexspence repositories, blob stores, and RBAC objects.",
		Attributes:  map[string]schema.Attribute{}, // filled in Task 6
	}
}

func (p *nexspenceProvider) Configure(_ context.Context, _ provider.ConfigureRequest, _ *provider.ConfigureResponse) {
} // filled in Task 6

func (p *nexspenceProvider) Resources(_ context.Context) []func() resource.Resource {
	return nil // filled by resource tasks
}

func (p *nexspenceProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil // filled in Task 13
}
```

- [ ] **Step 4: Write `Makefile`**

```makefile
default: build

build:
	go build ./...

test:
	go test ./... -count=1

testacc:
	TF_ACC=1 NEXSPENCE_URL=http://localhost:8081 NEXSPENCE_USERNAME=admin NEXSPENCE_PASSWORD=admin123 \
		go test ./internal/provider/ -v -count=1 -timeout 30m

stack-up:
	docker compose -f docker-compose.acc.yml up -d --wait

stack-down:
	docker compose -f docker-compose.acc.yml down -v

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run

docs:
	go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@latest generate
```

- [ ] **Step 5: Verify build, commit**

```bash
go mod tidy && go build ./...
git add -A && git commit -m "chore: scaffold provider skeleton"
```
Expected: builds with no errors.

---

### Task 2: Client core (auth, do(), errors)

**Files:**
- Create: `internal/client/client.go`
- Test: `internal/client/client_test.go`

- [ ] **Step 1: Write failing tests**

```go
package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServer returns a Client pointed at a test server running fn.
func newTestServer(t *testing.T, fn http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(fn)
	t.Cleanup(srv.Close)
	c, err := New(Config{URL: srv.URL, Token: "nxs_test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestNew_Validation(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("want error for missing URL")
	}
	if _, err := New(Config{URL: "http://x"}); err == nil {
		t.Fatal("want error when no auth method")
	}
	if _, err := New(Config{URL: "http://x", Token: "t", Username: "u", Password: "p"}); err == nil {
		t.Fatal("want error when both auth methods set")
	}
	if _, err := New(Config{URL: "http://x/", Token: "t"}); err != nil {
		t.Fatalf("valid config: %v", err)
	}
}

func TestDo_BearerAuth(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer nxs_test" {
			t.Errorf("auth header = %q", got)
		}
		w.Write([]byte(`{"ok":true}`))
	})
	var out map[string]bool
	if err := c.do(context.Background(), http.MethodGet, "/ping", nil, &out); err != nil {
		t.Fatal(err)
	}
	if !out["ok"] {
		t.Fatal("body not decoded")
	}
}

func TestDo_BasicAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "admin" || p != "secret" {
			t.Errorf("basic auth = %q/%q ok=%v", u, p, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	c, _ := New(Config{URL: srv.URL, Username: "admin", Password: "secret"})
	if err := c.do(context.Background(), http.MethodGet, "/ping", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDo_NotFound(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found: repo"}`))
	})
	err := c.do(context.Background(), http.MethodGet, "/x", nil, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDo_APIError(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"quota exceeds blob store quota"}`))
	})
	err := c.do(context.Background(), http.MethodPost, "/x", map[string]string{"a": "b"}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %v", err)
	}
	if apiErr.Status != 400 || apiErr.Message != "quota exceeds blob store quota" {
		t.Fatalf("apiErr = %+v", apiErr)
	}
}
```

- [ ] **Step 2: Run, verify FAIL**

Run: `go test ./internal/client/ -v`
Expected: compile error — `Config`, `New`, `do`, `ErrNotFound`, `APIError` undefined.

- [ ] **Step 3: Implement `internal/client/client.go`**

```go
// Package client is a thin REST client for the Nexspence Nexus-compat API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrNotFound is returned for any 404 response.
var ErrNotFound = errors.New("not found")

// APIError carries a non-2xx API response.
type APIError struct {
	Status  int
	Method  string
	Path    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("nexspence API: %s %s: %d: %s", e.Method, e.Path, e.Status, e.Message)
}

// Config configures a Client. Exactly one of Token or Username/Password is required.
type Config struct {
	URL      string
	Token    string // nxs_* API token
	Username string
	Password string
}

// Client talks to one Nexspence instance.
type Client struct {
	baseURL  string
	token    string
	username string
	password string
	hc       *http.Client
}

// New validates cfg and returns a Client.
func New(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, errors.New("url is required")
	}
	hasToken := cfg.Token != ""
	hasBasic := cfg.Username != "" || cfg.Password != ""
	if hasToken == hasBasic {
		return nil, errors.New("exactly one of token or username/password must be set")
	}
	return &Client{
		baseURL:  strings.TrimRight(cfg.URL, "/"),
		token:    cfg.Token,
		username: cfg.Username,
		password: cfg.Password,
		hc:       &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// do sends method path with optional JSON body, decoding a JSON response into out (if non-nil).
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s %s: %w", method, path, ErrNotFound)
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = json.Unmarshal(raw, &e)
		msg := e.Error
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return &APIError{Status: resp.StatusCode, Method: method, Path: path, Message: msg}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
```

- [ ] **Step 4: Run, verify PASS:** `go test ./internal/client/ -v` — all 5 tests PASS.

- [ ] **Step 5: Commit:** `git add internal/client && git commit -m "feat(client): core client with auth and error mapping"`

---

### Task 3: Client — blob stores

**Files:**
- Create: `internal/client/blobstores.go`
- Test: `internal/client/blobstores_test.go`

- [ ] **Step 1: Write failing tests**

```go
package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestBlobStoreCRUD(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /service/rest/v1/blobstores/s3":
			var bs BlobStore
			json.NewDecoder(r.Body).Decode(&bs)
			if bs.Name != "s3-main" || bs.Config["bucket"] != "artifacts" {
				t.Errorf("create payload = %+v", bs)
			}
			bs.ID = "bs-1"
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(bs)
		case "GET /service/rest/v1/blobstores/s3-main":
			json.NewEncoder(w).Encode(BlobStore{ID: "bs-1", Name: "s3-main", Type: "s3",
				Config: map[string]any{"bucket": "artifacts"}, QuotaBytes: 100})
		case "GET /service/rest/v1/blobstores":
			json.NewEncoder(w).Encode([]BlobStore{{ID: "bs-1", Name: "s3-main", Type: "s3"}})
		case "PUT /service/rest/v1/blobstores/s3/s3-main":
			json.NewEncoder(w).Encode(BlobStore{ID: "bs-1", Name: "s3-main", Type: "s3"})
		case "DELETE /service/rest/v1/blobstores/s3-main":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	})
	ctx := context.Background()

	created, err := c.CreateBlobStore(ctx, &BlobStore{Name: "s3-main", Type: "s3",
		Config: map[string]any{"bucket": "artifacts"}})
	if err != nil || created.ID != "bs-1" {
		t.Fatalf("create: %v %+v", err, created)
	}
	got, err := c.GetBlobStore(ctx, "s3-main")
	if err != nil || got.QuotaBytes != 100 {
		t.Fatalf("get: %v %+v", err, got)
	}
	list, err := c.ListBlobStores(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}
	if _, err := c.UpdateBlobStore(ctx, &BlobStore{Name: "s3-main", Type: "s3"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := c.DeleteBlobStore(ctx, "s3-main"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
```

- [ ] **Step 2: Run, verify FAIL:** `go test ./internal/client/ -run TestBlobStoreCRUD -v` — compile error, `BlobStore` undefined.

- [ ] **Step 3: Implement `internal/client/blobstores.go`**

```go
package client

import (
	"context"
	"net/http"
	"net/url"
)

// BlobStore mirrors the Nexus-compat blob store JSON.
type BlobStore struct {
	ID         string         `json:"id,omitempty"`
	Name       string         `json:"name"`
	Type       string         `json:"type,omitempty"`
	Config     map[string]any `json:"config"`
	QuotaBytes int64          `json:"quotaBytes,omitempty"`
	UsedBytes  int64          `json:"usedBytes,omitempty"`
}

func (c *Client) ListBlobStores(ctx context.Context) ([]BlobStore, error) {
	var out []BlobStore
	err := c.do(ctx, http.MethodGet, "/service/rest/v1/blobstores", nil, &out)
	return out, err
}

func (c *Client) GetBlobStore(ctx context.Context, name string) (*BlobStore, error) {
	var out BlobStore
	err := c.do(ctx, http.MethodGet, "/service/rest/v1/blobstores/"+url.PathEscape(name), nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateBlobStore(ctx context.Context, bs *BlobStore) (*BlobStore, error) {
	var out BlobStore
	err := c.do(ctx, http.MethodPost, "/service/rest/v1/blobstores/"+url.PathEscape(bs.Type), bs, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateBlobStore(ctx context.Context, bs *BlobStore) (*BlobStore, error) {
	var out BlobStore
	path := "/service/rest/v1/blobstores/" + url.PathEscape(bs.Type) + "/" + url.PathEscape(bs.Name)
	err := c.do(ctx, http.MethodPut, path, bs, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteBlobStore(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/service/rest/v1/blobstores/"+url.PathEscape(name), nil, nil)
}
```

- [ ] **Step 4: Run, verify PASS:** `go test ./internal/client/ -v`

- [ ] **Step 5: Commit:** `git add -A && git commit -m "feat(client): blob store CRUD"`

---

### Task 4: Client — repositories

**Files:**
- Create: `internal/client/repositories.go`
- Test: `internal/client/repositories_test.go`

- [ ] **Step 1: Write failing tests**

```go
package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestRepositoryCRUD(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /service/rest/v1/repositories/maven2/proxy":
			var repo Repository
			json.NewDecoder(r.Body).Decode(&repo)
			if repo.Name != "maven-central" || repo.ProxyConfig["remote_url"] != "https://repo1.maven.org/maven2/" {
				t.Errorf("create payload = %+v", repo)
			}
			repo.ID = "r-1"
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(repo)
		case "GET /service/rest/v1/repositories/maven-central":
			json.NewEncoder(w).Encode(Repository{ID: "r-1", Name: "maven-central",
				Format: "maven2", Type: "proxy", BlobStoreID: "bs-1"})
		case "GET /service/rest/v1/repositories":
			json.NewEncoder(w).Encode([]Repository{{Name: "maven-central"}})
		case "PUT /service/rest/v1/repositories/maven2/proxy/maven-central":
			json.NewEncoder(w).Encode(Repository{ID: "r-1", Name: "maven-central"})
		case "DELETE /service/rest/v1/repositories/maven-central":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	})
	ctx := context.Background()

	created, err := c.CreateRepository(ctx, &Repository{Name: "maven-central", Format: "maven2",
		Type: "proxy", ProxyConfig: map[string]any{"remote_url": "https://repo1.maven.org/maven2/"}})
	if err != nil || created.ID != "r-1" {
		t.Fatalf("create: %v %+v", err, created)
	}
	got, err := c.GetRepository(ctx, "maven-central")
	if err != nil || got.BlobStoreID != "bs-1" {
		t.Fatalf("get: %v %+v", err, got)
	}
	if _, err := c.ListRepositories(ctx); err != nil {
		t.Fatalf("list: %v", err)
	}
	if _, err := c.UpdateRepository(ctx, &Repository{Name: "maven-central", Format: "maven2", Type: "proxy"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := c.DeleteRepository(ctx, "maven-central"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
```

- [ ] **Step 2: Run, verify FAIL** (compile error: `Repository` undefined).

- [ ] **Step 3: Implement `internal/client/repositories.go`**

```go
package client

import (
	"context"
	"net/http"
	"net/url"
)

// Repository mirrors the Nexus-compat repository JSON.
type Repository struct {
	ID               string         `json:"id,omitempty"`
	Name             string         `json:"name"`
	Format           string         `json:"format,omitempty"`
	Type             string         `json:"type,omitempty"`
	BlobStoreID      string         `json:"blobStoreId,omitempty"`
	Online           *bool          `json:"online,omitempty"`
	AllowAnonymous   bool           `json:"allowAnonymous"`
	Description      string         `json:"description,omitempty"`
	QuotaBytes       int64          `json:"quotaBytes,omitempty"`
	FormatConfig     map[string]any `json:"formatConfig,omitempty"`
	ProxyConfig      map[string]any `json:"proxyConfig,omitempty"`
	CleanupPolicyIDs []string       `json:"cleanupPolicyIds,omitempty"`
	URL              string         `json:"url,omitempty"`
}

func (c *Client) ListRepositories(ctx context.Context) ([]Repository, error) {
	var out []Repository
	err := c.do(ctx, http.MethodGet, "/service/rest/v1/repositories", nil, &out)
	return out, err
}

func (c *Client) GetRepository(ctx context.Context, name string) (*Repository, error) {
	var out Repository
	err := c.do(ctx, http.MethodGet, "/service/rest/v1/repositories/"+url.PathEscape(name), nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateRepository(ctx context.Context, r *Repository) (*Repository, error) {
	var out Repository
	path := "/service/rest/v1/repositories/" + url.PathEscape(r.Format) + "/" + url.PathEscape(r.Type)
	err := c.do(ctx, http.MethodPost, path, r, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateRepository(ctx context.Context, r *Repository) (*Repository, error) {
	var out Repository
	path := "/service/rest/v1/repositories/" + url.PathEscape(r.Format) + "/" +
		url.PathEscape(r.Type) + "/" + url.PathEscape(r.Name)
	err := c.do(ctx, http.MethodPut, path, r, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteRepository(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/service/rest/v1/repositories/"+url.PathEscape(name), nil, nil)
}
```

- [ ] **Step 4: Run, verify PASS:** `go test ./internal/client/ -v`

- [ ] **Step 5: Commit:** `git commit -am "feat(client): repository CRUD"`

---

### Task 5: Client — security objects (selectors, privileges, roles, users)

**Files:**
- Create: `internal/client/security.go`
- Test: `internal/client/security_test.go`

- [ ] **Step 1: Write failing tests**

```go
package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestSecurityCRUD(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		// content selectors
		case "POST /service/rest/v1/security/content-selectors":
			var cs ContentSelector
			json.NewDecoder(r.Body).Decode(&cs)
			cs.ID = "cs-1"
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(cs)
		case "GET /service/rest/v1/security/content-selectors/cs-1":
			json.NewEncoder(w).Encode(ContentSelector{ID: "cs-1", Name: "team-a", Expression: `path.startsWith("/com/acme/")`})
		case "GET /service/rest/v1/security/content-selectors":
			json.NewEncoder(w).Encode([]ContentSelector{{ID: "cs-1", Name: "team-a"}})
		case "PUT /service/rest/v1/security/content-selectors/cs-1":
			json.NewEncoder(w).Encode(ContentSelector{ID: "cs-1", Name: "team-a"})
		case "DELETE /service/rest/v1/security/content-selectors/cs-1":
			w.WriteHeader(http.StatusNoContent)
		// privileges
		case "POST /service/rest/v1/security/privileges":
			var p Privilege
			json.NewDecoder(r.Body).Decode(&p)
			if p.Type != "repository-content-selector" || p.ContentSelectorID != "cs-1" {
				t.Errorf("privilege payload = %+v", p)
			}
			p.ID = "pr-1"
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(p)
		case "GET /service/rest/v1/security/privileges/pr-1":
			json.NewEncoder(w).Encode(Privilege{ID: "pr-1", Name: "team-a-rw", ContentSelectorID: "cs-1"})
		case "GET /service/rest/v1/security/privileges":
			json.NewEncoder(w).Encode([]Privilege{{ID: "pr-1", Name: "team-a-rw"}})
		case "PUT /service/rest/v1/security/privileges/pr-1":
			json.NewEncoder(w).Encode(Privilege{ID: "pr-1", Name: "team-a-rw"})
		case "DELETE /service/rest/v1/security/privileges/pr-1":
			w.WriteHeader(http.StatusNoContent)
		// roles
		case "POST /service/rest/v1/security/roles":
			var ro Role
			json.NewDecoder(r.Body).Decode(&ro)
			ro.ID = "ro-1"
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(ro)
		case "GET /service/rest/v1/security/roles":
			json.NewEncoder(w).Encode([]Role{{ID: "ro-1", Name: "team-a-dev", Privileges: []string{"pr-1"}}})
		case "PUT /service/rest/v1/security/roles/ro-1":
			json.NewEncoder(w).Encode(Role{ID: "ro-1", Name: "team-a-dev"})
		case "DELETE /service/rest/v1/security/roles/ro-1":
			w.WriteHeader(http.StatusNoContent)
		// users
		case "POST /service/rest/v1/security/users":
			body := map[string]any{}
			json.NewDecoder(r.Body).Decode(&body)
			if body["userId"] != "alice" || body["password"] != "s3cret123" {
				t.Errorf("user payload = %+v", body)
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(User{Username: "alice", Email: "a@x.io"})
		case "GET /service/rest/v1/security/users/alice":
			json.NewEncoder(w).Encode(User{Username: "alice", Email: "a@x.io", Roles: []string{"team-a-dev"}})
		case "PUT /service/rest/v1/security/users/alice":
			json.NewEncoder(w).Encode(User{Username: "alice", Email: "new@x.io"})
		case "PUT /service/rest/v1/security/users/alice/roles":
			body := map[string][]string{}
			json.NewDecoder(r.Body).Decode(&body)
			if len(body["roleIds"]) != 1 || body["roleIds"][0] != "ro-1" {
				t.Errorf("roleIds = %+v", body)
			}
			w.WriteHeader(http.StatusNoContent)
		case "PUT /service/rest/v1/security/users/alice/change-password":
			w.WriteHeader(http.StatusNoContent)
		case "DELETE /service/rest/v1/security/users/alice":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	})
	ctx := context.Background()

	cs, err := c.CreateContentSelector(ctx, &ContentSelector{Name: "team-a", Expression: `path.startsWith("/com/acme/")`})
	if err != nil || cs.ID != "cs-1" {
		t.Fatalf("cs create: %v %+v", err, cs)
	}
	if _, err := c.GetContentSelector(ctx, "cs-1"); err != nil {
		t.Fatalf("cs get: %v", err)
	}
	if _, err := c.ListContentSelectors(ctx); err != nil {
		t.Fatalf("cs list: %v", err)
	}
	if _, err := c.UpdateContentSelector(ctx, &ContentSelector{ID: "cs-1", Name: "team-a"}); err != nil {
		t.Fatalf("cs update: %v", err)
	}
	if err := c.DeleteContentSelector(ctx, "cs-1"); err != nil {
		t.Fatalf("cs delete: %v", err)
	}

	pr, err := c.CreatePrivilege(ctx, &Privilege{Name: "team-a-rw", Type: "repository-content-selector", ContentSelectorID: "cs-1"})
	if err != nil || pr.ID != "pr-1" {
		t.Fatalf("priv create: %v %+v", err, pr)
	}
	if _, err := c.GetPrivilege(ctx, "pr-1"); err != nil {
		t.Fatalf("priv get: %v", err)
	}
	if _, err := c.ListPrivileges(ctx); err != nil {
		t.Fatalf("priv list: %v", err)
	}
	if _, err := c.UpdatePrivilege(ctx, &Privilege{ID: "pr-1", Name: "team-a-rw", Type: "repository-content-selector"}); err != nil {
		t.Fatalf("priv update: %v", err)
	}
	if err := c.DeletePrivilege(ctx, "pr-1"); err != nil {
		t.Fatalf("priv delete: %v", err)
	}

	ro, err := c.CreateRole(ctx, &Role{Name: "team-a-dev", Privileges: []string{"pr-1"}})
	if err != nil || ro.ID != "ro-1" {
		t.Fatalf("role create: %v %+v", err, ro)
	}
	roles, err := c.ListRoles(ctx)
	if err != nil || len(roles) != 1 {
		t.Fatalf("role list: %v %+v", err, roles)
	}
	if _, err := c.UpdateRole(ctx, &Role{ID: "ro-1", Name: "team-a-dev"}); err != nil {
		t.Fatalf("role update: %v", err)
	}
	if err := c.DeleteRole(ctx, "ro-1"); err != nil {
		t.Fatalf("role delete: %v", err)
	}

	u, err := c.CreateUser(ctx, &User{Username: "alice", Email: "a@x.io"}, "s3cret123")
	if err != nil || u.Username != "alice" {
		t.Fatalf("user create: %v %+v", err, u)
	}
	got, err := c.GetUser(ctx, "alice")
	if err != nil || got.Roles[0] != "team-a-dev" {
		t.Fatalf("user get: %v %+v", err, got)
	}
	if _, err := c.UpdateUser(ctx, &User{Username: "alice", Email: "new@x.io"}); err != nil {
		t.Fatalf("user update: %v", err)
	}
	if err := c.SetUserRoles(ctx, "alice", []string{"ro-1"}); err != nil {
		t.Fatalf("user roles: %v", err)
	}
	if err := c.ChangePassword(ctx, "alice", "newpass123"); err != nil {
		t.Fatalf("user password: %v", err)
	}
	if err := c.DeleteUser(ctx, "alice"); err != nil {
		t.Fatalf("user delete: %v", err)
	}
}
```

- [ ] **Step 2: Run, verify FAIL** (compile errors).

- [ ] **Step 3: Implement `internal/client/security.go`**

```go
package client

import (
	"context"
	"net/http"
	"net/url"
)

const secBase = "/service/rest/v1/security"

// ContentSelector mirrors the content selector JSON.
type ContentSelector struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Expression  string `json:"expression"`
}

// Privilege mirrors the privilege JSON. Type is always repository-content-selector.
type Privilege struct {
	ID                string `json:"id,omitempty"`
	Name              string `json:"name"`
	Description       string `json:"description,omitempty"`
	Type              string `json:"type"`
	ContentSelectorID string `json:"contentSelectorId,omitempty"`
	ReadOnly          bool   `json:"readOnly,omitempty"`
}

// Role mirrors the role JSON. Privileges holds privilege IDs (read and write).
type Role struct {
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Privileges  []string `json:"privileges"`
	ReadOnly    bool     `json:"readOnly,omitempty"`
}

// User mirrors the user JSON. GET returns role names in Roles;
// writes go through SetUserRoles with role IDs instead.
type User struct {
	Username  string   `json:"userId"`
	Email     string   `json:"emailAddress"`
	FirstName string   `json:"firstName,omitempty"`
	LastName  string   `json:"lastName,omitempty"`
	Status    string   `json:"status,omitempty"`
	Source    string   `json:"source,omitempty"`
	Roles     []string `json:"roles,omitempty"`
}

// --- content selectors ---

func (c *Client) ListContentSelectors(ctx context.Context) ([]ContentSelector, error) {
	var out []ContentSelector
	err := c.do(ctx, http.MethodGet, secBase+"/content-selectors", nil, &out)
	return out, err
}

func (c *Client) GetContentSelector(ctx context.Context, id string) (*ContentSelector, error) {
	var out ContentSelector
	err := c.do(ctx, http.MethodGet, secBase+"/content-selectors/"+url.PathEscape(id), nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateContentSelector(ctx context.Context, cs *ContentSelector) (*ContentSelector, error) {
	var out ContentSelector
	err := c.do(ctx, http.MethodPost, secBase+"/content-selectors", cs, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateContentSelector(ctx context.Context, cs *ContentSelector) (*ContentSelector, error) {
	var out ContentSelector
	err := c.do(ctx, http.MethodPut, secBase+"/content-selectors/"+url.PathEscape(cs.ID), cs, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteContentSelector(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, secBase+"/content-selectors/"+url.PathEscape(id), nil, nil)
}

// --- privileges ---

func (c *Client) ListPrivileges(ctx context.Context) ([]Privilege, error) {
	var out []Privilege
	err := c.do(ctx, http.MethodGet, secBase+"/privileges", nil, &out)
	return out, err
}

func (c *Client) GetPrivilege(ctx context.Context, id string) (*Privilege, error) {
	var out Privilege
	err := c.do(ctx, http.MethodGet, secBase+"/privileges/"+url.PathEscape(id), nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreatePrivilege(ctx context.Context, p *Privilege) (*Privilege, error) {
	var out Privilege
	err := c.do(ctx, http.MethodPost, secBase+"/privileges", p, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdatePrivilege(ctx context.Context, p *Privilege) (*Privilege, error) {
	var out Privilege
	err := c.do(ctx, http.MethodPut, secBase+"/privileges/"+url.PathEscape(p.ID), p, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeletePrivilege(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, secBase+"/privileges/"+url.PathEscape(id), nil, nil)
}

// --- roles (no GET-by-id endpoint — callers list and match) ---

func (c *Client) ListRoles(ctx context.Context) ([]Role, error) {
	var out []Role
	err := c.do(ctx, http.MethodGet, secBase+"/roles", nil, &out)
	return out, err
}

func (c *Client) CreateRole(ctx context.Context, ro *Role) (*Role, error) {
	var out Role
	err := c.do(ctx, http.MethodPost, secBase+"/roles", ro, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateRole(ctx context.Context, ro *Role) (*Role, error) {
	var out Role
	err := c.do(ctx, http.MethodPut, secBase+"/roles/"+url.PathEscape(ro.ID), ro, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteRole(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, secBase+"/roles/"+url.PathEscape(id), nil, nil)
}

// --- users ---

func (c *Client) GetUser(ctx context.Context, username string) (*User, error) {
	var out User
	err := c.do(ctx, http.MethodGet, secBase+"/users/"+url.PathEscape(username), nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateUser(ctx context.Context, u *User, password string) (*User, error) {
	body := struct {
		User
		Password string `json:"password"`
	}{User: *u, Password: password}
	var out User
	err := c.do(ctx, http.MethodPost, secBase+"/users", body, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateUser(ctx context.Context, u *User) (*User, error) {
	var out User
	err := c.do(ctx, http.MethodPut, secBase+"/users/"+url.PathEscape(u.Username), u, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteUser(ctx context.Context, username string) error {
	return c.do(ctx, http.MethodDelete, secBase+"/users/"+url.PathEscape(username), nil, nil)
}

// SetUserRoles replaces the user's roles. roleIDs are role IDs, not names.
func (c *Client) SetUserRoles(ctx context.Context, username string, roleIDs []string) error {
	body := map[string][]string{"roleIds": roleIDs}
	return c.do(ctx, http.MethodPut, secBase+"/users/"+url.PathEscape(username)+"/roles", body, nil)
}

// ChangePassword sets a new password (admin reset — no oldPassword).
func (c *Client) ChangePassword(ctx context.Context, username, newPassword string) error {
	body := map[string]string{"newPassword": newPassword}
	return c.do(ctx, http.MethodPut, secBase+"/users/"+url.PathEscape(username)+"/change-password", body, nil)
}
```

- [ ] **Step 4: Run, verify PASS:** `go test ./internal/client/ -v`

- [ ] **Step 5: Commit:** `git commit -am "feat(client): security objects CRUD"`

---

### Task 6: Provider Configure + acceptance harness

**Files:**
- Modify: `internal/provider/provider.go`
- Create: `internal/provider/provider_test.go`, `docker-compose.acc.yml`

- [ ] **Step 1: Replace `internal/provider/provider.go` Schema/Configure with the real implementation**

```go
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nexspence/terraform-provider-nexspence/internal/client"
)

// New returns the provider factory used by main and by acceptance tests.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &nexspenceProvider{version: version}
	}
}

type nexspenceProvider struct {
	version string
}

type providerModel struct {
	URL      types.String `tfsdk:"url"`
	Token    types.String `tfsdk:"token"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

func (p *nexspenceProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "nexspence"
	resp.Version = p.version
}

func (p *nexspenceProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage Nexspence repositories, blob stores, and RBAC objects.",
		Attributes: map[string]schema.Attribute{
			"url": schema.StringAttribute{
				Optional:    true,
				Description: "Base URL of the Nexspence server. Falls back to NEXSPENCE_URL.",
			},
			"token": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "nxs_* API token (preferred). Falls back to NEXSPENCE_TOKEN. Mutually exclusive with username/password.",
			},
			"username": schema.StringAttribute{
				Optional:    true,
				Description: "Username for basic auth. Falls back to NEXSPENCE_USERNAME.",
			},
			"password": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Password for basic auth. Falls back to NEXSPENCE_PASSWORD.",
			},
		},
	}
}

func (p *nexspenceProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	get := func(v types.String, env string) string {
		if !v.IsNull() && v.ValueString() != "" {
			return v.ValueString()
		}
		return os.Getenv(env)
	}
	c, err := client.New(client.Config{
		URL:      get(cfg.URL, "NEXSPENCE_URL"),
		Token:    get(cfg.Token, "NEXSPENCE_TOKEN"),
		Username: get(cfg.Username, "NEXSPENCE_USERNAME"),
		Password: get(cfg.Password, "NEXSPENCE_PASSWORD"),
	})
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("url"), "Invalid provider configuration", err.Error())
		return
	}
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *nexspenceProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		// appended as resource tasks land:
		// NewBlobStoreResource, NewRepositoryResource, NewContentSelectorResource,
		// NewPrivilegeResource, NewRoleResource, NewUserResource,
	}
}

func (p *nexspenceProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}
```

- [ ] **Step 2: Write `internal/provider/provider_test.go`** (shared acceptance harness)

```go
package provider

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// testAccProtoV6ProviderFactories is used by every acceptance test.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"nexspence": providerserver.NewProtocol6WithError(New("test")()),
}

// testAccPreCheck skips unless the live-stack env is present.
func testAccPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("NEXSPENCE_URL") == "" {
		t.Fatal("NEXSPENCE_URL must be set for acceptance tests (make stack-up)")
	}
	if os.Getenv("NEXSPENCE_TOKEN") == "" &&
		(os.Getenv("NEXSPENCE_USERNAME") == "" || os.Getenv("NEXSPENCE_PASSWORD") == "") {
		t.Fatal("NEXSPENCE_TOKEN or NEXSPENCE_USERNAME/NEXSPENCE_PASSWORD must be set")
	}
}
```

Add deps: `go get github.com/hashicorp/terraform-plugin-testing@latest github.com/hashicorp/terraform-plugin-go@latest && go mod tidy`

- [ ] **Step 3: Write `docker-compose.acc.yml`**

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: nexspence
      POSTGRES_PASSWORD: nexspence
      POSTGRES_DB: nexspence
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U nexspence"]
      interval: 2s
      timeout: 3s
      retries: 20

  nexspence:
    image: ghcr.io/nexspence/nexspence:latest
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      NEXSPENCE_DATABASE_DSN: "postgres://nexspence:nexspence@postgres:5432/nexspence?sslmode=disable"
      NEXSPENCE_AUTH_JWT_SECRET: "acceptance-test-secret-min-32-chars!!"
      NEXSPENCE_HTTP_ADDR: ":8081"
      NEXSPENCE_STORAGE_DEFAULT_TYPE: "local"
      NEXSPENCE_STORAGE_LOCAL_BASE_PATH: "/data/blobs"
    ports:
      - "8081:8081"
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://localhost:8081/service/rest/v1/status"]
      interval: 2s
      timeout: 3s
      retries: 30
```

> If `/service/rest/v1/status` 404s on the current image, use `/api/v1/system/services` with auth or simply `http://localhost:8081/` — verify once during Task 7 and fix the healthcheck.

- [ ] **Step 4: Verify:** `go build ./... && go test ./internal/provider/ -v` (no acceptance tests yet — compiles, 0 tests run). `make stack-up` brings the stack healthy; `curl -u admin:admin123 http://localhost:8081/service/rest/v1/repositories` returns JSON. `make stack-down`.

- [ ] **Step 5: Commit:** `git add -A && git commit -m "feat(provider): Configure with env fallbacks + acceptance harness"`

---

### Task 7: Resource `nexspence_blobstore`

**Files:**
- Create: `internal/provider/blobstore_resource.go`
- Test: `internal/provider/blobstore_resource_test.go`

- [ ] **Step 1: Write failing acceptance test**

```go
package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccBlobStore_local(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "nexspence_blobstore" "test" {
  name = "acc-local"
  type = "local"
  path = "./data/blobs/acc-local"
}`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("nexspence_blobstore.test", "name", "acc-local"),
					resource.TestCheckResourceAttrSet("nexspence_blobstore.test", "id"),
				),
			},
			{ // update quota in place
				Config: `
resource "nexspence_blobstore" "test" {
  name        = "acc-local"
  type        = "local"
  path        = "./data/blobs/acc-local"
  quota_bytes = 1073741824
}`,
				Check: resource.TestCheckResourceAttr("nexspence_blobstore.test", "quota_bytes", "1073741824"),
			},
			{
				ResourceName:      "nexspence_blobstore.test",
				ImportState:       true,
				ImportStateId:     "acc-local",
				ImportStateVerify: true,
			},
		},
	})
}
```

- [ ] **Step 2: Run, verify FAIL:** `make stack-up && make testacc` — error: invalid resource type `nexspence_blobstore` (not registered).

- [ ] **Step 3: Implement `internal/provider/blobstore_resource.go`**

```go
package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nexspence/terraform-provider-nexspence/internal/client"
)

// NewBlobStoreResource is registered in provider.Resources.
func NewBlobStoreResource() resource.Resource { return &blobStoreResource{} }

type blobStoreResource struct {
	client *client.Client
}

type blobStoreS3Model struct {
	Bucket         types.String `tfsdk:"bucket"`
	Region         types.String `tfsdk:"region"`
	Endpoint       types.String `tfsdk:"endpoint"`
	AccessKey      types.String `tfsdk:"access_key"`
	SecretKey      types.String `tfsdk:"secret_key"`
	ForcePathStyle types.Bool   `tfsdk:"force_path_style"`
}

type blobStoreModel struct {
	ID         types.String      `tfsdk:"id"`
	Name       types.String      `tfsdk:"name"`
	Type       types.String      `tfsdk:"type"`
	Path       types.String      `tfsdk:"path"`
	S3         *blobStoreS3Model `tfsdk:"s3"`
	QuotaBytes types.Int64       `tfsdk:"quota_bytes"`
}

func (r *blobStoreResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_blobstore"
}

func (r *blobStoreResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Nexspence blob store (local filesystem or S3).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"type": schema.StringAttribute{
				Required:      true,
				Validators:    []validator.String{stringvalidator.OneOf("local", "s3")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"path": schema.StringAttribute{
				Optional:    true,
				Description: "Filesystem path (type = local).",
			},
			"quota_bytes": schema.Int64Attribute{Optional: true},
			"s3": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "S3 connection settings (type = s3).",
				Attributes: map[string]schema.Attribute{
					"bucket":           schema.StringAttribute{Required: true},
					"region":           schema.StringAttribute{Optional: true},
					"endpoint":         schema.StringAttribute{Optional: true, Description: "Custom S3 endpoint (MinIO/Ceph)."},
					"access_key":       schema.StringAttribute{Optional: true},
					"secret_key":       schema.StringAttribute{Optional: true, Sensitive: true},
					"force_path_style": schema.BoolAttribute{Optional: true},
				},
			},
		},
	}
}

func (r *blobStoreResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data blobStoreModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() || data.Type.IsUnknown() {
		return
	}
	switch data.Type.ValueString() {
	case "s3":
		if data.S3 == nil {
			resp.Diagnostics.AddAttributeError(path.Root("s3"), "Missing s3 block", `type = "s3" requires an s3 block`)
		}
	case "local":
		if data.S3 != nil {
			resp.Diagnostics.AddAttributeError(path.Root("s3"), "Unexpected s3 block", `s3 block is only valid when type = "s3"`)
		}
	}
}

func (r *blobStoreResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

// toAPI builds the API payload from the plan model.
func (m *blobStoreModel) toAPI() *client.BlobStore {
	cfg := map[string]any{}
	switch m.Type.ValueString() {
	case "local":
		if !m.Path.IsNull() {
			cfg["path"] = m.Path.ValueString()
		}
	case "s3":
		cfg["bucket"] = m.S3.Bucket.ValueString()
		if !m.S3.Region.IsNull() {
			cfg["region"] = m.S3.Region.ValueString()
		}
		if !m.S3.Endpoint.IsNull() {
			cfg["endpoint"] = m.S3.Endpoint.ValueString()
		}
		if !m.S3.AccessKey.IsNull() {
			cfg["access_key"] = m.S3.AccessKey.ValueString()
		}
		if !m.S3.SecretKey.IsNull() {
			cfg["secret_key"] = m.S3.SecretKey.ValueString()
		}
		if !m.S3.ForcePathStyle.IsNull() {
			cfg["force_path_style"] = m.S3.ForcePathStyle.ValueBool()
		}
	}
	return &client.BlobStore{
		Name:       m.Name.ValueString(),
		Type:       m.Type.ValueString(),
		Config:     cfg,
		QuotaBytes: m.QuotaBytes.ValueInt64(),
	}
}

// fromAPI refreshes non-secret state from the API object. Secrets (secret_key)
// keep their prior state value — the API never returns them.
func (m *blobStoreModel) fromAPI(bs *client.BlobStore) {
	m.ID = types.StringValue(bs.ID)
	m.Name = types.StringValue(bs.Name)
	m.Type = types.StringValue(bs.Type)
	if bs.QuotaBytes > 0 {
		m.QuotaBytes = types.Int64Value(bs.QuotaBytes)
	} else {
		m.QuotaBytes = types.Int64Null()
	}
	str := func(k string) types.String {
		if v, ok := bs.Config[k].(string); ok && v != "" {
			return types.StringValue(v)
		}
		return types.StringNull()
	}
	switch bs.Type {
	case "local":
		m.Path = str("path")
	case "s3":
		if m.S3 == nil {
			m.S3 = &blobStoreS3Model{SecretKey: types.StringNull()}
		}
		m.S3.Bucket = str("bucket")
		m.S3.Region = str("region")
		m.S3.Endpoint = str("endpoint")
		m.S3.AccessKey = str("access_key")
		if v, ok := bs.Config["force_path_style"].(bool); ok {
			m.S3.ForcePathStyle = types.BoolValue(v)
		} else {
			m.S3.ForcePathStyle = types.BoolNull()
		}
	}
}

func (r *blobStoreResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan blobStoreModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.client.CreateBlobStore(ctx, plan.toAPI())
	if err != nil {
		resp.Diagnostics.AddError("Create blob store failed", err.Error())
		return
	}
	plan.fromAPI(created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *blobStoreResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state blobStoreModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	bs, err := r.client.GetBlobStore(ctx, state.Name.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read blob store failed", err.Error())
		return
	}
	state.fromAPI(bs)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *blobStoreResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan blobStoreModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	updated, err := r.client.UpdateBlobStore(ctx, plan.toAPI())
	if err != nil {
		resp.Diagnostics.AddError("Update blob store failed", err.Error())
		return
	}
	plan.fromAPI(updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *blobStoreResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state blobStoreModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteBlobStore(ctx, state.Name.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Delete blob store failed", err.Error())
	}
}

// ImportState imports by blob store name.
func (r *blobStoreResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
```

Register in `provider.go` `Resources()`: `return []func() resource.Resource{NewBlobStoreResource}`.

- [ ] **Step 4: Run, verify PASS:** `make testacc` (stack must be up) — `TestAccBlobStore_local` PASS.

- [ ] **Step 5: Commit:** `git add -A && git commit -m "feat: nexspence_blobstore resource"`

---

### Task 8: Resource `nexspence_repository`

**Files:**
- Create: `internal/provider/repository_resource.go`
- Test: `internal/provider/repository_resource_test.go`

- [ ] **Step 1: Write failing acceptance test** (hosted + update, proxy, group, import)

```go
package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccRepository_hostedProxyGroup(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "nexspence_repository" "hosted" {
  name       = "acc-maven-releases"
  format     = "maven2"
  type       = "hosted"
  blob_store = "default"
}

resource "nexspence_repository" "proxy" {
  name       = "acc-maven-central"
  format     = "maven2"
  type       = "proxy"
  blob_store = "default"
  proxy {
    remote_url = "https://repo1.maven.org/maven2/"
  }
}

resource "nexspence_repository" "group" {
  name       = "acc-maven-all"
  format     = "maven2"
  type       = "group"
  blob_store = "default"
  group {
    member_names    = [nexspence_repository.hosted.name, nexspence_repository.proxy.name]
    writable_member = nexspence_repository.hosted.name
  }
}`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("nexspence_repository.hosted", "blob_store", "default"),
					resource.TestCheckResourceAttr("nexspence_repository.proxy", "proxy.remote_url", "https://repo1.maven.org/maven2/"),
					resource.TestCheckResourceAttr("nexspence_repository.group", "group.member_names.0", "acc-maven-releases"),
					resource.TestCheckResourceAttrSet("nexspence_repository.hosted", "id"),
				),
			},
			{ // in-place update of hosted repo
				Config: `
resource "nexspence_repository" "hosted" {
  name            = "acc-maven-releases"
  format          = "maven2"
  type            = "hosted"
  blob_store      = "default"
  allow_anonymous = true
  description     = "release artifacts"
}

resource "nexspence_repository" "proxy" {
  name       = "acc-maven-central"
  format     = "maven2"
  type       = "proxy"
  blob_store = "default"
  proxy {
    remote_url = "https://repo1.maven.org/maven2/"
  }
}

resource "nexspence_repository" "group" {
  name       = "acc-maven-all"
  format     = "maven2"
  type       = "group"
  blob_store = "default"
  group {
    member_names    = [nexspence_repository.hosted.name, nexspence_repository.proxy.name]
    writable_member = nexspence_repository.hosted.name
  }
}`,
				Check: resource.TestCheckResourceAttr("nexspence_repository.hosted", "allow_anonymous", "true"),
			},
			{
				ResourceName:      "nexspence_repository.hosted",
				ImportState:       true,
				ImportStateId:     "acc-maven-releases",
				ImportStateVerify: true,
			},
		},
	})
}
```

- [ ] **Step 2: Run, verify FAIL:** invalid resource type `nexspence_repository`.

- [ ] **Step 3: Implement `internal/provider/repository_resource.go`**

```go
package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nexspence/terraform-provider-nexspence/internal/client"
)

// repoFormats is the full Nexspence format enum (14 formats).
var repoFormats = []string{
	"maven2", "npm", "pypi", "docker", "go", "nuget", "raw",
	"apt", "yum", "helm", "cargo", "conan", "conda", "terraform",
}

// NewRepositoryResource is registered in provider.Resources.
func NewRepositoryResource() resource.Resource { return &repositoryResource{} }

type repositoryResource struct {
	client *client.Client
}

type repoProxyModel struct {
	RemoteURL types.String `tfsdk:"remote_url"`
}

type repoGroupModel struct {
	MemberNames    []types.String `tfsdk:"member_names"`
	WritableMember types.String   `tfsdk:"writable_member"`
}

type repositoryModel struct {
	ID               types.String    `tfsdk:"id"`
	Name             types.String    `tfsdk:"name"`
	Format           types.String    `tfsdk:"format"`
	Type             types.String    `tfsdk:"type"`
	BlobStore        types.String    `tfsdk:"blob_store"`
	Online           types.Bool      `tfsdk:"online"`
	AllowAnonymous   types.Bool      `tfsdk:"allow_anonymous"`
	Description      types.String    `tfsdk:"description"`
	QuotaBytes       types.Int64     `tfsdk:"quota_bytes"`
	CleanupPolicyIDs []types.String  `tfsdk:"cleanup_policy_ids"`
	Proxy            *repoProxyModel `tfsdk:"proxy"`
	Group            *repoGroupModel `tfsdk:"group"`
	URL              types.String    `tfsdk:"url"`
}

func (r *repositoryResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_repository"
}

func (r *repositoryResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Nexspence repository (hosted, proxy, or group) of any supported format.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"format": schema.StringAttribute{
				Required:      true,
				Validators:    []validator.String{stringvalidator.OneOf(repoFormats...)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"type": schema.StringAttribute{
				Required:      true,
				Validators:    []validator.String{stringvalidator.OneOf("hosted", "proxy", "group")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"blob_store": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("default"),
				Description: "Blob store name (resolved to its ID against the API).",
			},
			"online":          schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
			"allow_anonymous": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
			"description":     schema.StringAttribute{Optional: true},
			"quota_bytes":     schema.Int64Attribute{Optional: true},
			"cleanup_policy_ids": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
			},
			"url": schema.StringAttribute{Computed: true},
		},
		Blocks: map[string]schema.Block{
			"proxy": schema.SingleNestedBlock{
				Description: "Proxy settings (type = proxy).",
				Attributes: map[string]schema.Attribute{
					"remote_url": schema.StringAttribute{Optional: true}, // presence enforced in ValidateConfig
				},
			},
			"group": schema.SingleNestedBlock{
				Description: "Group settings (type = group).",
				Attributes: map[string]schema.Attribute{
					"member_names": schema.ListAttribute{
						Optional:    true,
						ElementType: types.StringType,
						Validators:  []validator.List{listvalidator.SizeAtLeast(1)},
					},
					"writable_member": schema.StringAttribute{Optional: true},
				},
			},
		},
	}
}

func (r *repositoryResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data repositoryModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() || data.Type.IsUnknown() {
		return
	}
	typ := data.Type.ValueString()
	if typ == "proxy" {
		if data.Proxy == nil || data.Proxy.RemoteURL.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("proxy"), "Missing proxy configuration",
				`type = "proxy" requires a proxy block with remote_url`)
		}
	} else if data.Proxy != nil {
		resp.Diagnostics.AddAttributeError(path.Root("proxy"), "Unexpected proxy block",
			`proxy block is only valid when type = "proxy"`)
	}
	if typ == "group" {
		if data.Group == nil || len(data.Group.MemberNames) == 0 {
			resp.Diagnostics.AddAttributeError(path.Root("group"), "Missing group configuration",
				`type = "group" requires a group block with member_names`)
		}
	} else if data.Group != nil {
		resp.Diagnostics.AddAttributeError(path.Root("group"), "Unexpected group block",
			`group block is only valid when type = "group"`)
	}
}

func (r *repositoryResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

// toAPI resolves blob_store name -> ID and assembles the API payload.
func (r *repositoryResource) toAPI(ctx context.Context, m *repositoryModel) (*client.Repository, error) {
	bs, err := r.client.GetBlobStore(ctx, m.BlobStore.ValueString())
	if err != nil {
		return nil, fmt.Errorf("resolve blob store %q: %w", m.BlobStore.ValueString(), err)
	}
	online := m.Online.ValueBool()
	repo := &client.Repository{
		Name:           m.Name.ValueString(),
		Format:         m.Format.ValueString(),
		Type:           m.Type.ValueString(),
		BlobStoreID:    bs.ID,
		Online:         &online,
		AllowAnonymous: m.AllowAnonymous.ValueBool(),
		Description:    m.Description.ValueString(),
		QuotaBytes:     m.QuotaBytes.ValueInt64(),
	}
	for _, id := range m.CleanupPolicyIDs {
		repo.CleanupPolicyIDs = append(repo.CleanupPolicyIDs, id.ValueString())
	}
	if m.Proxy != nil {
		repo.ProxyConfig = map[string]any{"remote_url": m.Proxy.RemoteURL.ValueString()}
	}
	if m.Group != nil {
		members := make([]any, 0, len(m.Group.MemberNames))
		for _, n := range m.Group.MemberNames {
			members = append(members, n.ValueString())
		}
		fc := map[string]any{"member_names": members}
		if !m.Group.WritableMember.IsNull() {
			fc["writable_member"] = m.Group.WritableMember.ValueString()
		}
		repo.FormatConfig = fc
	}
	return repo, nil
}

// fromAPI refreshes state from the API object, mapping blobStoreId -> name.
func (r *repositoryResource) fromAPI(ctx context.Context, m *repositoryModel, repo *client.Repository) error {
	m.ID = types.StringValue(repo.ID)
	m.Name = types.StringValue(repo.Name)
	m.Format = types.StringValue(repo.Format)
	m.Type = types.StringValue(repo.Type)
	if repo.Online != nil {
		m.Online = types.BoolValue(*repo.Online)
	} else {
		m.Online = types.BoolValue(true)
	}
	m.AllowAnonymous = types.BoolValue(repo.AllowAnonymous)
	if repo.Description != "" {
		m.Description = types.StringValue(repo.Description)
	} else {
		m.Description = types.StringNull()
	}
	if repo.QuotaBytes > 0 {
		m.QuotaBytes = types.Int64Value(repo.QuotaBytes)
	} else {
		m.QuotaBytes = types.Int64Null()
	}
	m.CleanupPolicyIDs = nil
	for _, id := range repo.CleanupPolicyIDs {
		m.CleanupPolicyIDs = append(m.CleanupPolicyIDs, types.StringValue(id))
	}
	m.URL = types.StringValue(repo.URL)

	// blobStoreId -> name
	m.BlobStore = types.StringNull()
	if repo.BlobStoreID != "" {
		stores, err := r.client.ListBlobStores(ctx)
		if err != nil {
			return fmt.Errorf("list blob stores: %w", err)
		}
		for _, s := range stores {
			if s.ID == repo.BlobStoreID {
				m.BlobStore = types.StringValue(s.Name)
				break
			}
		}
	}

	if repo.Type == "proxy" {
		ru, _ := repo.ProxyConfig["remote_url"].(string)
		m.Proxy = &repoProxyModel{RemoteURL: types.StringValue(ru)}
	} else {
		m.Proxy = nil
	}
	if repo.Type == "group" {
		g := &repoGroupModel{WritableMember: types.StringNull()}
		if raw, ok := repo.FormatConfig["member_names"].([]any); ok {
			for _, v := range raw {
				if s, ok := v.(string); ok {
					g.MemberNames = append(g.MemberNames, types.StringValue(s))
				}
			}
		}
		if wm, ok := repo.FormatConfig["writable_member"].(string); ok && wm != "" {
			g.WritableMember = types.StringValue(wm)
		}
		m.Group = g
	} else {
		m.Group = nil
	}
	return nil
}

func (r *repositoryResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan repositoryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	payload, err := r.toAPI(ctx, &plan)
	if err != nil {
		resp.Diagnostics.AddError("Create repository failed", err.Error())
		return
	}
	created, err := r.client.CreateRepository(ctx, payload)
	if err != nil {
		resp.Diagnostics.AddError("Create repository failed", err.Error())
		return
	}
	if err := r.fromAPI(ctx, &plan, created); err != nil {
		resp.Diagnostics.AddError("Refresh after create failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *repositoryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state repositoryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	repo, err := r.client.GetRepository(ctx, state.Name.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read repository failed", err.Error())
		return
	}
	if err := r.fromAPI(ctx, &state, repo); err != nil {
		resp.Diagnostics.AddError("Read repository failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *repositoryResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan repositoryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	payload, err := r.toAPI(ctx, &plan)
	if err != nil {
		resp.Diagnostics.AddError("Update repository failed", err.Error())
		return
	}
	updated, err := r.client.UpdateRepository(ctx, payload)
	if err != nil {
		resp.Diagnostics.AddError("Update repository failed", err.Error())
		return
	}
	if err := r.fromAPI(ctx, &plan, updated); err != nil {
		resp.Diagnostics.AddError("Refresh after update failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *repositoryResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state repositoryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteRepository(ctx, state.Name.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Delete repository failed", err.Error())
	}
}

// ImportState imports by repository name.
func (r *repositoryResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
```

Register `NewRepositoryResource` in `provider.go` `Resources()`.

- [ ] **Step 4: Run, verify PASS:** `make testacc` — all repository steps PASS. Watch for:
  - `ImportStateVerify` mismatches on `blob_store`/`online` defaults — if the API echoes different zero values, add `ImportStateVerifyIgnore: []string{...}` ONLY after confirming the live values are semantically equal.
  - Group `member_names` ordering is significant (ordered delegate) — list, not set, by design.

- [ ] **Step 5: Commit:** `git add -A && git commit -m "feat: nexspence_repository resource (hosted/proxy/group)"`

---

### Task 9: Resource `nexspence_content_selector`

**Files:**
- Create: `internal/provider/content_selector_resource.go`
- Test: `internal/provider/content_selector_resource_test.go`

- [ ] **Step 1: Write failing acceptance test**

```go
package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccContentSelector_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "nexspence_content_selector" "test" {
  name       = "acc-team-a"
  expression = "path.startsWith(\"/com/acme/\")"
}`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("nexspence_content_selector.test", "id"),
					resource.TestCheckResourceAttr("nexspence_content_selector.test", "name", "acc-team-a"),
				),
			},
			{
				Config: `
resource "nexspence_content_selector" "test" {
  name        = "acc-team-a"
  description = "team A artifacts"
  expression  = "path.startsWith(\"/com/acme/\") && format == \"maven2\""
}`,
				Check: resource.TestCheckResourceAttr("nexspence_content_selector.test", "description", "team A artifacts"),
			},
			{
				ResourceName:      "nexspence_content_selector.test",
				ImportState:       true,
				ImportStateId:     "acc-team-a", // import by NAME (custom importer)
				ImportStateVerify: true,
			},
		},
	})
}
```

- [ ] **Step 2: Run, verify FAIL.**

- [ ] **Step 3: Implement `internal/provider/content_selector_resource.go`**

```go
package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nexspence/terraform-provider-nexspence/internal/client"
)

// NewContentSelectorResource is registered in provider.Resources.
func NewContentSelectorResource() resource.Resource { return &contentSelectorResource{} }

type contentSelectorResource struct {
	client *client.Client
}

type contentSelectorModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Expression  types.String `tfsdk:"expression"`
}

func (r *contentSelectorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_content_selector"
}

func (r *contentSelectorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A CEL content selector (variables: format, path, repository).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{Optional: true},
			"expression":  schema.StringAttribute{Required: true},
		},
	}
}

func (r *contentSelectorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

func (m *contentSelectorModel) fromAPI(cs *client.ContentSelector) {
	m.ID = types.StringValue(cs.ID)
	m.Name = types.StringValue(cs.Name)
	if cs.Description != "" {
		m.Description = types.StringValue(cs.Description)
	} else {
		m.Description = types.StringNull()
	}
	m.Expression = types.StringValue(cs.Expression)
}

func (r *contentSelectorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan contentSelectorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.client.CreateContentSelector(ctx, &client.ContentSelector{
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
		Expression:  plan.Expression.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create content selector failed", err.Error())
		return
	}
	plan.fromAPI(created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *contentSelectorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state contentSelectorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cs, err := r.client.GetContentSelector(ctx, state.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read content selector failed", err.Error())
		return
	}
	state.fromAPI(cs)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *contentSelectorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan contentSelectorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	updated, err := r.client.UpdateContentSelector(ctx, &client.ContentSelector{
		ID:          plan.ID.ValueString(),
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
		Expression:  plan.Expression.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update content selector failed", err.Error())
		return
	}
	plan.fromAPI(updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *contentSelectorResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state contentSelectorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteContentSelector(ctx, state.ID.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Delete content selector failed", err.Error())
	}
}

// ImportState accepts the selector NAME, resolves it to the API ID via list.
func (r *contentSelectorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	all, err := r.client.ListContentSelectors(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Import failed", err.Error())
		return
	}
	for _, cs := range all {
		if cs.Name == req.ID {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), cs.ID)...)
			return
		}
	}
	resp.Diagnostics.AddError("Import failed", fmt.Sprintf("content selector %q not found", req.ID))
}
```

Register `NewContentSelectorResource` in `provider.go`.

- [ ] **Step 4: Run, verify PASS:** `make testacc` — `TestAccContentSelector_basic` PASS.

- [ ] **Step 5: Commit:** `git add -A && git commit -m "feat: nexspence_content_selector resource"`

---

### Task 10: Resource `nexspence_privilege`

**Files:**
- Create: `internal/provider/privilege_resource.go`
- Test: `internal/provider/privilege_resource_test.go`

- [ ] **Step 1: Write failing acceptance test**

```go
package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccPrivilege_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "nexspence_content_selector" "sel" {
  name       = "acc-priv-sel"
  expression = "format == \"raw\""
}

resource "nexspence_privilege" "test" {
  name             = "acc-priv"
  description      = "raw access"
  content_selector = nexspence_content_selector.sel.name
}`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("nexspence_privilege.test", "id"),
					resource.TestCheckResourceAttr("nexspence_privilege.test", "content_selector", "acc-priv-sel"),
				),
			},
			{
				ResourceName:      "nexspence_privilege.test",
				ImportState:       true,
				ImportStateId:     "acc-priv",
				ImportStateVerify: true,
			},
		},
	})
}
```

- [ ] **Step 2: Run, verify FAIL.**

- [ ] **Step 3: Implement `internal/provider/privilege_resource.go`**

```go
package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nexspence/terraform-provider-nexspence/internal/client"
)

const privilegeType = "repository-content-selector"

// NewPrivilegeResource is registered in provider.Resources.
func NewPrivilegeResource() resource.Resource { return &privilegeResource{} }

type privilegeResource struct {
	client *client.Client
}

type privilegeModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Description     types.String `tfsdk:"description"`
	ContentSelector types.String `tfsdk:"content_selector"`
}

func (r *privilegeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_privilege"
}

func (r *privilegeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A content-selector-scoped privilege (type repository-content-selector).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{Optional: true},
			"content_selector": schema.StringAttribute{
				Required:    true,
				Description: "Name of the content selector this privilege grants access through.",
			},
		},
	}
}

func (r *privilegeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

// selectorIDByName resolves a content selector name to its ID.
func (r *privilegeResource) selectorIDByName(ctx context.Context, name string) (string, error) {
	all, err := r.client.ListContentSelectors(ctx)
	if err != nil {
		return "", fmt.Errorf("list content selectors: %w", err)
	}
	for _, cs := range all {
		if cs.Name == name {
			return cs.ID, nil
		}
	}
	return "", fmt.Errorf("content selector %q not found", name)
}

// fromAPI refreshes state, mapping contentSelectorId -> name.
func (r *privilegeResource) fromAPI(ctx context.Context, m *privilegeModel, p *client.Privilege) error {
	m.ID = types.StringValue(p.ID)
	m.Name = types.StringValue(p.Name)
	if p.Description != "" {
		m.Description = types.StringValue(p.Description)
	} else {
		m.Description = types.StringNull()
	}
	m.ContentSelector = types.StringNull()
	if p.ContentSelectorID != "" {
		cs, err := r.client.GetContentSelector(ctx, p.ContentSelectorID)
		if err != nil && !errors.Is(err, client.ErrNotFound) {
			return fmt.Errorf("resolve content selector: %w", err)
		}
		if cs != nil {
			m.ContentSelector = types.StringValue(cs.Name)
		}
	}
	return nil
}

func (r *privilegeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan privilegeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	csID, err := r.selectorIDByName(ctx, plan.ContentSelector.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Create privilege failed", err.Error())
		return
	}
	created, err := r.client.CreatePrivilege(ctx, &client.Privilege{
		Name:              plan.Name.ValueString(),
		Description:       plan.Description.ValueString(),
		Type:              privilegeType,
		ContentSelectorID: csID,
	})
	if err != nil {
		resp.Diagnostics.AddError("Create privilege failed", err.Error())
		return
	}
	if err := r.fromAPI(ctx, &plan, created); err != nil {
		resp.Diagnostics.AddError("Refresh after create failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *privilegeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state privilegeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetPrivilege(ctx, state.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read privilege failed", err.Error())
		return
	}
	if err := r.fromAPI(ctx, &state, p); err != nil {
		resp.Diagnostics.AddError("Read privilege failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *privilegeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan privilegeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	csID, err := r.selectorIDByName(ctx, plan.ContentSelector.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Update privilege failed", err.Error())
		return
	}
	updated, err := r.client.UpdatePrivilege(ctx, &client.Privilege{
		ID:                plan.ID.ValueString(),
		Name:              plan.Name.ValueString(),
		Description:       plan.Description.ValueString(),
		Type:              privilegeType,
		ContentSelectorID: csID,
	})
	if err != nil {
		resp.Diagnostics.AddError("Update privilege failed", err.Error())
		return
	}
	if err := r.fromAPI(ctx, &plan, updated); err != nil {
		resp.Diagnostics.AddError("Refresh after update failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *privilegeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state privilegeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeletePrivilege(ctx, state.ID.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Delete privilege failed", err.Error())
	}
}

// ImportState accepts the privilege NAME, resolves to ID via list.
func (r *privilegeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	all, err := r.client.ListPrivileges(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Import failed", err.Error())
		return
	}
	for _, p := range all {
		if p.Name == req.ID {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), p.ID)...)
			return
		}
	}
	resp.Diagnostics.AddError("Import failed", fmt.Sprintf("privilege %q not found", req.ID))
}
```

Register `NewPrivilegeResource` in `provider.go`.

- [ ] **Step 4: Run, verify PASS.**

- [ ] **Step 5: Commit:** `git add -A && git commit -m "feat: nexspence_privilege resource"`

---

### Task 11: Resource `nexspence_role`

**Files:**
- Create: `internal/provider/role_resource.go`
- Test: `internal/provider/role_resource_test.go`

- [ ] **Step 1: Write failing acceptance test**

```go
package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccRole_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "nexspence_content_selector" "sel" {
  name       = "acc-role-sel"
  expression = "format == \"npm\""
}

resource "nexspence_privilege" "priv" {
  name             = "acc-role-priv"
  content_selector = nexspence_content_selector.sel.name
}

resource "nexspence_role" "test" {
  name        = "acc-role"
  description = "acceptance role"
  privileges  = [nexspence_privilege.priv.name]
}`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("nexspence_role.test", "id"),
					resource.TestCheckResourceAttr("nexspence_role.test", "privileges.#", "1"),
				),
			},
			{
				ResourceName:      "nexspence_role.test",
				ImportState:       true,
				ImportStateId:     "acc-role",
				ImportStateVerify: true,
			},
		},
	})
}
```

- [ ] **Step 2: Run, verify FAIL.**

- [ ] **Step 3: Implement `internal/provider/role_resource.go`**

```go
package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nexspence/terraform-provider-nexspence/internal/client"
)

// NewRoleResource is registered in provider.Resources.
func NewRoleResource() resource.Resource { return &roleResource{} }

type roleResource struct {
	client *client.Client
}

type roleModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Privileges  types.Set    `tfsdk:"privileges"`
}

func (r *roleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (r *roleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Nexspence role grouping privileges.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{Optional: true},
			"privileges": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Privilege names attached to this role.",
			},
		},
	}
}

func (r *roleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

// privilegeNamesToIDs resolves privilege names -> IDs.
func (r *roleResource) privilegeNamesToIDs(ctx context.Context, names []string) ([]string, error) {
	all, err := r.client.ListPrivileges(ctx)
	if err != nil {
		return nil, fmt.Errorf("list privileges: %w", err)
	}
	byName := make(map[string]string, len(all))
	for _, p := range all {
		byName[p.Name] = p.ID
	}
	ids := make([]string, 0, len(names))
	for _, n := range names {
		id, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("privilege %q not found", n)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// fromAPI refreshes state, mapping privilege IDs -> names.
func (r *roleResource) fromAPI(ctx context.Context, m *roleModel, ro *client.Role) error {
	m.ID = types.StringValue(ro.ID)
	m.Name = types.StringValue(ro.Name)
	if ro.Description != "" {
		m.Description = types.StringValue(ro.Description)
	} else {
		m.Description = types.StringNull()
	}
	all, err := r.client.ListPrivileges(ctx)
	if err != nil {
		return fmt.Errorf("list privileges: %w", err)
	}
	byID := make(map[string]string, len(all))
	for _, p := range all {
		byID[p.ID] = p.Name
	}
	names := make([]string, 0, len(ro.Privileges))
	for _, id := range ro.Privileges {
		if n, ok := byID[id]; ok {
			names = append(names, n)
		}
	}
	set, diags := types.SetValueFrom(ctx, types.StringType, names)
	if diags.HasError() {
		return errors.New("build privileges set")
	}
	if len(names) == 0 {
		set = types.SetNull(types.StringType)
	}
	m.Privileges = set
	return nil
}

// planPrivilegeNames extracts the configured privilege names from the plan.
func (m *roleModel) planPrivilegeNames(ctx context.Context) ([]string, error) {
	if m.Privileges.IsNull() || m.Privileges.IsUnknown() {
		return nil, nil
	}
	var names []string
	if diags := m.Privileges.ElementsAs(ctx, &names, false); diags.HasError() {
		return nil, errors.New("decode privileges set")
	}
	return names, nil
}

func (r *roleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan roleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	names, err := plan.planPrivilegeNames(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Create role failed", err.Error())
		return
	}
	ids, err := r.privilegeNamesToIDs(ctx, names)
	if err != nil {
		resp.Diagnostics.AddError("Create role failed", err.Error())
		return
	}
	created, err := r.client.CreateRole(ctx, &client.Role{
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
		Privileges:  ids,
	})
	if err != nil {
		resp.Diagnostics.AddError("Create role failed", err.Error())
		return
	}
	if err := r.fromAPI(ctx, &plan, created); err != nil {
		resp.Diagnostics.AddError("Refresh after create failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// readByID finds a role via list (the API has no GET-by-id for roles).
func (r *roleResource) readByID(ctx context.Context, id string) (*client.Role, error) {
	all, err := r.client.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, client.ErrNotFound
}

func (r *roleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state roleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ro, err := r.readByID(ctx, state.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read role failed", err.Error())
		return
	}
	if err := r.fromAPI(ctx, &state, ro); err != nil {
		resp.Diagnostics.AddError("Read role failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *roleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan roleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	names, err := plan.planPrivilegeNames(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Update role failed", err.Error())
		return
	}
	ids, err := r.privilegeNamesToIDs(ctx, names)
	if err != nil {
		resp.Diagnostics.AddError("Update role failed", err.Error())
		return
	}
	updated, err := r.client.UpdateRole(ctx, &client.Role{
		ID:          plan.ID.ValueString(),
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
		Privileges:  ids,
	})
	if err != nil {
		resp.Diagnostics.AddError("Update role failed", err.Error())
		return
	}
	if err := r.fromAPI(ctx, &plan, updated); err != nil {
		resp.Diagnostics.AddError("Refresh after update failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *roleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state roleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteRole(ctx, state.ID.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Delete role failed", err.Error())
	}
}

// ImportState accepts the role NAME, resolves to ID via list.
func (r *roleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	all, err := r.client.ListRoles(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Import failed", err.Error())
		return
	}
	for _, ro := range all {
		if ro.Name == req.ID {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), ro.ID)...)
			return
		}
	}
	resp.Diagnostics.AddError("Import failed", fmt.Sprintf("role %q not found", req.ID))
}
```

Register `NewRoleResource` in `provider.go`.

- [ ] **Step 4: Run, verify PASS.**

- [ ] **Step 5: Commit:** `git add -A && git commit -m "feat: nexspence_role resource"`

---

### Task 12: Resource `nexspence_user`

**Files:**
- Create: `internal/provider/user_resource.go`
- Test: `internal/provider/user_resource_test.go`

Key semantics (verified against core): GET user returns role **names**; role assignment goes through `PUT /security/users/:userId/roles` with role **IDs**; `PUT` on the user itself ignores roles; password is write-only — changes call the change-password endpoint.

- [ ] **Step 1: Write failing acceptance test**

```go
package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccUser_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "nexspence_role" "dev" {
  name = "acc-user-role"
}

resource "nexspence_user" "test" {
  username   = "acc-alice"
  password   = "s3cretPass123"
  email      = "alice@example.com"
  first_name = "Alice"
  roles      = [nexspence_role.dev.name]
}`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("nexspence_user.test", "username", "acc-alice"),
					resource.TestCheckResourceAttr("nexspence_user.test", "roles.#", "1"),
				),
			},
			{ // update email + roles
				Config: `
resource "nexspence_role" "dev" {
  name = "acc-user-role"
}

resource "nexspence_user" "test" {
  username = "acc-alice"
  password = "s3cretPass123"
  email    = "alice2@example.com"
  roles    = []
}`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("nexspence_user.test", "email", "alice2@example.com"),
					resource.TestCheckResourceAttr("nexspence_user.test", "roles.#", "0"),
				),
			},
			{
				ResourceName:            "nexspence_user.test",
				ImportState:             true,
				ImportStateId:           "acc-alice",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password"}, // write-only, never read back
			},
		},
	})
}
```

- [ ] **Step 2: Run, verify FAIL.**

- [ ] **Step 3: Implement `internal/provider/user_resource.go`**

```go
package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nexspence/terraform-provider-nexspence/internal/client"
)

// NewUserResource is registered in provider.Resources.
func NewUserResource() resource.Resource { return &userResource{} }

type userResource struct {
	client *client.Client
}

type userModel struct {
	Username  types.String `tfsdk:"username"`
	Password  types.String `tfsdk:"password"`
	Email     types.String `tfsdk:"email"`
	FirstName types.String `tfsdk:"first_name"`
	LastName  types.String `tfsdk:"last_name"`
	Status    types.String `tfsdk:"status"`
	Roles     types.Set    `tfsdk:"roles"`
}

func (r *userResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *userResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A local Nexspence user.",
		Attributes: map[string]schema.Attribute{
			"username": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"password": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "Write-only: the API never returns it. Changing it triggers a password reset.",
			},
			"email":      schema.StringAttribute{Required: true},
			"first_name": schema.StringAttribute{Optional: true},
			"last_name":  schema.StringAttribute{Optional: true},
			"status": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				Default:    stringdefault.StaticString("active"),
				Validators: []validator.String{stringvalidator.OneOf("active", "disabled")},
			},
			"roles": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Role names assigned to the user.",
			},
		},
	}
}

func (r *userResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

// roleNamesToIDs resolves role names -> role IDs via the roles list.
func (r *userResource) roleNamesToIDs(ctx context.Context, names []string) ([]string, error) {
	all, err := r.client.ListRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	byName := make(map[string]string, len(all))
	for _, ro := range all {
		byName[ro.Name] = ro.ID
	}
	ids := make([]string, 0, len(names))
	for _, n := range names {
		id, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("role %q not found", n)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// planRoleNames extracts role names from the plan model (nil if unset).
func (m *userModel) planRoleNames(ctx context.Context) ([]string, error) {
	if m.Roles.IsNull() || m.Roles.IsUnknown() {
		return nil, nil
	}
	var names []string
	if diags := m.Roles.ElementsAs(ctx, &names, false); diags.HasError() {
		return nil, errors.New("decode roles set")
	}
	return names, nil
}

// fromAPI refreshes non-secret state. GET returns role NAMES directly.
func (m *userModel) fromAPI(ctx context.Context, u *client.User) error {
	m.Username = types.StringValue(u.Username)
	m.Email = types.StringValue(u.Email)
	str := func(v string) types.String {
		if v == "" {
			return types.StringNull()
		}
		return types.StringValue(v)
	}
	m.FirstName = str(u.FirstName)
	m.LastName = str(u.LastName)
	m.Status = types.StringValue(u.Status)
	set, diags := types.SetValueFrom(ctx, types.StringType, u.Roles)
	if diags.HasError() {
		return errors.New("build roles set")
	}
	if len(u.Roles) == 0 {
		// Preserve null vs empty distinction: only force empty when config set [].
		if m.Roles.IsNull() {
			set = types.SetNull(types.StringType)
		} else {
			set, _ = types.SetValueFrom(ctx, types.StringType, []string{})
		}
	}
	m.Roles = set
	return nil
}

func (r *userResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.CreateUser(ctx, &client.User{
		Username:  plan.Username.ValueString(),
		Email:     plan.Email.ValueString(),
		FirstName: plan.FirstName.ValueString(),
		LastName:  plan.LastName.ValueString(),
		Status:    plan.Status.ValueString(),
	}, plan.Password.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Create user failed", err.Error())
		return
	}
	names, err := plan.planRoleNames(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Create user failed", err.Error())
		return
	}
	if names != nil {
		ids, err := r.roleNamesToIDs(ctx, names)
		if err != nil {
			resp.Diagnostics.AddError("Assign roles failed", err.Error())
			return
		}
		if err := r.client.SetUserRoles(ctx, plan.Username.ValueString(), ids); err != nil {
			resp.Diagnostics.AddError("Assign roles failed", err.Error())
			return
		}
	}
	u, err := r.client.GetUser(ctx, plan.Username.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Refresh after create failed", err.Error())
		return
	}
	if err := plan.fromAPI(ctx, u); err != nil {
		resp.Diagnostics.AddError("Refresh after create failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *userResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	u, err := r.client.GetUser(ctx, state.Username.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read user failed", err.Error())
		return
	}
	if err := state.fromAPI(ctx, u); err != nil {
		resp.Diagnostics.AddError("Read user failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *userResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	username := plan.Username.ValueString()

	if _, err := r.client.UpdateUser(ctx, &client.User{
		Username:  username,
		Email:     plan.Email.ValueString(),
		FirstName: plan.FirstName.ValueString(),
		LastName:  plan.LastName.ValueString(),
		Status:    plan.Status.ValueString(),
	}); err != nil {
		resp.Diagnostics.AddError("Update user failed", err.Error())
		return
	}

	// Password change requested in config?
	if !plan.Password.Equal(state.Password) {
		if err := r.client.ChangePassword(ctx, username, plan.Password.ValueString()); err != nil {
			resp.Diagnostics.AddError("Change password failed", err.Error())
			return
		}
	}

	names, err := plan.planRoleNames(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Update user failed", err.Error())
		return
	}
	if names != nil {
		ids, err := r.roleNamesToIDs(ctx, names)
		if err != nil {
			resp.Diagnostics.AddError("Assign roles failed", err.Error())
			return
		}
		if err := r.client.SetUserRoles(ctx, username, ids); err != nil {
			resp.Diagnostics.AddError("Assign roles failed", err.Error())
			return
		}
	}

	u, err := r.client.GetUser(ctx, username)
	if err != nil {
		resp.Diagnostics.AddError("Refresh after update failed", err.Error())
		return
	}
	if err := plan.fromAPI(ctx, u); err != nil {
		resp.Diagnostics.AddError("Refresh after update failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *userResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteUser(ctx, state.Username.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Delete user failed", err.Error())
	}
}

// ImportState imports by username. Password lands as null in state — the next
// plan will show a password change; documented in the resource docs.
func (r *userResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("username"), req, resp)
}
```

Register `NewUserResource` in `provider.go`.

- [ ] **Step 4: Run, verify PASS.**

- [ ] **Step 5: Commit:** `git add -A && git commit -m "feat: nexspence_user resource"`

---

### Task 13: Data sources `nexspence_repository` + `nexspence_repositories`

**Files:**
- Create: `internal/provider/repository_data_source.go`, `internal/provider/repositories_data_source.go`
- Test: `internal/provider/repository_data_source_test.go`

- [ ] **Step 1: Write failing acceptance test**

```go
package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccRepositoryDataSources(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "nexspence_repository" "ds" {
  name   = "acc-ds-raw"
  format = "raw"
  type   = "hosted"
}

data "nexspence_repository" "one" {
  name = nexspence_repository.ds.name
}

data "nexspence_repositories" "all" {
  depends_on = [nexspence_repository.ds]
}`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("data.nexspence_repository.one", "format", "raw"),
					resource.TestCheckResourceAttrSet("data.nexspence_repositories.all", "repositories.#"),
				),
			},
		},
	})
}
```

- [ ] **Step 2: Run, verify FAIL.**

- [ ] **Step 3: Implement `internal/provider/repository_data_source.go`**

```go
package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nexspence/terraform-provider-nexspence/internal/client"
)

// NewRepositoryDataSource is registered in provider.DataSources.
func NewRepositoryDataSource() datasource.DataSource { return &repositoryDataSource{} }

type repositoryDataSource struct {
	client *client.Client
}

type repositoryDataSourceModel struct {
	Name   types.String `tfsdk:"name"`
	Format types.String `tfsdk:"format"`
	Type   types.String `tfsdk:"type"`
	Online types.Bool   `tfsdk:"online"`
	URL    types.String `tfsdk:"url"`
}

func (d *repositoryDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_repository"
}

func (d *repositoryDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Look up one repository by name.",
		Attributes: map[string]schema.Attribute{
			"name":   schema.StringAttribute{Required: true},
			"format": schema.StringAttribute{Computed: true},
			"type":   schema.StringAttribute{Computed: true},
			"online": schema.BoolAttribute{Computed: true},
			"url":    schema.StringAttribute{Computed: true},
		},
	}
}

func (d *repositoryDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	d.client = c
}

func (d *repositoryDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data repositoryDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	repo, err := d.client.GetRepository(ctx, data.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Read repository failed", err.Error())
		return
	}
	data.Format = types.StringValue(repo.Format)
	data.Type = types.StringValue(repo.Type)
	online := true
	if repo.Online != nil {
		online = *repo.Online
	}
	data.Online = types.BoolValue(online)
	data.URL = types.StringValue(repo.URL)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
```

And `internal/provider/repositories_data_source.go`:

```go
package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/nexspence/terraform-provider-nexspence/internal/client"
)

// NewRepositoriesDataSource is registered in provider.DataSources.
func NewRepositoriesDataSource() datasource.DataSource { return &repositoriesDataSource{} }

type repositoriesDataSource struct {
	client *client.Client
}

type repositoriesItemModel struct {
	Name   types.String `tfsdk:"name"`
	Format types.String `tfsdk:"format"`
	Type   types.String `tfsdk:"type"`
}

type repositoriesDataSourceModel struct {
	Repositories []repositoriesItemModel `tfsdk:"repositories"`
}

func (d *repositoriesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_repositories"
}

func (d *repositoriesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "List all repositories.",
		Attributes: map[string]schema.Attribute{
			"repositories": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":   schema.StringAttribute{Computed: true},
						"format": schema.StringAttribute{Computed: true},
						"type":   schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *repositoriesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	d.client = c
}

func (d *repositoriesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	repos, err := d.client.ListRepositories(ctx)
	if err != nil {
		resp.Diagnostics.AddError("List repositories failed", err.Error())
		return
	}
	var data repositoriesDataSourceModel
	for _, r := range repos {
		data.Repositories = append(data.Repositories, repositoriesItemModel{
			Name:   types.StringValue(r.Name),
			Format: types.StringValue(r.Format),
			Type:   types.StringValue(r.Type),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
```

Register both in `provider.go` `DataSources()`:

```go
func (p *nexspenceProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{NewRepositoryDataSource, NewRepositoriesDataSource}
}
```

- [ ] **Step 4: Run, verify PASS.** Also run the FULL suite now: `make testacc` — all resources green together.

- [ ] **Step 5: Commit:** `git add -A && git commit -m "feat: repository data sources"`

---

### Task 14: Docs, examples, README

**Files:**
- Create: `examples/provider/provider.tf`, `examples/resources/nexspence_repository/resource.tf`, `examples/full-bootstrap/main.tf`, `README.md`, `docs/` (generated), `terraform-registry-manifest.json`

- [ ] **Step 1: Write `examples/provider/provider.tf`**

```hcl
terraform {
  required_providers {
    nexspence = {
      source  = "nexspence/nexspence"
      version = "~> 0.1"
    }
  }
}

provider "nexspence" {
  url   = "https://nexspence.example.com"
  token = var.nexspence_token
}
```

- [ ] **Step 2: Write `examples/full-bootstrap/main.tf`** (the README showcase: blobstore → repos → RBAC → user)

```hcl
resource "nexspence_blobstore" "main" {
  name = "main"
  type = "local"
  path = "./data/blobs/main"
}

resource "nexspence_repository" "maven_releases" {
  name       = "maven-releases"
  format     = "maven2"
  type       = "hosted"
  blob_store = nexspence_blobstore.main.name
}

resource "nexspence_repository" "maven_central" {
  name       = "maven-central"
  format     = "maven2"
  type       = "proxy"
  blob_store = nexspence_blobstore.main.name
  proxy {
    remote_url = "https://repo1.maven.org/maven2/"
  }
}

resource "nexspence_repository" "maven_all" {
  name       = "maven-all"
  format     = "maven2"
  type       = "group"
  blob_store = nexspence_blobstore.main.name
  group {
    member_names    = [nexspence_repository.maven_releases.name, nexspence_repository.maven_central.name]
    writable_member = nexspence_repository.maven_releases.name
  }
}

resource "nexspence_content_selector" "team_a" {
  name       = "team-a"
  expression = "path.startsWith(\"/com/acme/\")"
}

resource "nexspence_privilege" "team_a_rw" {
  name             = "team-a-rw"
  content_selector = nexspence_content_selector.team_a.name
}

resource "nexspence_role" "team_a_dev" {
  name       = "team-a-dev"
  privileges = [nexspence_privilege.team_a_rw.name]
}

resource "nexspence_user" "alice" {
  username = "alice"
  password = var.alice_password
  email    = "alice@example.com"
  roles    = [nexspence_role.team_a_dev.name]
}
```

(plus `variables.tf` with `variable "alice_password" { type = string, sensitive = true }`)

- [ ] **Step 3: Write `terraform-registry-manifest.json`**

```json
{
  "version": 1,
  "metadata": {
    "protocol_versions": ["6.0"]
  }
}
```

- [ ] **Step 4: Generate docs:** `make docs` — creates `docs/index.md` + per-resource pages from schema Descriptions. Review output; every resource/attribute must have a non-empty description (fix schemas if not).

- [ ] **Step 5: Write `README.md`:** title + registry badge, quick start (`required_providers` block from Step 1), the full-bootstrap example from Step 2, auth methods table (token / username+password / env vars), link to nexspence main repo, development section (`make stack-up && make testacc`), AGPLv3 notice.

- [ ] **Step 6: Commit:** `git add -A && git commit -m "docs: examples, generated registry docs, README"`

---

### Task 15: CI + goreleaser + release

**Files:**
- Create: `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `.goreleaser.yml`

- [ ] **Step 1: Write `.goreleaser.yml`** (HashiCorp's registry-publishing template)

```yaml
version: 2
builds:
  - env: ["CGO_ENABLED=0"]
    mod_timestamp: "{{ .CommitTimestamp }}"
    flags: ["-trimpath"]
    ldflags: ["-s -w -X main.version={{.Version}}"]
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    binary: "{{ .ProjectName }}_v{{ .Version }}"
archives:
  - formats: [zip]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
checksum:
  extra_files:
    - glob: "terraform-registry-manifest.json"
      name_template: "{{ .ProjectName }}_{{ .Version }}_manifest.json"
  name_template: "{{ .ProjectName }}_{{ .Version }}_SHA256SUMS"
  algorithm: sha256
signs:
  - artifacts: checksum
    args: ["--batch", "--local-user", "{{ .Env.GPG_FINGERPRINT }}", "--output", "${signature}", "--detach-sign", "${artifact}"]
release:
  extra_files:
    - glob: "terraform-registry-manifest.json"
      name_template: "{{ .ProjectName }}_{{ .Version }}_manifest.json"
changelog:
  disable: true
```

- [ ] **Step 2: Write `.github/workflows/ci.yml`**

```yaml
name: CI
on:
  push:
    branches: [main]
  pull_request:

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: make lint

  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: make test

  acceptance:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - uses: hashicorp/setup-terraform@v3
        with: { terraform_wrapper: false }
      - run: docker compose -f docker-compose.acc.yml up -d --wait
      - run: make testacc
      - if: always()
        run: docker compose -f docker-compose.acc.yml down -v

  docs-check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: make docs && git diff --exit-code docs/
```

- [ ] **Step 3: Write `.github/workflows/release.yml`**

```yaml
name: Release
on:
  push:
    tags: ["v*"]

permissions:
  contents: write

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - name: Import GPG key
        id: import_gpg
        uses: crazy-max/ghaction-import-gpg@v6
        with:
          gpg_private_key: ${{ secrets.GPG_PRIVATE_KEY }}
          passphrase: ${{ secrets.PASSPHRASE }}
      - uses: goreleaser/goreleaser-action@v6
        with:
          args: release --clean
        env:
          GPG_FINGERPRINT: ${{ steps.import_gpg.outputs.fingerprint }}
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 4: Verify CI locally where possible:** `make lint && make test` green; `goreleaser release --snapshot --clean --skip=sign` builds all 6 platform archives into `dist/`.

- [ ] **Step 5: Commit and push:** `git add -A && git commit -m "ci: lint/test/acceptance workflows + goreleaser registry config" && git push -u origin main`. Verify CI is green on GitHub.

- [ ] **Step 6: One-time release setup — MANUAL USER STEPS (present as a checklist, wait for confirmation):**
  1. Generate a GPG key: `gpg --full-generate-key` (RSA 4096, no expiry, identity e.g. `Nexspence Releases <releases@nexspence.com>`).
  2. `gpg --armor --export-secret-keys <FPR>` → repo secret `GPG_PRIVATE_KEY`; passphrase → secret `PASSPHRASE`.
  3. Sign in to registry.terraform.io with the GitHub account that owns the `nexspence` org; under Settings → Signing Keys add the **public** key (`gpg --armor --export <FPR>`) for the `nexspence` namespace.
  4. Registry → Publish → Provider → select `nexspence/terraform-provider-nexspence`.

- [ ] **Step 7: Release v0.1.0:** `git tag v0.1.0 && git push origin v0.1.0`. Verify: release assets contain zips + `SHA256SUMS` + `SHA256SUMS.sig` + manifest; registry shows version 0.1.0; then smoke-test from scratch:

```bash
mkdir /tmp/tf-smoke && cd /tmp/tf-smoke
cat > main.tf <<'EOF'
terraform {
  required_providers {
    nexspence = { source = "nexspence/nexspence", version = "0.1.0" }
  }
}
provider "nexspence" {
  url      = "http://localhost:8081"
  username = "admin"
  password = "admin123"
}
resource "nexspence_repository" "smoke" {
  name   = "tf-smoke-raw"
  format = "raw"
  type   = "hosted"
}
EOF
terraform init && terraform apply -auto-approve && terraform destroy -auto-approve
```

Expected: `terraform init` downloads the provider from the registry; apply/destroy succeed against a local stack (`make stack-up`).

---

### Task 16: Cross-repo follow-ups (in nexspence-core)

**Files:**
- Modify: `~/WORKING/AI/nexspence-core/README.md`, `~/WORKING/AI/nexspence-core/NEXT_RELEASE.md`, `~/WORKING/AI/nexspence-core/website/` (integrations/docs section)

- [ ] **Step 1:** Add a "Terraform Provider" section to nexspence-core `README.md` (after the Helm chart section): registry link `registry.terraform.io/providers/nexspence/nexspence`, 10-line `required_providers` + one-resource example.
- [ ] **Step 2:** Add the same inline (no GitHub links — docs must be self-contained) to the website docs section, both EN and RU. Remember the doc-sync rule: nexspence-core and the public mirror README together.
- [ ] **Step 3:** Append to `NEXT_RELEASE.md` under Features: "Terraform provider `nexspence/nexspence` v0.1.0 — repositories, blob stores, users, roles, content selectors, privileges as code; published on the Terraform Registry."
- [ ] **Step 4:** Branch + PR per repo rules (no direct push to main). Commit: `docs: announce terraform-provider-nexspence v0.1.0`.

---

## Self-review notes (already applied)

- **Spec coverage:** all 6 resources (T7–T12), 2 data sources (T13), provider config + env fallbacks (T6), import-by-name everywhere (custom importers where the API is ID-keyed: T9–T11), secrets write-only (T7 s3.secret_key, T12 password), 404→RemoveResource in every Read, registry publishing (T15), bootstrap example + README (T14), core README/website mention (T16).
- **Contract corrections vs the original spec draft** are embedded: compat blobstore endpoints, `blobStoreId`/`contentSelectorId`/privilege-IDs/role-IDs resolution, proxy block without remote auth, user roles read-names/write-IDs asymmetry, roles have no GET-by-id.
- **Known risk spots called out inline:** healthcheck endpoint on the live image (T6), `ImportStateVerify` default echoes (T8), null-vs-empty set semantics for `roles` (T12).
