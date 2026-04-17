# Nexspence — System Architecture

## Overview

Nexspence follows a clean layered architecture. Each layer depends only on the layer below it. Format-specific protocol handlers are loaded as packages, not plugins — keeping the binary self-contained.

```
┌─────────────────────────────────────────────────────────────────┐
│                        Clients                                  │
│  Maven (mvn)  ·  npm  ·  pip  ·  docker  ·  go get  ·  Browser │
└───────────────────────────┬─────────────────────────────────────┘
                            │ HTTP/HTTPS
┌───────────────────────────▼─────────────────────────────────────┐
│                     HTTP Layer (Gin)                            │
│  /repository/:name/*path  │  /api/v1/*  │  /service/rest/v1/*  │
│              ↑                    ↑               ↑             │
│         Format Router       REST API         Nexus-compat API   │
└────────────┬──────────────────────┬───────────────┬────────────┘
             │                      │               │
┌────────────▼──────────┐  ┌────────▼────────┐  ┌──▼────────────┐
│   Format Handlers     │  │  Core Services  │  │ Migration API │
│  ┌──────────────────┐ │  │                 │  │               │
│  │ Maven Handler    │ │  │ RepositorySvc   │  │ ExportSvc     │
│  │ npm Handler      │ │  │ ArtifactSvc     │  │ ImportSvc     │
│  │ Docker Handler   │ │  │ UserSvc         │  │               │
│  │ PyPI Handler     │ │  │ SearchSvc       │  └───────────────┘
│  │ Go Handler       │ │  │ CleanupSvc      │
│  │ NuGet Handler    │ │  │ AuditSvc        │
│  │ Helm Handler     │ │  │ StorageSvc      │
│  │ Raw Handler      │ │  │                 │
│  └──────────────────┘ │  └────────┬────────┘
└───────────────────────┘           │
                            ┌───────▼────────┐
                            │  Repositories  │
                            │  (DB layer)    │
                            │                │
                            │ RepoRepo       │
                            │ ArtifactRepo   │
                            │ UserRepo       │
                            │ BlobRepo       │
                            └───────┬────────┘
                                    │
              ┌─────────────────────┼──────────────────────┐
              │                     │                      │
    ┌─────────▼──────┐   ┌──────────▼──────┐   ┌──────────▼──────┐
    │  PostgreSQL 16 │   │  Storage Layer  │   │  Search Index   │
    │                │   │                 │   │  (PostgreSQL    │
    │  Metadata      │   │  LocalAdapter   │   │   full-text)    │
    │  Users/Roles   │   │  S3Adapter      │   │                 │
    │  Audit log     │   │                 │   └─────────────────┘
    └────────────────┘   └─────────────────┘
```

## Layer Responsibilities

### HTTP Layer
- TLS termination
- Request routing: format routes vs REST API vs Nexus-compat API
- Auth middleware (JWT Bearer + Basic Auth for legacy clients)
- Rate limiting, request size limits

### Format Handlers
Each format implements the `FormatHandler` interface:
```go
type FormatHandler interface {
    Name() string                                          // "maven2", "npm", etc.
    Routes(r gin.IRouter)                                  // register HTTP routes
    Upload(ctx context.Context, req UploadRequest) error
    Download(ctx context.Context, req DownloadRequest) (io.ReadCloser, *ArtifactMeta, error)
    Delete(ctx context.Context, path string) error
    ValidatePath(path string) error
}
```

### Core Services
Pure business logic, no HTTP concerns. Depend on Repository interfaces.

### Repository Layer
All DB access via interfaces. pgx v5 for PostgreSQL, no ORM — raw SQL with goose migrations.

### Storage Layer
```go
type BlobStore interface {
    Put(ctx context.Context, key string, r io.Reader, size int64) error
    Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) (bool, error)
    Size(ctx context.Context, key string) (int64, error)
}
```
Implementations: `LocalBlobStore`, `S3BlobStore` (MinIO/AWS/any S3-compatible)

## Nexus API Compatibility

Nexspence exposes two API surfaces:

| Path prefix | Purpose |
|-------------|---------|
| `/service/rest/v1/` | Nexus OSS v1 REST API (full compat) |
| `/service/rest/beta/` | Nexus beta endpoints (partial) |
| `/api/v1/` | Native Nexspence API (extended) |

Compatibility matrix (see `docs/api-spec.yaml` for details):
- Repository CRUD — 100% compatible
- Component/asset search — 100% compatible
- User/role management — 100% compatible
- Blob stores API — 100% compatible
- Cleanup policies — 100% compatible (Nexspence extension: cron expr)
- Replication — Nexspence-native (Nexus Pro only, we make it free)

## Migration Path from Nexus

```
Nexus instance                    Nexspence instance
─────────────                     ──────────────
GET /service/rest/v1/repositories ──► POST /api/v1/migration/import-repos
GET /service/rest/v1/blobstores   ──► (auto-mapped to Nexspence blob stores)
GET /service/rest/v1/components   ──► streaming artifact transfer
  ?repository=X&continuationToken ──► via /api/v1/migration/pull-artifacts
GET /service/rest/v1/security/... ──► POST /api/v1/migration/import-users
```

Migration tool (`nexspence migrate`) handles:
1. Pull repository definitions from live Nexus
2. Pull all component metadata
3. Stream artifact blobs via Nexus content API
4. Import users, roles, privileges
5. Import cleanup policies

## Request Flow Example: `mvn dependency:resolve`

```
mvn → GET /repository/maven-public/com/google/guava/guava/32.1.3-jre/guava-32.1.3-jre.jar
  │
  ▼
Gin router → GroupHandler("maven-public")
  │  resolves group members: [maven-releases, maven-snapshots, maven-central-proxy]
  │  tries each in order
  ▼
ProxyHandler("maven-central-proxy")
  │  check local blob cache → miss
  │  fetch https://repo1.maven.org/maven2/... → 200 OK
  │  stream to client AND store in BlobStore
  │  write ArtifactRecord to PostgreSQL
  ▼
client receives artifact
```

## Concurrency Model

- Gin runs N worker goroutines (configurable, default: GOMAXPROCS*4)
- Proxy downloads: per-artifact dedup lock (singleflight) — prevents thundering herd
- Blob writes: streaming — no full buffering in memory
- DB pool: pgx pooled connections (max 100 by default)
