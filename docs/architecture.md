# Nexspence — System Architecture

## Overview

Nexspence follows a clean layered architecture. Each layer depends only on the layer below it. Format-specific protocol handlers are compiled packages — the binary is self-contained with no external plugin loading required.

```
┌────────────────────────────────────────────────────────────────────────┐
│                              Clients                                   │
│  Maven · npm · pip · docker · go get · helm · cargo · Browser · CI/CD │
└───────────────────────────────┬────────────────────────────────────────┘
                                │ HTTP / HTTPS
┌───────────────────────────────▼────────────────────────────────────────┐
│                         HTTP Layer  (Gin)                              │
│                                                                        │
│  /repository/:name/*     /service/rest/v1/*     /api/v1/*             │
│       Format Router        Nexus-compat API       Native API           │
│                                                                        │
│  Middleware stack (in order):                                          │
│    Recovery → RequestLogger → CORS → MetricsMiddleware                │
│    → AuditMiddleware → AuthMiddleware / OptionalAuth                   │
│    → [future: RateLimitMiddleware, OTelTraceMiddleware]               │
└──────────────┬──────────────────────────────┬──────────────────────────┘
               │                              │
┌──────────────▼───────────┐   ┌──────────────▼──────────────────────────┐
│   Format Handler Registry│   │           Service Layer                  │
│  ┌─────────────────────┐ │   │                                          │
│  │ MavenHandler        │ │   │  RepositoryService   CleanupService      │
│  │ NpmHandler          │ │   │  UserService         BackupService       │
│  │ PypiHandler         │ │   │  TokenService        WebhookService      │
│  │ DockerHandler       │ │   │  ContentSelectorSvc  ScanService (Trivy) │
│  │ GoModHandler        │ │   │  RoutingRuleService                      │
│  │ NugetHandler        │ │   │                                          │
│  │ HelmHandler         │ │   │  [Phase 8+]                              │
│  │ CargoHandler        │ │   │  OIDCService         LDAPService         │
│  │ AptHandler          │ │   │  SBOMService         AnalyticsService    │
│  │ YumHandler          │ │   │  BlobGCService       ReplicationService  │
│  │ ConanHandler        │ │   │                                          │
│  │ CondaHandler        │ │   │                                          │
│  │ TerraformHandler    │ │   │                                          │
│  │ RawHandler          │ │   └────────────────┬─────────────────────────┘
│  │ GroupHandler        │ │                    │
│  │ ReproxyHandler      │ │   ┌────────────────▼─────────────────────────┐
│  └─────────────────────┘ │   │          Repository Layer                │
└──────────────────────────┘   │                                          │
          │                    │  RepositoryRepo   ComponentRepo           │
          │                    │  AssetRepo        UserRepo                │
          │                    │  BlobStoreRepo    RoleRepo                │
          │                    │  AuditRepo        CleanupPolicyRepo       │
          │                    │  UserTokenRepo    ContentSelectorRepo     │
          │                    │  WebhookRepo      RoutingRuleRepo         │
          │                    │                                          │
          │                    │  All interfaces in repository/interfaces.go│
          │                    │  Implementations in repository/postgres/  │
          └────────────────────┴────────────────┐
                                                │
              ┌─────────────────────────────────▼──────────────────────┐
              │                    Data Tier                            │
              │                                                        │
              │  ┌───────────────┐   ┌────────────────┐               │
              │  │  PostgreSQL   │   │   BlobStore     │               │
              │  │               │   │                 │               │
              │  │  Metadata     │   │  LocalBlobStore │               │
              │  │  Users/Roles  │   │  S3BlobStore    │               │
              │  │  Audit log    │   │  (MinIO/AWS/    │               │
              │  │  (partitioned)│   │   Ceph/B2/GCS)  │               │
              │  │  Full-text    │   │                 │               │
              │  │  tsvector     │   │  [Phase 10+]    │               │
              │  │  search       │   │  AzureBlob      │               │
              │  └───────────────┘   └────────────────┘               │
              └────────────────────────────────────────────────────────┘
```

---

## Layer Responsibilities

### HTTP Layer

- TLS termination (configure reverse proxy or `config.yaml tls.cert_file`)
- Request routing: format routes vs REST API vs Nexus-compat API
- **Auth middleware** (`internal/api/handlers/auth.go`):
  - JWT Bearer (`Authorization: Bearer <jwt>`)
  - User API tokens — `Authorization: Bearer nxs_…` or Basic password field
  - HTTP Basic with username + password (bcrypt check)
  - `OptionalAuth` variant for artifact endpoints — anonymous reads allowed per repo config
