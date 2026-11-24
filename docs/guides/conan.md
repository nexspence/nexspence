# Conan guide — hosted, proxy and group repositories

A Conan repository is an HTTP server that stores **recipes** (the
`conanfile.py` and its metadata) and **binary packages** (the built package).
A client resolves a recipe by `name/version@user/channel`, then downloads the
binary that matches its own settings.

Nexspence speaks two protocol versions of that protocol: the **v2 revisions
API**, which Conan 2.x clients use, and the **v1 REST API**, which Conan 1.x
clients use. A Conan repository here is a hosted repository, a proxy in front
of `center.conan.io`, or a group of the two.

Everything below assumes a fresh instance on `http://localhost:8081` (the
default port of the Docker Compose quick start). Replace the host everywhere
if yours differs.

## 1. Create the repositories

Nexspence ships a helper that creates all three repository types with the
right payloads:

```bash
BASE_URL=http://localhost:8081 ./scripts/create-conan-repos.sh
```

It defaults to `http://localhost:8081` — point it at your instance with
`BASE_URL` when it differs:

```bash
BASE_URL=https://nexus.example.com ./scripts/create-conan-repos.sh
```

That script creates `conan-hosted`, `conan-proxy` (in front of
`center.conan.io`) and `conan-group` — the three names this guide uses.

Under the hood it does three `POST` calls against the v1-compatible API. This
is the same thing by hand, and it is worth knowing because the API is what CI
scripts talk to:

```bash
# resolve the blob store id first — a repository must reference an existing one
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# hosted — your own recipes and binaries
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/conan/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"conan-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Conan (C/C++) hosted repository\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of the Conan Center
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/conan/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"conan-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Proxy to https://center.conan.io\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://center.conan.io\" }
  }"

# group: one URL for both
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/conan/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"conan-group\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Conan group (hosted + proxy)\",
    \"formatConfig\": { \"member_names\": [\"conan-hosted\", \"conan-proxy\"] }
  }"
```

Repository URLs follow one pattern:

```
http://localhost:8081/repository/<repository-name>/
```

## 2. Point the client at the repositories

A remote is a named URL in the client's configuration:

```bash
conan remote add nexspence-hosted http://localhost:8081/repository/conan-hosted/
conan remote add nexspence-group  http://localhost:8081/repository/conan-group/
conan remote add nexspence-proxy  http://localhost:8081/repository/conan-proxy/
```

```
$ conan remote list
conancenter: https://center2.conan.io [Verify SSL: True, Enabled: True]
nexspence-hosted: http://localhost:8081/repository/conan-hosted/ [Verify SSL: True, Enabled: True]
nexspence-group: http://localhost:8081/repository/conan-group/ [Verify SSL: True, Enabled: True]
nexspence-proxy: http://localhost:8081/repository/conan-proxy/ [Verify SSL: True, Enabled: True]
```

### Credentials for a private repository

`conan-hosted` is created with `"allowAnonymous": false`, so every request to
it needs credentials — including the ping. Conan 2 has no
`--username`/`--password` on `conan remote add`; the credentials go in with a
login:

```bash
conan remote login nexspence-hosted admin -p admin123
```

```
Authenticated in remote 'nexspence-hosted' with user 'admin'
Changed user of remote 'nexspence-hosted' from 'None' (anonymous) to 'admin' (authenticated)
```

The client keeps the credentials in its local cache and sends them as HTTP
Basic authentication. The server answers the login on the credential route
`GET /v2/users/authenticate`, and the client checks the result with
`GET /v2/users/check_credentials`:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/conan-hosted/v2/users/check_credentials
```

```
admin
```

Nexspence accepts an `nxs_*` API token as the password of that Basic
authentication, which is what you want in CI:

```bash
conan remote login nexspence-hosted ci-bot -p nxs_xxxxxxxxxxxxxxxxxxxx
```

The username is free-form here: a token identifies its owner, so the server
authenticates the user the token belongs to and ignores the name in the
Basic header. The token itself is created in the profile panel (the 🔑 icon
in the sidebar) — a user manages their own tokens; there is no admin page for
them — or through the API:

```bash
curl -s -u admin:admin123 -X POST http://localhost:8081/api/v1/tokens \
  -H "Content-Type: application/json" \
  -d '{"name":"conan-guide","expiresInDays":1}'
