# Alpine guide — repositories, the APKINDEX and unsigned indexes

An Alpine ("apk") repository is a directory tree that `apk` fetches by convention:

```
<repository>/<architecture>/APKINDEX.tar.gz
<repository>/<architecture>/<package>-<version>.apk
```

`apk` appends the architecture itself, so the line in `/etc/apk/repositories`
names the repository and `apk` adds `/x86_64/` to it — the architecture of the
machine the client runs on. Alpine's own mirrors put a release directory in
front of that (`/v3.20/main/`); the part of the path before the architecture
is free, and Nexspence indexes every directory separately.

Nexspence generates `APKINDEX.tar.gz` from the `.apk` files you upload — you
never write an index by hand, and the index is generated again for every
request, so an upload or a deletion is visible in the next one.

Assumes a fresh instance on `http://localhost:8081`.

## 1. Create the repository

```bash
BASE_URL=http://localhost:8081 ./scripts/create-alpine-repos.sh
```

That creates `alpine-hosted`, `alpine-proxy` (in front of
`https://dl-cdn.alpinelinux.org/alpine`) and `alpine-group`.

By hand, with the v1-compatible API:

```bash
# resolve the blob store id first — a repository must reference an existing one
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# hosted
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/alpine/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"alpine-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Internal packages\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of the Alpine mirror
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/alpine/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"alpine-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Proxy to the Alpine mirror\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://dl-cdn.alpinelinux.org/alpine\" }
  }"

# group: one URL for both
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/alpine/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"alpine-group\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Group of internal packages and the Alpine mirror\",
    \"formatConfig\": { \"member_names\": [\"alpine-hosted\", \"alpine-proxy\"] }
  }"
```

The hosted repository answers `401` without credentials, the proxy and the
group are anonymous.

## 2. The directory layout

One directory per repository per architecture, and every directory carries its
own index:

```
/repository/alpine-hosted/v3.20/main/x86_64/APKINDEX.tar.gz
/repository/alpine-hosted/v3.20/main/aarch64/APKINDEX.tar.gz
/repository/alpine-hosted/v3.20/main/x86_64/nexspence-demo-1.0.0-r0.apk
```

The directory a `.apk` is uploaded into decides which index it appears in —
`x86_64` and `aarch64` never see each other's packages, and neither do two
release lines under the same repository:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/alpine-hosted/v3.20/main/x86_64/APKINDEX.tar.gz \
  | tar -xzO APKINDEX
```

An empty repository answers `200` with an empty index. A path that is not an
`.apk` answers `405`, an `.apk` that was never uploaded answers `404`.

## 3. Publish a `.apk`

Alpine packages are built with `abuild`, which signs them with a key of your
own. The demo package below is a shell script in `/usr/bin`:

```bash
mkdir -p ~/aports/nexspence-demo && cd ~/aports/nexspence-demo
cat > APKBUILD <<'PKG'
pkgname=nexspence-demo
pkgver=1.0.0
pkgrel=0
pkgdesc="demo package for the alpine guide"
url="https://example.com/nexspence"
arch="x86_64"
license="MIT"
package() {
	mkdir -p "$pkgdir"/usr/bin
	printf '#!/bin/sh\necho "nexspence-demo %s"\n' "$pkgver" > "$pkgdir"/usr/bin/nexspence-demo
	chmod 0755 "$pkgdir"/usr/bin/nexspence-demo
}
PKG

apk add abuild
apk update
abuild-keygen -a -n
abuild -F -r
```

`abuild` does not leave the package in the build directory. It writes it into
the repository directory `$HOME/packages/<repo>/<arch>/`, where `<repo>` is
the name of the directory that holds the package directory — `aports` here
(`abuild` calls the root `REPODEST`, warns that it is not set and defaults it
to `$HOME/packages`):

```bash
find ~/packages -name 'nexspence-demo-*.apk'
```

```
/root/packages/aports/x86_64/nexspence-demo-1.0.0-r0.apk
```

(`/root` because the build above ran as root; it is `$HOME` of the user who
runs `abuild`.)

A second version, to show that `apk` picks the newest one:

```bash
sed -i 's/^pkgver=1.0.0$/pkgver=2.0.0/' APKBUILD
abuild -F -r
find ~/packages -name 'nexspence-demo-*.apk'
```

```
/root/packages/aports/x86_64/nexspence-demo-1.0.0-r0.apk
/root/packages/aports/x86_64/nexspence-demo-2.0.0-r0.apk
```

Upload both — the file name is the name `apk` will ask for later, so it has to
be `<pkgname>-<pkgver>.apk`:

```bash
cp ~/packages/aports/x86_64/nexspence-demo-1.0.0-r0.apk .
curl -u admin:admin123 -X PUT \
  http://localhost:8081/repository/alpine-hosted/v3.20/main/x86_64/nexspence-demo-1.0.0-r0.apk \
  --upload-file nexspence-demo-1.0.0-r0.apk

