### ✨ Features

* **Helm chart** — production-ready Helm chart under `deploy/helm/nexspence/` with pluggable ingress (nginx, Traefik, Cilium ingress-controller) and API Gateway support (Istio Gateway + VirtualService, Cilium K8s Gateway API with HTTPRoute). Includes bitnami/postgresql sub-chart, HPA, PVC for blob storage, S3 mode, and 5 ready-to-use example values files.
* **Landing page** — static marketing page at `landing/` with Holo dark design, app UI mockup, 14 format showcase, Demo video placeholder, Docker Compose + Helm quick start tabs, releases link. Deploy with `docker compose --profile landing up -d landing` on port 8080.

### 🐛 Bug Fixes

_No bug fixes in this release._