```

A group repository is logged in the same way — see
[publishing through a group](#publishing-through-a-group).

## 3. The ping endpoint, and which protocol the client speaks

`GET /ping` and `GET /v1/ping` are the cheapest request a client can make,
and they exist for one reason: **capability negotiation**. The body is
always the same:

```bash
curl -s -i -u admin:admin123 \
  http://localhost:8081/repository/conan-hosted/ping
```

```
HTTP/1.1 200 OK
X-Conan-Server-Capabilities: revisions
Content-Type: application/json; charset=utf-8

{"ok":true}
```

The `X-Conan-Server-Capabilities` header is the interesting part. It tells
the client which flavour of the protocol the server understands; `revisions`
means the server speaks the **v2 revisions API**, where every recipe and
every binary carries an immutable revision hash.

A **Conan 2.x client speaks the v2 protocol**. Everything it does after the
ping happens under `/v2/`:

| Request | What it is |
|---|---|
| `GET /v2/users/authenticate` | the login handshake |
| `GET /v2/users/check_credentials` | "who am I", called before an upload |
| `GET /v2/conans/<ref>/revisions` | the recipe revisions of one reference |
| `GET /v2/conans/<ref>/latest` | the newest recipe revision |
| `GET /v2/conans/<ref>/revisions/<rrev>/files` | the files of a recipe revision |
| `GET /v2/conans/search?q=<pattern>` | the recipe search |
| `PUT /v2/conans/<ref>/revisions/<rrev>/files/<file>` | an upload |

The **one exception is the ping itself**: the client asks for
`GET /v1/ping` to read the capability header, because that is the path
`conan_server` advertises it on. Nexspence returns the header on **both**
`/ping` and `/v1/ping` — the two answers are the same — but the client asks
for the `/v1/ping` one. It calls it once per remote and caches the answer,
so it does not appear in every request.

If the capability header is missing, the client refuses the remote
altogether:

```
ERROR: Remote http://localhost:8081/repository/conan-hosted/ doesn't seem like a valid Conan remote
```

### Where the v1 endpoints are still needed

The **v1 REST API** is still served, and it is what a **Conan 1.x client**
uses — Conan 1.x speaks v1 unless it is explicitly put into revisions mode.
The endpoints that matter are the ones a v1 client calls around the actual
file transfer:

| Request | What it is |
|---|---|
| `GET /v1/conans/<ref>` | the recipe manifest |
| `POST /v1/conans/<ref>/upload_urls` | "where do I PUT the files?" |
| `PUT /files/<ref>/<rrev>/export/<file>` | the upload itself |
| `PUT /files/<ref>/<rrev>/package/<pkgid>/<prev>/<file>` | the binary upload |
| `GET /v1/conans/<ref>/download_urls` | "where do I GET the files?" |
| `GET /files/<ref>/<rrev>/export/<file>` | the download itself |

The v1 upload is a two-step handshake, and it is worth seeing it once:

```bash
# 1. ask for the URLs
curl -s -u admin:admin123 -X POST \
  -H "Content-Type: application/json" \
  -d '{"conanfile.py": 31}' \
  http://localhost:8081/repository/conan-hosted/v1/conans/v1demo/1.0.0/_/_/upload_urls
```

```json
{"conanfile.py":"http://localhost:8081/repository/conan-hosted/files/v1demo/1.0.0/_/_/0/export/conanfile.py"}
```

```bash
# 2. PUT the file to the URL the server handed out
curl -s -o /dev/null -w "%{http_code}\n" -u admin:admin123 -X PUT \
  --data-binary @conanfile.py \
  http://localhost:8081/repository/conan-hosted/files/v1demo/1.0.0/_/_/0/export/conanfile.py
