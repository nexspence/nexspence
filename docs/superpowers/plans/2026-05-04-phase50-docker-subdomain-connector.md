# Phase 50: Docker Subdomain Connector — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable access to Docker repositories via subdomain (`myrepo.nexspence.example.com`) without a port or path prefix — equivalent to Nexus Docker Connector.

**Architecture:** An `http.Handler` wrapper (`SubdomainRewriter`) sits in front of the Gin engine. When a request arrives with a `Host` matching `*.{baseDomain}`, it rewrites `/v2/…` paths by injecting the subdomain as the repository name so existing routing (`/v2/:repoName/*dockerpath`) works unchanged. Config adds a `docker.subdomain_connector` block. The frontend shows connector status in the AdminPage Info tab.

**Tech Stack:** Go `net/http`, Gin, TypeScript/React, Vite, holo-kit components.

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/config/config.go` | Modify | Add `DockerConfig` + `SubdomainConnectorConfig` structs; add `Docker DockerConfig` to `Config` |
| `config.yaml` | Modify | Add commented-out `docker:` example block |
| `internal/api/subdomain_rewriter.go` | **Create** | `SubdomainRewriter` — `http.Handler` wrapper that rewrites `/v2/*` paths for subdomain requests |
| `internal/api/subdomain_rewriter_test.go` | **Create** | Unit tests for `SubdomainRewriter` |
| `internal/api/router.go` | Modify | Wrap `NewRouter` return value with `SubdomainRewriter` when connector is enabled; pass `cfg.Docker` to `SystemHandler` |
| `internal/api/handlers/system.go` | Modify | Expose Docker Connector status in `GET /api/v1/system/services` |
| `frontend/src/pages/AdminPage.tsx` | Modify | Add Docker Connector card to Info tab |

---

## Task 1: Config — `DockerConfig` struct

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.yaml`

- [ ] **Step 1: Add structs and field to Config**

In `internal/config/config.go`, add after `AuditConfig`:

```go
type DockerConfig struct {
	SubdomainConnector SubdomainConnectorConfig `mapstructure:"subdomain_connector"`
}

type SubdomainConnectorConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	BaseDomain string `mapstructure:"base_domain"` // e.g. "nexspence.example.com"
}
```

Add field to `Config` struct (after `Audit AuditConfig`):

```go
Docker DockerConfig `mapstructure:"docker"`
```

- [ ] **Step 2: Add Viper defaults**

In `Load()`, after `v.SetDefault("audit.lookahead_months", 2)`:

```go
v.SetDefault("docker.subdomain_connector.enabled", false)
v.SetDefault("docker.subdomain_connector.base_domain", "")
```

- [ ] **Step 3: Add config.yaml example block**

At the end of `config.yaml` add:

```yaml
# ── Docker Subdomain Connector ────────────────────────────────────────────────
# When enabled, Docker clients can access repositories via subdomain:
#   docker pull myrepo.nexspence.example.com/image:tag
# Requires a wildcard DNS record: *.nexspence.example.com → this server.
# See docs/docker-subdomain-connector.md for nginx/traefik reverse proxy setup.
docker:
  subdomain_connector:
    enabled: false
    base_domain: ""   # e.g. "nexspence.example.com"
```

- [ ] **Step 4: Build to check compilation**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core && go build ./...
```
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go config.yaml
git commit -m "feat(config): add DockerConfig with SubdomainConnector settings"
```

---

## Task 2: SubdomainRewriter — core http.Handler wrapper

**Files:**
- Create: `internal/api/subdomain_rewriter.go`

- [ ] **Step 1: Create the file**

```go
package api

import (
	"net/http"
	"strings"
)

// SubdomainRewriter is an http.Handler wrapper that rewrites Docker /v2/* paths
// for subdomain-based repository access.
//
// When a request arrives with Host matching "*.<baseDomain>", the subdomain is
// extracted as the repository name and injected into the URL path:
//
//	/v2/alpine/manifests/latest  →  /v2/<repoName>/alpine/manifests/latest
//	/v2/                         →  /v2/  (unchanged — OCI version check)
//
// This makes the existing /v2/:repoName/*dockerpath Gin routes work transparently.
type SubdomainRewriter struct {
	next       http.Handler
	baseDomain string // e.g. "nexspence.example.com"
}

// NewSubdomainRewriter wraps next with subdomain path rewriting.
// baseDomain must NOT have a leading dot (e.g. "nexspence.example.com").
func NewSubdomainRewriter(next http.Handler, baseDomain string) http.Handler {
	return &SubdomainRewriter{next: next, baseDomain: strings.ToLower(baseDomain)}
}

func (s *SubdomainRewriter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	repoName := s.extractRepo(r.Host)
	if repoName != "" && strings.HasPrefix(r.URL.Path, "/v2/") && r.URL.Path != "/v2/" {
		// Rewrite /v2/<imagepath> → /v2/<repoName>/<imagepath>
		suffix := strings.TrimPrefix(r.URL.Path, "/v2/")
		r.URL.Path = "/v2/" + repoName + "/" + suffix
		if r.URL.RawPath != "" {
			rawSuffix := strings.TrimPrefix(r.URL.RawPath, "/v2/")
			r.URL.RawPath = "/v2/" + repoName + "/" + rawSuffix
		}
	}
	s.next.ServeHTTP(w, r)
}

// extractRepo returns the subdomain when Host matches "*.<baseDomain>".
// Returns "" when the pattern doesn't match (passthrough).
func (s *SubdomainRewriter) extractRepo(host string) string {
	// Strip port if present.
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.ToLower(host)
	suffix := "." + s.baseDomain
	if !strings.HasSuffix(host, suffix) {
		return ""
	}
	sub := strings.TrimSuffix(host, suffix)
	// Must be exactly one level (no dots in the subdomain).
	if sub == "" || strings.Contains(sub, ".") {
		return ""
	}
	return sub
}
```

- [ ] **Step 2: Build**

```bash
go build ./internal/api/...
```
Expected: no errors.

---

## Task 3: Tests for SubdomainRewriter

**Files:**
- Create: `internal/api/subdomain_rewriter_test.go`

- [ ] **Step 1: Write failing tests**

```go
package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/api"
	"github.com/stretchr/testify/assert"
)

func capturePathHandler() (http.Handler, *string) {
	captured := new(string)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}), captured
}

func TestSubdomainRewriter_NonDockerPath_Passthrough(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest("GET", "/repository/myrepo/some/file", nil)
	req.Host = "myrepo.nexspence.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/repository/myrepo/some/file", *captured)
}

func TestSubdomainRewriter_V2Root_Passthrough(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest("GET", "/v2/", nil)
	req.Host = "myrepo.nexspence.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/v2/", *captured)
}

func TestSubdomainRewriter_V2ManifestPath_RepoInjected(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest("GET", "/v2/alpine/manifests/latest", nil)
	req.Host = "myrepo.nexspence.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/v2/myrepo/alpine/manifests/latest", *captured)
}

func TestSubdomainRewriter_V2BlobPath_RepoInjected(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest("GET", "/v2/myimage/blobs/sha256:abc123", nil)
	req.Host = "releases.nexspence.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/v2/releases/myimage/blobs/sha256:abc123", *captured)
}

func TestSubdomainRewriter_HostWithPort_RepoInjected(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest("GET", "/v2/alpine/tags/list", nil)
	req.Host = "myrepo.nexspence.example.com:443"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/v2/myrepo/alpine/tags/list", *captured)
}

func TestSubdomainRewriter_NonMatchingHost_Passthrough(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest("GET", "/v2/alpine/manifests/latest", nil)
	req.Host = "other.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	// Path not rewritten — no repo injected.
	assert.Equal(t, "/v2/alpine/manifests/latest", *captured)
}

func TestSubdomainRewriter_BaseDomainDirectAccess_Passthrough(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest("GET", "/v2/myrepo/alpine/manifests/latest", nil)
	req.Host = "nexspence.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	// Direct access — path already has repo name, not rewritten.
	assert.Equal(t, "/v2/myrepo/alpine/manifests/latest", *captured)
}

func TestSubdomainRewriter_DeepSubdomain_Passthrough(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest("GET", "/v2/alpine/manifests/latest", nil)
	req.Host = "a.b.nexspence.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	// Deep subdomains not supported — passthrough.
	assert.Equal(t, "/v2/alpine/manifests/latest", *captured)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/api/ -run TestSubdomainRewriter -v
```
Expected: FAIL — `api.NewSubdomainRewriter` is not yet exported (we just wrote the file, but if the package is `api` not `api_test`, adjust).

> Note: `subdomain_rewriter.go` uses `package api` and the test uses `package api_test` — this is the black-box test pattern consistent with the rest of the handlers. The test imports `"github.com/nexspence-oss/nexspence/internal/api"`.

- [ ] **Step 3: Run tests to verify they pass**

```bash
go test ./internal/api/ -run TestSubdomainRewriter -v
```
Expected: all 7 tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/api/subdomain_rewriter.go internal/api/subdomain_rewriter_test.go
git commit -m "feat(api): SubdomainRewriter — inject Docker repo name from Host header"
```

---

## Task 4: Wire SubdomainRewriter into router

**Files:**
- Modify: `internal/api/router.go`

The `NewRouter` function currently returns `http.Handler`. When subdomain connector is enabled, wrap the Gin engine before returning.

- [ ] **Step 1: Add import for `strings` if not present, then wrap return**

Find the end of `NewRouter`:

```go
return r
```

Replace with:

```go
if cfg.Docker.SubdomainConnector.Enabled && cfg.Docker.SubdomainConnector.BaseDomain != "" {
    return NewSubdomainRewriter(r, cfg.Docker.SubdomainConnector.BaseDomain)
}
return r
```

- [ ] **Step 2: Build**

```bash
go build ./...
```
Expected: no errors.

- [ ] **Step 3: Run all tests**

```bash
go test ./... -count=1 -short 2>&1 | tail -5
```
Expected: all pass (same count as before + 7 new).

- [ ] **Step 4: Commit**

```bash
git add internal/api/router.go
git commit -m "feat(router): wrap Gin engine with SubdomainRewriter when connector enabled"
```

---

## Task 5: Expose Docker Connector in system services API

**Files:**
- Modify: `internal/api/handlers/system.go`

Add Docker Connector as a service entry in `GET /api/v1/system/services` so the frontend can show its status.

- [ ] **Step 1: Add `WithDockerConnector` method to `SystemHandler`**

In `system.go`, add a field to `SystemHandler`:

```go
type SystemHandler struct {
    cfg        *config.Config
    pool       *pgxpool.Pool
    ldap       auth.LDAPAuthenticator
    oidc       auth.OIDCProvider
    blobRepo   repository.BlobStoreRepo
}
```

(The struct already has these fields — just verify no `docker` field is needed since we read from `h.cfg.Docker`.)

- [ ] **Step 2: Add Docker Connector check in `Services`**

In `func (h *SystemHandler) Services`, find where checks are assembled (after LDAP/OIDC checks). Add:

```go
// Docker Subdomain Connector
checks = append(checks, func(_ context.Context) ServiceStatus {
    if !h.cfg.Docker.SubdomainConnector.Enabled {
        return ServiceStatus{
            Name:   "Docker Subdomain Connector",
            Status: "disabled",
            Detail: "set docker.subdomain_connector.enabled=true to activate",
        }
    }
    bd := h.cfg.Docker.SubdomainConnector.BaseDomain
    if bd == "" {
        return ServiceStatus{
            Name:   "Docker Subdomain Connector",
            Status: "warn",
            Detail: "enabled but docker.subdomain_connector.base_domain is empty",
        }
    }
    return ServiceStatus{
        Name:   "Docker Subdomain Connector",
        Status: "ok",
        Detail: "*."+bd+" → docker pull <repo>." + bd + "/<image>:<tag>",
    }
})
```

- [ ] **Step 3: Build**

```bash
go build ./internal/api/handlers/...
```
Expected: no errors.

- [ ] **Step 4: Run handler tests**

```bash
go test ./internal/api/handlers/... -count=1 -short 2>&1 | tail -5
```
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/api/handlers/system.go
git commit -m "feat(system): expose Docker Subdomain Connector status in /api/v1/system/services"
```

---

## Task 6: Frontend — Docker Connector card in AdminPage Info tab

**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

The Info tab already renders a grid of `HoloCard` components showing system info and service connections. We'll add a Docker Connector card after the existing Service Connections card.

The service statuses come from `getServiceStatuses()` — the Docker Connector entry is already there from Task 5. We just need to render a dedicated card for it.

- [ ] **Step 1: Find the Docker Connector service in the statuses array and render a card**

In `AdminPage.tsx`, locate the Info tab section (`{tab === 'info' && (`). Find where `serviceStatuses` are rendered (the Service Connections HoloCard). After that card, add a new Docker Connector card.

Find the import for `Network` or add it. The lucide-react icon `Network` works well here. If it's not already imported, add it to the existing lucide import:

```typescript
import { /* existing icons */, Network } from 'lucide-react'
```

- [ ] **Step 2: Add the Docker Connector card**

After the Service Connections HoloCard, add:

```tsx
{/* Docker Subdomain Connector */}
{(() => {
  const connector = serviceStatuses?.find(s => s.name === 'Docker Subdomain Connector')
  if (!connector) return null
  const statusColor =
    connector.status === 'ok'       ? 'var(--holo-ok)'   :
    connector.status === 'warn'     ? 'var(--holo-warn)'  :
    connector.status === 'disabled' ? 'var(--holo-text-dim)' :
                                      'var(--holo-danger)'
  const statusLabel =
    connector.status === 'ok'       ? 'ACTIVE'   :
    connector.status === 'warn'     ? 'WARN'     :
    connector.status === 'disabled' ? 'DISABLED' : 'ERROR'
  return (
    <HoloCard>
      <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 8 }}>
        <Network size={14} style={{ color: 'var(--holo-primary)' }} />
        Docker Subdomain Connector
        <span style={{ marginLeft: 'auto', fontSize: 11, fontWeight: 700, color: statusColor }}>{statusLabel}</span>
      </div>
      <div style={{ fontSize: 12, color: 'var(--holo-text-dim)', lineHeight: 1.6 }}>
        {connector.detail}
      </div>
      {connector.status === 'ok' && (
        <div style={{ marginTop: 12, padding: '8px 12px', background: 'rgba(59,130,246,0.08)', borderRadius: 8, fontFamily: 'monospace', fontSize: 11, color: 'var(--holo-text)' }}>
          docker pull {connector.detail.split('*.')[1]?.split(' →')[0] ?? '…'}/<span style={{ color: 'var(--holo-primary)' }}>image:tag</span>
        </div>
      )}
      {connector.status === 'disabled' && (
        <div style={{ marginTop: 10, fontSize: 11, color: 'var(--holo-text-dim)' }}>
          Set <code style={{ background: 'rgba(255,255,255,0.06)', padding: '1px 4px', borderRadius: 3 }}>docker.subdomain_connector.enabled: true</code> in <code style={{ background: 'rgba(255,255,255,0.06)', padding: '1px 4px', borderRadius: 3 }}>config.yaml</code>
        </div>
      )}
    </HoloCard>
  )
})()}
```

- [ ] **Step 3: TypeScript build check**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core/frontend && npm run build 2>&1 | tail -10
```
Expected: build succeeds, 0 TypeScript errors.

