# nexspence

Nexspence — open-source universal artifact repository manager (Nexus OSS alternative).

## TL;DR

```bash
helm repo add nexspence https://nexspence-oss.github.io/charts
helm install my-nexspence nexspence/nexspence -f your-values.yaml
```

## Prerequisites

- Kubernetes >= 1.26
- Helm >= 3.x
- PersistentVolume provisioner (for local blob storage) or S3-compatible storage

## Installing from local source

```bash
cd deploy/helm/nexspence
helm dependency update
helm install my-nexspence . -f values-examples/nginx.yaml
```

## Networking modes

Exactly **one** networking mode should be enabled at a time:

| Mode | values key | Requires |
|------|-----------|---------|
| nginx Ingress | `ingress.enabled=true`, `ingress.className=nginx` | nginx ingress-controller |
| Traefik Ingress | `ingress.enabled=true`, `ingress.className=traefik` | Traefik |
| Cilium Ingress | `ingress.enabled=true`, `ingress.className=cilium` | Cilium ingress enabled |
| Istio Gateway | `gateway.istio.enabled=true` | Istio + istiod |
| Cilium Gateway API | `gateway.cilium.enabled=true` | Cilium + Gateway API CRDs |

See `values-examples/` for ready-to-use per-provider files.

## Storage

| Mode | Config |
|------|-------|
| Local PVC | `storage.type=local` (default) |
| S3/MinIO | `storage.type=s3` + fill `storage.s3.*` |

> For multi-replica deployments, use S3 storage — a single PVC with `ReadWriteOnce` does not scale horizontally.

## PostgreSQL

By default the bitnami/postgresql sub-chart is deployed. For production, disable it and provide an external DSN:

```bash
helm install my-nexspence . \
  --set postgresql.enabled=false \
  --set externalDatabase.dsn="postgres://user:pass@pg-host:5432/nexspence"
```

## Upgrading

```bash
helm upgrade my-nexspence . -f your-values.yaml
```

Database migrations run automatically on pod start.

## Values reference

See `values.yaml` — every key is annotated inline.