```

```
201
```

A binary follows the same shape, under `package/<package-id>/<revision>/`:

```bash
curl -s -o /dev/null -w "%{http_code}\n" -u admin:admin123 -X PUT \
  --data-binary @conaninfo.txt \
  http://localhost:8081/repository/conan-hosted/files/v1demo/1.0.0/_/_/0/package/abc123/pkgrev456/conaninfo.txt
```

```
201
```

The download is the mirror image — `GET /v1/conans/<ref>/download_urls`
returns the same map of file name → URL, and the client fetches those URLs
from `/files/...`.

One thing to know about the two protocol versions: **they write into two
separate trees**. A recipe uploaded by a Conan 2 client lives under
`/v2/conans/...` and is not visible under `/files/...`, and the other way
round. A v1 client therefore cannot download what a v2 client uploaded, and
the v2 search does not list v1 uploads.

## 4. Publish

The demo package below is header-only, so it needs no compiler:

```python
# conanfile.py
import os

from conan import ConanFile
from conan.tools.files import copy


class NexspenceDemoConan(ConanFile):
    name = "nexspence-demo"
    version = "1.0.0"
    description = "Header-only demo package for the Conan guide"
    license = "MIT"
    package_type = "header-library"
    exports_sources = "include/*"

    def package(self):
        copy(self, "*.h", src="include",
             dst=os.path.join(self.package_folder, "include"))
```

Build it in the local cache and publish it:

```bash
conan create .                       # builds the package locally
conan upload nexspence-demo/1.0.0 -r nexspence-hosted
```

```
-------- Uploading artifacts --------
nexspence-demo/1.0.0: Uploading recipe 'nexspence-demo/1.0.0#878bb16fbf9dea2b5a25ec6b5826f9a0' (849B)
nexspence-demo/1.0.0: Uploading package 'nexspence-demo/1.0.0#878bb16fbf9dea2b5a25ec6b5826f9a0:da39a3ee5e6b4b0d3255bfef95601890afd80709#1a37bb11a69909708d9763cf36c60d2b' (390B)
Upload completed in 0s
```

If the package is not in the local cache yet, the upload has nothing to
publish. In Conan 2 the build happens **before** the upload — `conan upload`
has no `--build` option, that is a Conan 1 flag — so `conan create .` is what
puts the package in the cache first. `conan install --build=missing` builds a
*dependency* from source when the remote has no binary for it.

### What the upload does under the hood

The client never asks for a URL to upload to — the v2 protocol puts the files
directly. The sequence of requests the server sees is:

```
GET  /repository/conan-hosted/v1/ping
GET  /repository/conan-hosted/v2/conans/nexspence-demo/1.0.0/_/_/revisions
GET  /repository/conan-hosted/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/packages/<pkgid>/revisions
GET  /repository/conan-hosted/v2/users/check_credentials
PUT  /repository/conan-hosted/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/files/conan_sources.tgz
PUT  /repository/conan-hosted/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/files/conanfile.py
PUT  /repository/conan-hosted/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/files/conanmanifest.txt
PUT  /repository/conan-hosted/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/packages/<pkgid>/revisions/<prev>/files/conan_package.tgz
PUT  /repository/conan-hosted/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/packages/<pkgid>/revisions/<prev>/files/conaninfo.txt
PUT  /repository/conan-hosted/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/packages/<pkgid>/revisions/<prev>/files/conanmanifest.txt
```

Three things are worth reading off that list:

- the client checks the credentials **before** it uploads anything, and
  refuses to write without a `200` from `/v2/users/check_credentials`;
- a **recipe** is uploaded as three files — `conanfile.py`,
  `conanmanifest.txt` and `conan_sources.tgz` (the exported sources);
- a **binary** is uploaded as `conan_package.tgz` (the package folder),
  `conaninfo.txt` (the settings it was built with) and its own
  `conanmanifest.txt`.

The revision hash in the path is the identity of the upload. The same recipe
built twice produces two revisions, and both are kept — that is what makes
revisions reproducible.

## 5. Resolve and search

Install through the group — one remote for your own packages and the
upstream's:

```bash
conan install --requires=nexspence-demo/1.0.0 -r nexspence-group
```

```
Requirements
    nexspence-demo/1.0.0#878bb16fbf9dea2b5a25ec6b5826f9a0:da39a3ee5e6b4b0d3255bfef95601890afd80709#1a37bb11a69909708d9763cf36c60d2b - Download (nexspence-group)

