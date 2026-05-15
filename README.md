<div align="center">
  <img src="frontend/src/assets/logo.png" alt="Nexspence" width="380">
  <br><br>
  <p><strong>Free, open-source universal artifact repository manager</strong></p>
  <p>A full-featured self-hosted alternative to Sonatype Nexus Repository</p>
  <br>

  ![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square&logo=go&logoColor=white)
  ![React](https://img.shields.io/badge/React-18-61DAFB?style=flat-square&logo=react&logoColor=black)
  ![TypeScript](https://img.shields.io/badge/TypeScript-5-3178C6?style=flat-square&logo=typescript&logoColor=white)
  ![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16+-4169E1?style=flat-square&logo=postgresql&logoColor=white)
  ![Docker](https://img.shields.io/badge/Docker-ready-2496ED?style=flat-square&logo=docker&logoColor=white)
  ![License](https://img.shields.io/badge/License-AGPLv3-22c55e?style=flat-square)

</div>

---

## What is Nexspence?

Nexspence is a self-hosted artifact repository manager that supports **14 package formats**, three repository types (hosted, proxy, group), fine-grained RBAC, SSO via OIDC/LDAP, audit logging, S3-compatible storage, and a modern dark-theme web UI — all in a single binary backed by PostgreSQL. It exposes the full **Sonatype Nexus v1 REST API** at `/service/rest/v1/` for drop-in compatibility with existing CI/CD pipelines, Maven/Gradle settings, and npm/pip configurations.

---

## Architecture

```
                         ┌─────────────────────┐
                         │   Load Balancer      │  (nginx / k8s Ingress / ALB)
                         └──────────┬──────────┘
                    ┌───────────────┴───────────────┐
                    ▼                               ▼
┌────────────┐  JWT/Basic  ┌──────────────────┐   ┌──────────────────┐
│  Client    │ ──────────▶ │  Nexspence node 1 │   │  Nexspence node 2│  (HA)
│ (curl/mvn/ │             │  Gin + Auth +     │   │  identical       │
│  pip/npm…) │ ◀────────── │  Audit + RBAC     │   └────────┬─────────┘
└────────────┘             └────────┬──────────┘            │
                                    │                        │
                    ┌───────────────▼────────────────────────▼──────┐
                    │           Shared State                          │
                    │  ┌──────────────┐  ┌─────────┐  ┌──────────┐ │
                    │  │  PostgreSQL  │  │  Redis  │  │  S3/MinIO│ │
                    │  │  (all data)  │  │  (locks │  │  (blobs) │ │
                    │  └──────────────┘  │  cache) │  └──────────┘ │
                    │                    └─────────┘                │
                    └────────────────────────────────────────────────┘
```

View the full site with interactive architecture diagram, install guide, and comparison: **[nexspence.online](https://nexspence.online)** →

---

## Screenshots

### Dashboard & Repositories

<table>
  <tr>
    <td><img src="docs/assets/screenshots/repositories.PNG" alt="Repositories page" width="480"></td>
    <td><img src="docs/assets/screenshots/browse.PNG" alt="Browse" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Repositories list</em></td>
    <td align="center"><em>Browse tree view</em></td>
  </tr>
</table>

### Admin & Security

<table>
  <tr>
    <td><img src="docs/assets/screenshots/admin_blobstores.PNG" alt="Blob Stores" width="480"></td>
    <td><img src="docs/assets/screenshots/security_roles.PNG" alt="Roles & RBAC" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Blob stores — S3 + local with connection test</em></td>
    <td align="center"><em>Roles, privileges, content selectors</em></td>
  </tr>
</table>

### Cleanup & Search

<table>
  <tr>
    <td><img src="docs/assets/screenshots/cleanup.PNG" alt="Cleanup policies" width="480"></td>
    <td><img src="docs/assets/screenshots/search.PNG" alt="Search" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Cleanup policies with dry-run preview</em></td>
    <td align="center"><em>Full-text component search</em></td>
  </tr>
</table>

---

## Quick Start — Docker Compose

**Requirements:** [Docker](https://docs.docker.com/get-docker/) 24+ with Compose v2

### 1. Clone and configure

```bash
# Clone the repository
git clone https://github.com/nexspence-oss/nexspence
cd nexspence

# Edit config.yaml — change at minimum these two values:
#   auth.jwt_secret   (min 32 characters)
#   bootstrap.admin_password
```

### 2. Start

```bash
# Start PostgreSQL + Nexspence (auto-migrates schema on first run)
docker compose up -d

# Check that everything is up
docker compose ps
docker compose logs -f nexspence
```

### 3. Open the web UI

| Service | URL | Default credentials |
|---------|-----|---------------------|
| Web UI & REST API | http://localhost:8081 | `admin` / `admin123` |
| Docker registry | localhost:5000 | same credentials |
| PostgreSQL | localhost:5437 | `nexspence` / `nexspence` |

> Change the default password immediately after first login via **Administration → Security → Users**.

### 4. Stop and clean up

```bash
# Stop containers (data preserved in Docker volumes)
docker compose down

# Stop AND remove all data volumes (full reset)
docker compose down -v
```

### Variant: With MinIO (S3-compatible storage)

MinIO is included in `docker-compose.yml` as a profile. Set the storage type via env var:

```bash
# Start with MinIO S3 storage
NEXSPENCE_STORAGE_DEFAULT_TYPE=s3 \
  docker compose up -d

# MinIO S3 API:   http://localhost:9000
# MinIO console:  http://localhost:9001  (minioadmin / minioadmin)
# Nexspence UI:   http://localhost:8081
```

### Variant: HA Cluster

Uses `docker-compose.ha.yml`: 2 × Nexspence nodes, nginx load balancer (`least_conn`), Redis, MinIO, PostgreSQL. All nodes are stateless — shared state lives in PostgreSQL, Redis, and S3.

```bash
# Start 2-node HA cluster
docker compose \
  -f docker-compose.ha.yml \
  up -d

# 2 × Nexspence + nginx LB (least_conn)
# + Redis + MinIO + PostgreSQL
# Load balancer:  http://localhost:8080
```

Enable Redis in `config.yaml` for each node:

```yaml
redis:
  enabled: true
  addr: "redis:6379"
```

See [`docs/ha-setup.md`](docs/ha-setup.md) for the full HA guide including Kubernetes probe examples.

### Variant: With Keycloak SSO

Starts a pre-configured Keycloak dev instance with the `nexspence` realm imported. "Sign in with Keycloak" appears on the login page automatically.

```bash
# Start with Keycloak OIDC provider
OIDC_ENABLED=true \
  docker compose \
  --profile keycloak \
  up -d

# Nexspence UI:    http://localhost:8081  (admin / admin123)
# Keycloak admin:  http://localhost:8180  (admin / admin)
# Test SSO user:   testuser / testpass (mapped to nx-admin role)
```

To configure OIDC manually with an external Keycloak, add to `config.yaml`:

```yaml
oidc:
  enabled: true
  issuer: "http://localhost:8180/realms/nexspence"
  client_id: "nexspence"
  client_secret: "<your-client-secret>"
  redirect_url: "http://localhost:8081/api/v1/auth/oidc/callback"
  frontend_base_url: "http://localhost:8081"
```

---

## Quick Start — Kubernetes (Helm)

**Requirements:** Helm 3.x, Kubernetes ≥ 1.26

The chart lives at `deploy/helm/nexspence/`. Choose exactly one networking option:

### nginx ingress-controller

```bash
# nginx ingress (most common)
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/nginx.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

### Traefik ingress-controller

```bash
# Traefik ingress (routes via websecure entrypoint, TLS secret: nexspence-tls)
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/traefik.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

### Cilium ingress-controller

```bash
# Requires: Cilium >= 1.12 with ingress controller enabled
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/cilium-ingress.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

### Istio Gateway + VirtualService

```bash
# Requires: istioctl install --set profile=default
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/istio-gateway.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

### Cilium Gateway API

```bash
# Requires: Cilium >= 1.14, K8s Gateway API CRDs installed
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/cilium-gateway.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

### Helm — External PostgreSQL

```bash
helm install nexspence deploy/helm/nexspence \
  --set postgresql.enabled=false \
  --set externalDatabase.dsn="postgres://user:pass@pg-host:5432/nexspence" \
  -f deploy/helm/nexspence/values-examples/nginx.yaml \
  --namespace nexspence --create-namespace
```

### Helm — S3 blob storage (production multi-replica)

```bash
helm install nexspence deploy/helm/nexspence \
  --set storage.type=s3 \
  --set storage.s3.endpoint="https://minio.example.com" \
  --set storage.s3.bucket="nexspence-blobs" \
  --set storage.s3.accessKey="minio" \
  --set storage.s3.secretKey="minio123" \
  -f deploy/helm/nexspence/values-examples/nginx.yaml \
  --namespace nexspence --create-namespace
```

> For multi-replica deployments, use S3 storage — a single PVC with `ReadWriteOnce` does not scale horizontally.

Full Helm reference: [`deploy/helm/nexspence/README.md`](deploy/helm/nexspence/README.md)

---

## Quick Start — From Source

**Requirements:** Go 1.22+, Node.js 22+, PostgreSQL 16+

```bash
# 1. Clone
git clone https://github.com/nexspence-oss/nexspence
cd nexspence

# 2. Start PostgreSQL only (skip if you have a local PostgreSQL)
docker compose up -d db

# 3. Build and run backend (runs DB migrations automatically)
go run ./cmd/server serve

# 4. In a separate terminal — frontend dev server
cd frontend && npm ci
npm run dev       # dev server on :5173 with HMR
# — or —
npm run build     # production build → frontend/dist/
```

The backend binary serves both the REST API and the built frontend SPA from `./frontend/dist`. For production builds:

```bash
# Build a self-contained binary
go build -o nexspence ./cmd/server
./nexspence serve
```

---

## Configuration

`config.yaml` is mounted into the container at `/app/config.yaml`. All keys can be overridden via environment variables using the pattern `NEXSPENCE_<SECTION>_<KEY>` (uppercase, underscore separator).

### HTTP server

```yaml
http:
  addr: ":8081"
  read_timeout_sec: 1800
  write_timeout_sec: 1800
  max_body_mb: 1024
  base_url: "http://localhost:8081"   # change to your public hostname in production
  tls:
    enabled: false
    cert_file: ""
    key_file: ""
```

### Database

```yaml
database:
  dsn: "postgres://nexspence:nexspence@localhost:5437/nexspence?sslmode=disable"
  max_conns: 100
  min_conns: 5
  max_idle_sec: 300
```

> When running via Docker Compose the DSN is overridden by `NEXSPENCE_DATABASE_DSN` — the container connects to the `postgres` service, not `localhost`.

### Storage

```yaml
storage:
  default_type: "local"   # "local" or "s3"
  local:
    base_path: "./data/blobs"
  # s3:
  #   bucket: "nexspence-blobs"
  #   region: "us-east-1"
  #   endpoint: "http://minio:9000"    # MinIO inside Docker Compose
  #   access_key_id: ""
  #   secret_access_key: ""
  #   force_path_style: true            # required for MinIO / non-AWS S3
```

### Authentication & Bootstrap

```yaml
auth:
  jwt_secret: "CHANGE_ME_AT_LEAST_32_CHARACTERS_LONG"
  jwt_expiry_hours: 24
  anonymous_enabled: true      # allow read-only access to public repos without login
  password_min_length: 8
  bcrypt_cost: 12
  token_max_days: 180          # maximum lifetime of user API tokens (nxs_*)

bootstrap:
  admin_username: "admin"
  admin_password: "changeme"
  admin_email: "admin@example.com"
  admin_first_name: "Admin"
```

### LDAP / Active Directory (optional)

```yaml
ldap:
  enabled: false
  host: "ldap.example.com"
  port: 636                    # 636 for LDAPS, 389 for plain/STARTTLS
  use_tls: true
  bind_dn: "CN=svc-nexspence,OU=ServiceAccounts,DC=example,DC=com"
  bind_password: ""            # set via NEXSPENCE_LDAP_BIND_PASSWORD env var
  search_base: "DC=example,DC=com"
  search_filter: "(sAMAccountName={0})"   # AD; for OpenLDAP use: (uid={0})
  auto_create_users: true
  admin_group: ""              # group CN whose members get nx-admin role
  # role_mappings:
  #   "nexus-administrators": "nx-admin"
  #   "dev-team": "developers"
```

> On every login, Nexspence syncs the user's roles from LDAP group membership using REPLACE semantics. `admin_group` accepts either a plain CN or a full DN.

### OIDC / OAuth2 SSO (optional)

Supports Keycloak, Google Workspace, Microsoft Entra ID, Okta.

```yaml
oidc:
  enabled: false
  display_name: "SSO"          # button label: "Sign in with {display_name}"
  issuer: ""                   # e.g. "https://kc.example.com/realms/nexspence"
  client_id: ""
  client_secret: ""            # set via NEXSPENCE_OIDC_CLIENT_SECRET env var
  redirect_url: ""             # "https://nexspence.example.com/api/v1/auth/oidc/callback"
  frontend_base_url: ""        # "https://nexspence.example.com"
  provisioning: "jit"          # jit | allowlist | manual
  admin_group: ""
```

See [`docs/oidc-setup.md`](docs/oidc-setup.md) for provider-specific setup guides (Keycloak, Google, Entra, Okta).

### Redis (optional, required for HA)

```yaml
redis:
  enabled: false
  addr: "localhost:6379"
  password: ""
  db: 0
```

---

## Supported Package Formats

| Format | Hosted | Proxy | Group |
|--------|:------:|:-----:|:-----:|
| Maven 2 / 3 | ✓ | ✓ | ✓ |
| npm | ✓ | ✓ | ✓ |
| PyPI | ✓ | ✓ | ✓ |
| Go modules (GOPROXY v2) | ✓ | ✓ | ✓ |
| Docker / OCI | ✓ | ✓ | ✓ |
| NuGet v2 / v3 | ✓ | ✓ | ✓ |
| Helm charts | ✓ | ✓ | ✓ |
| Cargo (Rust) | ✓ | ✓ | ✓ |
| Raw files | ✓ | ✓ | ✓ |
| APT (Debian/Ubuntu) | ✓ | ✓ | — |
| Yum / RPM | ✓ | ✓ | — |
| Conan (C/C++) | ✓ | ✓ | — |
| Conda | ✓ | ✓ | — |
| Terraform Registry | ✓ | ✓ | — |

---

## Features

### Repository Management
- **Hosted** — direct artifact upload and storage
- **Proxy** — transparent remote caching (cache-miss fetches from upstream, stores locally; mutations rejected with `405`)
- **Group** — ordered union of hosted + proxy repos under a single URL; `X-Nexspence-Source` header traces the serving member
- **Cleanup policies** — by age, last-downloaded, format; retain-N-versions; cron scheduler; dry-run preview
- **Component tags** — free-form text tags, GIN-indexed, searchable via API and UI
- **Staging & Build Promotion** — promotion rules (from/to repo, CEL path filter, scan pass gate, manual approval); single or bulk promote from Browse; webhook events on every state transition

### Security & Authentication
- **Local accounts** — JWT bearer tokens, bcrypt passwords, configurable expiry
- **LDAP / Active Directory** — JIT user provisioning, group-to-role mapping, admin-group sync, REPLACE semantics on every login
- **OIDC / OAuth 2.0 SSO** — Keycloak, Google Workspace, Microsoft Entra ID, Okta; Authorization Code + PKCE; fragment-based token delivery
- **User API tokens** — `nxs_…` prefixed, SHA-256 hashed in DB; usable as Bearer or HTTP Basic password
- **RBAC** — Roles, Privileges, Content Selectors (CEL expressions for path/format/version scoping)
- **Content Replication** — push artifacts to a remote Nexspence instance on a cron schedule; AES-256-GCM encrypted credentials; per-asset diff; run history

### Storage
- **Local filesystem** — default, zero configuration
- **S3-compatible** — AWS S3, MinIO, Ceph; configurable per blob store; connection test endpoint
- **Per-repository blob store routing** — each repository can use a different physical blob store
- **Storage quotas** — per blob store and per repository; enforced on upload
- **Blob store groups** — round-robin or write-to-first fill policy across multiple physical stores

### High Availability
- **Multi-node clustering** — stateless nodes behind any load balancer; shared state in PostgreSQL + Redis + S3
- **Distributed locking** — cleanup runs and blob-store migrations acquire per-operation Redis locks; only one node executes at a time
- **Health probes** — `GET /healthz` (liveness, always 200) and `GET /readyz` (readiness, parallel DB + Redis ping, 503 on failure)
- **Reference deployment** — `docker-compose.ha.yml`: 2 × Nexspence + nginx (`least_conn`) + Redis + MinIO + PostgreSQL

### Operations & Backup
- **Per-repository export** — streaming `.tar.gz` download (metadata + blobs)
- **Per-repository import** — multipart upload; skip or rename conflict resolution; SHA-256 deduplication
- **Full system backup / restore** — complete database + blob export
- **Live migration from Nexus** — import repositories, users, roles, cleanup policies from a running Nexus OSS/Pro instance

### Developer Experience
- **Nexus v1 REST API** — `/service/rest/v1/` fully compatible; drop-in replacement
- **Full-text search** — PostgreSQL tsvector across components and assets
- **Browse UI** — tree view for raw and Docker repositories; file details with download, copy-link, usage examples
- **Audit log** — every API action logged; filterable by date/user/path; NDJSON streaming export; 90-day partition rotation
- **Webhooks** — `artifact.published`, `artifact.deleted`, `repo.created`, `repo.updated`, `repo.deleted`, `promotion.*` events; HMAC-SHA256 signatures; see [`docs/webhooks.md`](docs/webhooks.md)
- **Vulnerability scanning** — Trivy (Docker images) + OSV.dev (Maven/npm/PyPI/Cargo); CVE results cached; aggregated dashboard with bulk re-scan
- **In-app documentation** — `/docs` route with Getting Started guide, 14 format reference pages, 7 how-to guides
- **Dark glassmorphism UI** — sidebar collapse/expand; tabbed admin pages; wizard-style create flows; responsive

---

## API Reference

Nexspence implements the Nexus v1 REST API — existing Nexus clients work without modification.

| Path prefix | Purpose |
|-------------|---------|
| `/service/rest/v1/` | Nexus v1 REST — drop-in compatible |
| `/service/rest/beta/` | Nexus beta endpoints |
| `/api/v1/` | Nexspence-native API (migration, backup, extended admin) |
| `/repository/:name/*` | Artifact protocol endpoints |
| `/v2/` | OCI Distribution Spec v2 (Docker) |

Key endpoints:

```
GET  /healthz                                       # Liveness probe
GET  /readyz                                        # Readiness probe (DB + Redis)
GET  /api/v1/metrics                                # JSON metrics snapshot
POST /api/v1/login                                  # JWT login

GET  /service/rest/v1/repositories                  # List repositories
POST /service/rest/v1/repositories/:format/hosted   # Create hosted repo
POST /service/rest/v1/repositories/:format/proxy    # Create proxy repo
POST /service/rest/v1/repositories/:format/group    # Create group repo

GET  /service/rest/v1/search?name=foo               # Search components
GET  /service/rest/v1/search/assets                 # Search assets

GET  /service/rest/v1/security/users                # List users
GET  /service/rest/v1/audit                         # Audit log

GET  /api/v1/repositories/:name/export              # Export repo as .tar.gz
POST /api/v1/repositories/import                    # Import repo from .tar.gz
```

Full OpenAPI 3.1 spec: [`docs/api-spec.yaml`](docs/api-spec.yaml)

---

## Roadmap

| Phase | Feature | Status |
|-------|---------|--------|
| 1–22 | Core: repos, RBAC, formats, blob stores, proxy, group, cleanup | ✓ complete |
| 25 | Audit log: detailed events, NDJSON export, 90-day partition rotation | ✓ complete |
| 26 | Docker `/v2/` anonymous fallthrough + OCI-shaped auth errors | ✓ complete |
| 28 | OIDC/OAuth2 SSO — Keycloak, Google, Entra, Okta; PKCE; JIT/allowlist | ✓ complete |
| 38–40 | Live Nexus migration, sidebar collapse, stepped wizard UI | ✓ complete |
| 41–47 | UI polish: token expiry, transfer lists, empty states, a11y | ✓ complete |
| 48–51 | Blob store groups, S3 routing, repo blob-store migration, group writes | ✓ complete |
| 53 | High Availability — Redis distributed locks, `/healthz` + `/readyz` | ✓ complete |
| 54 | Vulnerability dashboard — OSV.dev + Trivy, bulk re-scan | ✓ complete |
| 55 | Content Replication — push to remote instance, AES-256-GCM creds | ✓ complete |
| 56 | Staging & Build Promotion — CEL filter, scan gate, approval queue | ✓ complete |
| 60–62 | LDAP role mapping, Conda format, Terraform Registry Mirror | ✓ complete |
| 63 | Helm chart — 5 networking options, HPA, bitnami/postgresql sub-chart | ✓ complete |
| 64–66 | Landing page, in-app docs (14 formats + 7 guides), brand icons | ✓ complete |
| next | SBOM generation, cosign image signing | planned |
| next | Prometheus metrics endpoint, OpenTelemetry traces | planned |
| next | `nexctl` CLI, blob GC | planned |

---

## Contributing

Contributions are welcome. Please open an issue to discuss proposed changes before submitting a pull request.

```bash
# Run backend tests
go test ./...

# Run frontend linter
cd frontend && npm run lint
```

---

## License

AGPLv3 — see [LICENSE](LICENSE)

---

<div align="center">
  <img src="frontend/src/assets/mini_logo.png" alt="Nexspence" width="60">
  <br>
  <sub>AGPLv3 License · Built with Go + React</sub>
</div>
