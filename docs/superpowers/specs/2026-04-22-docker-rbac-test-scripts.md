# Docker RBAC Test Scripts — Design Spec

## Goal

Two standalone bash scripts that verify all Docker repository operations across three user accounts and three repository types (hosted, proxy, group), confirming both allowed and denied access matches expected RBAC policy.

---

## Scripts

| File | Tool | Purpose |
|------|------|---------|
| `scripts/test-docker-cli.sh` | `docker` CLI + `curl` for tags/list | End-to-end client behaviour |
| `scripts/test-docker-curl.sh` | `curl` only | Low-level OCI Distribution API |

---

## Configuration (variables at top of each file)

```bash
REGISTRY="localhost:8081"

# Repositories
REPO_HOSTED="docker"       # hosted, RBAC-scoped
REPO_PROXY="dockerproxy"   # proxy → Docker Hub
REPO_GROUP="dockergroup"   # group = [docker, dockerproxy]

# Users
ADMIN_USER="admin"        ; ADMIN_PASS="admin123"
USER_B="dev-team"          ; USER_B_PASS="pass123"   # scope: da/dev/*
USER_C="prod-team"          ; USER_C_PASS="pass123"   # scope: da/prod/*

# Scopes
SCOPE_B="da/dev"
SCOPE_C="da/prod"

# Test image (small, 3 tags)
IMAGE_BASE="alpine"
TAGS=("v1.0" "v2.0" "latest")
```

---

## Repository Behaviour Matrix

| Operation | Hosted | Proxy | Group |
|-----------|--------|-------|-------|
| pull | ✓ RBAC | ✓ (cache miss→upstream) | ✓ (fan-out) |
| push | ✓ RBAC | ✗ 405 | ✗ 405 |
| tags/list | ✓ RBAC | ✓ | ✓ (union) |
| manifest GET/HEAD | ✓ RBAC | ✓ | ✓ |
| blob HEAD/GET | ✓ RBAC | ✓ | ✓ |
| DELETE manifest | ✓ RBAC | ✗ 405 | ✗ 405 |

---

## RBAC Test Matrix (Hosted only)

| Operation | Admin | User B → dev | User B → prod | User C → prod | User C → dev |
|-----------|-------|-------------|-------------|-------------|-------------|
| push | ✅ 201 | ✅ 201 | ❌ 401/403 | ✅ 201 | ❌ 401/403 |
| pull | ✅ | ✅ | ❌ | ✅ | ❌ |
| tags/list | ✅ | ✅ | ❌ | ✅ | ❌ |
| manifest GET | ✅ | ✅ | ❌ | ✅ | ❌ |
| blob HEAD | ✅ | ✅ | ❌ | ✅ | ❌ |
| DELETE | ✅ | ✅ | ❌ | ✅ | ❌ |

---

## Output Format

```
[INFO]  Testing admin: push hosted da/dev/alpine:v1.0
[PASS]  admin: push da/dev/alpine:v1.0 → 201
[PASS]  user-b: push da/dev/alpine:v2.0 → 201 (allowed)
[DENY]  user-b: push da/prod/alpine:v2.0 → 403 (expected denial ✓)
[FAIL]  user-c: push da/dev/alpine:v1.0 → 201 (expected 403!)

────────────────────────────────
 Results: 22 passed, 1 failed
────────────────────────────────
```

- `[PASS]` green — got expected status code
- `[DENY]` yellow — got expected denial (403/401/405) — counts as pass
- `[FAIL]` red — unexpected result
- Summary line at end with exit code 1 if any `[FAIL]`

---

## CLI Script Test Plan (`test-docker-cli.sh`)

### Setup
1. `docker pull alpine:3` locally (source image)
2. Retag → `$REGISTRY/$REPO_HOSTED/$SCOPE_B/alpine:v1.0`, `v2.0`, `latest`
3. Retag → `$REGISTRY/$REPO_HOSTED/$SCOPE_C/alpine:v1.0`, `v2.0`, `latest`

### Hosted — Admin
4. `docker login` as admin
5. Push all 3 tags to `da/dev/alpine` → expect success each
6. Push all 3 tags to `da/prod/alpine` → expect success each
7. `curl GET /v2/docker/da/dev/alpine/tags/list` → expect `["v1.0","v2.0","latest"]`
8. `docker manifest inspect` `da/dev/alpine:v1.0` → expect JSON manifest
9. `docker pull` `da/dev/alpine:v1.0` from registry → expect success

### Hosted — User B (da/dev allowed, da/prod denied)
10. `docker login` as user-b
11. Push `da/dev/alpine:v2.0` → expect success
12. Push `da/prod/alpine:v2.0` → expect FAIL (docker reports error)
13. Pull `da/dev/alpine:v1.0` → expect success
14. Pull `da/prod/alpine:v1.0` → expect FAIL

