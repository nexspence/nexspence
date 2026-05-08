# Using Raw, Go, and Helm Repositories

## Quick Start

Create all three repository types for each format with a single script:

- **Raw:** `./scripts/create-raw-repos.sh`
- **Go (GOPROXY):** `./scripts/create-go-repos.sh`
- **Helm:** `./scripts/create-helm-repos.sh`

Each script creates a hosted, proxy, and group repository and accepts environment variables to override defaults (server URL, credentials, repo names, upstream URL).

---

## Raw Repositories

### Concept

Raw repositories store arbitrary files without imposing any format-specific structure — release binaries, configuration files, installers, OS packages, or any other binary/text artifact. Paths are free-form; Nexspence stores and serves whatever you upload under the path you choose.

### When to use: hosted vs proxy vs group

| Type | When to use |
|------|-------------|
| **hosted** | Store your own artifacts — internal builds, release binaries, config files |
| **proxy** | Cache external downloads — OS packages, tools, ISOs — reduces internet traffic |
| **group** | One URL for everything — clients use `raw-common` regardless of where the file lives |

### 17.1 Quick Start — run the script

```bash
./scripts/create-raw-repos.sh
```

Override defaults as needed:

```bash
BASE_URL=http://192.168.1.10:8080 \
HOSTED_NAME=my-raw \
PROXY_REMOTE=https://releases.example.com \
./scripts/create-raw-repos.sh
```

To store artifacts in a specific blob store instead of the default:

```bash
BLOB_STORE=s3-prod ./scripts/create-raw-repos.sh
```

The script resolves the blob store name to its UUID automatically. If the blob store does not exist the script exits with an error before creating any repositories. The group repository uses no direct storage and is not assigned a blob store.

Available environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `BASE_URL` | `http://localhost:8080` | Server URL |
| `ADMIN_USER` | `admin` | Admin login |
| `ADMIN_PASS` | `admin123` | Admin password |
| `HOSTED_NAME` | `raw-artifacts` | Hosted repo name |
| `PROXY_NAME` | `raw-proxy` | Proxy repo name |
| `GROUP_NAME` | `raw-common` | Group repo name |
| `PROXY_REMOTE` | `https://example.com` | Remote URL for proxy |
| `BLOB_STORE` | `default` | Blob store for hosted + proxy |

This creates three repositories:

| Name | Type |
|------|------|
| `raw-artifacts` | hosted |
| `raw-proxy` | proxy (remote: `https://example.com`) |
| `raw-common` | group (members: `raw-artifacts`, `raw-proxy`) |

### Hosted — upload and download files

Upload a release binary:

```bash
curl -u admin:admin123 \
  -T myapp-1.0.0-linux-amd64.tar.gz \
  http://localhost:8080/repository/raw-artifacts/releases/myapp/1.0.0/myapp-1.0.0-linux-amd64.tar.gz
```

Upload a configuration file:

```bash
curl -u admin:admin123 \
  -T config.yml \
  http://localhost:8080/repository/raw-artifacts/configs/prod/config.yml
```

Download a file:

```bash
curl -u admin:admin123 -O \
  http://localhost:8080/repository/raw-artifacts/releases/myapp/1.0.0/myapp-1.0.0-linux-amd64.tar.gz
```

### Proxy — cache remote files

The proxy repository fetches files from its configured remote URL on the first request and caches them locally. Subsequent requests are served from the cache. Configure the remote when creating the repository:

```bash
PROXY_REMOTE=https://releases.hashicorp.com ./scripts/create-raw-repos.sh
```

Download a proxied file (fetched from remote on first access, served from cache thereafter):

```bash
curl -O http://localhost:8080/repository/raw-proxy/terraform/1.7.5/terraform_1.7.5_linux_amd64.zip
```

### Group — unified access

The group repository aggregates hosted and proxy members under a single URL. Clients point to the group and Nexspence resolves requests against each member in order, returning the first match.

