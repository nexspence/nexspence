### ✨ Features

* **Helm chart** — production-ready Helm chart under `deploy/helm/nexspence/` with pluggable ingress (nginx, Traefik, Cilium ingress-controller) and API Gateway support (Istio Gateway + VirtualService, Cilium K8s Gateway API with HTTPRoute). Includes bitnami/postgresql sub-chart, HPA, PVC for blob storage, S3 mode, and 5 ready-to-use example values files.
* **Landing page** — static marketing page at `landing/` with Holo dark design, app UI mockup, 14 format showcase, Demo video placeholder, Docker Compose + Helm quick start tabs, releases link. Deploy with `docker compose --profile landing up -d landing` on port 8080.
* **In-app documentation page** — `/docs` route accessible to all authenticated users; "Documentation" button in the sidebar footer above user info. Two-column layout: left nav with all sections + right scrollable content. Includes Getting Started (auth methods, API token usage, base URL), and full reference pages for all 14 repository formats (Maven, npm, PyPI, Docker, Go, NuGet, Raw, Helm, Cargo, Apt, Yum, Conan, Conda, Terraform) — each with repository URL, authentication config, publish examples, and download/install curl commands. All code blocks have copy-to-clipboard. Base URL is detected dynamically from `window.location.origin` so examples show the correct deployment URL automatically.
* **In-app how-to guides** — seven step-by-step guide articles in the Docs page (Guides section): Creating Repositories (Hosted/Proxy/Group wizard walkthrough), Managing Users, Roles & Privileges (RBAC model: Content Selector → Privilege → Role → User), Content Selectors (CEL expression reference with examples), Security Scanning (OSV vulnerability dashboard + bulk scan), Cleanup Policies (criteria, cron schedule, attach to repo, Run Now), and API Tokens (create, copy-once, Basic Auth, Bearer usage, revoke). Each step includes a screenshot placeholder — add a PNG to `frontend/public/docs/screenshots/<name>.png` to replace the dashed placeholder automatically.
* **Format nav brand icons** — all 14 format buttons in the Docs page left nav now use real brand icons via Simple Icons CDN (`cdn.simpleicons.org`) with emoji fallback when the CDN is unreachable.

### 🐛 Bug Fixes

* **Go modules docs** — corrected environment variable from `GONOSUMCHECK` (invalid) to `GONOSUMDB` in both the in-app docs and README.

### 🔒 Security

* **Directory listing blocked** — `http.FileServer` previously exposed directory contents at any path that mapped to a directory in `frontend/dist/` (e.g. `/docs/screenshots/`). The static file handler now returns the SPA root for any directory request that does not contain an `index.html`, preventing enumeration of static asset paths.