- **Audit middleware** — goroutine write after POST/PUT/DELETE/PATCH on key paths; login events use `action=LOGIN`, `entityName=username` (set by Login handler via `c.Set("username", ...)` before returning)
- **Metrics middleware** — atomic counter increments (requests, errors); cumulative counters (`ArtifactsStored`, `BytesStored`, `DownloadsTotal`) seeded from DB on startup
- **[Phase 8]** Rate limiting — token bucket per `userID`; 429 with `Retry-After`
- **[Phase 9]** OTel trace middleware — span per request with format/repo labels

### Format Handlers

Each format lives in `internal/formats/<name>/handler.go`:

```go
// Handler receives shared dependencies — no global state.
type Handler struct{ deps formats.Deps }

// formats.Deps carries all repo interfaces + blob store + base URL:
type Deps struct {
    Repos      repository.RepositoryRepo
    Components repository.ComponentRepo
    Assets     repository.AssetRepo
    Blobs      repository.BlobStoreRepo
    BlobStore  storage.BlobStore
    BaseURL    string
    Webhooks   WebhookDispatcher  // optional
}
```

**Hosted path**: `StoreArtifact` / `FetchArtifact` / `DeleteArtifact` in `base/store.go`
- Checksums (SHA256/SHA1/MD5) computed via `io.MultiWriter` pipe during upload — no buffering
- Blob key = `sha256(repoName + ":" + path)` — deterministic, dedup-safe within a repo
- Metrics counters incremented after successful store/fetch/delete

**Proxy path**: `repoproxy.ServeGET`
- Cache hit → serve from blob store immediately
- Cache miss → stream upstream + blob store + response writer simultaneously (one pass, zero copy)
- Upstream auth: Docker Hub uses anonymous bearer token; `[Phase 8]` configurable upstream credentials per proxy repo

**Group path**: `group.Handler`
- Fans out to each member's full `FormatHandler.ServeHTTP` in order
- First non-404 wins; sets `X-Nexspence-Source` header
- Uses `httptest.ResponseRecorder` + `gin.CreateTestContext` isolation

### Service Layer

Pure business logic; no HTTP concerns; depend only on repository interfaces.

| Service | Responsibility |
|---------|----------------|
| `RepositoryService` | CRUD + group member validation + proxy URL validation + cleanup policy ID validation + quota field persistence |
| `UserService` | User CRUD, bcrypt password, role assignment, JWT issuance; LDAP login reloads roles from DB after sync |
| `TokenService` | `nxs_*` token issuance (SHA-256 hash stored); `TouchLastUsed` |
| `CleanupService` | Stale asset scan; batched blob delete; scheduler (6h); manual run |
| `BackupService` | Full tar.gz export (metadata + blobs); non-destructive restore with UUID remapping |
| `ContentSelectorService` | CEL program compilation + cache; Variant B gate evaluation |
| `WebhookService` | Async delivery with HMAC-SHA256; retry; inactive hook skip |
| `ScanService` | Trivy invocation; result cache in `components.extra`; Phase 8 wiring |
| **[Phase 8]** `OIDCService` | Exchange OIDC id_token for Nexspence JWT; group → role mapping |
| **[Phase 8]** `LDAPService` | Bind + search; group sync → Nexspence roles |
| **[Phase 8]** `SBOMService` | Generate SPDX / CycloneDX from component + asset metadata |
| **[Phase 9]** `AnalyticsService` | Daily download buckets (PostgreSQL date_trunc); top-N packages; bandwidth |
| **[Phase 9]** `ReplicationService` | Push-on-publish to secondary instance via webhook + streaming blob copy |
| **[Phase 10]** `BlobGCService` | `ListAllBlobKeys` vs `BlobStore.ListKeys` → delete orphan blobs; dry-run |

### Repository Layer

All DB access through interfaces in `internal/repository/interfaces.go`. Implementations in `internal/repository/postgres/`. In-memory mocks for all interfaces in `internal/testutil/mocks.go`.

```
interfaces.go declares:
  RepositoryRepo    BlobStoreRepo    ComponentRepo    AssetRepo
  UserRepo          RoleRepo         CleanupPolicyRepo AuditRepo
  UserTokenRepo     ContentSelectorRepo  RoutingRuleRepo  WebhookRepo
```

### Storage Layer

```go
type BlobStore interface {
    Put(ctx context.Context, key string, r io.Reader, size int64) error
    Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) (bool, error)
    Size(ctx context.Context, key string) (int64, error)
    UsedBytes(ctx context.Context) (int64, error)
    ListKeys(ctx context.Context) ([]string, error)  // for blob GC
}
```

Implementations:
- `LocalBlobStore` — atomic writes via temp file + rename; `storage/local.go`
- `S3BlobStore` — AWS SDK v2; works with MinIO, AWS, Ceph, Backblaze, GCS; `storage/s3.go`
- **[Phase 10]** `AzureBlobStore` — Azure Blob Storage adapter

