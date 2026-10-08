# Hosted APT Repositories

A hosted `apt` repository stores `.deb` files and generates the indexes apt
reads: `dists/<dist>/Release`, `InRelease`, and
`dists/<dist>/main/binary-<arch>/Packages(.gz)`. To sign them, see
[apt-signing.md](apt-signing.md).

## Upload

```bash
curl --fail -u "$USER:$TOKEN" -T libfoo2_2.3.1-1_amd64.deb \
  "$NEXSPENCE/repository/apt-hosted/pool/main/libfoo2_2.3.1-1_amd64.deb"
```

A `PUT` to any path under `/pool/` stores the file there. A `PUT` to the
repository root, or a Nexus-style multipart `POST` with a `file` field, is
filed under `/pool/main/<prefix>/<Package>/<filename>`.

## What goes into `Packages`

Each `.deb` is read on upload, the same way `dpkg-scanpackages` reads it: the
`control` file inside `control.tar`, `control.tar.gz`, `control.tar.xz` or
`control.tar.zst`. Its stanza in `Packages` is that control paragraph as
written, so `Depends`, `Pre-Depends`, `Recommends`, `Conflicts`, `Breaks`,
`Provides`, `Replaces`, `Installed-Size` and `Description` reach apt. The
server then appends the fields it computes from the stored file: `Filename`,
`Size`, `MD5sum`, `SHA1` and `SHA256`.

`Package`, `Version` and `Architecture` come from the control file, never from
the filename. A file named `libfoo2_2.3.1-1+build5_amd64.deb` whose control
says `Version: 2.3.1-1` is indexed as `2.3.1-1`, so `Depends: libfoo2 (= 2.3.1-1)`
elsewhere resolves. The architecture decides which `binary-<arch>` index lists
the package. `Architecture: all` packages appear in every index.

## One file per package identity

apt can index only one file per `Package`, `Version` and `Architecture`. A
second file that claims an identity already stored under a different path is
refused with `409 Conflict`, naming the path that holds it.

A path keeps the identity it was first stored under. Uploading the same path
again with the same identity is a redeploy, which the repository's deployment
policy (`formatConfig.write_policy`) allows or refuses. A redeploy whose
control file names a different package, version or architecture is refused
with `409`; delete the old file first.

A repository is one namespace of identities. Content that reuses package
versions with different bytes needs its own repository.

Promotion copies files without going through the upload checks. If it brings
a second file for an identity the target already holds, the index still lists
one stanza for it, the one at the first path in sort order.

## Components, path selectors and retention

A deb uploaded with a control file is a component whose `group` is its
architecture. Path-based selectors in promotion rules therefore see
`/<Architecture>/<Package>` (for example `/amd64/libfoo2`), and cleanup
retention ("keep the newest N versions") counts per architecture.

## Rejected uploads

| Upload | Response |
|---|---|
| An `ar` archive whose control file is missing, malformed, not UTF-8 text, has more than one paragraph or more than one `control` entry, or lacks `Package`, `Version` or `Architecture` | `400` |
| A control member over the in-memory limits: 32 MiB for the archive head, 16 MiB for the decompressed `control.tar`, 1 MiB for the control file, or an xz dictionary over 64 MiB | `413` |

A body that is not an `ar` archive at all is stored as before, with its
coordinates taken from the `name_version_arch.deb` filename and a minimal
stanza. It follows the same one-file-per-identity rule.

## Packages uploaded before this behavior

Debs stored by earlier versions keep the minimal filename-derived stanza
(`Package`, `Version`, `Architecture`, `Filename`, `Size` and checksums), and
a component with an empty `group`: path selectors see `//<Package>`, and their
versions are retained separately from newly uploaded ones. They keep their
identity: a new upload of the same package, version and architecture is
refused under another path, and under the same path too, since the stored file
is filed under different coordinates. Delete the old file, then upload it
again to get the full stanza.