nexspence-demo/1.0.0: Retrieving package da39a3ee5e6b4b0d3255bfef95601890afd80709 from remote 'nexspence-group'
nexspence-demo/1.0.0: Downloaded package revision 1a37bb11a69909708d9763cf36c60d2b
```

That download is a walk down the tree, one request per file:

```
GET  /repository/conan-group/v1/ping
GET  /repository/conan-group/v2/conans/nexspence-demo/1.0.0/_/_/latest
GET  /repository/conan-group/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/files
GET  /repository/conan-group/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/files/conanmanifest.txt
GET  /repository/conan-group/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/files/conanfile.py
GET  /repository/conan-group/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/packages/<pkgid>/latest
GET  /repository/conan-group/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/packages/<pkgid>/revisions/<prev>/files
GET  /repository/conan-group/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/packages/<pkgid>/revisions/<prev>/files/conanmanifest.txt
GET  /repository/conan-group/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/packages/<pkgid>/revisions/<prev>/files/conaninfo.txt
GET  /repository/conan-group/v2/conans/nexspence-demo/1.0.0/_/_/revisions/<rrev>/packages/<pkgid>/revisions/<prev>/files/conan_package.tgz
```

The client learns the recipe revision from `/latest`, lists the files of that
revision, then resolves the binary: the package ID it needs is computed from
the recipe and the profile, and the server answers which package revision
holds it.

Search works the same way, over the recipe tree:

```bash
conan search "nexspence*" -r nexspence-hosted
```

```
Found 1 pkg/version recipes matching nexspence* in nexspence-hosted
nexspence-hosted
  nexspence-demo
    nexspence-demo/1.0.0
```

The pattern is a shell-style glob (`*`, `?`, `[seq]`), matched against
`name/version` for the default user and channel and against
`name/version@user/channel` otherwise. The server does the matching:

```bash
curl -s -u admin:admin123 \
  "http://localhost:8081/repository/conan-hosted/v2/conans/search?q=nexspence*"