cp ~/packages/aports/x86_64/nexspence-demo-2.0.0-r0.apk .
curl -u admin:admin123 -X PUT \
  http://localhost:8081/repository/alpine-hosted/v3.20/main/x86_64/nexspence-demo-2.0.0-r0.apk \
  --upload-file nexspence-demo-2.0.0-r0.apk
```

Each answer is `201 Created`.

The server reads the `.PKGINFO` inside the package, the same way `apk index`
does — `pkgname`, `pkgver`, `pkgdesc`, `license`, `size` and `depend` come
from the package itself, not from the file name.

## 4. The index

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/alpine-hosted/v3.20/main/x86_64/APKINDEX.tar.gz \
  | tar -xzO APKINDEX
```

```
C:Q1QjoDgWgBAckL3Ro55/sZkzjUXGw=
P:nexspence-demo
V:1.0.0-r0
A:x86_64
S:1432
I:4096
T:demo package for the alpine guide
L:MIT
p:cmd:nexspence-demo=1.0.0-r0

C:Q1vHuzy+hUHi7QECMxnNyd98X8gdo=
P:nexspence-demo
V:2.0.0-r0
A:x86_64
S:1428
I:4096
T:demo package for the alpine guide
L:MIT
p:cmd:nexspence-demo=2.0.0-r0
```

One stanza per package, blank line separated, the format `apk` reads. The
checksum and the size belong to the bytes that were uploaded — a rebuild of
the same source produces a different signature and therefore different
values. The fields that matter:

| Field | Meaning |
|---|---|
| `C:` | the checksum of the package's control segment — `Q1` followed by the base64 of its SHA-1 |
| `P:` | the package name, from `.PKGINFO` |
| `V:` | the version, from `.PKGINFO` |
| `A:` | the architecture, taken from the directory the package lives in |
| `S:` | the size of the stored `.apk` |
| `I:` | the installed size, from `.PKGINFO` |
| `T:` | the description |
| `L:` | the license |
| `D:` | the dependencies |
| `p:` | what the package provides |

The `C:` field is the one that decides whether a client accepts a package:
`apk` recomputes the SHA-1 over the same control segment of the file it
downloaded and compares. Nexspence computes it the way `apk index` does — the
checksum of the control segment, not of the whole file — so the value in the
index is the value `abuild` writes in its own index for the same package.

The index is generated for every request. There is no file to refresh, and no
`apk index` to run on the server: upload a package and the next request for
the index lists it, delete one and the next request no longer lists it.

Nexspence writes only `C`, `P`, `V`, `A`, `S`, `I`, `T`, `L`, `D` and `p`.
The apk index format knows more: `U:` (the home page), `o:` (the origin),
`m:` (the maintainer), `t:` (the build time), `c:` (the commit), `i:`
(install-if) and `k:` (provider priority). For the demo package `abuild`
fills in three of them — `U:`, `o:` and `t:`. `c:` needs a git checkout and
stays empty, `m:` needs a `# Maintainer:` comment in the APKBUILD and is left
out. The Alpine mirror writes all seven; Nexspence writes none of them.
`apk` treats them as optional and installs the package either way — `apk info`
shows an empty `webpage:` because of the missing `U:`.

## 5. The repositories line

The line in `/etc/apk/repositories` names the repository. `apk` appends the
architecture itself:

```
http://admin:admin123@localhost:8081/repository/alpine-hosted/v3.20/main
```