```bash
curl -O http://localhost:8080/repository/raw-common/releases/myapp/1.0.0/myapp-1.0.0-linux-amd64.tar.gz
```

List available components via the REST API:

```bash
curl http://localhost:8080/service/rest/v1/components?repository=raw-common
```

### Real-world examples

**Release pipeline — upload build artifacts:**

```bash
# Upload Linux and macOS binaries after CI build
curl -u admin:admin123 -T dist/myapp-2.1.0-linux-amd64   http://localhost:8080/repository/raw-artifacts/releases/myapp/2.1.0/myapp-2.1.0-linux-amd64
curl -u admin:admin123 -T dist/myapp-2.1.0-darwin-amd64  http://localhost:8080/repository/raw-artifacts/releases/myapp/2.1.0/myapp-2.1.0-darwin-amd64
curl -u admin:admin123 -T dist/myapp-2.1.0-windows.exe   http://localhost:8080/repository/raw-artifacts/releases/myapp/2.1.0/myapp-2.1.0-windows.exe
```

**Kubernetes bootstrap — store cluster configs:**

```bash
curl -u admin:admin123 -T kubeconfig-prod.yaml http://localhost:8080/repository/raw-artifacts/k8s/prod/kubeconfig.yaml
curl -u admin:admin123 -T kubeconfig-staging.yaml http://localhost:8080/repository/raw-artifacts/k8s/staging/kubeconfig.yaml
```

**Offline installer cache — proxy an upstream downloads site:**

```bash
# First request downloads from remote; all subsequent requests hit the cache
curl -O http://localhost:8080/repository/raw-proxy/node/v20.11.1/node-v20.11.1-linux-x64.tar.gz
curl -O http://localhost:8080/repository/raw-proxy/python/3.12.2/Python-3.12.2.tar.xz
```

---

## Go Module Repositories

### Concept

Go modules are fetched via the **GOPROXY protocol** — a simple HTTP API with four endpoints: `$module/@v/list`, `$module/@v/$version.info`, `$module/@v/$version.mod`, and `$module/@v/$version.zip`. The `go` toolchain resolves the `GOPROXY` environment variable, which accepts a comma-separated list of proxy URLs followed by an optional `direct` fallback or `off` to block public access entirely.

Nexspence implements the full GOPROXY v2 protocol. A proxy repository caches downloaded modules from an upstream (typically `proxy.golang.org`), enabling offline builds, reproducible CI, and an audit trail of every module version your builds depend on. A hosted repository accepts `go mod` uploads for private/internal modules.

### When to use: hosted vs proxy vs group

| Type | When to use |
|------|-------------|
| **hosted** | Publish internal or private Go modules that must not go to public registries |
| **proxy** | Cache public modules from `proxy.golang.org` — faster builds, offline capability |
| **group** | One `GOPROXY` URL — resolves private hosted modules first, then falls back to the proxy cache |

### 17.2 Quick Start — run the script

```bash
./scripts/create-go-repos.sh
```

Override defaults as needed:

```bash
BASE_URL=http://192.168.1.10:8080 \
PROXY_REMOTE=https://proxy.golang.org \
./scripts/create-go-repos.sh
```

To store modules in a specific blob store:

```bash
BLOB_STORE=s3-prod ./scripts/create-go-repos.sh
```

Available environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `BASE_URL` | `http://localhost:8080` | Server URL |
| `ADMIN_USER` | `admin` | Admin login |
| `ADMIN_PASS` | `admin123` | Admin password |
| `HOSTED_NAME` | `go-hosted` | Hosted repo name |
| `PROXY_NAME` | `go-proxy` | Proxy repo name |
| `GROUP_NAME` | `go-group` | Group repo name |
| `PROXY_REMOTE` | `https://proxy.golang.org` | Remote URL for proxy |
| `BLOB_STORE` | `default` | Blob store for hosted + proxy |

This creates three repositories:

| Name | Type |
|------|------|
| `go-hosted` | hosted |
| `go-proxy` | proxy (remote: `https://proxy.golang.org`) |
| `go-group` | group (members: `go-hosted`, `go-proxy`) |