- [ ] **Step 4: Commit**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core
git add frontend/src/pages/AdminPage.tsx
git commit -m "feat(frontend): Docker Subdomain Connector card in Admin → Info tab"
```

---

## Task 7: Documentation

**Files:**
- Create: `docs/docker-subdomain-connector.md`

- [ ] **Step 1: Create documentation file**

```markdown
# Docker Subdomain Connector

Access Docker repositories via subdomain without specifying a port or `/repository/` path prefix.

## Overview

With the connector enabled, Docker clients can use:
```
docker pull myrepo.nexspence.example.com/alpine:latest
```
instead of:
```
docker pull nexspence.example.com:8081/repository/myrepo/alpine:latest
```

## Setup

### 1. Enable in config.yaml

```yaml
docker:
  subdomain_connector:
    enabled: true
    base_domain: "nexspence.example.com"
```

### 2. Wildcard DNS

Add a wildcard DNS A record pointing to your Nexspence server:

```
*.nexspence.example.com  →  <your-server-ip>
```

### 3. Reverse Proxy (nginx)

```nginx
server {
    listen 443 ssl;
    server_name *.nexspence.example.com;

    ssl_certificate     /etc/ssl/certs/nexspence.crt;
    ssl_certificate_key /etc/ssl/private/nexspence.key;

    location / {
        proxy_pass         http://localhost:8081;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_read_timeout 1800s;
        client_max_body_size 0;
    }
}
```

