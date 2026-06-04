# Nexspence Helm Chart

Nexspence — open-source universal artifact repository manager (Nexus OSS alternative).

## Requirements

- Helm 3.x
- Kubernetes >= 1.26
- PersistentVolume provisioner (for local blob storage) or S3-compatible storage

---

## Installation

Download `nexspence-vX.Y.Z.zip` from the latest release and extract it:
**[github.com/nexspence/nexspence/releases](https://github.com/nexspence/nexspence/releases)**

The Helm chart is at `deploy/helm/nexspence/` inside the extracted directory.

```bash
# Fetch dependencies (bitnami/postgresql sub-chart)
cd deploy/helm/nexspence
helm dependency update
```

Then install with exactly one of the networking options below.

### nginx ingress-controller

```bash
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/nginx.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

### Traefik (HTTPS via websecure entrypoint)

```bash
# TLS secret: nexspence-tls
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/traefik.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

### Cilium ingress-controller (>= 1.12)

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

### Cilium K8s Gateway API (>= 1.14)

```bash
# Requires: Cilium >= 1.14, Gateway API CRDs installed
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/cilium-gateway.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

---

## External PostgreSQL

Disable the bundled bitnami sub-chart and provide your own DSN:

```bash
helm install nexspence \
  deploy/helm/nexspence \
  --set postgresql.enabled=false \
  --set externalDatabase.dsn="postgres://user:pass@pg-host:5432/nexspence" \
  -f deploy/helm/nexspence/values-examples/nginx.yaml \
  --namespace nexspence \
  --create-namespace
```

---

## S3 / MinIO Blob Store

Set `config.storage.defaultType=s3` and provide bucket + endpoint. Use this
for any multi-replica deployment — a single `ReadWriteOnce` PVC does not scale
horizontally.

```bash
helm install nexspence \
  deploy/helm/nexspence \
  --set config.storage.defaultType=s3 \
  --set config.storage.s3.endpoint="https://minio.example.com" \
  --set config.storage.s3.bucket="nexspence-blobs" \
  --set config.storage.s3.accessKey="minio" \
  --set config.storage.s3.secretKey="minio123" \
  -f deploy/helm/nexspence/values-examples/nginx.yaml \
  --namespace nexspence \
  --create-namespace
```

---

## Scaling (HPA)

```bash
helm install nexspence \
  deploy/helm/nexspence \
  --set autoscaling.enabled=true \
  --set autoscaling.minReplicas=2 \
  --set autoscaling.maxReplicas=10 \
  -f deploy/helm/nexspence/values-examples/nginx.yaml \
  --namespace nexspence \
  --create-namespace
```

For multi-replica deployments, use S3 storage (see above).

---

## Upgrading

```bash
helm upgrade nexspence \
  deploy/helm/nexspence \
  -f your-values.yaml \
  --namespace nexspence
```

Database migrations run automatically on pod start — no manual step needed.

---

## Uninstall

```bash
helm uninstall nexspence -n nexspence
```

This removes all chart-managed resources. Persistent volumes are **not**
deleted by default. To also remove data:

```bash
kubectl delete pvc -l app.kubernetes.io/instance=nexspence -n nexspence
```

---

## Values Reference

See `values.yaml` — every key is annotated inline.
