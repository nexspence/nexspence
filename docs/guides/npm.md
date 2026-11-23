# npm guide — hosted, proxy and group registries

How to run an npm registry on Nexspence: create the repositories, publish with
the real npm client, resolve from a group, and search.

Assumes a fresh instance on `http://localhost:8081`.

## 1. Create the repositories

```bash
BASE_URL=http://localhost:8081 ./scripts/create-npm-repos.sh
```

By hand, with the v1-compatible API:

```bash
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# hosted — your own packages
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/npm/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"npm-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Internal npm packages\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of the public registry
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/npm/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"npm-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Proxy to registry.npmjs.org\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://registry.npmjs.org\" }
  }"

# group — one registry for both
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/npm/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"npm-public\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Group of hosted and proxy\",
    \"formatConfig\": { \"member_names\": [\"npm-hosted\", \"npm-proxy\"] }
  }"
```

Repository URLs:

```
http://localhost:8081/repository/<repository-name>/
```

## 2. Configure the client

### `~/.npmrc` — resolve through the group

```ini
registry=http://localhost:8081/repository/npm-public/
```

### Authentication

npm sends `Authorization: Bearer <token>` when you use `_authToken`, and Basic
authentication when you use `_auth`. Nexspence accepts both:

```ini
//localhost:8081/repository/npm-hosted/:_authToken=nxs_xxxxxxxxxxxxxxxxxxxx
//localhost:8081/repository/npm-hosted/:always-auth=true
```

`_auth` is base64 of `username:password`:

```bash
echo -n "admin:admin123" | base64
# YWRtaW46YWRtaW4xMjM=
```

```ini
//localhost:8081/repository/npm-hosted/:_auth=YWRtaW46YWRtaW4xMjM=
//localhost:8081/repository/npm-hosted/:always-auth=true
```

Use `_authToken` with an `nxs_*` API token in CI — the token is scoped and
revocable, a password is not. Create it in **Admin → Security → Tokens**.

> The path in the `.npmrc` key must match the repository path exactly,
> including the trailing slash behaviour of npm. `//host/repository/npm-hosted/`
> and `//host/repository/npm-hosted` are different keys to npm.

## 3. Publish

```bash
npm publish --registry http://localhost:8081/repository/npm-hosted/
```

The npm client `PUT`s the packument to the package path with the tarball
attached:

```
PUT /repository/npm-hosted/@scope%2fname
```

Which means the package name maps to the path — a scoped package `@scope/name`
becomes `@scope/name` in the URL. The stored tarball is served back at:

```
http://localhost:8081/repository/npm-hosted/@scope/name/-/name-1.0.0.tgz
```

### Publishing without the npm client

The raw API is a `PUT` with a JSON body carrying the tarball in `_attachments`:

```bash
curl -u admin:admin123 -X PUT \
  http://localhost:8081/repository/npm-hosted/my-package \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-package",
    "versions": {
      "1.0.0": {
        "name": "my-package",
        "version": "1.0.0",
        "dist": { "tarball": "", "shasum": "" }
      }
    },
    "dist-tags": { "latest": "1.0.0" },
    "_attachments": {
      "my-package-1.0.0.tgz": {
        "content_type": "application/octet-stream",
        "data": "<base64 of the tarball>",
        "length": 1234
      }
    }
  }'
```

`scripts/seed-packages.sh` does exactly this to seed a package without a
published tarball.

## 4. Resolve, install and search

```bash
# install from the group
npm install --registry http://localhost:8081/repository/npm-public/ my-package

# what does the registry hold?
npm view my-package versions --registry http://localhost:8081/repository/npm-hosted/

# registry search
curl -u admin:admin123 \
  "http://localhost:8081/repository/npm-hosted/-/v1/search?text=my-package"

# the packument itself
curl -u admin:admin123 http://localhost:8081/repository/npm-hosted/my-package
```

Nexspence also answers the two endpoints npm clients probe during
authentication:

- `GET /-/ping` → `{"ok":true}`
- `GET /-/v1/search` → the registry search endpoint

## 5. Dist-tags and versions

A group repository merges the members' packuments: versions and dist-tags from
every member end up in one document. If the same version exists in two members,
the hosted member wins — the same first-match rule the other formats use.

Search results come from the local index, not from the upstream registry. That
means a package that was never requested through the proxy will not show up in
search until it is fetched at least once. See [npm-search.md](../npm-search.md)
for the details.

## 6. Anonymous access

Proxy and group repositories with `"allowAnonymous": true` serve reads without
credentials. Keep hosted repositories non-anonymous so that publishing requires
authentication, and grant read access through roles — see
[security-rbac.md](../security-rbac.md).

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `ENEEDAUTH` | No credentials for that registry path | Add the `//host/path/:_authToken=…` line — the path must match the registry URL exactly |
| `403 Forbidden` on publish | Authenticated, but the role lacks write access to that repository | Grant the write privilege on the hosted repository to the publishing role |
| `405 Method Not Allowed` | Publishing into a proxy repository | Proxy repositories are read-only; publish to the hosted one |
| `E404` for a package that exists upstream | The proxy has not cached it yet and the remote URL is wrong | Check `proxyConfig.remote_url`; the client must be able to reach it |
| `npm install` resolves the wrong version | A group member shadows the version | Check which member holds the version — hosted wins over proxy |
| Package missing from search results | The local index only knows packages that were fetched | Fetch the package once, or search the upstream registry |
| `EINVALIDTAGNAME` / odd 404s on scoped packages | The scope was not URL-encoded somewhere in the chain | Use `npm publish --registry`, not a hand-written `PUT` with a raw scope |
| `npm ci` fails with `401` in CI | The token was not exported to the step that runs npm | Set `NPM_CONFIG_//localhost:8081/repository/npm-hosted/:_authToken` via `NODE_AUTH_TOKEN` and an `.npmrc` that references it |

## Related

- [npm search](../npm-search.md)
- [RBAC and content selectors](../security-rbac.md)
- [Migration from Sonatype Nexus](../nexus-migration.md)
- [GitHub Actions guide](github-actions.md)
