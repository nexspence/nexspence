# Nexspence — Deployment Guide

## Quick Start (Docker Compose)

```bash
git clone https://github.com/nexspence-oss/nexspence
cd nexspence
docker compose up --build
```

Services:
- **Nexspence**: http://localhost:8081 (admin / admin123)
- **PostgreSQL**: localhost:5432

The backend automatically runs database migrations on startup — no manual step needed.

---

## Prerequisites

| Dependency | Version  | Notes |
|------------|----------|-------|
| Go         | 1.22+    | Backend build |
| Node.js    | 20+      | Frontend build |
| PostgreSQL | 16+      | Primary datastore |
| Docker     | 24+      | Container builds (optional) |

---

## Build from Source

```bash
# 1. Clone
git clone https://github.com/nexspence-oss/nexspence && cd nexspence

# 2. Build frontend
cd frontend && npm install && npm run build && cd ..

# 3. Build backend binary
go build -o nexspence ./cmd/server

# 4. Run
./nexspence serve
```

The binary serves both the REST API and the frontend SPA from `./frontend/dist`.

---

## Configuration

### config.yaml (default location: `./config.yaml`)

```yaml
http:
  listen: ":8081"
  base_url: "https://nexspence.example.com"  # External URL for download links

database:
  dsn: "postgres://nexspence:nexspence@localhost:5432/nexspence?sslmode=disable"

storage:
  local:
    base_path: "/var/nexspence/blobs"

auth:
  jwt_secret: "change-me-to-a-random-64-char-string"
  jwt_expiry_hours: 8
  bcrypt_cost: 12

bootstrap:
  admin_username: "admin"
  admin_password: "admin123"   # Change on first deploy!
  admin_email:    "admin@example.com"
  admin_first_name: "Admin"

log:
  level: "info"    # debug | info | warn | error
  format: "json"   # json | text
```

### Environment overrides

Every config key is overridable via `NEXSPENCE_` prefixed env vars using `_` as separator:

```bash
NEXSPENCE_DATABASE_DSN="postgres://..."
NEXSPENCE_AUTH_JWT_SECRET="my-secret"
NEXSPENCE_BOOTSTRAP_ADMIN_PASSWORD="newpass"
NEXSPENCE_HTTP_LISTEN=":9090"
NEXSPENCE_LOG_LEVEL="debug"
```

---

## Docker

### Single container

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY . .
RUN go build -o /nexspence ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /nexspence /usr/local/bin/nexspence
COPY config.yaml /etc/nexspence/config.yaml
EXPOSE 8081
ENTRYPOINT ["nexspence", "serve"]
```

```bash
docker build -t nexspence:latest .
docker run -d \
  -p 8081:8081 \
  -e NEXSPENCE_DATABASE_DSN="postgres://nexspence:nexspence@db:5432/nexspence?sslmode=disable" \
  -e NEXSPENCE_AUTH_JWT_SECRET="$(openssl rand -hex 32)" \
  -v /data/nexspence/blobs:/var/nexspence/blobs \
  nexspence:latest
```

### Docker Compose (production)

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: nexspence
      POSTGRES_PASSWORD: nexspence
      POSTGRES_DB: nexspence
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U nexspence"]
      interval: 5s
      retries: 10

  nexspence:
    image: nexspence:latest
    ports:
      - "8081:8081"
    environment:
      NEXSPENCE_DATABASE_DSN: "postgres://nexspence:nexspence@postgres:5432/nexspence?sslmode=disable"
      NEXSPENCE_AUTH_JWT_SECRET: "${JWT_SECRET}"
      NEXSPENCE_BOOTSTRAP_ADMIN_PASSWORD: "${ADMIN_PASSWORD}"
      NEXSPENCE_HTTP_BASE_URL: "https://nexspence.example.com"
      NEXSPENCE_STORAGE_LOCAL_BASE_PATH: "/blobs"
    volumes:
      - blobdata:/blobs
    depends_on:
      postgres:
        condition: service_healthy

volumes:
  pgdata:
  blobdata:
```

---

## Kubernetes

### ConfigMap + Secret

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: nexspence-config
data:
  NEXSPENCE_HTTP_LISTEN: ":8081"
  NEXSPENCE_HTTP_BASE_URL: "https://nexspence.example.com"
  NEXSPENCE_LOG_LEVEL: "info"
  NEXSPENCE_LOG_FORMAT: "json"
  NEXSPENCE_STORAGE_LOCAL_BASE_PATH: "/blobs"
