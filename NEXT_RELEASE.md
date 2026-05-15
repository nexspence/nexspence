### ✨ Features

* **Staging & Build Promotion** — controlled artifact promotion workflow between repositories (e.g. `staging-maven → prod-maven`). Admins define promotion rules with optional CEL path filters, scan pass requirements, and manual approval gates. Users promote individual components from the Browse detail panel or bulk-select components via checkboxes. Auto-approved promotions execute immediately (blob copy + metadata re-registration in target repo); manual-approval requests queue in the Admin → Promotion tab for an admin to approve or reject with a note. Webhook events `promotion.requested`, `promotion.approved`, `promotion.rejected`, `promotion.done` are dispatched for each state transition. API: `GET/POST/PUT/DELETE /api/v1/promotion/rules`, `POST /api/v1/promotion/promote`, `GET /api/v1/promotion/requests`, `POST /api/v1/promotion/requests/:id/approve|reject`, `GET /api/v1/components/:id/promotion-rules`.

### 🐛 Bug Fixes

_No bug fixes in this release._
