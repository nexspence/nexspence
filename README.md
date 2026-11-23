<div align="center">
  <img src="https://nexspence.com/assets/logo.png" alt="Nexspence" width="380">
  <br><br>
  <p><strong>Free, open-source universal artifact repository manager</strong></p>
  <p>A full-featured self-hosted alternative to Sonatype Nexus Repository</p>
  <br>

  [![Latest release](https://img.shields.io/github/v/release/nexspence/nexspence?style=flat-square&color=22c55e)](https://github.com/nexspence/nexspence/releases/latest)
  [![Stars](https://img.shields.io/github/stars/nexspence/nexspence?style=flat-square&color=f59e0b)](https://github.com/nexspence/nexspence/stargazers)
  [![Forks](https://img.shields.io/github/forks/nexspence/nexspence?style=flat-square&color=0ea5e9)](https://github.com/nexspence/nexspence/forks)
  ![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat-square&logo=go&logoColor=white)
  ![React](https://img.shields.io/badge/React-19-61DAFB?style=flat-square&logo=react&logoColor=black)
  ![TypeScript](https://img.shields.io/badge/TypeScript-6-3178C6?style=flat-square&logo=typescript&logoColor=white)
  ![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16+-4169E1?style=flat-square&logo=postgresql&logoColor=white)
  ![Docker](https://img.shields.io/badge/Docker-ready-2496ED?style=flat-square&logo=docker&logoColor=white)
  [![GHCR image](https://img.shields.io/ghcr/v/nexspence/nexspence?logo=docker&style=flat-square&color=2496ED)](https://github.com/nexspence/nexspence/pkgs/container/nexspence)
  [![License: AGPL v3 or later](https://img.shields.io/badge/License-AGPL--3.0--or--later-22c55e?style=flat-square)](LICENSE)
  ![Lint](https://img.shields.io/badge/lint-golangci--lint%20v2-22c55e?style=flat-square&logo=go&logoColor=white)
  ![Tests](https://img.shields.io/badge/tests-2600+%20passing-22c55e?style=flat-square)](https://github.com/nexspence/nexspence/actions/workflows/ci.yml)

  <br>

  [![Website](https://img.shields.io/badge/nexspence.com-0f172a?style=for-the-badge&logo=firefox&logoColor=white)](https://nexspence.com)
  [![Docs](https://img.shields.io/badge/Documentation-1e293b?style=for-the-badge&logo=readthedocs&logoColor=white)](https://nexspence.com/docs/)
  [![Discussions](https://img.shields.io/badge/Discussions-6b7280?style=for-the-badge&logo=github&logoColor=white)](https://github.com/nexspence/nexspence/discussions)
  [![Telegram community](https://img.shields.io/badge/Telegram-@nexspence__community-26A5E4?style=for-the-badge&logo=telegram&logoColor=white)](https://t.me/nexspence_community)

</div>

---

## Start in two minutes

One binary, one Postgres, no license key. Requires Docker 24+ and a free port 8081.

```bash
docker network create nexspence-net 2>/dev/null

docker run -d --name nexspence-db --network nexspence-net \
  -e POSTGRES_USER=nexspence -e POSTGRES_PASSWORD=nexspence -e POSTGRES_DB=nexspence \
  postgres:16-alpine

# wait until Postgres accepts connections
until docker exec nexspence-db pg_isready -U nexspence >/dev/null 2>&1; do sleep 1; done

docker run -d --name nexspence --network nexspence-net \
  -p 8081:8081 \
  -e NEXSPENCE_DATABASE_DSN="postgres://nexspence:nexspence@nexspence-db:5432/nexspence?sslmode=disable" \
  -e NEXSPENCE_AUTH_ALLOW_INSECURE_DEFAULTS=true \
  -v nexspence-blobs:/app/data/blobs \
  ghcr.io/nexspence/nexspence:latest
```

Open **http://localhost:8081** — sign in as `admin` / `admin123`, then change the password.

> `NEXSPENCE_AUTH_ALLOW_INSECURE_DEFAULTS=true` permits the shipped development password and is for local evaluation only. In production, set `NEXSPENCE_AUTH_JWT_SECRET` (32+ characters) and `NEXSPENCE_BOOTSTRAP_ADMIN_PASSWORD` instead.

Want the full stack — Prometheus, Grafana, Keycloak SSO, MinIO? `git clone https://github.com/nexspence/nexspence && cd nexspence && docker compose up -d`.

**Already running Sonatype Nexus?** Nexspence imports a live Nexus instance over its REST API — repository definitions, artifacts, container images, users, roles and routing rules — while the source keeps serving traffic. The job is pausable, resumable and survives a restart: **[migration guide](docs/nexus-migration.md)**.

---

## 🎬 Demo

<p align="center">
  <a href="https://nexspence.com/assets/demo.mp4">
    <img src="website/assets/screenshots/repositories.PNG" alt="Nexspence demo: repositories dashboard" width="640">
  </a>
</p>

▶️ **[Watch the full demo on nexspence.com](https://nexspence.com)** — repositories, RBAC, blob stores, cleanup policies, the vulnerability dashboard and live Nexus migration, in two minutes.

---

## What is Nexspence?

Nexspence is a self-hosted artifact repository manager that supports **19 package formats**, three repository types (hosted, proxy, group), fine-grained RBAC, SSO via OIDC/LDAP, audit logging, S3-compatible storage, and a modern dark-theme web UI — all in a single binary backed by PostgreSQL. It exposes the full **Sonatype Nexus v1 REST API** at `/service/rest/v1/` for drop-in compatibility with existing CI/CD pipelines and package manager configs.

---

## Why not Sonatype Nexus Repository?

The short version: Nexus Repository Community Edition is no longer open source, it is capped, it phones home, and the features people actually want (HA, SSO, replication, staging) sit in paid tiers. Nexspence has no such tiers — because it has no paid edition at all.

| | Nexus Repository CE | Nexspence |
|---|---|---|
| Licence | EULA (proprietary) since 3.77.0 — the open-source core covers only Maven, raw and APT | AGPL-3.0-or-later, one licence, no tiers |
| Component cap | 40,000 components | none |
| Request cap | 100,000 requests/day | none |
| Telemetry | mandatory base telemetry since 3.97.0 (Oct 2026), acknowledged at install; an instance that cannot report for 10 days (30 on 3.98+) goes read-only — and CE has no exemption | none |
| High availability | paid tiers | included — stateless nodes, Redis locks, `/healthz` + `/readyz` |
| SSO | LDAP in CE; OIDC/SAML in paid tiers | OIDC/OAuth2 **and** LDAP, included |
| Blob stores | S3 / Azure / GCS in paid tiers | S3-compatible (AWS, MinIO, Ceph) and Azure, included |
| Staging & promotion | paid tiers | included — CEL filters, scan gate, approval queue |
| Runtime | JVM, ~1.5–2 GB RAM | one Go binary, ~60–80 MB RAM |
| API compatibility | — | full Nexus v1 REST API at `/service/rest/v1/` — change the host, not your CI |
| Migration from Nexus | per-repository export/import | live import of a running instance — repositories, artifacts, images, users, roles, routing rules |
| Cost | free tier + paid Pro / Pro+ / Enterprise | free forever, funded by sponsors |

<sub>Sources: [Nexus Repository editions and feature availability](https://help.sonatype.com/en/nexus-repository-tiers-and-feature-availability.html), [Nexus Repository telemetry](https://help.sonatype.com/en/nexus-repository-telemetry.html), [2025 release notes](https://help.sonatype.com/en/nexus-repository-2025-release-notes.html), [Introducing the Community Edition](https://www.sonatype.com/blog/sonatype-nexus-repository-community-edition). Limits and tiers change with every Sonatype release — check the linked pages for the current state. "Sonatype" and "Nexus Repository" are trademarks of Sonatype, Inc.; Nexspence is not affiliated with or endorsed by Sonatype.</sub>

Honest comparison with the rest of the field: **JFrog Artifactory** (the free OSS edition covers Maven/Gradle/Ivy/SBT only; the rest is priced per user), **Pulp** (GPL, multi-format, plugin-driven, Red Hat-backed), **Harbor** (OCI registry only), **Verdaccio** (npm only), **Reposilite** (Maven only), **Gitea/Forgejo** (a registry as a side feature of a forge).

---

## Architecture

<p align="center">
  <img src="docs/assets/architecture.png" alt="Nexspence architecture: clients and load balancer above stateless Nexspence nodes (HTTP layer, auth and RBAC, format handlers, services, background schedulers), over shared PostgreSQL, optional Redis and blob storage" width="900">
</p>

One Go binary — Gin HTTP layer, embedded React UI, pluggable artifact formats. Every node is stateless; all state lives in PostgreSQL, blob storage, and (for HA) Redis, so scaling out is a matter of running more nodes behind the load balancer.

View the full site with interactive architecture diagram, install guide, and comparison: **[nexspence.com](https://nexspence.com)** →

---

## Screenshots

### Dashboard & Repositories

<table>
  <tr>
    <td><img src="website/assets/screenshots/repositories.PNG" alt="Repositories page" width="480"></td>
    <td><img src="website/assets/screenshots/browse.PNG" alt="Browse" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Repositories list</em></td>
    <td align="center"><em>Browse tree view</em></td>
  </tr>
</table>

### Admin & Security

<table>
  <tr>
    <td><img src="website/assets/screenshots/admin_blobstores.PNG" alt="Blob Stores" width="480"></td>
    <td><img src="website/assets/screenshots/security_roles.PNG" alt="Roles & RBAC" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Blob stores — S3, Azure + local with connection test</em></td>
    <td align="center"><em>Roles, privileges, content selectors</em></td>
  </tr>
</table>

### Cleanup & Search

<table>
  <tr>
    <td><img src="website/assets/screenshots/cleanup.PNG" alt="Cleanup policies" width="480"></td>
    <td><img src="website/assets/screenshots/search.PNG" alt="Search" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Cleanup policies — criteria, schedule and scope</em></td>
    <td align="center"><em>Full-text component search</em></td>
  </tr>
</table>

### OCI, Vulnerabilities & Monitoring

<table>
  <tr>
    <td><img src="website/assets/screenshots/browse_oci.PNG" alt="OCI repository" width="480"></td>
    <td><img src="website/assets/screenshots/security_vulnerabilities.PNG" alt="Vulnerability dashboard" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Helm charts pushed with <code>helm push oci://</code></em></td>
    <td align="center"><em>Vulnerability dashboard — Trivy + OSV findings</em></td>
  </tr>
  <tr>
    <td><img src="website/assets/screenshots/monitoring_charts.PNG" alt="Monitoring charts" width="480"></td>
    <td><img src="website/assets/screenshots/audit.PNG" alt="Audit log" width="480"></td>
  </tr>
  <tr>
    <td align="center"><em>Requests/sec, error rate and storage over time</em></td>
    <td align="center"><em>Audit log — every mutation, filterable and exportable</em></td>
  </tr>
</table>

---

## Quick Start — full stack

> Just want to try it? The [two-minute start](#start-in-two-minutes) above is enough. This section is the complete stack.

**Requirements:** [Docker](https://docs.docker.com/get-docker/) 24+ with Compose v2

```bash
git clone https://github.com/nexspence/nexspence
cd nexspence
docker compose up -d
```

| Service | URL | Default credentials |
|---------|-----|---------------------|
| Web UI & REST API | http://localhost:8081 | `admin` / `admin123` |
| Docker / OCI registry | http://localhost:8081/v2/ | same credentials |
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

> `config.jwtSecret` is optional — the chart auto-generates a unique secret (persisted across upgrades) when it is omitted. Set it only to pin a known value.

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
| Docker | ✓ | ✓ | ✓ |
| OCI artifacts (Helm `oci://` push, ORAS, cosign) | ✓ | ✓ | ✓ |
| NuGet v2 / v3 | ✓ | ✓ | ✓ |
| Helm charts | ✓ | ✓ | ✓ |
| Cargo (Rust) | ✓ | ✓ | ✓ |
| Raw files | ✓ | ✓ | ✓ |
| APT (Debian/Ubuntu) | ✓ | ✓ | ✓ |
| Yum / RPM | ✓ | ✓ | ✓ |
| Conan (C/C++) | ✓ | ✓ | ✓ |
| Conda | ✓ | ✓ | ✓ |
| Terraform Registry | ✓ | ✓ | ✓ |
| RubyGems | ✓ | ✓ | — |
| R / CRAN | ✓ | ✓ | ✓ |
| Alpine (apk) | ✓ | ✓ | ✓ |
| Hugging Face (models, datasets, spaces) | ✓ | ✓ | ✓ |

---

## Features

**Repository Types**
- Hosted — direct upload and storage
- Proxy — transparent remote caching; mutations rejected with 405
- Group — ordered union of hosted + proxy repos under one URL; index documents are merged across members, not shadowed by the first one that answers

**OCI Registry**
- One implementation serving two format labels: `docker` for container images, `oci` for Helm charts pushed with `helm push oci://`, ORAS artifacts and cosign signatures
- Referrers API (`/v2/{name}/referrers/{digest}`) with `artifactType` filtering — cosign signatures, SBOMs and in-toto attestations are discoverable
- Catalog and paginated tag listing; cross-repository blob mounts, access-checked against the source
- Artifact types recorded on push and on proxy cache-fill, and shown in the browse tree

**Security & Auth**
- Local accounts with JWT bearer tokens and bcrypt passwords
- LDAP / Active Directory — JIT provisioning, group-to-role mapping
- OIDC / OAuth2 SSO — Keycloak, Google, Entra ID, Okta; PKCE
- User API tokens (`nxs_*` prefix, SHA-256 hashed)
- RBAC — Roles, Privileges, Content Selectors (CEL expressions)

**Storage**
- Local filesystem (default), S3-compatible (AWS S3, MinIO, Ceph), or Azure Blob Storage
- Per-repository blob store routing; blob store groups (round-robin / write-to-first)
- Storage quotas per blob store and per repository

**Operations**
- High Availability — stateless nodes, Redis distributed locks, `/healthz` + `/readyz`
- Cleanup policies — by age, last-downloaded, retain-N-versions; cron scheduler; dry-run
- Per-repository export / import (streaming `.tar.gz`); full system backup / restore
- Live migration from a running Nexus instance — repositories, artifacts, container images, privileges/roles/users, routing rules; pausable, resumable, restart-safe ([guide](docs/nexus-migration.md))
- Vulnerability scanning — OSV.dev for Maven/npm/PyPI/Cargo out of the box; Docker/OCI images with a Trivy you supply ([setup](docs/scanning.md))
- Audit log — every action logged; NDJSON streaming export; 90-day partition rotation
- Webhooks — HMAC-SHA256 signed; `artifact.published`, `artifact.deleted`, repo events
- Content Replication — push to remote instance on cron schedule
- **Monitoring** — Prometheus `/metrics` endpoint (Bearer-auth by default, anonymous with `metrics.public`); pre-built Grafana dashboard; ring-buffer history API; UI Charts + Repositories tabs

---

## Documentation

Full documentation — deployment variants, HA setup, OIDC SSO, webhooks, the RBAC guide, the OpenAPI spec, and the architecture overview — lives on the website:

📖 **[nexspence.com/docs](https://nexspence.com/docs/)**

### Format guides

End-to-end guides for using Nexspence as a registry — create the repositories,
point the client at them, publish, consume, and debug what breaks:

| | | |
|---|---|---|
| [Maven](docs/guides/maven.md) | [npm](docs/guides/npm.md) | [PyPI](docs/guides/pypi.md) |
| [Docker / OCI](docs/guides/docker.md) | [GitHub Actions](docs/guides/github-actions.md) | [All guides](docs/guides/README.md) |

The Helm chart reference ships with the chart itself: [`deploy/helm/nexspence/README.md`](deploy/helm/nexspence/README.md).

Every release is listed in **[CHANGELOG.md](CHANGELOG.md)**.

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
| 69 | Blob GC — age-gated orphan collection, global cron scheduler, UI panel | ✓ complete |
| 70 | OpenTelemetry tracing — spans across API, proxy and background jobs, trace_id in structured logs | ✓ complete |
| CLI | [`nxs` CLI](https://github.com/nexspence/nxs) — terminal & CI/CD client, v0.1.0 | ✓ complete |
| next | SBOM generation, cosign image signing | planned |

---

## Sponsors

Nexspence is free and AGPLv3 — no paid tier, no license fees, nothing behind a paywall.
Sponsorship pays for the demo instance, the docs site, CI minutes and development time.
Where every dollar goes: **[nexspence.com/#sponsors](https://nexspence.com/#sponsors)**.

<div align="center">
  <a href="https://github.com/sponsors/nexspence">
    <img src="https://img.shields.io/badge/Sponsor%20on%20GitHub-ea4aaa?style=for-the-badge&logo=githubsponsors&logoColor=white" alt="Sponsor Nexspence on GitHub">
  </a>
</div>

| Tier | Price | Rewards |
|------|-------|---------|
| ☕ Coffee | $3 once | Thank-you in the next release notes |
| ☕ Supporter | $5/mo | Sponsor badge · your name in [SPONSORS.md](SPONSORS.md) · newsletter |
| 🚀 Backer | $25/mo | + your name and link in this README · a say in roadmap polls |
| 🏢 Sponsor | $100/mo | + your logo here and on [nexspence.com](https://nexspence.com) · priority issue triage |

<!-- SPONSORS:START — company logos and backer names go here -->
_No sponsors yet — [be the first](https://github.com/sponsors/nexspence), your logo goes here._
<!-- SPONSORS:END -->

Full list and details: **[SPONSORS.md](SPONSORS.md)**

---

## Contributing

Contributions are welcome. Please open an issue to discuss proposed changes before submitting a pull request.

### Where to start

New to the codebase? These are the fastest ways to land a first contribution:

- **Pick a task:** issues labelled [`good first issue`](https://github.com/nexspence/nexspence/labels/good%20first%20issue) and [`help wanted`](https://github.com/nexspence/nexspence/labels/help%20wanted) are scoped small on purpose. Comment to claim one before you start.
- **Ask first, code later:** open a [Discussion](https://github.com/nexspence/nexspence/discussions) for anything design-shaped — it is cheaper than a rejected pull request.
- **Docs count:** a missing guide for your ecosystem (Maven, npm, PyPI, Helm, Docker) is a real contribution, and format-specific guides are listed under `good first issue`.
- **Migrating from Nexus?** The [migration guide](docs/nexus-migration.md) is the source of truth for what is imported and what is not.

### Local development

```bash
# Backend
go test ./...

# Frontend
cd frontend && npm run lint && npm test
```

Full contributor rules — licensing of contributions, commit conventions, coverage gates — are in **[CONTRIBUTING.md](CONTRIBUTING.md)**.

---

## License

`AGPL-3.0-or-later` — the full text is in [LICENSE](LICENSE), the copyright and
third-party notices in [NOTICE](NOTICE).

AGPL rather than a permissive licence for one reason: Nexspence is a network
service, and §13 is what keeps a hosted fork from being closed off. Running it
inside your company, however you like, needs nothing from you — the obligation
only starts if you offer a modified Nexspence to others over a network, and then
it is to publish those modifications.

Contributions are accepted under the same licence, with no CLA and no sign-off
to remember — see [CONTRIBUTING.md](CONTRIBUTING.md).

## Community & contact

| Where | What for |
|-------|----------|
| 💬 **[Discussions](https://github.com/nexspence/nexspence/discussions)** | Questions, ideas, "how do I…", show-and-tell — the best place to get help |
| 💬 **[@nexspence_community](https://t.me/nexspence_community)** | Telegram channel — release announcements, setup questions, roadmap talk |
| 🐞 **[GitHub Issues](https://github.com/nexspence/nexspence/issues)** | Bug reports and feature requests, tracked to a release |
| ⭐ **[Star the repo](https://github.com/nexspence/nexspence/stargazers)** | The only "like" button that exists here — it is how other people find the project |
| ✉️ **[@skensel](https://t.me/skensel)** | Direct line to the maintainer — security reports, sponsorship, anything private |

Security problems: please do **not** open a public issue — contact the maintainer directly so a fix can ship before the details are public.

---

<div align="center">
  <img src="https://nexspence.com/assets/mini_logo.png" alt="Nexspence" width="60">
  <br>
  <sub>AGPLv3 License · Built with Go + React</sub>
</div>
