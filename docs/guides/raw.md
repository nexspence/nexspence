# Raw guide — arbitrary paths, uploads and cleanup

A raw repository is a file store: **any path, any bytes**. `PUT` stores a file
at a path, `GET` hands it back, `DELETE` removes it. There is no package
format, no index and no version — the path *is* the identity.

Raw is where cleanup policies and storage quotas become visible, because the
server has nothing else to reason about: a file is either there or it is not.

Assumes a fresh instance on `http://localhost:8081`.

## 1. Create the repositories

```bash
BASE_URL=http://localhost:8081 ./scripts/create-raw-repos.sh
```

That creates `raw-artifacts` (hosted), `raw-proxy` (in front of a plain HTTP
upstream) and `raw-common` (a group of the two).

By hand, with the v1-compatible API:

```bash
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# hosted — your own files
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/raw/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"raw-artifacts\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Build artifacts\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of an HTTP upstream
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/raw/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"raw-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Proxy to an upstream file server\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://files.example.com\" }
  }"

# group — one namespace for both
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/raw/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"raw-common\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Group of hosted and proxy\",
    \"formatConfig\": { \"member_names\": [\"raw-artifacts\", \"raw-proxy\"] }
  }"
```

## 2. Upload a file

```bash
curl -u admin:admin123 --upload-file app-1.0.0.tar.gz \
  http://localhost:8081/repository/raw-artifacts/releases/app-1.0.0.tar.gz
```

The path after the repository name is the path inside the repository. It may
have any number of segments, and the segments become folders in the browse
tree.

The answer is `201 Created` with an **empty body** — the server does not echo
the metadata back. Read it from the asset list instead:

```bash
curl -s -u admin:admin123 \
  "http://localhost:8081/service/rest/v1/assets?repository=raw-artifacts"
```

```json
{
  "items": [
    {
      "path": "/config/app.yml",
      "fileSize": 13,
      "contentType": "application/octet-stream",
      "sha256": "717bae503ae61089…",
      "downloadUrl": "http://localhost:8081/repository/raw-artifacts/config/app.yml",
      "createdAt": "2026-11-24T08:01:17.601262Z"
    }
  ]
}
```

`POST` works as well as `PUT`, and the path is normalized before it is stored:
`/a/../config/app.yml` is stored as `/config/app.yml`.

An anonymous upload is refused — a hosted repository with
`"allowAnonymous": false` answers `401`.

## 3. Download a file

```bash
curl -u admin:admin123 -O \
  http://localhost:8081/repository/raw-artifacts/releases/app-1.0.0.tar.gz
```

`-O` writes the file under its own name. The response carries the checksums
of the stored bytes:

```
X-Checksum-SHA256: 717bae503ae6108953042113e8bb6284b71a2c1c73665b7cc9cc06711dbe7138
X-Checksum-SHA1:  d0454d45f98dc821048023a9dccd36329331c41c
X-Checksum-MD5:   89f4942cc21aba7700c73bc4a5b0fd75
ETag:              "717bae503ae6108953042113e8bb6284b71a2c1c73665b7cc9cc06711dbe7138"
```

`HEAD` answers `200` with the same headers and a `Content-Length`, without the
body.

To remove a file:

```bash
curl -u admin:admin123 -X DELETE \
  http://localhost:8081/repository/raw-artifacts/releases/app-1.0.0.tar.gz
```

The answer is `204 No Content`, and the path answers `404` from then on.

## 4. The path is the identity — there are no versions

Uploading the same path twice **replaces the file**. The second upload is not
a second version of the first: the repository still holds one asset at that
path, and the second upload's bytes are what you get back.

That has two consequences worth planning around:

- a raw repository cannot hold two versions of the same file, so a
  "keep the last N versions" rule has nothing to keep
- the download URL is stable — `…/releases/app-1.0.0.tar.gz` always means
  "whatever is stored at that path right now"

If you need versions, put the version in the path.

