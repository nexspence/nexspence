# Go modules guide — GOPROXY, publishing and the checksum database

Go talks to a module proxy through one environment variable, and the protocol
is small enough to drive with `curl`. This guide sets up a hosted repository, a
proxy in front of the public proxy, and a group — then shows how the client
resolves through them, how a module is published, and the one setting that
makes a private module work at all.

Assumes a fresh instance on `http://localhost:8081`.

## 1. Create the repositories

```bash
BASE_URL=http://localhost:8081 ./scripts/create-go-repos.sh
```

That creates `go-hosted`, `go-proxy` and `go-group`.

By hand, with the v1-compatible API:

```bash
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# hosted — your own modules
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/go/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"go-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Internal Go modules\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of the public module proxy
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/go/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"go-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Proxy to proxy.golang.org\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://proxy.golang.org\" }
  }"

# group — one GOPROXY for both
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/go/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"go-group\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Group of hosted and proxy\",
    \"formatConfig\": { \"member_names\": [\"go-hosted\", \"go-proxy\"] }
  }"
```

## 2. Point the client at it

```bash
export GOPROXY=http://localhost:8081/repository/go-group/
```

**The trailing slash is not optional.** `GOPROXY` is a base URL: without the
slash the client asks for `…/go-groupexample.com/…` and every request 404s.
This is the single most common way to get a Go proxy wrong.

Two more settings matter for a private module:

```bash
export GOFLAGS=-mod=mod
export GONOSUMDB=example.com/*
```

- `GOFLAGS=-mod=mod` lets the client add a requirement to a `go.mod` that has
  none yet — without it, `go get` in a fresh module refuses to write the file.
- `GONOSUMDB` tells the client not to check the listed module paths against
  the public checksum database. A module that only exists on your instance is
  not in `sum.golang.org`, and the lookup fails before the download even
  starts:

  ```
  go: example.com/nexspence-demo@v1.0.0: verifying go.mod:
      reading https://sum.golang.org/lookup/example.com/nexspence-demo@v1.0.0: 404 Not Found
  ```

> **About `GONOSUMCHECK` and `GOPRIVATE`.** `GONOSUMCHECK` is a vgo-era name
> that current Go ignores — it is not what turns the checksum database off.
> `GOPRIVATE` does turn it off, but it also sets `GONOPROXY`, which makes the
> client **bypass your proxy** and fetch those modules straight from their
> source. That is the opposite of what you want for a module you are serving.
> Use `GONOSUMDB=<your prefix>` to keep the proxy in play, or `GOSUMDB=off` to
> turn the checksum database off for everything.

With `GOSUMDB=off` instead, the client trusts whatever the proxy serves —
fine for an instance you control, not what you want for a mixed setup where
public modules also come through the proxy.

## 3. Resolve through the group

```bash
# what versions are there?
go list -m -versions example.com/nexspence-demo

# add the dependency to the current module
go get example.com/nexspence-demo@v1.0.0

# download what the build list names
go mod download
```

`go mod download` with no arguments downloads the modules in the build list —
the ones `go get` just added. Naming a module that is not in the build list
is an error (`not a known dependency`), not a download.

The proxy member answers for modules it fetches from the public proxy and
caches them:

```bash
go mod download golang.org/x/text@v0.14.0
```

## 4. `GOPROXY=<url>,direct`

The list after the URL is the fallback chain. `,direct` tells the client to
fetch the module from its source — the VCS or the vanity import path — when
the proxy does not have it:

```bash
export GOPROXY=http://localhost:8081/repository/go-group/,direct
```

The difference is visible in the error. Without `,direct` a missing module
stops at the proxy:

```
go: example.com/does-not-exist@v1.0.0: reading
    http://localhost:8081/repository/go-group/example.com/does-not-exist/@v/v1.0.0.info:
    404 Not Found
```

With `,direct` the client goes on to the source and fails there instead:

```
go: example.com/does-not-exist@v1.0.0: unrecognized import path
    "example.com/does-not-exist": reading https://example.com/does-not-exist?go-get=1:
    404 Not Found
```