---

## Nexus API Compatibility

| Path prefix | Purpose |
|-------------|---------|
| `/service/rest/v1/` | Nexus v1 REST API — 100% drop-in compatible |
| `/service/rest/beta/` | Nexus beta endpoints — partial |
| `/api/v1/` | Native Nexspence API |
| `/repository/:name/*` | Artifact protocol endpoints |
| `/v2/repository/:name/*` | Docker OCI Distribution Spec v2 |

---

## Security: RBAC Model

Nexspence uses a three-level access model:

```
Content Selector ──► Privilege ──► Role ──► User
```

- **Content Selector** — CEL expression filtering artifacts by `format`, `path`, `repository`.
- **Privilege** — a `repository-content-selector` permission bound to a Content Selector with a set of actions (`browse`, `read`, `write`, `delete`).
- **Role** — a set of Privileges assigned to users via `PUT /service/rest/v1/security/users/{userId}/roles`.

Built-in roles (`nx-admin`, `nx-anonymous`, `nx-developer`) are `readOnly: true` and cannot be deleted.

See [docs/security-rbac.md](security-rbac.md) for full setup guide and CEL expression examples.

API routes:
| Route | Description |
|-------|-------------|
| `GET/POST /service/rest/v1/security/content-selectors` | CRUD Content Selectors |
| `GET/POST/PUT/DELETE /service/rest/v1/security/privileges` | CRUD Privileges |
| `GET/POST /service/rest/v1/security/roles` | CRUD Roles |
| `PUT /service/rest/v1/security/roles/:id/privileges` | Assign privileges to role |
| `PUT /service/rest/v1/security/users/:userId/roles` | Assign roles to user |

---

## Planned Architecture Additions (Phases 9–10)

### Phase 8 — Security & Compliance

```
                    ┌───────────────────────────────────┐
                    │         Auth Extensions            │
                    │                                    │
                    │  OIDCService  ←→  Keycloak/GitHub  │
                    │  LDAPService  ←→  AD / OpenLDAP    │
                    │  K8s SA Token ←→  kube-apiserver   │
                    │  RateLimiter  ←  token bucket      │
                    └───────────────────────────────────┘

                    ┌───────────────────────────────────┐
                    │       Supply Chain Security        │
                    │                                    │
                    │  ScanService   →  Trivy (sidecar)  │
                    │  SBOMService   →  SPDX / CycloneDX │
                    │  CosignVerify  →  Sigstore PKI      │
                    │                                    │
                    │  Stored in components.extra        │
                    └───────────────────────────────────┘
```

### Phase 9 — Developer Experience

```
                    ┌───────────────────────────────────────┐
                    │         Developer Tooling              │
                    │                                        │
                    │  nexctl CLI  →  /api/v1/*              │
                    │  OTel traces →  OTLP (Jaeger/Tempo)    │
                    │  Badge API   →  SVG for README         │
                    │  Config-as-code: repositories.yaml     │
                    │     PUT /api/v1/config/apply           │
                    │  Analytics: per-repo download trends   │
                    └───────────────────────────────────────┘
```

### Phase 10 — Scale & Reliability

```
┌─────────────────────────────────────────────────────────────────┐
│                    Multi-Node Deployment                         │
│                                                                  │
│   Nexspence-1  Nexspence-2  Nexspence-3   (all stateless)       │
│        │            │            │                               │
│        └────────────┼────────────┘                               │
│                     │                                            │
│             ┌───────▼────────┐     ┌──────────────────────┐     │
│             │  PostgreSQL    │     │  S3-compatible store │     │
│             │  (shared state)│     │  (shared blobs)      │     │
│             └────────────────┘     └──────────────────────┘     │
│                                                                  │
│   Advisory locks replace in-process singleflight for proxy dedup │
│   Healthcheck /service/rest/v1/status/check → load balancer     │
└─────────────────────────────────────────────────────────────────┘

                    ┌──────────────────────────────────┐
                    │        Observability Stack        │
                    │                                   │
                    │  /metrics (Prometheus text)       │
                    │  → Prometheus scrape              │
                    │  → Grafana dashboard              │
                    │  → Alerts: high error rate,       │
                    │    quota breach, CVE severity      │
                    │                                   │
                    │  OTel traces → Jaeger / Tempo      │
                    └──────────────────────────────────┘
```

---

## Request Flow Examples

### Artifact upload (hosted)

