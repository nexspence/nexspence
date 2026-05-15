### ✨ Features

* **Staging & Build Promotion** — controlled artifact promotion workflow between repositories (e.g. `staging-maven → prod-maven`). Admins define promotion rules with optional CEL path filters, scan pass requirements, and manual approval gates. Users promote individual components from the Browse detail panel or bulk-select components via checkboxes. Auto-approved promotions execute immediately (blob copy + metadata re-registration in target repo); manual-approval requests queue in the Admin → Promotion tab for an admin to approve or reject with a note. Webhook events `promotion.requested`, `promotion.approved`, `promotion.rejected`, `promotion.done` are dispatched for each state transition. API: `GET/POST/PUT/DELETE /api/v1/promotion/rules`, `POST /api/v1/promotion/promote`, `GET /api/v1/promotion/requests`, `POST /api/v1/promotion/requests/:id/approve|reject`, `GET /api/v1/components/:id/promotion-rules`.
* **nexspence.online website** — full product site added to `website/` with interactive architecture diagram, install guide (Docker Compose variants, Helm with 5 networking options), feature comparison vs Nexus OSS, and brand icons for all 14 formats. Served via nginx Docker profile: `docker compose --profile website up -d`.
* **AGPLv3 License** — `LICENSE` file added to the repository.

### 📚 Documentation

* **README rewritten** — shortened from 618 → 245 lines with links to dedicated docs instead of inline content.
* **`docs/deployment.md`** — complete deployment reference: Docker Compose (standard, MinIO, HA, Keycloak SSO), From Source, and full configuration reference table.
* **`deploy/helm/nexspence/README.md`** — full Helm reference: all 5 networking variants (nginx, Traefik, Cilium ingress, Istio Gateway, Cilium Gateway API), external PostgreSQL, S3 storage, HPA, upgrade and uninstall instructions.

### 🐛 Bug Fixes

_No bug fixes in this release._
