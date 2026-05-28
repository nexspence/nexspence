# Staging & Build Promotion — Design Spec

**Date:** 2026-05-14  
**Phase:** 56  
**Status:** Approved

---

## Goal

Controlled artifact promotion workflow between repositories (e.g. `staging-maven → prod-maven`). Administrators define promotion rules with optional CEL path filters, scan requirements, and manual approval gates. Users promote individual or bulk-selected artifacts from the Browse UI.

---

## Data Model

### `promotion_rules`

Defines a promotion route from one repository to another.

```sql
id                    UUID PRIMARY KEY DEFAULT gen_random_uuid()
name                  TEXT NOT NULL
from_repo             TEXT NOT NULL REFERENCES repositories(name) ON UPDATE CASCADE ON DELETE CASCADE
to_repo               TEXT NOT NULL REFERENCES repositories(name) ON UPDATE CASCADE ON DELETE CASCADE
path_filter           TEXT        -- CEL expression; NULL = all paths
require_scan_pass     BOOL NOT NULL DEFAULT false
require_manual_approval BOOL NOT NULL DEFAULT false
created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
```

### `promotion_requests`

One row per component per promote action. Provides full audit trail for both automatic and manual promotions.

```sql
id            UUID PRIMARY KEY DEFAULT gen_random_uuid()
rule_id       UUID NOT NULL REFERENCES promotion_rules(id) ON DELETE CASCADE
component_id  UUID NOT NULL REFERENCES components(id) ON DELETE CASCADE
status        TEXT NOT NULL CHECK (status IN ('pending','approved','rejected','completed','failed'))
requested_by  UUID NOT NULL REFERENCES users(id)
reviewed_by   UUID        -- NULL while pending; set on approve/reject
reviewed_at   TIMESTAMPTZ
completed_at  TIMESTAMPTZ
error         TEXT        -- reason for rejection or failure message
created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
```

Bulk promotion = N independent `promotion_request` rows, each with its own status.

---

## Architecture: Approach C — Unified Request Queue

Every promotion (auto or manual) creates a `promotion_request`. For rules with `require_manual_approval=false` the service immediately executes the blob copy within the same request and marks status `completed`. For `require_manual_approval=true` the request stays `pending` until an `nx-admin` approves it.

This gives a uniform audit trail and a single queue for admins to review.

---

## Service Layer

### `PromotionService`

**`ListRulesForComponent(ctx, componentID) ([]PromotionRule, error)`**
- Loads the component to get its `repository` and `path`
- Queries all rules where `from_repo = component.repository`
- For each rule with a non-empty `path_filter`, evaluates the CEL expression against the component path (same pattern as `content_selector_service.go`)
- Returns matching rules only; empty list = no "Promote" button shown

**`Promote(ctx, componentIDs []string, ruleID string, userID string) ([]PromotionRequest, error)`**
For each component ID:
1. If `require_scan_pass=true`: checks OSV scan result on the component — rejects with error if not scanned or if any HIGH/CRITICAL finding exists
2. Creates a `promotion_request` with status `pending`
3. If `require_manual_approval=false`: immediately calls `executeCopy()` and sets status `completed` (or `failed` on error)
Returns all created/completed requests.

**`Approve(ctx, requestID, adminUserID string) error`**
- Verifies caller has role `nx-admin`
- Sets `reviewed_by`, `reviewed_at`
- Calls `executeCopy()` → sets `completed_at`, status `completed` (or `failed`)

**`Reject(ctx, requestID, adminUserID, reason string) error`**
- Verifies caller has role `nx-admin`
- Sets status `rejected`, `reviewed_by`, `reviewed_at`, `error = reason`

**`executeCopy(ctx, request PromotionRequest, rule PromotionRule) error`** *(internal)*
1. Load component + assets from source repo
2. For each asset: read blob from source blob store, write to `to_repo`'s blob store
3. Insert component row in `to_repo` (or upsert by name+version)
4. Insert asset rows pointing to new blob keys
5. Fire `artifact.published` webhook for `to_repo`

Reuses blob read/write patterns from `ReplicationService`.

---

## API Endpoints

### Promotion Rules (nx-admin only)
```
GET    /api/v1/promotion/rules
POST   /api/v1/promotion/rules
PUT    /api/v1/promotion/rules/:id
DELETE /api/v1/promotion/rules/:id
```

### Promote Action (any authenticated user)
```
POST   /api/v1/promotion/promote
Body:  { "rule_id": "...", "component_ids": ["...", "..."] }
Response: { "requests": [{ "id", "component_id", "status" }, ...] }
```

### Requests Queue
```
GET    /api/v1/promotion/requests?status=pending&rule_id=...
POST   /api/v1/promotion/requests/:id/approve   (nx-admin only)
POST   /api/v1/promotion/requests/:id/reject    (nx-admin only, body: { "reason": "..." })
```

### Component-level rule discovery (for Browse UI)
```
GET    /api/v1/components/:id/promotion-rules
Response: [{ "id", "name", "to_repo", "require_scan_pass", "require_manual_approval" }, ...]
```

---

## Frontend

### Browse — Single Component

In the component detail panel:
- On open, fetch `GET /api/v1/components/:id/promotion-rules`
- If list is non-empty: show "Promote" button
- Click → modal: rule selector (Select component), confirm button
- After submit: toast "Promoted to `<to_repo>`" (auto) or "Approval requested" (manual)

### Browse — Bulk

- Checkbox column added to component table rows
- When 1+ selected: toolbar appears with "Promote selected (N)" button
- Modal: rule selector filtered by current repository's `from_repo` rules (one request for all selected)
- Single `POST /api/v1/promotion/promote` with full `component_ids` array

### AdminPage — Promotion Tab

Two sections:

**Rules card:**
- Cards showing: rule name, `from_repo → to_repo`, CEL preview (truncated), scan/approval badge chips
- Create / Edit / Delete buttons
- Create/Edit modal fields: Name, From Repo (Select), To Repo (Select), Path Filter (textarea, CEL hint), Require Scan Pass (checkbox), Require Manual Approval (checkbox)

**Requests card:**
- Table with columns: Component, From → To, Requested By, Status, Created At, Actions
- Status filter (All / Pending / Approved / Rejected / Completed / Failed)
- Pending rows: inline Approve + Reject buttons; Reject opens small input for reason

---

## Error Cases

| Scenario | Behaviour |
|----------|-----------|
| Scan not run yet and `require_scan_pass=true` | `Promote` returns 422: "component has not been scanned" |
| Scan has HIGH/CRITICAL and `require_scan_pass=true` | `Promote` returns 422: "component has scan findings" |
| Component already exists in `to_repo` at same version | `executeCopy` upserts — no duplicate, silent |
| `to_repo` blob store unreachable | `executeCopy` fails, request status = `failed`, error message stored |
| Non-admin calls approve/reject | 403 Forbidden |
| Rule deleted while request pending | Request stays, approve/reject returns 404 on rule lookup |

---

## Testing

- Unit tests for `PromotionService`: mock repos + blob store; cover auto-approve path, manual-approve path, scan-fail path, CEL filter matching
- Handler tests: promote endpoint, approve/reject auth check (non-admin → 403)
- Migration test: tables created, constraints enforced
