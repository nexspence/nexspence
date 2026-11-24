# Conda guide — channels, platforms and the repodata index

A working Conda setup against Nexspence: how to create the repositories, how
to publish, how to resolve, and what breaks when it does not.

Everything below assumes a fresh instance on `http://localhost:8081` (the default
port of the Docker Compose quick start). Replace the host everywhere if yours
differs.

## 1. Create the repositories

Nexspence ships a helper that creates all three repository types with the right
payloads:

```bash
./scripts/create-conda-repos.sh
```

It defaults to `http://localhost:8081` — point it at your instance with
`BASE_URL` when it differs:

```bash
BASE_URL=https://nexus.example.com ./scripts/create-conda-repos.sh
```

That script creates `conda-hosted`, `conda-proxy` (in front of
`https://conda.anaconda.org/conda-forge`) and `conda-group` — the three names
this guide uses.

Under the hood it does three `POST` calls against the v1-compatible API. This is
the same thing by hand, and it is worth knowing because the API is what CI
scripts talk to:

```bash
# resolve the blob store id first — a repository must reference an existing one
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# hosted
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/conda/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"conda-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Conda hosted repository\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of conda-forge
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/conda/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"conda-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Proxy to conda-forge\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://conda.anaconda.org/conda-forge\" }
  }"

# group: one URL for both
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/conda/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"conda-group\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Group of releases and conda-forge\",
    \"formatConfig\": { \"member_names\": [\"conda-hosted\", \"conda-proxy\"] }
  }"
```

Each of them answers `201 Created` with the repository it created. On a server
where a repository of that name already exists the API answers `409 Conflict`
instead, and the helper script above skips it.

## 2. The channel layout

A Conda channel is a directory of packages and an index. In Nexspence the
channel is the repository URL, and everything lives one level below it, in a
subdirectory named after the **platform**:

| What | URL |
|---|---|
| the channel — what goes into `.condarc` or `-c` | `http://localhost:8081/repository/conda-group/` |
| the channel index | `http://localhost:8081/repository/conda-group/<platform>/repodata.json` |
| a package file | `http://localhost:8081/repository/conda-group/<platform>/<filename>` |

Two things follow from that table, and both are worth stating separately.

**The channel root holds neither the index nor the packages.** It is not a flat
file store: every path under a Conda repository is `/<platform>/<file>`, so the
root of the channel answers `400`:

```bash
curl -u admin:admin123 http://localhost:8081/repository/conda-hosted/repodata.json
```

```json
{"error":"path must be /<platform>/<file>"}
```

The index and the package files are therefore not two different places either:
they share the platform subdirectory. The index is the document
`<platform>/repodata.json` inside it, and the package is the file next to it.
The distinction that matters is between the **channel root** and the **platform
subdirectory** — and it decides what you put into `.condarc`.

**The platform subdirectory is chosen by the client, not by you.** A Conda
client looks at the machine it runs on, picks the matching subdir
(`osx-arm64`, `linux-64`, `linux-aarch64`, `win-64`, …) and fetches the index
of that subdir *and* of `noarch`:

```bash
conda info | grep platform
```

```
               platform : osx-arm64
```

A request that reaches the server therefore looks like this, for a client on
Apple Silicon:

```
GET /repository/conda-group/osx-arm64/repodata.json
GET /repository/conda-group/noarch/repodata.json
```

`noarch` is where a package that is not tied to a platform belongs. A
`noarch` package installs on every platform, which is why the demo package in
this guide is published under `noarch/`.

## 3. Configure the client

### `~/.condarc` — the channel list

```yaml
channels:
  - http://admin:admin123@localhost:8081/repository/conda-hosted/
default_channels: []
channel_priority: strict
repodata_use_shards: false
```