---
apiVersion: v1
kind: Secret
metadata:
  name: nexspence-secrets
stringData:
  NEXSPENCE_DATABASE_DSN: "postgres://nexspence:nexspence@postgres-svc:5432/nexspence?sslmode=disable"
  NEXSPENCE_AUTH_JWT_SECRET: "replace-with-64-char-random-string"
  NEXSPENCE_BOOTSTRAP_ADMIN_PASSWORD: "admin123"
```

### Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nexspence
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nexspence
  template:
    metadata:
      labels:
        app: nexspence
    spec:
      containers:
        - name: nexspence
          image: nexspence:latest
          ports:
            - containerPort: 8081
          envFrom:
            - configMapRef:
                name: nexspence-config
            - secretRef:
                name: nexspence-secrets
          volumeMounts:
            - name: blobs
              mountPath: /blobs
          livenessProbe:
            httpGet:
              path: /service/rest/v1/status/check
              port: 8081
            initialDelaySeconds: 10
            periodSeconds: 30
          readinessProbe:
            httpGet:
              path: /service/rest/v1/status/check
              port: 8081
            initialDelaySeconds: 5
            periodSeconds: 10
      volumes:
        - name: blobs
          persistentVolumeClaim:
            claimName: nexspence-blobs-pvc
---
apiVersion: v1
kind: Service
metadata:
  name: nexspence-svc
spec:
  selector:
    app: nexspence
  ports:
    - port: 80
      targetPort: 8081
```

### PersistentVolumeClaim

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: nexspence-blobs-pvc
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 100Gi
```

> **Note**: For multi-replica deployments, use S3-compatible storage (MinIO, AWS S3) instead of local filesystem. S3 adapter is on the roadmap.

---

## Database

### PostgreSQL setup

```sql
CREATE USER nexspence WITH PASSWORD 'nexspence';
CREATE DATABASE nexspence OWNER nexspence;
```

Migrations run automatically on `nexspence serve`. To run manually:

```bash
nexspence migrate --dsn "postgres://nexspence:nexspence@localhost:5432/nexspence"
```

### Backup

```bash
# Full backup
pg_dump -U nexspence -d nexspence -F custom -f nexspence_$(date +%Y%m%d).dump

# Restore
pg_restore -U nexspence -d nexspence nexspence_20260417.dump
```

---

## Reverse Proxy (nginx)

```nginx
server {
    listen 443 ssl http2;
    server_name nexspence.example.com;

    ssl_certificate     /etc/ssl/nexspence.crt;
    ssl_certificate_key /etc/ssl/nexspence.key;

    client_max_body_size 10g;   # Large artifact uploads

    location / {
        proxy_pass         http://localhost:8081;
        proxy_set_header   Host $host;
        proxy_set_header   X-Real-IP $remote_addr;
        proxy_set_header   X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```

---

## Monitoring

### Health check

```
GET /service/rest/v1/status/check   # no auth — for load balancers
GET /service/rest/v1/status         # auth required — full status
```

### Metrics

```
GET /api/v1/metrics   # no auth — JSON snapshot
```

Example response:
```json
{
  "uptime_seconds": 3600,
  "requests_total": 12450,
  "request_errors": 3,
  "artifacts_stored": 892,
  "bytes_stored": 4831838208,
  "goroutines": 14,
  "memory": {
    "alloc_bytes": 8388608,
    "total_alloc_bytes": 134217728,
    "sys_bytes": 67108864,
    "gc_cycles": 42
  }
}
```

---

## First Login

1. Open `http://localhost:8081` (or your configured URL)
2. Login: **admin** / **admin123** (or your bootstrap password)
3. Change the admin password immediately: Settings → Security → Users → admin → Change Password

---

## Upgrade

1. Pull the new image or build from source
2. Stop the running instance
3. Start the new instance — migrations run automatically

Migrations are idempotent and backward-compatible within a minor version.

---

## Security Recommendations

| Setting | Recommendation |
|---------|----------------|
| `jwt_secret` | 64+ random characters, stored in a secret manager |
| `bcrypt_cost` | 12 (default) — increase to 14 on high-end hardware |
| Admin password | Change immediately after first deploy |
| TLS | Always use HTTPS in production (via reverse proxy) |
| DB password | Use a strong password; restrict DB user to `nexspence` database only |
| Blob storage | Restrict filesystem/S3 bucket access to the nexspence process |