## 5. The path layout is the browse tree

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/api/v1/browse/repositories/raw-artifacts/raw-tree
```

```json
{
  "repository": "raw-artifacts",
  "format": "raw",
  "root": {
    "kind": "folder",
    "label": "/",
    "path": "/",
    "children": [
      {
        "kind": "folder",
        "label": "config",
        "path": "/config",
        "children": [
          { "kind": "file", "label": "app.yml", "path": "/config/app.yml", "size": 13 }
        ]
      },
      {
        "kind": "folder",
        "label": "releases",
        "path": "/releases",
        "children": [
          { "kind": "file", "label": "app-1.0.0.tar.gz", "path": "/releases/app-1.0.0.tar.gz", "size": 22 }
        ]
      }
    ]
  }
}
```

The tree is built from the asset paths: every `/` starts a folder, the last
segment is the file. A group repository serves the tree of its members merged.

## 6. The proxy

A proxy repository caches what it fetches from the upstream and serves it
from its own blob store.

```bash
curl -s http://localhost:8081/repository/raw-proxy/releases/app-1.0.0.tar.gz
```

Three things to know about it:

- **it is read-only.** An anonymous write is refused with `401` (a write
  always needs credentials), and an authenticated write is refused with
  `405 Method Not Allowed`.
- **an upstream directory listing is re-rooted at the repository.** An upstream
  that serves `href="/dist/file.txt"` would send a browser off the site, so
  the links are rewritten to `href="/repository/raw-proxy/dist/file.txt"`.
  Links that are already relative are left alone.
- **the upstream bytes are cached.** The listing you see is the one that was
  fetched; a change at the upstream shows up only after the cached copy is
  replaced.

### An upstream on an internal network

The proxy's HTTP client is **SSRF-guarded**: it refuses to dial loopback,
RFC1918, link-local and CGNAT addresses, because `remote_url` is
admin-configured. An upstream on `10.0.0.5` or on the compose network answers
`502 Bad Gateway` with:

```
{"error":"upstream fetch failed: Get \"http://internal:8899/file.txt\":
 dial tcp: lookup internal: no such host"}
```

The operator opts in per range:

```yaml
# docker-compose.yml
services:
  nexspence:
    environment:
      NEXSPENCE_OUTBOUND_ALLOWED_INTERNAL_CIDRS: "10.0.0.0/8,172.27.0.0/16"
```

The server logs a warning at startup when the list is not empty. See
[deployment.md](../deployment.md).

## 7. The group

A group repository merges its members into one namespace:

```bash
curl -u admin:admin123 http://localhost:8081/repository/raw-common/config/app.yml
```

A path that exists in both members is served by the first member that has
it. A group has no storage of its own.

## 8. A cleanup policy

A cleanup policy finds assets that match a rule and deletes them. Create it
first:

```bash
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/cleanup-policies \
  -H "Content-Type: application/json" \
  -d '{
    "name": "raw-aged",
    "format": "raw",
    "criteria": { "artifactAgeDays": 90 },
    "scope": { "repositoryName": "raw-artifacts" },
    "dryRun": true
  }'
```

Then attach it to the repository:

```bash
curl -u admin:admin123 -X PUT \
  http://localhost:8081/service/rest/v1/repositories/raw/hosted/raw-artifacts \
  -H "Content-Type: application/json" \
  -d '{ "cleanupPolicyIds": ["<policy-id>"] }'
```

Look before you leap — the preview lists what a run would remove, and deletes
nothing:

```bash
curl -u admin:admin123 -X POST \
  http://localhost:8081/api/v1/cleanup-policies/<policy-id>/preview
