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

## Suites

One repository can serve several release lines, each as its own suite
(`dists/<suite>/`), from one shared `pool/`. Name the suite when uploading:

```bash
curl --fail -u "$USER:$TOKEN" -T acme_1.5.1_all.deb \
  "$NEXSPENCE/repository/apt-acme/pool/main/acme_1.5.1_all.deb?suite=acme-1.5.1"
```

A client then picks the line with its sources entry, and `apt upgrade` stays
within it:

```
deb [signed-by=/usr/share/keyrings/acme.gpg] https://repo.example.com/repository/apt-acme acme-1.5.1 main
```

`dists/<suite>/Release`, `InRelease`, `Release.gpg` and
`main/binary-<arch>/Packages(.gz)` list only the debs of that suite. Groups
pass the suite through to their members.

| Upload with `?suite=…` | Result |
|---|---|
| A package, version and architecture not stored yet | Stored, in the named suites (`201`) |
| The same file (same SHA-256) at the path it is stored at | Nothing is stored; the file joins the named suites (`200`) |
| The same package, version and architecture at another path | `409`, naming the path that holds it: upload it there to add suites |
| Other content at the same path (a redeploy) | The deployment policy decides; the file keeps its suites and joins the named ones |
| A body that is not a Debian package with a control file | `400` |
| Any upload naming a suite, to a read-only repository | `400`, as any write to it |

A new file's suites are recorded together with it, so it is never listed in
a suite it was not uploaded to, not even for a moment.

`suite` can be repeated (`?suite=acme-1.5&suite=acme-1.5.1`). A suite name is
letters, digits and `.` `_` `+` `~` `-`, starting with a letter or digit, up
to 100 characters; anything else is refused with `400`.

To take a file out of one suite without deleting it:

```bash
curl -X DELETE -u "$USER:$TOKEN" \
  "$NEXSPENCE/repository/apt-acme/pool/main/acme-toolchain_1.0_amd64.deb?suite=acme-1.4"
```

A file taken out of its last suite stays stored but is listed in no suite. A
`DELETE` without `suite` deletes the file, from every suite. A redeploy keeps
the suites a file is in, whether or not it names any.

A suite's `Release` `Date` follows the newest package in the suite and the
latest change to suite membership anywhere in the repository, so apt sees a
newer `Release` whenever a suite changes, including when a file leaves it.
`dists/<name>/` with a name that is not a valid suite name is `404`.

Suites belong to the repository a file is listed in. Promotion copies a deb
without its source suites: a new copy is listed in every suite of the
target, and a copy that replaces a target file keeps that file's suites.

### Debs uploaded without a suite

A deb uploaded without `suite` belongs to every suite, so a repository that
never names one serves the same index under any `dists/<name>/`, as before.
Uploading such a file again with `?suite=…` records its suites, and from then
on it is listed under those only: name every suite clients already use for it.

A package identity is still one file across the whole repository: two release
lines cannot ship different builds of the same package, version and
architecture. Give the builds different versions, or use separate
repositories.

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
identity: a new upload of the same package, version and architecture under
another path is refused.

Uploading the same package, version and architecture again at its existing
path upgrades it in place, as a redeploy: it gets the full stanza and moves to
the component its control file names. The deployment policy still decides
whether that redeploy is allowed. In a write-once repository, delete the old
file first.
