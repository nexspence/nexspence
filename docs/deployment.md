# Nexspence — Deployment Guide

## Download

All releases — Docker images, `docker-compose.yml`, `config.yaml.example` — are at:

**[github.com/nexspence/nexspence/releases](https://github.com/nexspence/nexspence/releases)**

Download `docker-compose.yml` and `config.yaml.example` from the latest release, then follow the relevant section below.

---

## Docker Compose — Standard

```bash
# 1. Download files from the latest release:
#    https://github.com/nexspence/nexspence/releases/latest
#    → docker-compose.yml
#    → config.yaml.example  (rename to config.yaml and edit)

cp config.yaml.example config.yaml

# 2. Edit config.yaml — change at minimum:
#      auth.jwt_secret        (min 32 characters)
#      bootstrap.admin_password

# 3. Start PostgreSQL + Nexspence (auto-migrates schema on first run)
docker compose up -d

# 4. Verify
docker compose ps
docker compose logs -f nexspence
```

| Service | URL | Default credentials |
|---------|-----|---------------------|
| Web UI & REST API | http://localhost:8081 | `admin` / `admin123` |
| Docker registry | localhost:5000 | same credentials |
| PostgreSQL | localhost:5437 | `nexspence` / `nexspence` |

> Change the admin password immediately after first login via **Admin → Security → Users**.

---

## Docker Compose — With MinIO (S3)

MinIO is included in `docker-compose.yml` as an optional profile:

```bash
# Start with MinIO as the default blob store
NEXSPENCE_STORAGE_DEFAULT_TYPE=s3 \
  docker compose up -d

# MinIO S3 API:    http://localhost:9000
# MinIO console:   http://localhost:9001  (minioadmin / minioadmin)
# Nexspence UI:    http://localhost:8081
```

---

## Docker Compose — HA Cluster

`docker-compose.ha.yml` (included in the release) runs 2 Nexspence nodes, nginx (`least_conn` load balancer), Redis, MinIO, and PostgreSQL. All nodes are stateless — shared state lives in PostgreSQL, Redis, and S3.

```bash
# Download docker-compose.ha.yml from the latest release, then:
docker compose -f docker-compose.ha.yml up -d

# Load balancer:  http://localhost:8080
# 2 x Nexspence + nginx LB + Redis + MinIO + PostgreSQL
```

Enable Redis in `config.yaml` for each node:

```yaml
redis:
  enabled: true
  addr: "redis:6379"
  password: ""
  db: 0
```

See [docs/ha-setup.md](ha-setup.md) for the full HA guide including Kubernetes probe examples.

---

## Docker Compose — With Keycloak SSO

Starts a pre-configured Keycloak dev instance with the `nexspence` realm imported. "Sign in with Keycloak" appears on the login page automatically.

```bash
OIDC_ENABLED=true \
  docker compose --profile keycloak up -d

# Nexspence UI:    http://localhost:8081  (admin / admin123)
# Keycloak admin:  http://localhost:8180  (admin / admin)
# Test SSO user:   testuser / testpass (mapped to nx-admin role)
```

See [docs/oidc-setup.md](oidc-setup.md) for manual OIDC config and all supported providers (Keycloak, Google, Entra ID, Okta).

---

## Native Install (no Docker)

Run Nexspence directly on Linux (`.deb`/`.rpm`), macOS, or Windows with systemd /
launchd / Windows-service integration. The single binary embeds the web UI and requires
only an external PostgreSQL. See the full guide — including reverse-proxy and multi-node
load-balancer configs — in [install-local.md](install-local.md).

---

## Kubernetes (Helm)

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

Five networking options (nginx, Traefik, Cilium ingress, Istio Gateway, Cilium Gateway API), external PostgreSQL, S3 storage, and HPA — see [deploy/helm/nexspence/README.md](../deploy/helm/nexspence/README.md).

---

## Configuration Reference

`config.yaml` is the primary configuration file. Every key can be overridden via an environment variable using the pattern `NEXSPENCE_<SECTION>_<KEY>` (uppercase, underscore-separated).

| Key | Default | Description |
|-----|---------|-------------|
| `http.addr` | `:8081` | Listen address |
| `http.base_url` | `http://localhost:8081` | Public URL used in download links |
| `database.dsn` | `postgres://nexspence:nexspence@localhost:5437/nexspence` | PostgreSQL connection string |
| `storage.default_type` | `local` | `local` or `s3` |
| `storage.local.base_path` | `./data/blobs` | Filesystem path for local blob store |
| `storage.s3.bucket` | — | S3 bucket name (required when type=s3) |
| `storage.s3.endpoint` | — | S3 endpoint URL (e.g. `http://minio:9000`) |
| `storage.s3.force_path_style` | `true` | Required for MinIO / non-AWS S3 |
| `auth.jwt_secret` | — | JWT signing key — **change before production** |
| `auth.encryption_key` | — | Optional base64 32-byte key for replication credentials (decouples them from `jwt_secret`; existing rows are re-encrypted automatically at startup). Generate: `openssl rand -base64 32` |
| `auth.jwt_expiry_hours` | `24` | JWT token lifetime |
| `auth.anonymous_enabled` | `true` | Allow unauthenticated read on public repos |
| `auth.token_max_days` | `180` | Maximum lifetime for user API tokens (`nxs_*`) |
| `bootstrap.admin_password` | `admin123` | Auto-created admin password — **change this** |
| `cleanup.default_schedule` | `0 2 * * *` | Default cron for cleanup policies |
| `audit.retention_days` | `90` | Audit log partition retention |
| `redis.enabled` | `false` | Enable Redis (required for HA) |
| `redis.addr` | `localhost:6379` | Redis address |
