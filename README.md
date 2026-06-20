<div align="center">
  <img src="https://nexspence.com/assets/logo.png" alt="Nexspence" width="380">
  <br><br>
  <p><strong>Free, open-source universal artifact repository manager</strong></p>
  <p>A full-featured self-hosted alternative to Sonatype Nexus Repository</p>
  <br>

  ![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat-square&logo=go&logoColor=white)
  ![React](https://img.shields.io/badge/React-19-61DAFB?style=flat-square&logo=react&logoColor=black)
  ![TypeScript](https://img.shields.io/badge/TypeScript-6-3178C6?style=flat-square&logo=typescript&logoColor=white)
  ![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16+-4169E1?style=flat-square&logo=postgresql&logoColor=white)
  ![Docker](https://img.shields.io/badge/Docker-ready-2496ED?style=flat-square&logo=docker&logoColor=white)
  ![License](https://img.shields.io/badge/License-AGPLv3-22c55e?style=flat-square)
  ![Lint](https://img.shields.io/badge/lint-golangci--lint%20v2-22c55e?style=flat-square&logo=go&logoColor=white)
  ![Tests](https://img.shields.io/badge/tests-1846%20passing-22c55e?style=flat-square)

</div>

---

## 🎬 Demo

▶️ **[Watch the demo on nexspence.com](https://nexspence.com)**

---

## What is Nexspence?

Nexspence is a self-hosted artifact repository manager that supports **14 package formats**, three repository types (hosted, proxy, group), fine-grained RBAC, SSO via OIDC/LDAP, audit logging, S3-compatible storage, and a modern dark-theme web UI — all in a single binary backed by PostgreSQL. It exposes the full **Sonatype Nexus v1 REST API** at `/service/rest/v1/` for drop-in compatibility with existing CI/CD pipelines and package manager configs.

---

## Architecture

```
                         ┌─────────────────────┐
                         │   Load Balancer     │  (nginx / k8s Ingress / ALB)
                         └──────────┬──────────┘
                    ┌───────────────┴───────────────┐
                    ▼                               ▼
┌────────────┐  JWT/Basic  ┌──────────────────┐   ┌──────────────────┐
│  Client    │ ──────────▶ │  Nexspence node 1 │  │  Nexspence node 2│  (HA)
│ (curl/mvn/ │             │  Gin + Auth +     │  │  identical       │
│  pip/npm…) │ ◀────────── │  Audit + RBAC     │  └────────┬─────────┘
└────────────┘             └────────┬──────────┘           │
                                    │                      │
                    ┌───────────────▼──────────────────────▼──────┐
                    │           Shared State                      │
                    │  ┌──────────────┐  ┌─────────┐  ┌──────────┐│
                    │  │  PostgreSQL  │  │  Redis  │  │  S3/MinIO││
                    │  │  (all data)  │  │  (locks │  │  (blobs) ││
                    │  └──────────────┘  │  cache) │  └──────────┘│
                    │                    └─────────┘              │
                    └─────────────────────────────────────────────┘
```

View the full site with interactive architecture diagram, install guide, and comparison: **[nexspence.com](https://nexspence.com)** →

---

## Screenshots

### Dashboard & Repositories

<table>
  <tr>
    <td><img src="https://nexspence.com/assets/screenshots/repositories.PNG" alt="Repositories page" width="480"></td>
    <td><img src="https://nexspence.com/assets/screenshots/browse.PNG" alt="Browse" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Repositories list</em></td>
    <td align="center"><em>Browse tree view</em></td>
  </tr>
</table>

### Admin & Security

<table>
  <tr>
    <td><img src="https://nexspence.com/assets/screenshots/admin_blobstores.PNG" alt="Blob Stores" width="480"></td>
    <td><img src="https://nexspence.com/assets/screenshots/security_roles.PNG" alt="Roles & RBAC" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Blob stores — S3 + local with connection test</em></td>
    <td align="center"><em>Roles, privileges, content selectors</em></td>
  </tr>
</table>

### Cleanup & Search

<table>
  <tr>
    <td><img src="https://nexspence.com/assets/screenshots/cleanup.PNG" alt="Cleanup policies" width="480"></td>
    <td><img src="https://nexspence.com/assets/screenshots/search.PNG" alt="Search" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Cleanup policies with dry-run preview</em></td>
    <td align="center"><em>Full-text component search</em></td>
  </tr>
</table>

---

## Quick Start

**Requirements:** [Docker](https://docs.docker.com/get-docker/) 24+ with Compose v2

```bash
git clone https://github.com/nexspence/nexspence
cd nexspence
docker compose up -d
```

| Service | URL | Default credentials |
|---------|-----|---------------------|
| Web UI & REST API | http://localhost:8081 | `admin` / `admin123` |
| Docker registry | localhost:5001 | same credentials |
| PostgreSQL | localhost:5437 | `nexspence` / `nexspence` |

> Change the admin password immediately after first login.

### Docker Compose Profiles

The compose file uses profiles to opt into optional services. Combine as needed:

| Profile | Adds | Command |
|---------|------|---------|
| _(none)_ | Nexspence + PostgreSQL + MinIO | `docker compose up -d` |
| `monitoring` | Prometheus + Grafana | `docker compose --profile monitoring up -d` |
| `keycloak` | Keycloak OIDC IdP | `OIDC_ENABLED=true docker compose --profile keycloak up -d` |
| `keycloak` + `monitoring` | Both | `OIDC_ENABLED=true docker compose --profile keycloak --profile monitoring up -d` |
| `dev` | Vite frontend dev server | `docker compose --profile dev up` |

**Monitoring setup** — before starting the `monitoring` profile, create a Bearer token:

```bash
# Copy the example and fill in a valid nxs_* API token
cp deploy/monitoring/prometheus-token.example deploy/monitoring/prometheus-token
# edit the file and paste your token
```

Once running: Prometheus at **http://localhost:9090** · Grafana at **http://localhost:3000** (admin / admin)

The pre-built Grafana dashboard (`Nexspence Overview`) loads automatically with 8 panels: requests/sec, error rate, latency p95, artifacts, storage, downloads, goroutines, memory.

**Standalone monitoring** (target an existing Nexspence instance):

```bash
cd deploy/monitoring
NEXSPENCE_URL=http://my-server:8081 docker compose up -d
```

For all deployment variants (MinIO, HA cluster, Keycloak SSO, from source) see the **[documentation](https://nexspence.com/docs/)**.

---

### Native Install (no Docker)

Prefer running on bare metal? Download the `.deb`/`.rpm` (Linux) or the macOS/Windows
archive from the [latest release](https://github.com/nexspence/nexspence/releases/latest).
Each ships with systemd / launchd / Windows-service integration, and the binary embeds
the web UI (self-contained). Full walkthrough — including reverse-proxy (nginx/Caddy)
and multi-node load-balancer setups — in the **[documentation](https://nexspence.com/docs/)**.
Requires an external PostgreSQL.

---

## CLI Tool — `nxs`

Manage Nexspence from the terminal or CI/CD pipelines:

```bash
# Install
curl -sSfL https://raw.githubusercontent.com/nexspence/nxs/main/install.sh | sh

# Login and use
nxs login --url http://localhost:8081 --user admin
nxs repo list
nxs push my-repo path/to/artifact.jar artifact.jar
nxs search --repo maven-releases --q mylib --json | jq '.[].version'
```

Full command reference and CI/CD examples: **[github.com/nexspence/nxs](https://github.com/nexspence/nxs)**

---

## Kubernetes (Helm)

**Requirements:** Helm 3.x, Kubernetes >= 1.26

```bash
cd deploy/helm/nexspence && helm dependency update
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/nginx.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

Five networking options (nginx, Traefik, Cilium ingress, Istio Gateway, Cilium Gateway API), external PostgreSQL, S3 storage, and HPA — see **[deploy/helm/nexspence/README.md](deploy/helm/nexspence/README.md)**.

---

## Terraform Provider

Manage Nexspence as code with the official Terraform provider — repositories, blob stores, users, roles, content selectors, and privileges.

```hcl
terraform {
  required_providers {
    nexspence = {
      source  = "nexspence/nexspence"
      version = "~> 0.2"
    }
  }
}

provider "nexspence" {
  url   = "https://nexspence.example.com"
  token = var.nexspence_token # nxs_* API token
}

resource "nexspence_repository" "maven_central" {
  name       = "maven-central"
  format     = "maven2"
  type       = "proxy"
  blob_store = "default"
  proxy {
    remote_url = "https://repo1.maven.org/maven2/"
  }
}
```

Published on the [Terraform Registry](https://registry.terraform.io/providers/nexspence/nexspence) — source at **[nexspence/terraform-provider-nexspence](https://github.com/nexspence/terraform-provider-nexspence)**.

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

**Repository Types**
- Hosted — direct upload and storage
- Proxy — transparent remote caching; mutations rejected with 405
- Group — ordered union of hosted + proxy repos under one URL

**Security & Auth**
- Local accounts with JWT bearer tokens and bcrypt passwords
- LDAP / Active Directory — JIT provisioning, group-to-role mapping
- OIDC / OAuth2 SSO — Keycloak, Google, Entra ID, Okta; PKCE
- User API tokens (`nxs_*` prefix, SHA-256 hashed)
- RBAC — Roles, Privileges, Content Selectors (CEL expressions)

**Storage**
- Local filesystem (default) or S3-compatible (AWS S3, MinIO, Ceph)
- Per-repository blob store routing; blob store groups (round-robin / write-to-first)
- Storage quotas per blob store and per repository

**Operations**
- High Availability — stateless nodes, Redis distributed locks, `/healthz` + `/readyz`
- Cleanup policies — by age, last-downloaded, retain-N-versions; cron scheduler; dry-run
- Per-repository export / import (streaming `.tar.gz`); full system backup / restore
- Live migration from a running Nexus OSS/Pro instance
- Vulnerability scanning — Trivy (Docker) + OSV.dev (Maven/npm/PyPI/Cargo)
- Audit log — every action logged; NDJSON streaming export; 90-day partition rotation
- Webhooks — HMAC-SHA256 signed; `artifact.published`, `artifact.deleted`, repo events
- Content Replication — push to remote instance on cron schedule
- **Monitoring** — Prometheus `/metrics` endpoint (Bearer-auth); pre-built Grafana dashboard; ring-buffer history API; UI Charts + Repositories tabs

---

## Documentation

Full documentation — deployment variants, HA setup, OIDC SSO, webhooks, the RBAC guide, the OpenAPI spec, and the architecture overview — lives on the website:

📖 **[nexspence.com/docs](https://nexspence.com/docs/)**

The Helm chart reference ships with the chart itself: [`deploy/helm/nexspence/README.md`](deploy/helm/nexspence/README.md).

---

## Roadmap

| Phase | Feature | Status |
|-------|---------|--------|
| 1–22 | Core — repos, RBAC, formats, blob stores, proxy, group, cleanup | ✓ complete |
| 25–28 | Audit log, Docker anon auth, OIDC/OAuth2 SSO | ✓ complete |
| 38–51 | Live Nexus migration, sidebar collapse, S3 routing, blob store groups | ✓ complete |
| 53–55 | High Availability, vulnerability dashboard, content replication | ✓ complete |
| 56 | Staging & Build Promotion — CEL filter, scan gate, approval queue | ✓ complete |
| 60–63 | LDAP role mapping, Conda, Terraform, Helm chart | ✓ complete |
| 64–67 | Landing page, in-app docs, security hardening | ✓ complete |
| 68 | Extended monitoring — Prometheus endpoint, Grafana dashboard, UI Charts tab | ✓ complete |
| CLI | [`nxs` CLI](https://github.com/nexspence/nxs) — terminal & CI/CD client, v0.1.0 | ✓ complete |
| next | SBOM generation, cosign image signing | planned |
| next | OpenTelemetry traces | planned |
| next | blob GC | planned |

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
  <img src="https://nexspence.com/assets/mini_logo.png" alt="Nexspence" width="60">
  <br>
  <sub>AGPLv3 License · Built with Go + React</sub>
</div>