- `admin:admin123@` — the credentials of the hosted repository, which is not
  anonymous
- `/repository/alpine-hosted` — the repository
- `/v3.20/main` — the directory in front of the architecture; any name works,
  this one mirrors the layout of the Alpine mirror so the hosted and the
  proxied repository can be used through the same group

What `apk` fetches from that line:

| Path | Content |
|---|---|
| `/repository/alpine-hosted/v3.20/main/x86_64/APKINDEX.tar.gz` | the index of that directory |
| `/repository/alpine-hosted/v3.20/main/x86_64/nexspence-demo-2.0.0-r0.apk` | the package |

Write the line down:

```bash
echo "http://admin:admin123@localhost:8081/repository/alpine-hosted/v3.20/main" \
  | sudo tee /etc/apk/repositories
```

`apk` masks the password in everything it prints, so the log shows
`http://admin:*@localhost:8081/...`.

## 6. `apk update` and `apk add`

Nexspence does not sign the index it generates, and `apk` insists on a
signature:

```bash
sudo apk update
```

```
fetch http://admin:*@localhost:8081/repository/alpine-hosted/v3.20/main/x86_64/APKINDEX.tar.gz
WARNING: updating and opening http://admin:*@localhost:8081/repository/alpine-hosted/v3.20/main: UNTRUSTED signature
2 unavailable, 0 stale; 14 distinct packages available
```

`--allow-untrusted` tells `apk` to accept the repository without a signature:

```bash
sudo apk update --allow-untrusted
```

```
fetch http://admin:*@localhost:8081/repository/alpine-hosted/v3.20/main/x86_64/APKINDEX.tar.gz
OK: 16 distinct packages available
```

What the flag does: it switches off the signature check for the index and for
the packages fetched from it. Nothing else changes — the checksum in the index
is still verified against the package that was downloaded, so a package whose
bytes changed on the way is still rejected.

When it is safe to use it: on a test registry, on a network you control, and
for as long as the repository is reachable only over a trusted channel. It is
not safe for a registry that serves anybody else, because the signature is the
only thing that ties the index to a publisher — with it switched off, anybody
who can answer for the repository can serve a different package under the same
name. A signed alternative does not exist in this implementation: the index is
always generated unsigned.

The package itself is a different matter. `abuild` signed it, and `apk`
verifies that signature against the keys in `/etc/apk/keys` — install the
public half of your signing key on the client:

```bash
sudo cp ~/.abuild/*.rsa.pub /etc/apk/keys/
```

Then the package is checked against your key, and only the index is taken on
trust.

Install the package:

```bash
sudo apk add --allow-untrusted nexspence-demo
```

```
(1/1) Installing nexspence-demo (2.0.0-r0)
Executing busybox-1.36.1-r31.trigger
OK: 8 MiB in 15 packages
```

`apk` picks the newest version the index offers. What is installed:

```bash
apk list --installed nexspence-demo
```

```
WARNING: opening from cache http://admin:*@localhost:8081/repository/alpine-hosted/v3.20/main: UNTRUSTED signature
nexspence-demo-2.0.0-r0 x86_64 {nexspence-demo} (MIT) [installed]
```

## 7. The `@tag` in the repositories line

`apk-tools` 3 accepts a tag in front of the URL:

```
@nexspence http://admin:admin123@localhost:8081/repository/alpine-group/v3.20/main
```

What the tag does: it marks the repository as **not used by default**. The
index is still fetched, but the packages in it are masked, and `apk` refuses
to select them:

```
ERROR: unable to select packages:
  nexspence-demo-2.0.0-r0:
    masked in: @nexspence
    satisfies: world[nexspence-demo]
```

A package is taken from a tagged repository when it is asked for by name and
tag:

```bash
sudo apk add --allow-untrusted 'nexspence-demo@nexspence'
```

```
(1/1) Installing nexspence-demo@nexspence (2.0.0-r0)
OK: 8216 KiB in 17 packages
```

The tagged `apk add` writes the constraint into `/etc/apk/world`:

```
alpine-baselayout
alpine-keys
alpine-release
apk-tools
busybox
musl-utils
nexspence-demo@nexspence
```

