# Group Blob Stores

A **group blob store** combines two or more physical blob stores (local or S3) into a single logical store. Repositories assigned to a group store distribute writes automatically across members according to a **fill policy**. Reads and deletes are unaffected — every artifact tracks exactly which physical store holds it.

## When to use

| Use case | Recommended policy |
|----------|-------------------|
| Horizontal scaling — spread writes evenly | `round_robin` |
| Tiered storage — fill fast SSD first, overflow to HDD | `write_to_first_fill` |
| Capacity extension without downtime | `write_to_first_fill` |

## Fill policies

### `round_robin`
Distributes writes evenly across members in a rotating cycle. Member order is the order in `member_ids`. The counter resets on server restart (acceptable — purpose is load distribution, not strict ordering). Quota per member is not checked — use `write_to_first_fill` if you need quota-aware routing.

### `write_to_first_fill`
Writes to the **first** member until its quota is exhausted, then moves to the second, and so on. A member with no quota set is treated as unlimited. If all members are at capacity, uploads return **507 Insufficient Storage**.

## Create via UI

1. Open **Admin → Blob Stores**.
2. Click **Create**, select type **Group**.
3. Choose a fill policy (Round Robin / Write to First Fill).
4. Check one or more non-group stores as members.
5. Click **Save**.

## Create via API

```bash
curl -u admin:admin123 -X POST http://localhost:8081/service/rest/v1/blobstores/group \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "fast-group",
    "config": {
      "fill_policy": "round_robin",
      "member_ids": ["<uuid-of-store-a>", "<uuid-of-store-b>"]
    }
  }'
```

Assign the group to a repository just like any other blob store — use the group name in the repository create/update payload.

## Constraints

- **No nesting** — a group cannot be a member of another group.
- **Member deletion blocked** — you cannot delete a blob store that is currently a member of a group. Remove it from the group first.
- **`used_bytes` is per-member** — the group's `usedBytes` field in the list API is 0; use the `/api/v1/blob-stores/:name/usage` endpoint to see aggregate and per-member usage.
- **Migration** — existing artifacts in member stores are not rebalanced when a group is created or when members are added/removed.

## Quota and 507 behaviour

`write_to_first_fill`: if the currently active member's quota is full, the next member is tried. If **all** members are at capacity, the upload is rejected with HTTP **507 Insufficient Storage**.

`round_robin`: quota is not checked during member selection. Individual member quotas are still enforced by the underlying store — an upload may fail with 507 if the selected member happens to be full.