### Proxy — download Go modules

Point `GOPROXY` at the proxy repository. The first `go get` for a module version fetches it from `proxy.golang.org` and caches it locally; all subsequent builds use the cache.

```bash
GOPROXY=http://localhost:8080/repository/go-proxy go get github.com/gin-gonic/gin@v1.9.1
GOPROXY=http://localhost:8080/repository/go-proxy go get github.com/rs/zerolog@v1.32.0
GOPROXY=http://localhost:8080/repository/go-proxy go get github.com/jackc/pgx/v5@v5.5.4
```

Check all available versions of a module through the proxy:

```bash
GOPROXY=http://localhost:8080/repository/go-proxy go list -m -versions github.com/gin-gonic/gin
```

### Hosted — publishing custom modules

Publish an internal module to the hosted repository. Use standard `go mod` tooling with `GOPROXY` pointing to the hosted repo:

```bash
# Tag the release in git, then push the module artifact
GOPROXY=http://localhost:8080/repository/go-hosted \
  go mod download github.com/myorg/mylib@v1.0.0
```

When using hosted modules in builds, set `GONOSUMCHECK` to skip the public checksum database for private modules:

```bash
GONOSUMCHECK=github.com/myorg/* \
GOPROXY=http://localhost:8080/repository/go-group,direct \
  go build ./...
```

Or configure in `go env` to apply to all builds in your environment:

```bash
go env -w GONOSUMCHECK=github.com/myorg/*
go env -w GONOSUMDB=github.com/myorg/*
go env -w GOPRIVATE=github.com/myorg/*
```

### Group — unified access

The group repository serves both private hosted modules and public cached modules through a single URL. Nexspence checks the hosted member first; if the module is not found it delegates to the proxy member.

Set `GOPROXY` once and forget the distinction between private and public modules:

```bash
GOPROXY=http://localhost:8080/repository/go-group,direct go get ./...
```

Make it permanent in your environment:

```bash
go env -w GOPROXY=http://localhost:8080/repository/go-group,direct
```

In CI pipelines, set the variable in your environment config:

```yaml
# GitHub Actions example
env:
  GOPROXY: http://nexspence.internal:8080/repository/go-group,direct
  GONOSUMCHECK: "*"
```

### Real-world examples

**Fetch popular web framework (gin) through proxy cache:**

```bash
GOPROXY=http://localhost:8080/repository/go-proxy \
  go get github.com/gin-gonic/gin@v1.9.1
```

**Fetch structured logger (zerolog):**

```bash
GOPROXY=http://localhost:8080/repository/go-proxy \
  go get github.com/rs/zerolog@v1.32.0
```

**Fetch PostgreSQL driver (pgx) through proxy cache:**

```bash
GOPROXY=http://localhost:8080/repository/go-proxy \
  go get github.com/jackc/pgx/v5@v5.5.4
```

**Offline build — all dependencies already cached:**

```bash
GOPROXY=http://localhost:8080/repository/go-group,off \
  go build ./...
```

Using `off` as the final fallback prevents the `go` toolchain from going to the public internet if a module is not in the cache, making the build fully air-gapped.

---

## Helm Chart Repositories

### Concept

Helm chart repositories follow the Helm HTTP protocol: a repository is simply an `index.yaml` file listing available charts, with chart packages served as `.tgz` files at stable URLs. Nexspence generates `index.yaml` dynamically from its database when Helm clients request it, so the index stays current automatically after each upload.

Charts are standard `helm package` tarballs. Hosted repositories accept uploads via HTTP PUT or multipart POST. Proxy repositories cache charts from upstream Helm registries such as `https://charts.helm.sh/stable` or format-specific repos like `https://charts.jetstack.io`.

### When to use: hosted vs proxy vs group