While the repository is masked, a plain `apk add nexspence-demo` refuses to
select the package — the tag has to be part of the request that asks for it.
Once the package is installed through the tagged repository, the plain command
accepts the pinning:

```
(1/1) Updating pinning nexspence-demo (2.0.0-r0)
```

That plain command rewrites the world entry and drops the tag again — the
entry reads `nexspence-demo` after it, not `nexspence-demo@nexspence`.

When it is safe to use a tag: it is a selection mechanism, not a trust
mechanism. It keeps a repository out of the way until a package is asked for
by name — useful for a repository that carries test builds or packages that
must not be pulled in by a dependency. It does **not** change anything about
signatures: a tagged repository without a signature still needs
`--allow-untrusted`, and `@untrusted` is not a magic word — apk reads it as
the name of the tag and nothing more.

## 8. The proxy and the group

The proxy sits in front of the Alpine mirror and serves the upstream index
unchanged:

```bash
curl -s http://localhost:8081/repository/alpine-proxy/v3.20/main/x86_64/APKINDEX.tar.gz \
  | tar -xzO APKINDEX | grep -c '^P:'
```

```
5525
```

The same count as the mirror's own index — the proxy does not rebuild it, it
caches what the upstream serves. A client can therefore install upstream
packages through it:

```bash
echo "http://localhost:8081/repository/alpine-proxy/v3.20/main" \
  | sudo tee /etc/apk/repositories
sudo apk update --allow-untrusted
sudo apk add --allow-untrusted lua-augeas
```

```
(1/1) Installing lua-augeas (0.1.2-r7)
OK: 8 MiB in 16 packages
```

The group merges the indexes of its members into one document, so a client
uses one line for the internal packages and the upstream ones:

```bash
echo "http://admin:admin123@localhost:8081/repository/alpine-group/v3.20/main" \
  | sudo tee /etc/apk/repositories
sudo apk update --allow-untrusted
sudo apk add --allow-untrusted nexspence-demo
```

```
(1/1) Installing nexspence-demo (2.0.0-r0)
OK: 8 MiB in 16 packages
```

The merged index carries the stanzas of both members — the two internal
packages and the 5525 upstream ones — with the first member winning on a
package name and version it holds twice. The response says which members
contributed:

```bash
curl -s -o /dev/null -D - \
  http://localhost:8081/repository/alpine-group/v3.20/main/x86_64/APKINDEX.tar.gz \
  | grep -i x-nexspence-source
```

```
X-Nexspence-Source: alpine-hosted,alpine-proxy
```

Packages are not merged: a request for a `.apk` goes to the first member that
has it, the hosted member first.

## 9. What a deletion does to the index

```bash
curl -u admin:admin123 -X DELETE \
  http://localhost:8081/repository/alpine-hosted/v3.20/main/x86_64/nexspence-demo-2.0.0-r0.apk
```

The answer is `204 No Content`, and the next request for the index no longer
lists that version — the index is generated from what is stored, so there is
no stale copy to expire. `apk` then offers the version that is left:

```
(1/1) Installing nexspence-demo (1.0.0-r0)
```

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `UNTRUSTED signature` on `apk update` | The index is not signed | `apk update --allow-untrusted` |
| `Permission denied` on every fetch | The hosted repository is not anonymous | Put the credentials in the repositories line: `http://user:pass@host/…` |
| `unable to select packages: … masked in: @tag` | The repository is tagged and the package was not asked for by tag | `apk add 'name@tag'` |
| `package mentioned in index not found` | The `.apk` is stored under a name that is not `<name>-<version>.apk` | Upload it under the name the index entry carries |
| `404` on the `.apk` after a successful upload | The package is in another architecture's directory | Upload it into the directory the client asks for |
| `405 Method Not Allowed` on a write | The path does not end in `.apk`, or the repository is a proxy | Proxy repositories are read-only; publish to the hosted one |
| The index does not list a package that was uploaded | The package is in another directory | The directory decides the index; check the path you uploaded to |
| `WARNING: The indexes contain broken packages which might not function properly` | An old upstream index read by a newer `apk` | Use the `apk` of the Alpine release the mirror directory belongs to |

## Related

- [RBAC and content selectors](../security-rbac.md)
- [API specification](../api-spec.yaml)