### 4. Reverse Proxy (Traefik)

```yaml
# docker-compose labels on the Nexspence service
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.nexspence-subdomain.rule=HostRegexp(`{subdomain:[a-z0-9-]+}.nexspence.example.com`)"
  - "traefik.http.routers.nexspence-subdomain.entrypoints=websecure"
  - "traefik.http.routers.nexspence-subdomain.tls=true"
  - "traefik.http.services.nexspence-subdomain.loadbalancer.server.port=8081"
```

## How It Works

`SubdomainRewriter` is an `http.Handler` wrapper that sits in front of the Gin engine. When it detects a request with `Host: <repo>.<baseDomain>`, it rewrites the URL path:

```
GET /v2/alpine/manifests/latest  (Host: myrepo.nexspence.example.com)
→
GET /v2/myrepo/alpine/manifests/latest
```

The OCI version check endpoint `/v2/` is not rewritten. Direct access via the base domain (`nexspence.example.com`) continues to work unchanged.

## Limitations

- Only single-level subdomains are supported (`myrepo.example.com`, not `a.b.example.com`).
- HTTPS + wildcard TLS certificate required for production (`*.nexspence.example.com`).
- The connector does not replace the need for `docker login` — authentication is still enforced per repository.
```

- [ ] **Step 2: Commit**

```bash
git add docs/docker-subdomain-connector.md
git commit -m "docs: Docker Subdomain Connector setup guide (nginx, Traefik, DNS)"
```

---

## Task 8: Update task_plan.md

**Files:**
- Modify: `task_plan.md`

- [ ] **Step 1: Mark Phase 49 complete**

Find `## Phase 49: Change Repository Blob Store — Content Migration Task` and update:

```markdown
**Status:** complete (2026-05-04)
```

Replace the task checklist items with checked boxes.

- [ ] **Step 2: Mark Phase 50 complete**

Find `## Phase 50: Docker Subdomain Connector` and update:

```markdown
**Status:** complete (2026-05-04)
```

- [ ] **Step 3: Commit**

```bash
git add task_plan.md
git commit -m "docs(plan): mark Phase 49 and Phase 50 complete"
```

---

## Self-Review

### Spec Coverage

| Spec requirement | Task |
|-----------------|------|
| Middleware: extract repo from `Host` header | Task 2 |
| Config: `docker.subdomain_connector.enabled` + `base_domain` | Task 1 |
| Router: rewrite `/v2/*` with repo from subdomain | Task 2, 4 |
| Документация nginx/traefik пример | Task 7 |
| Frontend: AdminPage → System → Docker Connector settings | Task 6 |

### Placeholder Scan

No TBDs, no "implement later", no "add appropriate error handling". All code blocks are complete.

### Type Consistency

- `SubdomainRewriter` struct — `baseDomain string` — used consistently
- `NewSubdomainRewriter(next http.Handler, baseDomain string) http.Handler` — matches usage in router
- `cfg.Docker.SubdomainConnector.Enabled` and `.BaseDomain` — matches struct definition in Task 1
- `ServiceStatus` struct — used as-is from existing `system.go`