```

```json
{
  "assets": [
    {
      "path": "/config/app.yml",
      "repository": "raw-artifacts",
      "sizeBytes": 13,
      "lastDownloaded": "2026-11-24T08:01:57.600318Z",
      "createdAt": "2026-05-08T08:04:06.322716Z",
      "reason": "age 90d"
    }
  ],
  "totalCount": 1,
  "totalBytes": 13
}
```

The `reason` field says which criterion matched.

A run reports what it did:

```bash
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/cleanup-policies/<policy-id>/run
```

```json
{
  "policyId": "19ed5ddf-af57-4dde-b030-1ba4c291c07f",
  "deleted": 1,
  "freedBytes": 22,
  "dryRun": false,
  "skipped": false
}
```

With `"dryRun": true` the run reports the same numbers and **deletes
nothing** — it is the same accounting, without the deletions.

### The criteria

| Criterion | Meaning |
|---|---|
| `artifactAgeDays` | delete assets whose `created_at` is older than N days |
| `lastDownloadedDays` | delete assets not downloaded for N days — an asset that was **never** downloaded counts as not downloaded |
| `pathPrefix` | only assets under this prefix |
| `nameGlob` | only assets whose path matches this glob |

`scope.repositoryName` limits the policy to one repository. Without it, the
policy applies to every repository it is attached to — and a policy that is
attached to nothing applies to nothing.

`scope.pathPrefix` narrows the policy to one subtree:

```json
{ "scope": { "repositoryName": "raw-artifacts", "pathPrefix": "/tmp/" } }
```

### What it deletes, and what it does not

A run deletes **the asset and its bytes** in the repositories the policy
covers, and nothing else. Concretely, it does **not**:

- delete anything that is younger than `artifactAgeDays`, even in the same
  repository — the preview shows exactly which paths are in range
- touch an asset outside `pathPrefix`
- touch another repository, even one with a similar name
- delete the *group* — a group has no storage, and the members are what hold
  the files

The `dryRun` result and the real result carry the same `deleted` and
`freedBytes` numbers, so a dry run is a reliable rehearsal.

### Schedule

```json
{ "scheduleCron": "0 3 * * *", "enabled": true }
```

A scheduled policy runs on its own. Without a schedule the policy only runs
when you trigger it.

## 9. Storage quotas

A quota is a byte budget for one repository:

```bash
curl -u admin:admin123 -X PUT \
  http://localhost:8081/service/rest/v1/repositories/raw/hosted/raw-artifacts \
  -H "Content-Type: application/json" \
  -d '{ "quotaBytes": 1073741824 }'
```

Read the current usage:

```bash
curl -u admin:admin123 \
  http://localhost:8081/api/v1/repositories/raw-artifacts/quota
```

```json
{
  "repository": "raw-artifacts",
  "usedBytes": 13,
  "quotaBytes": 1073741824,
  "percentUsed": 0.00000121
}
```

The quota is enforced on upload. An upload that would cross it is refused
with `507 Insufficient Storage`:

```json
{
  "error": "storage quota exceeded: repository \"raw-artifacts\" usage 13 + 8192 > limit 1024"
}
```

The repository's own quota and the blob store's quota are both checked, and
both must hold. A repository quota cannot be set above its blob store's quota
— the server refuses that combination at update time.

## 10. Anonymous access

Proxy and group repositories with `"allowAnonymous": true` serve reads without
credentials. A hosted repository with `"allowAnonymous": false` needs them,
for reads and for writes alike.

Keep hosted repositories non-anonymous so that publishing requires
authentication, and grant read access through roles — see
[security-rbac.md](../security-rbac.md).

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `401` on upload | The hosted repository is not anonymous | Add credentials |
| `405 Method Not Allowed` on an authenticated write | The repository is a proxy | Upload to the hosted repository |
| `502 Bad Gateway` from a proxy | The upstream is unreachable, or it is on an internal address | Check the URL; add the range to `outbound.allowed_internal_cidrs` |
| The second upload is all you can download | A path holds one file, not versions | Put the version in the path |
| The browse tree is empty | The repository is a proxy with nothing cached yet | Fetch a path through it first |
| `507 Insufficient Storage` on upload | The repository or the blob store is full | Raise the quota or clean up |
| A cleanup run deletes nothing | Nothing matched the criteria | Read the preview — it lists the candidates |
| A cleanup run reports `deleted: 0` but the preview showed files | The policy is a dry run | Set `"dryRun": false` |
| `400` on a repository update | The body was not valid JSON | Check the quoting |

## Related

- [RBAC and content selectors](../security-rbac.md)
- [Deployment](../deployment.md)
- [GitHub Actions guide](github-actions.md)
- [API specification](../api-spec.yaml)