```

```json
{"results":["nexspence-demo/1.0.0"]}
```

A group repository serves the **union** of its members' results — see
[how a group repository answers](#6-how-a-group-repository-answers).

## 6. How a group repository answers

A group repository is an ordered union of its members. Two rules decide what
a client sees, and they are different:

- **Members are asked in order** — hosted members first, then proxy members,
  each in `member_names` order. The first member that answers with something
  other than `404` wins, and the response carries the name of the member
  that answered it in `X-Nexspence-Source`. A recipe the hosted member does
  not have therefore falls through to the proxy member:

  ```bash
  curl -s -i http://localhost:8081/repository/conan-group/v2/conans/zlib/1.2.11/_/_/latest
  ```

  ```
  HTTP/1.1 200 OK
  X-Nexspence-Source: conan-proxy

  {
    "revision" : "fca992a7d96a1b92bd956caa8a97d18f",
    "time" : "2024-01-23T08:39:54.642+0000"
  }
  ```

  The hosted member answered `404` for that reference, so the group asked the
  proxy member, and the proxy member fetched it from the upstream.

- **The recipe search is merged across the members.** The repository-wide
  search (`/v2/conans/search`) is the one aggregated document of the Conan
  protocol, so the group serves the **union** of the members' result lists
  instead of the first member's answer:

  ```bash
  curl -s http://localhost:8081/repository/conan-group/v2/conans/search?q=zlib*
  ```

  ```json
  {"results":["zlib/1.2.11"]}
  ```

  Three details of that merge:

  - the members are asked in the same order as above, and **the first member
    that holds a recipe wins it** — a recipe in the hosted member shadows the
    same reference in the proxy member;
  - an empty result list is an empty contribution: the hosted member having
    nothing to report no longer hides what the proxy member has;
  - `X-Nexspence-Source` names every member that **answered** — including one
    whose result list was empty (for `?q=zlib*` the hosted member contributes
    nothing and is still named):

    ```
    HTTP/1.1 200 OK
    X-Nexspence-Source: conan-hosted,conan-proxy
    ```

- **The package-level search is not merged.** `/v2/conans/<ref>/search`
  describes the binaries of **one** recipe, and each member answers for its
  own revision of it — a union would mix package IDs of revisions the group
  does not serve. It keeps the first-non-404 fan-out, which already reaches
  the member that holds the recipe:

  ```bash
  curl -s -i "http://localhost:8081/repository/conan-group/v2/conans/zlib/1.2.11/_/_/search?list_only=true"
  ```

  ```
  HTTP/1.1 200 OK
  X-Nexspence-Source: conan-proxy
  ```

- **The v1 manifest is answered by the hosted member.** A hosted member
  answers `GET /v1/conans/<ref>` with a synthetic manifest for any
  reference, so it shadows the upstream's real one:

  ```bash
  curl -s http://localhost:8081/repository/conan-group/v1/conans/zlib/1.2.11/_/_
  ```

  ```json
  {"reference":"zlib/1.2.11@_/_"}
  ```

  Use the v2 protocol to resolve a reference the hosted member does not have.

- **Writes go to the hosted member.** A `PUT` into the group is forwarded to
  the first hosted member, so a group can be used as a publish target — see
  the section on publishing through a group below.

## 7. The proxy

`conan-proxy` sits in front of `https://center.conan.io` and caches what it
fetches:

```bash
curl -s http://localhost:8081/repository/conan-proxy/v1/conans/zlib/1.2.11/_/_
```

```json
{
  "conan_export.tgz" : "a964f6311af80bec4911c883dfa6f933",
  "conan_sources.tgz" : "9e108336c2556d1a7ffa94c6b8185606",
  "conanmanifest.txt" : "927692850fe882423b0ea97848414d0e",
  "conanfile.py" : "d9bb57a4fbc1697dfdb568cac8692873"
}
```

Two limits decide what a client can do through the proxy:

- **The proxy answers the v2 search itself.** The upstream's own search
  would have to be fetched without the pattern (see below), so the proxy
  reports the recipes it has cached under the v2 tree instead — the ones
  that have been resolved through it. The pattern is applied locally:

  ```bash
  curl -s "http://localhost:8081/repository/conan-proxy/v2/conans/search?q=zlib*"
  ```

  ```json
  {"results":["zlib/1.2.11"]}
  ```

  A recipe the proxy has never fetched is not in that list — the upstream
  catalogue is not mirrored, only the parts that were asked for.

  Resolving a recipe by name works, though — the proxy forwards the v2
  revision paths to the upstream and caches what comes back:

  ```bash
  conan install --requires=zlib/1.2.11 -r nexspence-proxy
  ```

  ```
  zlib/1.2.11#fca992a7d96a1b92bd956caa8a97d18f - Downloaded (nexspence-proxy)
  ```

  That is the recipe. The binary is a separate question, and the upstream
  decides it: the Conan Center has no build of `zlib/1.2.11` for every
  configuration, so a profile it does not cover ends in `ERROR: Missing
  binary`. `conan install --build=missing` builds it from source instead —
  though an old upstream recipe can fail against a new toolchain
  (`zlib/1.2.11` asks CMake for a policy older than CMake 5 accepts).

- **The proxy's v1 search drops the query string.** The upstream fetch is
  made without the pattern, so every pattern returns the same catalogue —
  the whole Conan Center index, however many references it holds:

  ```bash
  curl -s "http://localhost:8081/repository/conan-proxy/v1/conans/search?q=boost*"
  ```

  A pattern that matches nothing upstream answers exactly like one that
  matches everything.

