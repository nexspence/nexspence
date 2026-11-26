# npm guide — hosted, proxy and group registries

How to run an npm registry on Nexspence: create the repositories, publish with
the real npm client, resolve from a group, and search.

Assumes a fresh instance on `http://localhost:8081`.

## 1. Create the repositories

```bash
BASE_URL=http://localhost:8081 ./scripts/create-npm-repos.sh
```

That script creates `npm-hosted`, `npm-proxy` and `npm-group` — the three names
this guide uses.

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
    \"name\": \"npm-group\",
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
registry=http://localhost:8081/repository/npm-group/
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
revocable, a password is not. A user creates and revokes their own tokens in
the profile panel (the 🔑 icon in the sidebar); there is no admin page for
them.

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
npm install --registry http://localhost:8081/repository/npm-group/ my-package

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

## 7. Behind a reverse proxy

npm takes `dist.tarball` literally: it resolves it against nothing at all, so a
relative URL is read as a path on the client's own disk — the
[reverse-proxy guide](reverse-proxy.md) lists the clients that behave this way.
The tarball URL therefore has to name a host, and it has to name the host the
client actually reached. A URL that names the proxy's internal address sends
the client somewhere it cannot get to; one that names a host the client chose
sends it wherever the client wants to go.

By default that host is the deployment's own `http.base_url` —
`http://localhost:8081` in the quick start. A deployment behind a reverse proxy
does not have to guess it: when the request came from a peer listed in
`http.trusted_proxies`, the server takes the host from the proxy's
`X-Forwarded-Host` and the scheme from its `X-Forwarded-Proto`, and only `http`
or `https` are believed. Whatever the proxy does not say comes from
`http.base_url`.

The forwarded header is believed only from a trusted peer, and the request's own
`Host` header is never believed, not even from a trusted proxy. The host in a
link is the one thing a client must not be able to choose: the packument is
cached, so one request that named a host of its choosing would send every later
client there instead of to this instance.

When more than one proxy sits in front of the instance, the rightmost value of
`X-Forwarded-Host` is the one taken: a proxy that overwrites the header leaves
one value, and a proxy that appends to it puts its own view last.

### What an administrator configures

The proxy says which host the client used, and the instance trusts the proxy
that says it:

```nginx
server {
    listen 443 ssl;
    server_name repo.example.com;

    location / {
        proxy_pass http://127.0.0.1:8081;

        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host  $host;   # the host the client used
    }
}
```

```yaml
http:
  trusted_proxies: ["10.0.0.0/8"]     # the proxy's address as the server sees it
```

```bash
NEXSPENCE_HTTP_TRUSTED_PROXIES="10.0.0.0/8"    # the same list, as an environment variable
```

The address in the list is the peer as the server sees it — behind Docker
Compose that is the gateway address of the compose network, not the address on
the host. `["*"]` trusts every hop, and is only safe when the instance cannot
be reached except through a proxy the deployment controls.

### What the client sees

None of this reaches the client. The `.npmrc` names the registry and nothing
more:

```ini
registry=https://repo.example.com/repository/npm-group/
```

The client fetches the packument and reads `dist.tarball` out of it, and that
is the URL it downloads from — the one the server put there. A client cannot
repair a wrong tarball URL by editing its `.npmrc`; the fix belongs on the
server.

### Checking it

The commands and outputs below were taken against a stand on port 8091 — the
commands use that port so they can be copied as they are. The rest of this
guide uses the default port 8081.

```bash
curl -s -u admin:admin123 \
  "http://localhost:8091/repository/npm-group/lodash" \
  | jq -r '.versions[."dist-tags".latest].dist.tarball'
```

```
http://localhost:8091/repository/npm-group/lodash/-/lodash-4.18.1.tgz
```

The stand trusts no proxy, so the host in the tarball URL is the configured
`http.base_url` — `localhost:8091` there, `http://localhost:8081` in the quick
start. A client that reaches the instance directly cannot change that, whatever
headers it sends:

```bash
curl -s -u admin:admin123 \
  -H "Host: evil.example" \
  -H "X-Forwarded-Host: evil.example" \
  -H "X-Forwarded-Proto: https" \
  "http://localhost:8091/repository/npm-group/lodash" \
  | jq -r '.versions[."dist-tags".latest].dist.tarball'
```

```
http://localhost:8091/repository/npm-group/lodash/-/lodash-4.18.1.tgz
```

The request did not come from a trusted proxy, so the forwarded host is
ignored. Behind a trusted proxy the request the proxy sends carries
`X-Forwarded-Host: repo.example.com` and `X-Forwarded-Proto: https`, and the
tarball URL names that host in that scheme instead.

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
| The tarball URL names a host the client cannot reach | The packument was minted with `http.base_url`, which still names the internal address | Set `http.base_url` to the public URL, or trust the reverse proxy as described in [Behind a reverse proxy](#7-behind-a-reverse-proxy) |

## Related

- [npm search](../npm-search.md)
- [RBAC and content selectors](../security-rbac.md)
- [Migration from Sonatype Nexus](../nexus-migration.md)
- [GitHub Actions guide](github-actions.md)
- [Running behind a reverse proxy](reverse-proxy.md)