- `channels` — the channels the client consults, highest priority first. With
  `channel_priority: strict` the first channel that carries the package wins,
  and the client stops there. The credentials in the URL are what the hosted
  repository asks for — see [credentials](#credentials) below.
- `default_channels: []` — clears the default `defaults` channel, so the
  client does not reach for `repo.anaconda.com` on its own. The channel list
  above is then the whole world the client sees.
- `repodata_use_shards: false` — see [the group](#8-how-a-group-merges). A
  current Conda client asks for a sharded index first, and a group does not
  merge that document; without this setting the packages of a hosted member
  stay invisible.

The same file, written by the client itself:

```bash
conda config --add channels http://admin:admin123@localhost:8081/repository/conda-hosted/
conda config --set channel_priority strict
conda config --show channels
```

```
channels:
  - http://admin:admin123@localhost:8081/repository/conda-hosted/
```

The list also carries whatever the installer put in it — Miniforge, for one,
adds `conda-forge`.

### `--override-channels`

`--override-channels` makes the client ignore the channel list and use only
what the command line names. It is the way to keep a resolution inside one
repository — and it insists on a channel of its own:

```bash
conda install -p ./env nexspence-demo --override-channels
```

```
ArgumentError: At least one -c / --channel flag must be supplied when using --override-channels.
```

So `--override-channels` and `-c` always come as a pair:

```bash
conda install -p ./env nexspence-demo \
  --override-channels -c http://localhost:8081/repository/conda-group/
```

### Credentials

A hosted repository is created with `"allowAnonymous": false`, so the client has
to authenticate. Conda takes the credentials from the channel URL:

```
http://admin:admin123@localhost:8081/repository/conda-hosted/
```

The proxy and the group created here allow anonymous reads, so the credentials
are only needed for `conda-hosted` — but a channel URL that carries them works
for all three.

An API token works in the same place — `nxs_*` tokens are accepted as the
password of a Basic authentication, which is what you want in CI:

```
http://admin:nxs_xxxxxxxxxxxxxxxxxxxx@localhost:8081/repository/conda-hosted/
```

A proxy or group repository created with `"allowAnonymous": true` is read
without credentials.

## 4. Publish

A Conda package is a `.tar.bz2` (or a `.conda`, a zip of zstd streams) that
holds the payload and an `info/` directory. `conda build` produces one, but the
format is simple enough to write by hand for a small package:

```bash
mkdir -p pkg/info/recipe pkg/bin

cat > pkg/info/index.json <<'JSON'
{ "arch": null, "build": "py_0", "build_number": 0, "depends": [], "license": "MIT",
  "name": "nexspence-demo", "noarch": "generic", "platform": null, "subdir": "noarch",
  "timestamp": 1735000000000, "version": "1.0.0" }
JSON

cat > pkg/info/about.json <<'JSON'
{"license":"MIT","summary":"demo package for the conda guide"}
JSON

printf '#!/bin/sh\necho "nexspence-demo 1.0.0"\n' > pkg/bin/nexspence-demo
chmod 755 pkg/bin/nexspence-demo

sha=$(shasum -a 256 pkg/bin/nexspence-demo | cut -d' ' -f1)
size=$(wc -c < pkg/bin/nexspence-demo | tr -d ' ')
cat > pkg/info/paths.json <<JSON
{"paths":[{"_path":"bin/nexspence-demo","path_type":"hardlink","sha256":"$sha","size_in_bytes":$size}],"paths_version":1}
JSON

cat > pkg/info/recipe/meta.yaml <<'YAML'
package:
  name: nexspence-demo
  version: 1.0.0
build:
  number: 0
requirements:
  run: []
about:
  license: MIT
  summary: demo package for the conda guide
YAML

printf 'bin/nexspence-demo\n' > pkg/info/files

# pin the timestamps, so two builds of the same package match
find pkg -exec touch -t 202501010000 {} +
tar -cjf nexspence-demo-1.0.0-py_0.tar.bz2 -C pkg .
```

`info/index.json` is the metadata the client reads. For a `noarch` package
`platform` is `null` — writing `"platform": "noarch"` there makes the client
reject the package with `ValidationError("'noarch' is not a valid Platform")`.

The installer works from `info/paths.json`; `conda build` writes
`info/about.json` and `info/files` as well, and the snippet keeps them so that
it produces the same package the verification run uploaded.

Upload it to the platform subdirectory it belongs to:

```bash
curl -u admin:admin123 -X PUT --data-binary @nexspence-demo-1.0.0-py_0.tar.bz2 \
  http://localhost:8081/repository/conda-hosted/noarch/nexspence-demo-1.0.0-py_0.tar.bz2
```

```json
{"saved":true}
```

The answer is `201 Created`. The package is now part of the channel index:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/conda-hosted/noarch/repodata.json
```

```json
{
  "info": { "subdir": "noarch" },
  "packages": {
    "nexspence-demo-1.0.0-py_0.tar.bz2": {
      "arch": null,
      "build": "py_0",
      "build_number": 0,
      "depends": [],
      "license": "MIT",
      "md5": "610ceb9b079147853eb28e7c4229ab99",
      "name": "nexspence-demo",
      "noarch": "generic",
      "platform": null,
      "sha256": "dba228473a364fa451c61df85ad8703fc3762e0a1bb1ef61aed00f2c74418800",
      "size": 1312,
      "subdir": "noarch",
      "timestamp": 1735000000000,
      "version": "1.0.0"
    }
  },
  "packages.conda": {}
}
```

`md5`, `sha256` and `size` are the checksums of the file that was uploaded. The
tar records the account that built it, so an archive built elsewhere has
different ones — the server always reports the checksums of the bytes it
holds.

You never write that index yourself, and you never upload a checksum sidecar.
The server reads `info/index.json` out of the package, adds the checksums and
the size of the stored file, and serves the result.

A publish to a **group** repository is forwarded to its first hosted member:

```bash
curl -u admin:admin123 -X PUT --data-binary @nexspence-demo-1.0.0-py_0.tar.bz2 \
  http://localhost:8081/repository/conda-group/noarch/nexspence-demo-1.0.0-py_0.tar.bz2
```

```json
{"saved":true}
```

The package lands in `conda-hosted`, and the group's index lists it.

## 5. Install and create

```bash
conda create -y -p ./env nexspence-demo=1.0.0 \
  --override-channels -c http://admin:admin123@localhost:8081/repository/conda-group/
```

```
Channels:
 - http://localhost:8081/repository/conda-group
Platform: osx-arm64
Collecting package metadata (repodata.json): ...working... done
Solving environment: ...working... done

## Package Plan ##

  environment location: ./env

  added / updated specs:
    - nexspence-demo=1.0.0

The following packages will be downloaded:

    package                    |            build
    ---------------------------|-----------------
    nexspence-demo-1.0.0       |             py_0           1 KB  http://localhost:8081/repository/conda-group
    ------------------------------------------------------------
                                           Total:           1 KB

The following NEW packages will be INSTALLED:

  nexspence-demo     repository/conda-group/noarch::nexspence-demo-1.0.0-py_0
```

The client resolved the package out of the `noarch` subdir of the group and
downloaded it from the same URL the index named.

Installing into an environment that already exists works the same way — here it
moves the environment from `1.0.0` to `2.0.0`, with `2.0.0` published the same
way as `1.0.0`:

```bash
conda install -y -p ./env nexspence-demo \
  --override-channels -c http://admin:admin123@localhost:8081/repository/conda-group/
```

```
The following packages will be UPDATED:

  nexspence-demo                                 1.0.0-py_0 --> 2.0.0-py_0
```

```bash
conda list -p ./env
```

```
# packages in environment at ./env:
#
# Name                     Version          Build            Channel
nexspence-demo             2.0.0            py_0             http://localhost:8081/repository/conda-group
```

With the channel in `.condarc` and no `-c` on the command line, the same
resolution works:

```bash
conda create -y -p ./env2 nexspence-demo
```

## 6. What the server does

**The index is generated, not stored.** There is no `repodata.json` file in the
repository. Every `GET .../<platform>/repodata.json` is answered from the
packages the repository holds: the server lists the files of that subdir,
takes each file's `info/index.json` and adds the stored file's `md5`, `sha256`
and `size`. A `.tar.bz2` goes into `packages`, a `.conda` into
`packages.conda`.

**A deletion takes the package out of the index at once.**

```bash
curl -u admin:admin123 -X DELETE \
  http://localhost:8081/repository/conda-hosted/noarch/nexspence-demo-1.0.0-py_0.tar.bz2
```

```
204 No Content
```

The next `GET` of the index no longer lists it — there is no stale index to
refresh.

**A package is keyed by platform, name, version and build.** Two builds of one
version (`py310_0` and `py311_0`) are two packages, because they have
different dependencies. The `.conda` and the `.tar.bz2` of one build are one
package and share a component.

```bash
curl -s -u admin:admin123 \
  "http://localhost:8081/service/rest/v1/components?repository=conda-hosted"
```

```json
{"items":[{"group":"noarch","name":"nexspence-demo","version":"1.0.0-py_0","format":"conda", ...}]}
```

**A package whose metadata cannot be read still lands.** The server falls back
to the filename. A `.tar.bz2` without an `info/` directory at all:

```bash
mkdir -p nometa
printf 'hello\n' > nometa/readme.txt
touch -t 202501010000 nometa/readme.txt
tar -cjf nameless-pkg-3.1.4-py_0.tar.bz2 -C nometa readme.txt

curl -u admin:admin123 -X PUT --data-binary @nameless-pkg-3.1.4-py_0.tar.bz2 \
  http://localhost:8081/repository/conda-hosted/noarch/nameless-pkg-3.1.4-py_0.tar.bz2
```

is read as the package `nameless-pkg`, version `3.1.4`, build `py_0`, and the
index entry is built from that:

```json
{
  "build": "py_0",
  "build_number": 0,
  "depends": [],
  "md5": "c8dad67ff984c2efcb5fae5ca092d376",
  "name": "nameless-pkg",
  "sha256": "833e1974e619630a6f973407fe550d8890b879b4b1d5e2f1f201ec6a9bebc8f0",
  "size": 387,
  "subdir": "noarch",
  "version": "3.1.4"
}
```

The checksums are the ones of the archive that was uploaded, as above.

## 7. The proxy

The proxy sits in front of `https://conda.anaconda.org/conda-forge` and caches
what it fetches:

```bash
curl -s http://localhost:8081/repository/conda-proxy/noarch/repodata.json
```

Three things are worth knowing about it.

- **The index is read from upstream on every request** and is not cached,
  because the index changes when the channel does. An entry that carries a
  `url` or `urls` field is rewritten onto the proxy; the conda-forge entries
  are file-name keyed and carry neither, so the client resolves the file names
  against the channel URL — the download still goes through Nexspence.
- **The compressed index variants are refused.** `repodata.json.bz2` and
  `repodata.json.zst` answer `404` with a pointer to the plain document; the
  client falls back to `repodata.json` on its own.
- **The proxy is read-only.** A publish is `405`:

```bash
curl -u admin:admin123 -X PUT --data-binary "x" \
  http://localhost:8081/repository/conda-proxy/noarch/x-1.0-0.tar.bz2
```

```json
{"error":"proxy repository is read-only (use a hosted repository to publish)"}
```

Package files themselves are cached, so the second download of a package does
not reach upstream.

## 8. How a group merges

A group repository is an ordered union of its members, and it merges two
different things in two different ways.

**The index document is merged.** `repodata.json` of every member is fetched,
parsed and combined into one document — the packages of all members are in it,
not only the packages of the member that answered first:

```bash
curl -s -D - -o /dev/null \
  http://localhost:8081/repository/conda-group/noarch/repodata.json
```

```
X-Nexspence-Source: conda-hosted,conda-proxy
```

**On a package that is in both members, the first member wins.** The merge is
per file name, and the members are consulted in order — hosted members first,
then proxy members, each kind in the order it is listed in
`formatConfig.member_names`. A file name that two members hold is taken from
the first of them, and the file itself is served from that member too.

**A publish goes to the first hosted member**, as shown in
[publish](#4-publish).

**Two index documents are not merged.** The group merges `repodata.json` and
nothing else:

- `current_repodata.json` — the trimmed index an older client asks for first —
  is served from the proxy member alone, so a client that prefers it does not
  see the packages of the hosted member.
- `repodata_shards.msgpack.zst` — the sharded index a current client asks for
  first — is served from the proxy member as well, because it is a file and
  not a document the server knows how to merge.

That is why the client configuration in this guide sets
`repodata_use_shards: false`: with it, the client reads `repodata.json`, which
is the merged document, and the hosted packages are found. Without it, the
client reads the sharded index of the proxy member, and the same command
fails:

```bash
conda create -y -p ./env3 nexspence-demo \
  --override-channels -c http://localhost:8081/repository/conda-group/
```

```
PackagesNotFoundInChannelsError: The following packages are not available from current channels:

  - nexspence-demo
```

One more property of the group is worth knowing before you hand it to a build
agent: a group with `"allowAnonymous": true` serves the packages of its hosted
member to anonymous readers as well. The hosted member's own
`"allowAnonymous": false` does not carry over to the group.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `PackagesNotFoundInChannelsError` although the package is in the index | The client read the sharded repodata, which a group does not merge | Set `repodata_use_shards: false` in `.condarc` |
| `401 Unauthorized` on every fetch of a hosted repository | The repository is not anonymous | Put the credentials in the channel URL, or use a proxy/group that is anonymous |
| `ArgumentError: At least one -c / --channel flag must be supplied when using --override-channels` | `--override-channels` without a channel | Add `-c <channel>` |
| `400` with `path must be /<platform>/<file>` | The URL carries no platform subdirectory | The channel URL is the repository root; the client appends the platform |
| The client looks for the index one level too deep | The platform was put into the channel URL | Configure the repository root, not `<root>/<platform>` |
| `404` on `repodata.json.bz2` or `repodata.json.zst` | The compressed index variants are not served | Nothing to do — the client falls back to `repodata.json` |
| `405 Method Not Allowed` on a publish | You are pushing into a **proxy** repository | Publish to the hosted repository |
| `ValidationError("'noarch' is not a valid Platform")` | A hand-built package puts `"platform": "noarch"` in `info/index.json` | Use `"platform": null` for a `noarch` package |
| The client downloads from `conda.anaconda.org` instead of the proxy | The upstream entry names an absolute URL on another host | The proxy hands such entries back unchanged — the download bypasses the cache |
| The index does not list a package that was published | The package went to another platform subdir | Publish to the subdir of the platform the client runs on |

## Related

- [RBAC and content selectors](../security-rbac.md)
- [Migration from Sonatype Nexus](../nexus-migration.md)
- [GitHub Actions guide](github-actions.md)
- [API specification](../api-spec.yaml)
