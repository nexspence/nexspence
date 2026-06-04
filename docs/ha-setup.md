# High Availability Setup

Nexspence supports multi-node clustered deployments with shared PostgreSQL, Redis,
and S3-compatible blob storage. Each node is stateless at the application layer —
all shared state lives in the database, Redis, and object storage.

## Requirements

| Component | Minimum | Notes |
|---|---|---|
| PostgreSQL | 14+ | Shared across all nodes |
| Redis | 7+ | Single-node sufficient for small clusters; use Sentinel or Cluster for production HA |
| S3 blob store | Any S3-compatible | MinIO, AWS S3, Cloudflare R2, etc. |
| Load balancer | Any | nginx, Traefik, k8s Ingress, AWS ALB |

## Quick Start (Docker Compose)

Download `docker-compose.ha.yml` from the latest release:
**[github.com/nexspence/nexspence/releases](https://github.com/nexspence/nexspence/releases)**

```bash
# Start 2-node HA cluster
docker compose -f docker-compose.ha.yml up -d
```

Web UI: http://localhost:8080 (nginx load balances between both nodes)

## Configuration

Enable Redis in `config.yaml` or via env vars:

```yaml
redis:
  enabled: true
  addr: "redis:6379"
  password: ""
  db: 0
```

| Env var | Default | Description |
|---|---|---|
| `NEXSPENCE_REDIS_ENABLED` | `false` | Enable Redis for HA |
| `NEXSPENCE_REDIS_ADDR` | `localhost:6379` | Redis address |
| `NEXSPENCE_REDIS_PASSWORD` | `""` | Redis password |
| `NEXSPENCE_REDIS_DB` | `0` | Redis DB index |

## Health Probes

| Endpoint | Purpose | Success |
|---|---|---|
| `GET /healthz` | Liveness — process alive | Always `200 {"status":"ok"}` |
| `GET /readyz` | Readiness — ready for traffic | `200` when DB + Redis reachable, `503` otherwise |

### Kubernetes example

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8081
  initialDelaySeconds: 10
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /readyz
    port: 8081
  initialDelaySeconds: 5
  periodSeconds: 5
```

## What Redis Is Used For

| Feature | Redis key | TTL |
|---|---|---|
| Docker anon-check cache | `nexspence:docker:anon_allowed` | 30s |
| Cleanup lock | `nexspence:lock:cleanup:run` | 30 min |
| Blob migration lock | `nexspence:lock:blobmig:<repo>` | 2 hours |

## Production Redis

For production, use Redis Sentinel (for automatic failover) or Redis Cluster
(for sharding). Update `redis.addr` to point at the Sentinel/Cluster endpoint.
The `go-redis` client handles both transparently via the address format.

## Scaling

All Nexspence nodes are identical — run as many replicas as needed:

```bash
docker compose -f docker-compose.ha.yml up --scale nexspence_1=3
```

Or in Kubernetes, set `spec.replicas` on the Deployment.
