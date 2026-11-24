# APT guide — suites, indexes and signing

An APT repository is a directory tree that `apt` fetches by convention:

```
dists/<suite>/Release
dists/<suite>/InRelease
dists/<suite>/Release.gpg
dists/<suite>/main/binary-<arch>/Packages
dists/<suite>/main/binary-<arch>/Packages.gz
pool/main/<package>_<version>_<arch>.deb
```

Nexspence generates that tree from the `.deb` files you upload — you never
write an index by hand. One repository can serve several **suites** (release
lines) from one shared `pool/`.

The technical details of what ends up in `Packages` and how uploads are
validated are in [apt-hosted.md](../apt-hosted.md); the signing setup is in
[apt-signing.md](../apt-signing.md). This guide is the client-facing walk
through.

Assumes a fresh instance on `http://localhost:8081`.

## 1. Create the repository

```bash
BASE_URL=http://localhost:8081 ./scripts/create-apt-repos.sh
```

That creates `apt-hosted`, `apt-proxy` (in front of `deb.debian.org`) and
`apt-group`.

By hand, with the v1-compatible API:

```bash
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/apt/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"apt-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Internal packages\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"
```

## 2. Publish a `.deb`

```bash
curl -u admin:admin123 --fail -T nexspence-demo_1.0.0_amd64.deb \
  "http://localhost:8081/repository/apt-hosted/pool/main/nexspence-demo_1.0.0_amd64.deb?suite=nexspence-1.0"
```

The `suite` query parameter names the release line the package belongs to.
Without it, the package joins **every** suite the repository serves.

The server reads the `control` file inside the `.deb` — the same way
`dpkg-scanpackages` does — so `Package`, `Version`, `Architecture`,
`Maintainer`, `Section`, `Priority` and `Description` come from the package
itself, not from the filename.

## 3. The index

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/apt-hosted/dists/nexspence-1.0/main/binary-amd64/Packages
```

```
Package: nexspence-demo
Version: 1.0.0
Architecture: amd64
Maintainer: Nexspence <apt@example.com>
Section: utils
Priority: optional
Description: demo package for the apt guide
Filename: pool/main/nexspence-demo_1.0.0_amd64.deb
Size: 784
MD5sum: 50f023e0652885f988ee91a9d0d66d1f
SHA1: ffdcce326de4a580b100c34054309a79eeee7465
SHA256: cd31666874058e9a096ae7a85e553ddef004daa0425caafe8a5b99cb687c6dc3
```

`Filename` is **relative to the repository root** — no leading slash, no host.
`apt` resolves it against the base URL of the source line, so the file lands
at:

```
http://localhost:8081/repository/apt-hosted/pool/main/nexspence-demo_1.0.0_amd64.deb
```

The release document:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/apt-hosted/dists/nexspence-1.0/Release
```

```
Origin: Nexspence
Label: Nexspence
Suite: nexspence-1.0
Codename: nexspence-1.0
Date: Tue, 24 Nov 2026 08:09:39 UTC
Architectures: amd64 all
Components: main
Description: Nexspence APT Repository
MD5Sum:
 2ef89b42e41986e03d478cdcb26839c5 800 main/binary-amd64/Packages
 59c1b2e5224d37aa4c8c50ea200d7550 407 main/binary-amd64/Packages.gz
 d41d8cd98f00b204e9800998ecf8427e   0 main/binary-all/Packages
```

`Architectures` lists the architectures the suite actually holds, plus `all`
for architecture-independent packages. `Components` lists the components —
`main` is the only one.

A suite name that is not a valid name (`not a suite`) answers `404`. A valid
name that holds no packages answers `200` with an empty index.

## 4. The apt source line

The source line has three parts: the transport, the **suite**, and the
**component**.

```
deb http://localhost:8081/repository/apt-hosted nexspence-1.0 main
```

- `deb` — download the package
- `http://localhost:8081/repository/apt-hosted` — the repository
- `nexspence-1.0` — the suite
- `main` — the component

The suite in the source line is the one the client subscribes to. A client
that names `nexspence-1.0` sees the packages of that suite and no other, so
`apt upgrade` stays inside its release line.

Write it down:

```bash
echo "deb [trusted=yes] http://localhost:8081/repository/apt-hosted nexspence-1.0 main" \
  | sudo tee /etc/apt/sources.list.d/nexspence.list

sudo apt update
```

`[trusted=yes]` tells apt to accept the repository without a signature. It is
the right choice for a test, and the wrong one for anything else — use
`signed-by` instead, below.

### A private repository

A hosted repository with `"allowAnonymous": false` answers `401`. `apt` takes
the credentials from `/etc/apt/auth.conf`:

```
machine http://localhost password <token>
```

The `http://` prefix is **required** for a plain-HTTP repository. Without it
apt matches the host but refuses to send the credentials over an unencrypted
connection:

```
W: Credentials for localhost match, but the protocol is not encrypted.
   Annotate with http:// to use.
```

With HTTPS, the plain form works:

```
machine repo.example.com login <user> password <token>
```

The same credentials in the source line itself work too:

```
deb [user=admin password=admin123 trusted=yes] http://localhost:8081/repository/apt-hosted nexspence-1.0 main
```