### Hosted — User C (da/prod allowed, da/dev denied)
15. `docker login` as user-c
16. Push `da/prod/alpine:v2.0` → expect success
17. Push `da/dev/alpine:v2.0` → expect FAIL
18. Pull `da/prod/alpine:v1.0` → expect success
19. Pull `da/dev/alpine:v1.0` → expect FAIL

### Proxy — Admin
20. `docker login` as admin
21. Pull `$REGISTRY/$REPO_PROXY/library/alpine:latest` → expect success (cache miss → upstream)
22. Pull again → expect success (cache hit)
23. Push to proxy → expect FAIL (405 from server)

### Proxy — User B / User C
24. Pull `$REGISTRY/$REPO_PROXY/library/alpine:latest` as user-b → expect FAIL 401/403
    (content selector `path.startsWith("/v2/da/dev/")` does not match `/v2/library/alpine/…`)
25. Same for user-c → expect FAIL 401/403

### Group — Admin
25. Pull image that exists in hosted member (`da/dev/alpine:v1.0`) via group → expect success
26. Pull image from proxy member (`library/alpine:latest`) via group → expect success
27. Push to group → expect FAIL (405)

### Cleanup
28. `docker login` as admin → `docker manifest rm` / API delete all test tags

---

## Curl Script Test Plan (`test-docker-curl.sh`)

The curl script requires no running Docker daemon. It crafts a minimal synthetic OCI image:
- **Config blob**: `{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}` (~74 bytes)
- **Layer blob**: empty gzipped tar (handful of bytes)
- **Manifest**: OCI v2 JSON referencing config+layer digests

### Auth & Version Check
1. `GET /v2/` unauthenticated → expect `401`
2. `GET /v2/` as admin → expect `200` + `Docker-Distribution-API-Version: registry/2.0`

### Hosted — Admin full push cycle (da/dev/alpine)
3. `POST /v2/docker/da/dev/alpine/blobs/uploads/` → 202, get upload UUID
4. `PATCH uploads/<uuid>` with config blob data → 202
5. `PUT uploads/<uuid>?digest=sha256:<config>` → 201
6. Repeat steps 3-5 for layer blob
7. `PUT /v2/docker/da/dev/alpine/manifests/v1.0` with manifest JSON → 201, get `Docker-Content-Digest`
8. Repeat for tags `v2.0`, `latest`
9. Repeat full push for `da/prod/alpine`

### Hosted — Read operations (Admin)
10. `GET /v2/docker/da/dev/alpine/tags/list` → 200, body contains tags array
11. `GET /v2/docker/da/dev/alpine/manifests/v1.0` → 200 + `Docker-Content-Digest`
12. `GET /v2/docker/da/dev/alpine/manifests/<sha256-digest>` → 200 (pull by digest)
13. `HEAD /v2/docker/da/dev/alpine/manifests/v1.0` → 200, no body
14. `HEAD /v2/docker/da/dev/alpine/blobs/<config-digest>` → 200
15. `GET /v2/docker/da/dev/alpine/blobs/<config-digest>` → 200, body = config JSON

### Hosted — RBAC: User B (da/dev allowed)
16. All read ops on `da/dev/alpine` → 200
17. Full push cycle `da/dev/alpine:v2.0` → 201

### Hosted — RBAC: User B (da/prod denied)
18. `GET /v2/docker/da/prod/alpine/tags/list` → 401/403
19. `GET /v2/docker/da/prod/alpine/manifests/v1.0` → 401/403
20. Push initiation `POST .../da/prod/alpine/blobs/uploads/` → 401/403

### Hosted — RBAC: User C (da/prod allowed, da/dev denied)
21-26. Mirror of User B tests with scopes swapped

### Hosted — Delete
27. `DELETE /v2/docker/da/dev/alpine/manifests/<digest>` as admin → 202
28. `DELETE /v2/docker/da/dev/alpine/manifests/<digest>` as user-b on da/prod → 401/403
29. `GET` deleted manifest → 404

### Proxy
30. `GET /v2/dockerproxy/library/alpine/manifests/latest` as admin → 200 (upstream fetch)
31. `HEAD /v2/dockerproxy/library/alpine/blobs/<digest>` → 200
32. `POST .../blobs/uploads/` to proxy → 405
33. `DELETE .../manifests/latest` on proxy → 405

### Group
34. `GET /v2/dockergroup/da/dev/alpine/manifests/v1.0` → 200 (from hosted member)
35. `GET /v2/dockergroup/library/alpine/manifests/latest` → 200 (from proxy member)
36. `GET /v2/dockergroup/da/dev/alpine/tags/list` → 200, union of tags
37. `POST .../blobs/uploads/` to group → 405

---

## Exit Codes

- `0` — all tests passed
- `1` — one or more unexpected results

---

## Prerequisites

Both scripts check at startup:
- CLI script: `docker` binary in PATH, `curl` available, `$REGISTRY` reachable
- Curl script: `curl`, `openssl` (for sha256 computation), `$REGISTRY` reachable

---

## Files to Create

```
scripts/
  test-docker-cli.sh    (chmod +x)
  test-docker-curl.sh   (chmod +x)
```
