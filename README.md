# Nexspence

**Free, open-source universal artifact repository manager** — a full-featured alternative to Sonatype Nexus Repository OSS/Pro.

Supports 12 package formats out of the box, hosted and proxy repositories, RBAC, cleanup policies, audit log, S3-compatible storage, and a dark glassmorphism web UI.

### Authentication

- **Login UI / REST:** JWT bearer tokens (`POST /api/v1/login`, `Authorization: Bearer <jwt>`).
- **User API tokens:** Random **`nxs_…`** tokens (hashed in DB). Use as **`Authorization: Bearer nxs_…`** or **HTTP Basic** with username + token as password. Implemented by **`TokenService`** and enforced together with JWT in **`AuthMiddleware`** / **`OptionalAuth`** (`internal/api/handlers/auth.go`, wired in `internal/api/router.go`).

---

## Supported formats

| Format | Hosted | Proxy | Group |
|--------|:------:|:-----:|:-----:|
| Maven 2/3 | ✓ | ✓ | ✓ |
| npm | ✓ | ✓ | ✓ |
| PyPI | ✓ | ✓ | ✓ |
| Go modules (GOPROXY) | ✓ | ✓ | ✓ |
| Docker / OCI | ✓ | ✓ | ✓ |
| NuGet v2/v3 | ✓ | ✓ | ✓ |
| Helm | ✓ | ✓ | ✓ |
| Cargo (Rust) | ✓ | ✓ | ✓ |
| APT (Debian) | ✓ | ✓ | — |
| Yum / DNF (RPM) | ✓ | ✓ | — |
| Conan (C/C++) | ✓ | ✓ | — |
| Raw (any file) | ✓ | ✓ | ✓ |

---

## Quick start — Docker Compose

```bash
git clone https://github.com/nexspence-oss/nexspence
cd nexspence
docker compose up --build
```

Web UI: http://localhost:8081  
Default credentials: `admin` / `admin123`

> Change `bootstrap.admin_password` in `config.yaml` before production use.

---

## Quick start — from source

**Requirements:** Go 1.25+, Node.js 22+, PostgreSQL 16+

```bash
# Start PostgreSQL (or use the compose DB service)
docker compose up -d db

# Build and run backend (runs DB migrations automatically)
go run ./cmd/server serve

# Build frontend (separate terminal)
cd frontend
npm ci
npm run dev       # dev server on :5173
# or
npm run build     # production build → frontend/dist/
```

---

## Configuration

All settings are in `config.yaml`. Every key can be overridden by an environment variable:

```
NEXSPENCE_HTTP_ADDR=:8081
NEXSPENCE_DATABASE_DSN=postgres://user:pass@host:5432/db?sslmode=disable
NEXSPENCE_AUTH_JWT_SECRET=your-secret-here
NEXSPENCE_BOOTSTRAP_ADMIN_PASSWORD=strongpassword
NEXSPENCE_STORAGE_DEFAULT_TYPE=s3    # or "local"
```

### S3-compatible storage (MinIO, AWS, Ceph, Backblaze)

```yaml
storage:
  default_type: "s3"
  s3:
    bucket: "nexspence-blobs"
    region: "us-east-1"
    endpoint: "http://minio:9000"   # omit for AWS S3
    access_key_id: "minioadmin"
    secret_access_key: "minioadmin"
    force_path_style: true          # required for MinIO
```

---

## Uploading artifacts

All artifact endpoints follow the pattern:

```
http://localhost:8081/repository/<repo-name>/<format-specific-path>
```

Create a hosted repository first (UI → Repositories → New Repository, or via API):

```bash
curl -u admin:admin123 -X POST http://localhost:8081/service/rest/v1/repositories/raw/hosted \
  -H 'Content-Type: application/json' \
  -d '{"name":"my-raw","online":true,"storage":{"blobStoreName":"default","strictContentTypeValidation":false},"cleanup":null}'
```

---

### Raw (any file)

```bash
# Upload
curl -u admin:admin123 -X PUT \
  http://localhost:8081/repository/my-raw/path/to/myfile.zip \
  --upload-file myfile.zip

# Download
curl -O http://localhost:8081/repository/my-raw/path/to/myfile.zip

# Delete
curl -u admin:admin123 -X DELETE \
  http://localhost:8081/repository/my-raw/path/to/myfile.zip
```

---

### Maven 2/3

Configure in `~/.m2/settings.xml`:

```xml
<settings>
  <servers>
    <server>
      <id>nexspence</id>
      <username>admin</username>
      <password>admin123</password>
    </server>
  </servers>
</settings>
```

In `pom.xml`:

```xml
<distributionManagement>
  <repository>
    <id>nexspence</id>
    <url>http://localhost:8081/repository/my-maven-hosted/</url>
  </repository>
  <snapshotRepository>
    <id>nexspence</id>
    <url>http://localhost:8081/repository/my-maven-snapshots/</url>
  </snapshotRepository>
</distributionManagement>
```

```bash
mvn deploy
```

Direct upload via curl:

```bash
curl -u admin:admin123 -X PUT \
  "http://localhost:8081/repository/my-maven-hosted/com/example/mylib/1.0.0/mylib-1.0.0.jar" \
  --upload-file mylib-1.0.0.jar
```

---

### npm

```bash
# Point npm at Nexspence
npm config set registry http://localhost:8081/repository/my-npm/

# Authenticate
npm login --registry=http://localhost:8081/repository/my-npm/

# Publish a package
npm publish --registry=http://localhost:8081/repository/my-npm/

# Install from Nexspence
npm install my-package --registry=http://localhost:8081/repository/my-npm/
```

---

### PyPI

**Upload** with [twine](https://twine.readthedocs.io/):

```bash
pip install twine

twine upload \
  --repository-url http://localhost:8081/repository/my-pypi/ \
  --username admin \
  --password admin123 \
  dist/*
```

**Install** with pip:

```bash
pip install my-package \
  --index-url http://admin:admin123@localhost:8081/repository/my-pypi/simple/ \
  --trusted-host localhost
```

Or configure `~/.pip/pip.conf`:

```ini
[global]
index-url = http://admin:admin123@localhost:8081/repository/my-pypi/simple/
trusted-host = localhost
```

---

### Go modules (GOPROXY)

```bash
# Set proxy
export GOPROXY=http://localhost:8081/repository/my-go/,direct
export GONOSUMCHECK=localhost

go get github.com/some/module@v1.2.3
```

Upload a module manually:

```bash
# Upload .info, .mod, .zip
curl -u admin:admin123 -X PUT \
  "http://localhost:8081/repository/my-go/github.com/example/lib/@v/v1.0.0.mod" \
  --upload-file go.mod

curl -u admin:admin123 -X PUT \
  "http://localhost:8081/repository/my-go/github.com/example/lib/@v/v1.0.0.zip" \
  --upload-file v1.0.0.zip
```

---

### Docker / OCI

```bash
# Configure Docker daemon for HTTP (add to /etc/docker/daemon.json):
# {"insecure-registries": ["localhost:8081"]}

# Login
docker login localhost:8081 -u admin -p admin123

# Tag — include the full repository path after the host:port
docker tag myimage:latest localhost:8081/repository/my-docker/myimage:latest
docker push localhost:8081/repository/my-docker/myimage:latest

# Pull
docker pull localhost:8081/repository/my-docker/myimage:latest
```

> **Note**: The Docker client sends API requests to `/v2/repository/<repoName>/...`.
> Nexspence registers these routes automatically — no extra configuration needed.

> **Common mistake:** `docker pull localhost:8081/dockerproxy/library/alpine:latest` is **wrong** — the client calls `/v2/dockerproxy/...` (without `repository`), hits the web UI, and you get errors like `unexpected media type text/html` when a layer is actually HTML. Always include **`repository`** in the image name: `docker pull localhost:8081/repository/dockerproxy/library/alpine:latest`.

---

### NuGet

```bash
# Register source
nuget sources add \
  -Name Nexspence \
  -Source http://localhost:8081/repository/my-nuget/index.json \
  -Username admin \
  -Password admin123

# Push package
nuget push MyPackage.1.0.0.nupkg \
  -Source Nexspence \
  -ApiKey admin123

# Restore / install
dotnet add package MyPackage --source http://localhost:8081/repository/my-nuget/index.json
```

---

### Helm

```bash
# Add repo
helm repo add nexspence \
  http://localhost:8081/repository/my-helm/ \
  --username admin --password admin123

helm repo update

# Install chart from repo
helm install my-release nexspence/my-chart

# Push chart (requires helm-push plugin)
helm plugin install https://github.com/chartmuseum/helm-push

helm cm-push my-chart-1.0.0.tgz nexspence

# Or upload directly with curl
curl -u admin:admin123 -X POST \
  http://localhost:8081/repository/my-helm/api/charts \
  -F "chart=@my-chart-1.0.0.tgz"
```

---

### Cargo (Rust)

Configure `~/.cargo/config.toml`:

```toml
[registries.nexspence]
index = "sparse+http://localhost:8081/repository/my-cargo/"

[net]
git-fetch-with-cli = true
```

```bash
# Publish crate
cargo publish --registry nexspence

# Add dependency
cargo add my-crate --registry nexspence
```

---

### APT (Debian / Ubuntu)

Add repository to apt sources:

```bash
echo "deb [trusted=yes] http://localhost:8081/repository/my-apt/ stable main" \
  | sudo tee /etc/apt/sources.list.d/nexspence.list

sudo apt update
sudo apt install my-package
```

Upload a `.deb` package:

```bash
curl -u admin:admin123 -X PUT \
  "http://localhost:8081/repository/my-apt/pool/main/my-package_1.0.0_amd64.deb" \
  --upload-file my-package_1.0.0_amd64.deb
```

---

### Yum / DNF (RPM)

Configure `/etc/yum.repos.d/nexspence.repo`:

```ini
[nexspence]
name=Nexspence
baseurl=http://localhost:8081/repository/my-yum/
enabled=1
gpgcheck=0
```

```bash
sudo yum install my-package
# or
sudo dnf install my-package
```

Upload an `.rpm` package:

```bash
curl -u admin:admin123 -X PUT \
  "http://localhost:8081/repository/my-yum/my-package-1.0.0.x86_64.rpm" \
  --upload-file my-package-1.0.0.x86_64.rpm
```

---

### Conan (C/C++)

```bash
# Add remote
conan remote add nexspence http://localhost:8081/repository/my-conan/

# Authenticate
conan user admin -r nexspence -p admin123

# Upload package
conan upload my-lib/1.0.0@ -r nexspence --all

# Install
conan install my-lib/1.0.0@ -r nexspence
```

---

## REST API

Nexspence implements the Nexus OSS v1 REST API — existing Nexus clients work without modification.

| Base path | Purpose |
|-----------|---------|
| `/service/rest/v1/` | Nexus OSS v1 REST API (100% compatible) |
| `/api/v1/` | Native Nexspence API |
| `/repository/:name/*path` | Artifact protocol endpoints |

Full OpenAPI 3.1 spec: [`docs/api-spec.yaml`](docs/api-spec.yaml)

Key endpoints:

```
GET  /api/v1/status                          # Health check
GET  /api/v1/metrics                         # Metrics (public)
POST /api/v1/login                           # JWT login

GET  /service/rest/v1/repositories           # List repos
POST /service/rest/v1/repositories/:format/hosted  # Create hosted repo
POST /service/rest/v1/repositories/:format/proxy   # Create proxy repo
POST /service/rest/v1/repositories/:format/group   # Create group repo (formatConfig.member_names)

GET  /service/rest/v1/search?name=foo        # Search components
GET  /service/rest/v1/search/assets          # Search assets

GET  /service/rest/v1/security/users         # List users
GET  /service/rest/v1/security/roles         # List roles
GET  /service/rest/v1/audit                  # Audit log

GET  /service/rest/v1/cleanup-policies       # List cleanup policies
POST /service/rest/v1/cleanup-policies/:id/run  # Run policy now
```

---

## Proxy repositories

A proxy repository caches artifacts from an upstream registry on first request. Subsequent requests are served from the local cache (blob store), without hitting the upstream again.

### How it works

1. Client requests an artifact from Nexspence
2. Cache hit → served immediately from local blob store
3. Cache miss → Nexspence fetches from `remote_url`, streams to client, and persists to blob store simultaneously (zero-copy, no memory buffering)
4. Mutations (push, upload, delete) are rejected with `405 Method Not Allowed`

**Docker Hub:** the public registry answers with **401** and a `WWW-Authenticate` Bearer challenge. Nexspence resolves that server-side (anonymous token from `https://auth.docker.io/token`) and retries upstream. Credentials from `docker login <nexspence-host>` are **not** forwarded to Docker Hub — forwarding them caused Docker to report *“authentication required - incorrect username or password”* when it tried Hub’s token flow with Nexspence users.

### Create a proxy repository (UI)

Repositories → **New Repository** → select format → select type **proxy** → set **Remote URL**.

### Create a proxy repository (API)

The JSON body always uses the `proxy_config.remote_url` field:

```bash
# Maven — proxy Maven Central
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/maven2/proxy \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "maven-central",
    "type": "proxy",
    "format": "maven2",
    "proxy_config": {"remote_url": "https://repo1.maven.org/maven2/"}
  }'

# npm — proxy npmjs.org
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/npm/proxy \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "npm-proxy",
    "type": "proxy",
    "format": "npm",
    "proxy_config": {"remote_url": "https://registry.npmjs.org/"}
  }'

# PyPI — proxy PyPI.org
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/pypi/proxy \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "pypi-proxy",
    "type": "proxy",
    "format": "pypi",
    "proxy_config": {"remote_url": "https://pypi.org/"}
  }'

# Go modules — proxy pkg.go.dev / sum.golang.org
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/go/proxy \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "go-proxy",
    "type": "proxy",
    "format": "go",
    "proxy_config": {"remote_url": "https://proxy.golang.org/"}
  }'

# Docker — proxy Docker Hub (unauthenticated registries / mirrors)
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/docker/proxy \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "docker-hub-proxy",
    "type": "proxy",
    "format": "docker",
    "proxy_config": {"remote_url": "https://registry-1.docker.io/"}
  }'

# Helm — proxy Artifact Hub / Bitnami
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/helm/proxy \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "bitnami-proxy",
    "type": "proxy",
    "format": "helm",
    "proxy_config": {"remote_url": "https://charts.bitnami.com/bitnami/"}
  }'

# NuGet — proxy nuget.org
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/nuget/proxy \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "nuget-proxy",
    "type": "proxy",
    "format": "nuget",
    "proxy_config": {"remote_url": "https://api.nuget.org/v3/"}
  }'

# Cargo — proxy crates.io sparse index
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/cargo/proxy \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "crates-proxy",
    "type": "proxy",
    "format": "cargo",
    "proxy_config": {"remote_url": "https://index.crates.io/"}
  }'

# APT — proxy Ubuntu archive
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/apt/proxy \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "ubuntu-noble-proxy",
    "type": "proxy",
    "format": "apt",
    "proxy_config": {"remote_url": "http://archive.ubuntu.com/ubuntu/"}
  }'

# Yum — proxy EPEL
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/yum/proxy \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "epel-proxy",
    "type": "proxy",
    "format": "yum",
    "proxy_config": {"remote_url": "https://dl.fedoraproject.org/pub/epel/9/Everything/x86_64/"}
  }'

# Raw — proxy any HTTP file server
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/raw/proxy \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "my-mirror",
    "type": "proxy",
    "format": "raw",
    "proxy_config": {"remote_url": "https://files.example.com/"}
  }'
```

### Using a proxy repository

Client configuration is identical to hosted repositories — just point at the Nexspence URL:

```bash
# Maven — use proxy instead of Maven Central directly
# In settings.xml mirror block:
#   <url>http://localhost:8081/repository/maven-central/</url>

# npm
npm install react --registry http://localhost:8081/repository/npm-proxy/

# pip
pip install requests \
  --index-url http://localhost:8081/repository/pypi-proxy/simple/

# Go
export GOPROXY=http://localhost:8081/repository/go-proxy/,direct

# Docker (pull via proxy cache)
docker pull localhost:8081/repository/docker-hub-proxy/library/ubuntu:24.04

# Helm
helm repo add nexspence-proxy http://localhost:8081/repository/bitnami-proxy/
helm install my-release nexspence-proxy/nginx

# Cargo (~/.cargo/config.toml)
# [registries.crates-proxy]
# index = "sparse+http://localhost:8081/repository/crates-proxy/"
```

### Proxy format support matrix

| Format | Proxy | Notable upstream |
|--------|:-----:|-----------------|
| maven2 | ✓ | `https://repo1.maven.org/maven2/` |
| npm | ✓ | `https://registry.npmjs.org/` |
| pypi | ✓ | `https://pypi.org/` |
| go | ✓ | `https://proxy.golang.org/` |
| raw | ✓ | any HTTP server |
| docker | ✓ | `https://registry-1.docker.io/` (unauthenticated) |
| helm | ✓ | `https://charts.bitnami.com/bitnami/` |
| nuget | ✓ | `https://api.nuget.org/v3/` |
| cargo | ✓ | `https://index.crates.io/` |
| apt | ✓ | `http://archive.ubuntu.com/ubuntu/` |
| yum | ✓ | `https://dl.fedoraproject.org/pub/epel/…` |
| conan | ✓ | `https://center2.conan.io/` |

> **Note:** Docker Hub requires authentication for most images beyond the free pull rate limit. For authenticated proxy use a registry mirror (e.g. your own ECR pull-through cache) as the `remote_url`.

---

## Group repositories

A **group** repository exposes a single URL that aggregates **hosted** and/or **proxy** repositories of the **same format**. Members are tried in **order**; the first successful response (anything other than **404**) is returned. This matches Nexus-style “group” merges (e.g. Maven releases + snapshots + Maven Central proxy behind one URL).

### Configuration

- **API / UI:** `type: "group"` and **`formatConfig.member_names`**: ordered JSON array of repository **names** (the UI checklist on Repositories → New Repository writes the same field).
- **Validation:** On create/update, every member must exist, must **not** be another group, and must use the **same** `format` as the group.
- **Read-only:** **GET** and **HEAD** only. **PUT** / **POST** / **PATCH** / **DELETE** return **405** — publish to a **hosted** member (or use a member proxy as usual).

### Behaviour

- **HTTP artifacts:** `http://localhost:8081/repository/<group-name>/<same-path-as-member>` — each member’s `FormatHandler` runs in order; proxy members can still fetch upstream on cache miss.
- **Docker:** `docker pull localhost:8081/repository/<group-name>/…` — uses `/v2/repository/<group-name>/…` internally; same ordering rules.
- **Tracing:** Responses may include **`X-Nexspence-Source`** with the member repository name that satisfied the request.

### Example (API): npm group over hosted + proxy

```bash
# Assume repos "npm-private" (hosted) and "npmjs" (proxy) already exist (same format npm).
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/npm/group \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "npm-all",
    "type": "group",
    "format": "npm",
    "formatConfig": { "member_names": ["npm-private", "npmjs"] }
  }'

npm install lodash --registry http://localhost:8081/repository/npm-all/
```

### Browse, Search, and Docker tree with groups

Group URLs are for **reads** (GET/HEAD). **Component metadata** in PostgreSQL is stored per **member** repository, not per group.

- **Implemented:** When you select a **group** on **Browse** or filter **Search** by a group name, the backend expands the group to **`member_names`** and queries **`components` / `assets` across those members** (`ListByRepoNames`, `SearchParams.RepositoryNames`, aggregated **docker-tree**). You should see the **union** of artifacts from all members (same image may appear twice if present in multiple members).
- **Column “Repository”** in API/UI responses typically shows the **member** repository that owns the row — that is expected.
- **What to verify:** After `docker pull` (or other clients) through **members** so the DB has rows, open Browse → choose the **group** — the tree/table should **not** be empty. If members are empty, the group browse will be empty too.

### Roadmap

| Phase | Feature | Status |
|-------|---------|--------|
| 6 | Cleanup policies, quotas, audit log, backup/restore, metrics | ✓ complete |
| 7 | Tests (>80% coverage), API docs, deployment guides | pending |
| 8 | CVE scanning (Trivy), SBOM generation, cosign, OIDC/SSO, LDAP, rate limiting | planned |
| 9 | `nexctl` CLI, config-as-code, k8s SA token auth, OTel traces, analytics, badges | planned |
| 10 | Multi-node HA, replication, blob GC, Prometheus metrics, soft delete, cache TTL | planned |
| 15D | Docker uploader + `last_downloaded` in browse UI | deferred |

See [`task_plan.md`](task_plan.md) for detailed task lists per phase.

---

## Architecture

```
┌────────────┐   JWT/Basic   ┌──────────────────────┐
│  Client    │ ────────────▶ │  Gin HTTP Router      │
│ (curl/mvn/ │               │  + Auth Middleware     │
│  pip/npm…) │ ◀──────────── │  + Audit Middleware    │
└────────────┘               └──────────┬───────────┘
                                         │
                    ┌────────────────────▼───────────┐
                    │      Format Handler Registry    │
                    │  maven│npm│pypi│go│docker│…    │
                    └────────────────────┬───────────┘
                              ┌──────────▼──────────┐
                    ┌─────────▼──────┐  ┌───────────▼───────┐
                    │  BlobStore     │  │  PostgreSQL         │
                    │  Local / S3    │  │  Repos, Components, │
                    └────────────────┘  │  Assets, Users, …  │
                                        └───────────────────┘
```

---

## License

Apache 2.0 — see [LICENSE](LICENSE)