| Type | When to use |
|------|-------------|
| **hosted** | Publish internal Helm charts — application deployments, infrastructure charts |
| **proxy** | Cache charts from upstream registries — faster CI, reproducible deployments |
| **group** | One `helm repo add` URL — resolves internal charts first, then upstream cache |

### 17.3 Quick Start — run the script

```bash
./scripts/create-helm-repos.sh
```

Override defaults as needed:

```bash
BASE_URL=http://192.168.1.10:8080 \
PROXY_REMOTE=https://charts.helm.sh/stable \
./scripts/create-helm-repos.sh
```

To store charts in a specific blob store:

```bash
BLOB_STORE=s3-prod ./scripts/create-helm-repos.sh
```

Available environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `BASE_URL` | `http://localhost:8080` | Server URL |
| `ADMIN_USER` | `admin` | Admin login |
| `ADMIN_PASS` | `admin123` | Admin password |
| `HOSTED_NAME` | `helm-hosted` | Hosted repo name |
| `PROXY_NAME` | `helm-proxy` | Proxy repo name |
| `GROUP_NAME` | `helm-charts` | Group repo name |
| `PROXY_REMOTE` | `https://charts.helm.sh/stable` | Remote URL for proxy |
| `BLOB_STORE` | `default` | Blob store for hosted + proxy |

This creates three repositories:

| Name | Type |
|------|------|
| `helm-hosted` | hosted |
| `helm-proxy` | proxy (remote: `https://charts.helm.sh/stable`) |
| `helm-charts` | group (members: `helm-hosted`, `helm-proxy`) |

### Proxy — cache charts from upstream

Add the group or proxy repository as a Helm repo, then use it exactly like any official Helm registry:

```bash
helm repo add nexspence http://localhost:8080/repository/helm-charts
helm repo update
```

Search available charts:

```bash
helm search repo nexspence/
```

Pull a chart tarball manually (useful for air-gapped inspection):

```bash
helm pull nexspence/nginx-ingress --version 4.11.3
```

### Hosted — publish internal charts

Upload a chart package via `curl`:

```bash
curl -u admin:admin123 \
  -F "chart=@mychart-0.1.0.tgz" \
  http://localhost:8080/repository/helm-hosted/
```

Upload using the `helm-push` plugin (compatible with ChartMuseum-style endpoints):

```bash
helm plugin install https://github.com/chartmuseum/helm-push
helm cm-push mychart/ nexspence
```

After uploading, the chart is immediately searchable:

```bash
helm repo update
helm search repo nexspence/mychart
```

### Group — unified access

With the group repository configured, a single `helm repo add` gives clients access to both internal and upstream-cached charts:

```bash
helm repo add nexspence http://localhost:8080/repository/helm-charts
helm repo update
```

Helm resolves charts against group members in order — internal hosted charts take precedence over proxied upstream charts with the same name.

### Real-world examples

**Install nginx-ingress-controller from cache:**

```bash
helm repo add nexspence http://localhost:8080/repository/helm-charts
helm repo update
helm install my-ingress nexspence/nginx-ingress --version 4.11.3
```

**Install cert-manager into its own namespace:**

```bash
helm install cert-manager nexspence/cert-manager \
  --version v1.14.4 \
  --namespace cert-manager \
  --create-namespace
```

**Install Redis with namespace isolation:**

```bash
helm install redis nexspence/redis \
  --version 19.0.1 \
  -n redis \
  --create-namespace
```

**Package and publish an internal application chart:**

```bash
helm package ./deploy/helm/myapp
curl -u admin:admin123 \
  -F "chart=@myapp-1.5.0.tgz" \
  http://localhost:8080/repository/helm-hosted/
helm repo update
helm upgrade --install myapp nexspence/myapp --version 1.5.0
```

**CI pipeline — reproducible chart installs:**

```bash
# In CI, always use a pinned version from the Nexspence cache
helm repo add nexspence "${NEXSPENCE_URL}/repository/helm-charts"
helm repo update
helm dependency update ./deploy/helm/myapp
helm install myapp nexspence/myapp --version "${CHART_VERSION}"
```
