# Design: Group Blob Stores (Phase 48)

**Date:** 2026-05-02  
**Status:** Approved

## Goal

Allow combining multiple blob stores into a logical group with a fill policy (`round_robin` or `write_to_first_fill`). Repositories assigned to a group store distribute writes across members automatically. Reads and deletes require no changes — `assets.blob_store_id` always records the physical member ID.

## Non-Goals

- Nested groups (a group cannot be a member of another group)
- Rebalancing existing assets between members
- Changing `assets.blob_store_id` to point to a group after the fact

---

## Database

**Migration `014_blob_store_group.sql`** — extends the existing CHECK constraint only:

```sql
ALTER TABLE blob_stores DROP CONSTRAINT IF EXISTS blob_stores_type_check;
ALTER TABLE blob_stores ADD CONSTRAINT blob_stores_type_check
  CHECK (type IN ('local', 's3', 'group'));
```

No new tables. Group-specific config lives in the existing `config jsonb` column:

```json
{
  "fill_policy": "round_robin",
  "member_ids": ["uuid-a", "uuid-b", "uuid-c"]
}
```

`domain.BlobStore` is unchanged. A group appears as a normal blob store with `Type = "group"`.

---

## Fill Policy Logic

### Storage layer — `internal/storage/registry.go`

Add a `sync.Map` of round-robin counters keyed by group ID:

```go
type Registry struct {
    mu           sync.RWMutex
    instances    map[string]BlobStore
    defaultStore BlobStore
    rrCounters   sync.Map // groupID → *atomic.Uint64
}
```

New method: `PickMember(groupID string, policy string, members []domain.BlobStore) string`

Receives already-resolved `[]domain.BlobStore` (with `UsedBytes` and `QuotaBytes` populated from DB) so both policies can work without additional DB calls.

- **`round_robin`**: `counter % len(members)`, atomically incremented. In-memory only — resets on restart (acceptable; purpose is load distribution, not strict ordering).
- **`write_to_first_fill`**: iterate members in order, return first whose `used_bytes < quota_bytes` (nil quota = unlimited). Falls back to next member when current is full.

### `internal/formats/base/store.go` — `resolveBlobStoreRef`

Extended to handle group stores:

```
if bs.Type == "group":
    memberIDs = bs.Config["member_ids"].([]string)
    memberID  = registry.PickMember(bs.ID, members)  // members = fetched []domain.BlobStore
    memberBS  = d.Blobs.GetByID(ctx, memberID)
    return memberBS.ID, memberBS.Name  // physical member → assets.blob_store_id
```

The asset's `blob_store_id` is written with the **member's** ID, never the group's ID. This means GET and DELETE in `FetchArtifact` / `DeleteArtifact` require zero changes.

**Double-call fix:** `resolveBlobStoreRef` is currently called twice in `StoreArtifact` (once for `physStore`, once inside `RegisterStoredBlob`). For group stores with round-robin this would advance the counter twice and pick different members. Fix: `StoreArtifact` resolves once, passes the result `(blobStoreID, blobStoreName string)` as explicit params to `RegisterStoredBlob`. `RegisterStoredBlob` signature gains these two params; callers in `docker/handler.go` and `repoproxy/repoproxy.go` pass empty strings (falls back to internal resolution, which for non-group stores is correct).

### Quota enforcement

`checkQuota` for a group sums `QuotaBytes` across all members (nil = unlimited per member). If all members are at capacity: return `ErrQuotaExceeded` → HTTP 507. No fallback writing.

---

## Validation (BlobStoreHandler.Create / Update)

When `type == "group"`:

1. `fill_policy` must be `"round_robin"` or `"write_to_first_fill"`
2. `member_ids` must be non-empty
3. Every member ID must exist in `blob_stores`
4. No member may itself be of type `"group"` (no nesting)
5. On delete: reject if any `blob_stores.id` in `member_ids` of another group references this store (prevent orphaned groups)

---

## API

No new routes. The existing router already dispatches `POST /service/rest/v1/blobstores/:type`.

| Method | Path | Notes |
|--------|------|-------|
| `POST` | `/service/rest/v1/blobstores/group` | Create group store |
| `GET` | `/service/rest/v1/blobstores` | Groups appear in list as `type="group"` |
| `GET` | `/service/rest/v1/blobstores/:name` | Returns group config with member_ids |
| `PUT` | `/service/rest/v1/blobstores/group/:name` | Update fill_policy or member list |
| `DELETE` | `/service/rest/v1/blobstores/:name` | Rejected if referenced by a repository |
| `GET` | `/api/v1/blob-stores/:name/usage` | Aggregates across all members |

**Create request body:**

```json
{
  "name": "fast-group",
  "config": {
    "fill_policy": "round_robin",
    "member_ids": ["uuid-a", "uuid-b"]
  }
}
```

**Usage response for a group** — `linkedRepositories` and `totalAssetBytes` aggregate across all members; response also includes per-member usage breakdown.

---

## Frontend (AdminPage → Blob Stores tab)

- **Create modal**: "Group" option in the type dropdown alongside Local / S3
- **Group form fields**: `fill_policy` radio (Round Robin / Write to First Fill) + multiselect of existing non-group stores as members
- **List badge**: `GROUP` badge on group rows (same style as existing `S3` badge)
- **Detail modal**: shows member list with individual `usedBytes` / `quotaBytes`, plus group-level totals

---

## Documentation (`docs/blob-store-groups.md`)

Covers:
- What group blob stores are and when to use them (horizontal storage scaling, separating hot/cold data)
- Fill policy comparison: `round_robin` (even distribution, ignores quota) vs `write_to_first_fill` (fills first member before spilling to next)
- Create via UI walkthrough
- Create via API (curl example)
- Constraints: no nesting; members cannot be deleted while in a group; member `used_bytes` is tracked individually, group shows aggregate
- Quota and 507 behavior

---

## Files Changed

| File | Change |
|------|--------|
| `internal/db/migrations/014_blob_store_group.sql` | New — extend type CHECK constraint |
| `internal/storage/registry.go` | Add `rrCounters sync.Map`, `PickMember` method |
| `internal/formats/base/store.go` | `resolveBlobStoreRef` handles `type="group"` |
| `internal/formats/base/store.go` | `checkQuota` sums member quotas for groups |
| `internal/api/handlers/blobstores.go` | Group validation in Create/Update |
| `frontend/src/pages/AdminPage.tsx` | Group type in create modal, GROUP badge, detail view |
| `docs/blob-store-groups.md` | New — user-facing guide |