```
curl PUT /repository/my-maven/com/example/lib/1.0/lib.jar
  → OptionalAuth   (sets userID in context)
  → AuditMiddleware (queued; fires after handler)
  → format router  (repo.format=maven2, repo.type=hosted)
  → maven.Handler.ServeHTTP → base.StoreArtifact
      → checkQuota (pre-write: declared size)
      → BlobStore.Put (streaming: hash writers + blob store)
      → checkQuota (post-write: actual size, if not declared)
      → ComponentRepo.Create + AssetRepo.Create
      → BlobStoreRepo.UpdateUsedBytes
      → metrics.ArtifactsStored.Add(1), BytesStored.Add(size)
      → WebhookService.Dispatch(artifact.published)
  → 201 Created
  → AuditMiddleware goroutine: Write(action=CREATE, domain=REPOSITORY)
```

### Proxy cache miss

```
go get github.com/some/lib@v1.2.3
  → GET /repository/go-proxy/github.com/some/lib/@v/v1.2.3.zip
  → OptionalAuth
  → format router: type=proxy → gomod.Handler → repoproxy.ServeGET
      → AssetRepo.GetByPath → nil (cache miss)
      → HTTP GET https://proxy.golang.org/... → 200
      → io.MultiWriter: gin.ResponseWriter + BlobStore.Put pipe + hash writers
      → base.RegisterStoredBlob (component + asset rows)
      → metrics.ArtifactsStored.Add(1) + DownloadsTotal.Add(1)
  → response streamed to client (zero buffering)
```

### Group fan-out

```
mvn GET /repository/maven-all/com/google/guava/...
  → format router: type=group, member_names=[maven-releases, maven-snapshots, maven-central]
  → group.Handler:
      try maven-releases  → httptest recorder → 404
      try maven-snapshots → httptest recorder → 404
      try maven-central   → repoproxy.ServeGET → cache miss → upstream fetch → 200
  → copy recorder response to real writer
  → set X-Nexspence-Source: maven-central
```

### LDAP login flow

```
POST /api/v1/login  {username, password}
  → Login handler: c.Set("username", req.Username)   ← for audit + logging
  → UserService.Login → user.Source=ldap → loginLDAP
      → ldap.Authenticate(ctx, username, password)    ← service bind + user bind
      → upsert user record (Create or Update)
      → syncLDAPAdminRole → SetUserRoles (DB)
      → GetUserRoles (DB reload)  ← critical: updates existing.Roles
      → GenerateToken(id, username, roles)
  → c.Set("userID", user.ID)
  → log.Infow("login success", username, ip, roles)
  → AuditMiddleware goroutine: Write(action=LOGIN, entityName=username, result=success)
```

### Server startup sequence

```
main.go serve:
  1. config.Load
  2. db.Migrate (goose up)
  3. db.Connect → log "database connected host=..."
  4. log storage type (local path or S3 bucket)
  5. if LDAP enabled: log host; NewLDAPService.TestConnection → log OK / FAILED
  6. SELECT COUNT/SUM FROM assets → metrics.Seed()
  7. bootstrapAdmin (upsert admin user + nx-admin role)
  8. api.NewRouter (wires all handlers + starts cleanup scheduler)
  9. http.Server.ListenAndServe
```

---

## Concurrency Model

- Gin: N worker goroutines (GOMAXPROCS × 4 default)
- Proxy cache dedup: in-process `singleflight` per blob key — prevents thundering herd
  - **[Phase 10]** Replace with PostgreSQL advisory lock for multi-node safety
- Blob writes: streaming via `io.Pipe` + goroutine — no full buffer in memory
- Audit writes: goroutine fire-and-forget (non-blocking on handler path)
- Webhook delivery: goroutine per dispatch; HTTP client with 10s timeout
- Cleanup scheduler: `time.Ticker` goroutine started at server boot
- DB pool: pgx pooled connections (max 100 by default; configurable)

---

## Data Model Summary

```
repositories        — name, format, type, online, blob_store_id, cleanup_policy_ids[], quota_bytes, format_config JSONB
components          — repository_id, format, group, name, version, extra JSONB (scan_result, etc.)
assets              — component_id, repository_id, path, blob_key, blob_store_id, size_bytes, sha256, content_type
users               — username, password_hash, email, status, source (local|ldap|oidc)
user_tokens         — user_id, name, token_hash, last_used_at
roles               — id, name, description, privilege_ids[]
user_roles          — user_id, role_id
blob_stores         — name, type (local|s3), config JSONB, used_bytes, quota_bytes
cleanup_policies    — name, format, criteria JSONB (age_days, last_downloaded_days), schedule_cron
audit_events        — partitioned by month; user_id, domain, action, entity_type, entity_name, result
migration_jobs      — source_url, status, progress JSONB
routing_rules       — name, mode (ALLOW|BLOCK), matchers[]
content_selectors   — name, expression (CEL)
webhooks            — name, url, events[], secret, active
scan_results        — component_id, scanner (trivy|osv), status, critical/high/medium/low/unknown/total counts, scanned_at, raw JSONB, error
ldap_servers        — host, port, bind_dn, search_base, group_map JSONB
```