A proxy repository is read-only. A write into it is refused:

```bash
curl -s -o /dev/null -w "%{http_code}\n" -u admin:admin123 -X PUT \
  --data-binary "x" \
  http://localhost:8081/repository/conan-proxy/files/x/1.0/_/_/0/export/conanfile.py
```

```
405
```

## 8. Anonymous access

A proxy or group repository with `"allowAnonymous": true` can be read without
credentials, which is what you want for a shared build agent. A hosted
repository should keep `"allowAnonymous": false` so that publishing stays
authenticated — with it, *every* request answers `401`, the ping included:

```bash
curl -s -o /dev/null -w "%{http_code}\n" \
  http://localhost:8081/repository/conan-hosted/ping
```

```
401
```

Read access is then granted per role — see
[security-rbac.md](../security-rbac.md).

### Publishing through a group

The group forwards writes to its hosted member, and its credential handshake
works: the group passes the authenticated principal on to the member that
answers it, so a client logs in to a group exactly as it logs in to a hosted
repository.

```bash
conan remote login nexspence-group admin -p admin123
```

```
Authenticated in remote 'nexspence-group' with user 'admin'
Changed user of remote 'nexspence-group' from 'None' (anonymous) to 'admin' (authenticated)
```

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/conan-group/v2/users/check_credentials
```

```
admin
```

With that login, a publish through the group works:

```bash
conan upload nexspence-demo/1.1.0 -r nexspence-group
```

The recipe lands in the group's hosted member — a group has no storage of
its own:

```bash
curl -s -o /dev/null -w "%{http_code}\n" -u admin:admin123 \
  http://localhost:8081/repository/conan-hosted/v2/conans/nexspence-demo/1.1.0/_/_/latest
```

```
200
```

A group with `"allowAnonymous": true` can be **read** without credentials;
publishing through it always needs the login above.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `Remote ... doesn't seem like a valid Conan remote` | The ping answered `200` without `X-Conan-Server-Capabilities` | The URL does not point at a Conan repository — check the host and the `/repository/<name>/` path |
| `401` on every request to the hosted repository | The repository is not anonymous and the client is not logged in | `conan remote login nexspence-hosted <user> -p <password>` |
| `Remote 'x' needs authentication, obtaining credentials` and the client hangs | The remote is not logged in and the client is waiting on a prompt | Log the remote in first, or answer the prompt; in CI pass `-p` to `conan remote login` |
| `405 Client Error: Method Not Allowed` on upload | You are publishing into a **proxy** repository | Proxy repositories are read-only — publish to the hosted one, or through the group |
| `conan search` through the group does not list an upstream recipe | The proxy member only reports the recipes it has cached under the v2 tree | Fetch the recipe once (`conan install --requires=<ref> -r nexspence-group`) — it is listed afterwards |
| The group's v1 manifest names a recipe the hosted member does not have | A hosted member answers `GET /v1/conans/<ref>` with a synthetic manifest for any reference | Resolve the reference over the v2 protocol (`/v2/conans/<ref>/latest`) |
| The proxy's search returns the whole catalogue | The proxy's upstream fetch drops the query string | Filter the answer yourself; the pattern is not applied |
| `ERROR: Missing binary` for a recipe from the upstream | The Conan Center has no build for that configuration | `conan install --build=missing`, or use a configuration the upstream covers |
| A Conan 1.x client cannot find what a Conan 2.x client uploaded | The two protocol versions write into separate trees | Upload and download with the same protocol version |
| `Package 'x' not resolved` in `conan install` | The package ID of the local profile has no binary in that repository | Build and upload it for that configuration, or use `--build=missing` |

## Related

- [RBAC and content selectors](../security-rbac.md)
- [Maven guide](maven.md)
- [Migration from Sonatype Nexus](../nexus-migration.md)
- [API specification](../api-spec.yaml)