Use `,direct` when your build still needs a module that only exists in a VCS.
Leave it off when you want the proxy to be the only source of truth — with
`direct` in the chain, a module the proxy does not have is fetched from the
internet instead of failing, which quietly defeats an air-gapped setup.

## 5. Publish a module

A module is three files. The zip holds the source under a
`<module>@<version>/` prefix, and the `.mod` file is the module's own
`go.mod`:

```
example.com/nexspence-demo@v1.0.0/go.mod
example.com/nexspence-demo@v1.0.0/hello.go
```

```bash
V=http://localhost:8081/repository/go-hosted/example.com/nexspence-demo/@v

curl -u admin:admin123 -X PUT --data-binary @demo.info "$V/v1.0.0.info"
curl -u admin:admin123 -X PUT --data-binary @demo.mod  "$V/v1.0.0.mod"
curl -u admin:admin123 -X PUT --data-binary @demo.zip  "$V/v1.0.0.zip"
```

`demo.info` is the version's metadata:

```json
{"Version":"v1.0.0","Time":"2026-01-01T00:00:00Z"}
```

`scripts/seed-packages.sh` builds all three and uploads them — it is the
shortest way to see the exact shape.

The protocol endpoints of a hosted repository are:

| Request | Answers |
|---|---|
| `GET /<module>/@v/list` | the versions, one per line |
| `GET /<module>/@v/<version>.info` | the version's metadata |
| `GET /<module>/@v/<version>.mod` | the `go.mod` |
| `GET /<module>/@v/<version>.zip` | the module source |
| `GET /<module>/@latest` | the latest version's metadata |
| `PUT /<module>/@v/<version>.mod` | upload the `go.mod` |
| `PUT /<module>/@v/<version>.zip` | upload the module zip |

## 6. How a module path maps to a URL

The module path is the URL path, with one twist from the proxy protocol: an
uppercase letter is written as `!` followed by its lowercase form, so that
every path segment is case-insensitively unique on a case-insensitive
filesystem.

```
github.com/Azure/azure-sdk  →  /github.com/!azure/azure-sdk/@v/
```

The server stores the path exactly as the protocol writes it:

```bash
# this is the path a client asks for
curl -u admin:admin123 \
  http://localhost:8081/repository/go-hosted/example.com/!azure/demo/@v/v1.0.0.mod
```

The unescaped spelling is a different path and answers `404`:

```bash
curl -u admin:admin123 \
  http://localhost:8081/repository/go-hosted/example.com/Azure/demo/@v/v1.0.0.mod
# 404
```

## 7. How the group resolves

The group is an ordered union of its members:

- a module that is in the hosted repository is served from there;
- a module that is not is fetched through the proxy member and cached;
- the proxy member is read-only — a `PUT` answers `405 Method Not Allowed`.

## 8. Anonymous access

Proxy and group repositories with `"allowAnonymous": true` serve reads without
credentials — that is the normal setup for a shared build agent. Keep hosted
repositories non-anonymous so that publishing requires authentication, and
grant read access through roles — see [security-rbac.md](../security-rbac.md).

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Every request 404s and the URL looks like `…/go-groupexample.com/…` | `GOPROXY` has no trailing slash | `GOPROXY=http://host:8081/repository/go-group/` |
| `reading https://sum.golang.org/lookup/…: 404 Not Found` | The client checks the public checksum database for a private module | `GONOSUMDB=<your prefix>` or `GOSUMDB=off` |
| `not a known dependency` | `go mod download` named a module that is not in the build list | `go get` it first, then `go mod download` |
| `go get` refuses to write `go.mod` | The module file has no requirements yet | `GOFLAGS=-mod=mod` |
| The client ignores the proxy and goes to the internet | `GOPRIVATE` (or `GONOPROXY`) matches the module path | Use `GONOSUMDB` instead |
| `unrecognized import path` | The `,direct` fallback tried the source and failed | Expected — the module is not in the proxy and not at its source |
| `405 Method Not Allowed` on upload | The repository is a proxy | Publish to the hosted repository |
| The module is served but the build still fails | The zip is not prefixed with `<module>@<version>/` | Rebuild the zip with that prefix |

## Related

- [RBAC and content selectors](../security-rbac.md)
- [GitHub Actions guide](github-actions.md)
- [API specification](../api-spec.yaml)