## 5. Installing

```bash
sudo apt update
apt-cache policy nexspence-demo
```

```
nexspence-demo:
  Installed: (none)
  Candidate: 2.0.0
  Version table:
     2.0.0 500
        500 http://localhost:8081/repository/apt-hosted nexspence-1.0/main amd64 Packages
```

```bash
sudo apt install nexspence-demo
```

```
Setting up nexspence-demo (2.0.0) ...
```

`apt` reads `Depends`, `Pre-Depends`, `Recommends`, `Conflicts`, `Breaks`,
`Provides` and `Replaces` from the control file, so dependency resolution
works the way it does with a distribution repository.

## 6. Signing the suite

An unsigned repository only works with `[trusted=yes]`. Configure a GPG key
on the repository and the server signs the release metadata — the full
procedure is in [apt-signing.md](../apt-signing.md); this is the client side
of it.

```bash
curl -fsSL http://localhost:8081/repository/apt-hosted/public.gpg \
  | gpg --dearmor | sudo tee /usr/share/keyrings/nexspence.gpg > /dev/null

echo "deb [signed-by=/usr/share/keyrings/nexspence.gpg] http://localhost:8081/repository/apt-hosted nexspence-1.0 main" \
  | sudo tee /etc/apt/sources.list.d/nexspence.list

sudo apt update
```

What the client fetches:

| Path | Content |
|---|---|
| `/repository/<repo>/dists/<suite>/Release` | the release document |
| `/repository/<repo>/dists/<suite>/InRelease` | the same document, clearsigned |
| `/repository/<repo>/dists/<suite>/Release.gpg` | detached signature, `404` when unsigned |
| `/repository/<repo>/public.gpg` | the public half of the signing key, `404` when unsigned |

`signed-by` points apt at the keyring it should trust for this source only —
not the system-wide `/etc/apt/trusted.gpg.d/`. Signatures are SHA-256; apt
rejects SHA-1.

## 7. What the `Filename` and the release date do when a package is deleted

Delete the newest package:

```bash
curl -u admin:admin123 -X DELETE \
  http://localhost:8081/repository/apt-hosted/pool/main/nexspence-demo_2.0.0_amd64.deb
```

The answer is `204 No Content`, and the index is regenerated:

```
Package: nexspence-demo
Version: 1.0.0
Filename: pool/main/nexspence-demo_1.0.0_amd64.deb
```

- **the deleted version leaves the index** — the stanza is gone, and
  `apt-cache policy` no longer offers it
- **the `Filename` of the remaining package is unchanged** — it is still
  `pool/main/nexspence-demo_1.0.0_amd64.deb`, because `Filename` names the
  path the file is stored at, not its place in a list
- **the `Release` `Date` moves forward** — from `Tue, 24 Nov 2026 08:09:39
  UTC` to `Tue, 24 Nov 2026 08:09:42 UTC`

That last one matters. The `Date` follows the newest package in the suite
**and** the latest change to suite membership anywhere in the repository. A
deletion is a membership change, so the `Date` becomes the moment of that
change. `apt` compares the `Date` against the one it cached, sees a newer
one, and refetches the index — which is what makes a removal visible to
clients that would otherwise keep the stale package.

`Release` is byte-identical between requests as long as nothing changed, so
apt can verify `Release.gpg` against the `Release` it fetched earlier.

To take a file out of one suite but keep it stored:

```bash
curl -u admin:admin123 -X DELETE \
  "http://localhost:8081/repository/apt-hosted/pool/main/nexspence-demo_1.0.0_amd64.deb?suite=nexspence-1.0"
```

## 8. The proxy and the group

A proxy repository sits in front of an upstream repository and caches what it
fetches:

```bash
curl -s http://localhost:8081/repository/apt-proxy/dists/bookworm/Release
```

A group repository merges its members' suites, so a client can use one
source line for both your own packages and the upstream's.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `401 Unauthorized` on every fetch | The repository is not anonymous | Add the credentials to `/etc/apt/auth.conf` |
| `Credentials for … match, but the protocol is not encrypted` | `auth.conf` has no protocol prefix | `machine http://host login … password …` |
| `NO_PUBKEY` | The key is not in the keyring, or `trusted=yes` is missing | Install `public.gpg` and use `[signed-by=…]` |
| `E: Unable to locate package …` | The package is in another suite, or the component is not `main` | Check the suite and component in the source line |
| `E: Some index files failed to download` | The suite name does not exist | `GET dists/<suite>/Release` to see whether it does |
| `Hash Sum mismatch` | The `.deb` was replaced under the same identity | Upload under a new version |
| `409 Conflict` on upload | The package identity is already stored under another path | Upload to that path, or delete it first |
| The client still offers a deleted version | The client did not refresh | `apt update` — the `Release` `Date` moves on a deletion |
| `400` on upload | The body is not a `.deb` with a readable control file | Build the package with `dpkg-deb --build` |

## Related

- [Signing an APT repository](../apt-signing.md)
- [Hosted APT repositories](../apt-hosted.md)
- [RBAC and content selectors](../security-rbac.md)
- [API specification](../api-spec.yaml)
