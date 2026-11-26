# Yum / DNF (RPM) guide — hosted, proxy and group repositories

A working `dnf` setup against Nexspence: how to create the repositories, how to
point `dnf` at them instead of the distribution's own mirrors, how to publish an
RPM, and how the client reads the metadata.

Everything below assumes a fresh instance on `http://localhost:8081` (the default
port of the Docker Compose quick start) with the `admin` / `admin123` account.
Replace the host and port everywhere if yours differs. The client outputs are
from dnf 4.14.0 on Rocky Linux 9.9.

The commands were run against a stand on port 8091. The outputs that echo a URL
back show that port — the client container reaches the instance as
`host.docker.internal:8091` — while the commands use the default port.

A Yum repository is a directory of RPM packages plus a `repodata/` directory
that describes them. The client fetches `repodata/repomd.xml` first, follows
the pointers in it to the package metadata, and downloads the RPMs it decides
to install. Nexspence generates that metadata from the files you publish, so
there is no index document to upload and no index to go stale.

Three repository types are covered here: **hosted** (you publish into it),
**proxy** (it relays and caches an upstream mirror such as
`dl.rockylinux.org`) and **group** (one URL in front of several members).

## 1. Create the repositories

Nexspence ships a helper that creates all three repository types with the right
payloads:

```bash
./scripts/create-yum-repos.sh
```

It defaults to `http://localhost:8081` — point it at your instance with
`BASE_URL` when it differs:

```bash
BASE_URL=https://nexus.example.com ./scripts/create-yum-repos.sh
```

The script creates `yum-hosted`, `yum-proxy` (in front of
`https://dl.rockylinux.org/pub/rocky/9/BaseOS/x86_64/os/`) and `yum-group` —
the three names this guide uses. It is idempotent: a repository that already
exists is reported and skipped.

Under the hood it does three `POST` calls against the v1-compatible API. This is
the same thing by hand, and it is worth knowing because CI scripts talk to the
API:

```bash
# resolve the blob store id first — a repository must reference an existing one
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# hosted
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/yum/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"yum-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Yum/DNF (RPM) hosted repository\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of the Rocky Linux 9 BaseOS mirror
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/yum/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"yum-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Yum proxy → https://dl.rockylinux.org/pub/rocky/9/BaseOS/x86_64/os/\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://dl.rockylinux.org/pub/rocky/9/BaseOS/x86_64/os/\" }
  }"

# group: one URL for both
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/yum/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"yum-group\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Yum group (hosted + proxy)\",
    \"formatConfig\": { \"member_names\": [\"yum-hosted\", \"yum-proxy\"] }
  }"
```

A group has no blob store of its own: its content lives in the members.

Repository URLs follow one pattern:

```
http://localhost:8081/repository/<repository-name>/
```

## 2. Where the metadata lives

A Yum repository serves the metadata and the packages side by side:

```
/repository/yum-hosted/repodata/repomd.xml          ← the index of the index
/repository/yum-hosted/repodata/primary.xml         ← the packages and their coordinates
/repository/yum-hosted/repodata/primary.xml.gz      ← the same document, gzipped
/repository/yum-hosted/repodata/filelists.xml(.gz)  ← the files each package owns
/repository/yum-hosted/repodata/other.xml(.gz)      ← the changelog data
/repository/yum-hosted/Packages/f/fuse-common-3.10.2-9.el9.x86_64.rpm
```

`dnf` starts at `repodata/repomd.xml`, which names the other two documents and
the checksum of each. Here is the `repomd.xml` of the hosted repository of §1
after one package was published:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<repomd xmlns="http://linux.duke.edu/metadata/repo">
  <revision>1795608130</revision>
  <data type="primary">
    <location href="repodata/primary.xml.gz"/>
    <checksum type="sha256">42599d1e71ab767273e292f4ff5e867749980b3b01e970dbc0d9921fb20b4a5f</checksum>
    <open-checksum type="sha256">1123639cda4041cd3d537145145fe85086f3935632e1f49c62fac2ca3e99c0e2</open-checksum>
    <timestamp>1795608130</timestamp>
    <size>320</size>
    <open-size>456</open-size>
  </data>
  <data type="filelists">
    <location href="repodata/filelists.xml.gz"/>
    <checksum type="sha256">4e574d7c99c7be26f004b3f1e8d9a133f7651bf9ba999d8425e73c7f39c9955b</checksum>
    <open-checksum type="sha256">4b03bcd87ea4db147425bd612ef6e69507009ffb8f75d3e8bd375fa9393ffc8d</open-checksum>
    <timestamp>1795608130</timestamp>
    <size>240</size>
    <open-size>299</open-size>
  </data>
  <data type="other">
    <location href="repodata/other.xml.gz"/>
    <checksum type="sha256">a9b03b7ad026950ca97acb110dec5ff3a4ba690b33cd4625ab0981f81d515d5d</checksum>
    <open-checksum type="sha256">9662c52f4ffa57480b35376aa3a426e40ed830d2a0f1867d7ddcf53699901db2</open-checksum>
    <timestamp>1795608130</timestamp>
    <size>238</size>
    <open-size>295</open-size>
  </data>
</repomd>
```

The server rewrites this document on every read: `checksum` is the SHA-256 of
the compressed file the client downloads, `open-checksum` the SHA-256 of the
same document decompressed, and `timestamp` is the moment the snapshot was
built. `dnf` verifies both against the bytes it received and refuses the
repository when they differ, so the two are always taken over the documents
that are actually served.

`primary.xml` is the document the client reads to learn what there is to
install:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" packages="1">
  <package type="rpm">
    <name>fuse-common</name>
    <arch>x86_64</arch>
    <version epoch="0" ver="3.10.2" rel="1"/>
    <checksum type="sha256" pkgid="YES">9e778d03985d8927d56daf33728ba17e67e92a697dcc71bbaf372893b646265a</checksum>
    <size package="7447"/>
    <location href="Packages/f/fuse-common-3.10.2-9.el9.x86_64.rpm"/>
  </package>
</metadata>
```

**The hrefs are relative.** `repodata/primary.xml.gz` and
`Packages/f/fuse-common-3.10.2-9.el9.x86_64.rpm` are resolved against the
repository base URL the client is already using, not against the server's own
host name. That is what makes a repository work behind a reverse proxy that
publishes it under a different prefix or host name — the client never needs a
second URL. See the [reverse proxy guide](reverse-proxy.md); the Yum format
needs no `http.base_url` for this reason.

The metadata is generated from the stored files on every request, so it is
never stale and there is no index document to upload. A repository that holds
no package answers `200` with an empty index, not `404`.

## 3. Point `dnf` at the repository

`dnf` reads one file per repository, `/etc/yum.repos.d/<name>.repo`:

```ini
[nexspence]
name=Nexspence Yum group
baseurl=http://localhost:8081/repository/yum-group/
enabled=1
gpgcheck=0

[nexspence-proxy]
name=Nexspence Yum proxy
baseurl=http://localhost:8081/repository/yum-proxy/
enabled=1
gpgcheck=0
```

- `baseurl` is the **repository root** — the URL ends with the repository name
  and a slash. `dnf` appends `repodata/repomd.xml` itself.
- `gpgcheck=0` because the repositories are not signed with a GPG key. The
  server does not sign `repomd.xml`, so `repo_gpgcheck` has to stay `0` as
  well; see [GPG](#gpg-and-the-package-signature) below.
- `enabled=1` turns the repository on. Add `enabled=0` to keep a file around
  without using it.

With the file in place:

```bash
dnf --disablerepo='*' --enablerepo=nexspence makecache
```

```
Nexspence Yum group                             484  B/s | 729  B     00:01
Metadata cache created.
```

`dnf list` shows what the repository holds:

```
$ dnf --disablerepo='*' --enablerepo=nexspence list --available
Available Packages
fuse-common.x86_64                        3.10.2-1                     nexspence
nexspence-demo.noarch                     1.0-1                        nexspence
```

and `dnf info` describes one package:

```
$ dnf --disablerepo='*' --enablerepo=nexspence info nexspence-demo
Available Packages
Name         : nexspence-demo
Version      : 1.0
Release      : 1
Architecture : noarch
Size         : 6.4 k
Source       : None
Repository   : nexspence
Summary      :
License      :
Description  :
```

The empty `Summary`, `License` and `Description` are not a client problem: the
server fills the coordinates and the checksum from the file name and the stored
bytes, and leaves the descriptive fields empty.

`dnf clean all` drops the cached metadata and the next command re-downloads it:

```
$ dnf --disablerepo='*' --enablerepo=nexspence clean all
5 files removed
```

### `dnf` in a container

`localhost` inside a container is the container, not the host, so a client
container needs the address the instance answers on. On Docker Desktop
(macOS, Windows) `host.docker.internal` is provided for you and points at the
host:

```bash
cat > /tmp/yum.d/nexspence.repo <<EOF
[nexspence]
name=Nexspence Yum group
baseurl=http://host.docker.internal:8081/repository/yum-group/
enabled=1
gpgcheck=0
EOF

docker run --rm --add-host=host.docker.internal:host-gateway \
  -v /tmp/yum.d:/etc/yum.repos.d rockylinux:9 \
  dnf --disablerepo='*' --enablerepo=nexspence list --available
```

On an Apple Silicon host, pin the platform as well: the `rockylinux:9` image
runs as `aarch64` there, and every package in an `x86_64` mirror is then
refused by the client.

```
$ docker run --rm --platform linux/amd64 rockylinux:9 rpm -E %{_arch}
x86_64
```

Without it the client answers

```
Error: 
 Problem: conflicting requests
  - package fuse-common-3.10.2-9.el9.x86_64 from nexspence does not have a compatible architecture
```

which is the client's architecture filter, not a repository problem.

### Credentials

The hosted repository of §1 is created with `"allowAnonymous": false`, so an
anonymous read is refused:

```bash
curl -s -i http://localhost:8081/repository/yum-hosted/repodata/repomd.xml
```

```
HTTP/1.1 401 Unauthorized
Content-Type: application/json; charset=utf-8
Www-Authenticate: Basic realm="Nexspence"
Content-Length: 35

{"error":"authentication required"}
```

`401` means the server did not accept the credentials; `403` means it accepted
them and the account is not allowed to do this. The two are different
problems — see [RBAC and content selectors](../security-rbac.md).

Put the credentials in the `.repo` file:

```ini
[nexspence-hosted]
name=Nexspence Yum hosted
baseurl=http://localhost:8081/repository/yum-hosted/
enabled=1
gpgcheck=0
username=admin
password=admin123
```

```
$ dnf --disablerepo='*' --enablerepo=nexspence-hosted list --available
Nexspence Yum hosted                             28 kB/s | 560  B     00:00
Available Packages
fuse-common.x86_64                   3.10.2-1                   nexspence-hosted
```

The option is `password=`. `password_` — the spelling some tools accept — is
not known to dnf 4.14, which reports it and sends no password at all:

```
$ dnf -v --disablerepo='*' --enablerepo=nexspence-hosted list --available
DNF version: 4.14.0
cachedir: /var/cache/dnf
Unknown configuration option: password_ = admin123 in /etc/yum.repos.d/nexspence.repo
User-Agent: constructed: 'libdnf (Rocky Linux 9.9; generic; Linux.x86_64)'
repo: downloading from remote: nexspence-hosted
error: Status code: 401 for http://host.docker.internal:8091/repository/yum-hosted/repodata/repomd.xml (IP: 192.168.65.254) (http://host.docker.internal:8091/repository/yum-hosted/repodata/repomd.xml).
Nexspence Yum hosted                             41  B/s |  35  B     00:00
Errors during downloading metadata for repository 'nexspence-hosted':
  - Status code: 401 for http://host.docker.internal:8091/repository/yum-hosted/repodata/repomd.xml (IP: 192.168.65.254)
Error: Failed to download metadata for repo 'nexspence-hosted': Cannot download repomd.xml: Cannot download repodata/repomd.xml: All mirrors were tried
```

A proxy or a group created with `"allowAnonymous": true` needs no credentials
at all.

## 4. Publish an RPM

An RPM is published with a `PUT` to the path it should be served under. The
path is yours to choose — the directory structure is what the upstream mirrors
use (`Packages/<first letter>/`), and it is the path the index will point at:

```bash
curl -u admin:admin123 --fail -w "HTTP %{http_code}\n" \
  -T fuse-common-3.10.2-9.el9.x86_64.rpm \
  "http://localhost:8081/repository/yum-hosted/Packages/f/fuse-common-3.10.2-9.el9.x86_64.rpm"
```

```
HTTP 201
```

Where the RPM comes from does not matter. Taking one out of the proxy is a
convenient way to get a genuine RPM: read the upstream index through the proxy,
then download the file it names.

```bash
# the upstream repomd.xml names the metadata documents
curl -s http://localhost:8081/repository/yum-proxy/repodata/repomd.xml
```

```
  <data type="primary">
    <checksum type="sha256">707889cc789c4224b686cbe567994bae489dc5c1366f8915b9690ba474279997</checksum>
    <open-checksum type="sha256">ffab2c8cbafd973ceafa37b6d476792c70beca8d705dbcf6b6cc4acf78c4b3b3</open-checksum>
    <location href="repodata/707889cc789c4224b686cbe567994bae489dc5c1366f8915b9690ba474279997-primary.xml.gz"/>
```

```bash
# read the upstream package list
curl -s http://localhost:8081/repository/yum-proxy/repodata/707889cc789c4224b686cbe567994bae489dc5c1366f8915b9690ba474279997-primary.xml.gz \
  | gunzip > /tmp/rocky-primary.xml

grep -A10 "<name>fuse-common</name>" /tmp/rocky-primary.xml
```

```
  <name>fuse-common</name>
  <arch>x86_64</arch>
  <version epoch="0" ver="3.10.2" rel="9.el9"/>
  <checksum type="sha256" pkgid="YES">9e778d03985d8927d56daf33728ba17e67e92a697dcc71bbaf372893b646265a</checksum>
  <summary>Common files for File System in Userspace (FUSE) v2 and v3</summary>
  <description>Common files for FUSE v2 and FUSE v3.</description>
  <packager>Rocky Linux Build System (Peridot) &lt;releng@rockylinux.org&gt;</packager>
  <url>http://fuse.sf.net</url>
  <time file="1730759952" build="1730759815"/>
  <size package="7447" installed="38" archive="292"/>
  <location href="Packages/f/fuse-common-3.10.2-9.el9.x86_64.rpm"/>
```

```bash
# download it through the proxy
curl -s -o fuse-common-3.10.2-9.el9.x86_64.rpm \
  http://localhost:8081/repository/yum-proxy/Packages/f/fuse-common-3.10.2-9.el9.x86_64.rpm
```

The index picks the file up on the next read — it is generated from what is
stored, so there is nothing to rebuild:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/yum-hosted/repodata/primary.xml
```

```xml
<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" packages="1">
  <package type="rpm">
    <name>fuse-common</name>
    <arch>x86_64</arch>
    <version epoch="0" ver="3.10.2" rel="1"/>
    <checksum type="sha256" pkgid="YES">9e778d03985d8927d56daf33728ba17e67e92a697dcc71bbaf372893b646265a</checksum>
    <size package="7447"/>
    <location href="Packages/f/fuse-common-3.10.2-9.el9.x86_64.rpm"/>
  </package>
</metadata>
```

Downloading the package back gives the same bytes the index advertises:

```bash
curl -s -u admin:admin123 -o /tmp/back.rpm \
  http://localhost:8081/repository/yum-hosted/Packages/f/fuse-common-3.10.2-9.el9.x86_64.rpm

shasum -a 256 /tmp/back.rpm
```

```
9e778d03985d8927d56daf33728ba17e67e92a697dcc71bbaf372893b646265a  /tmp/back.rpm
```

### What the server derives from the file name

The RPM header is **not** parsed. The coordinates in the index come from the
path you uploaded to:

| Field | Where it comes from |
|---|---|
| `name` | the file name without its architecture and its last two dash-separated segments |
| `ver` | the second-to-last dash-separated segment of the file name |
| `rel` | always `1` |
| `epoch` | always `0` |
| `arch` | the last dot-separated segment of the file name |
| `checksum`, `size` | the bytes that were stored |

For `fuse-common-3.10.2-9.el9.x86_64.rpm` that gives the name `fuse-common`,
the version `3.10.2`, the architecture `x86_64` — and the release `1`, while
the upstream index says `rel="9.el9"`. The server cannot know the release,
because it does not open the file.

A file whose name disagrees with its header is indexed under the name, not the
header. Publishing the same RPM under a different path:

```bash
curl -u admin:admin123 --fail -w "HTTP %{http_code}\n" \
  -T fuse-common-3.10.2-9.el9.x86_64.rpm \
  "http://localhost:8081/repository/yum-hosted/Packages/r/renamed-demo-4.5.6-1.x86_64.rpm"
```

gives an entry that says nothing about `fuse-common`:

```xml
<package type="rpm">
  <name>renamed-demo</name>
  <arch>x86_64</arch>
  <version epoch="0" ver="4.5.6" rel="1"/>
  <checksum type="sha256" pkgid="YES">9e778d03985d8927d56daf33728ba17e67e92a697dcc71bbaf372893b646265a</checksum>
  <size package="7447"/>
  <location href="Packages/r/renamed-demo-4.5.6-1.x86_64.rpm"/>
</package>
```

So name the file after the package it holds: `name-version-release.arch.rpm`.
The release in the file name does not reach the index — it is always `1` — so
two files of the same package and version (say `foo-1.0-1.el9.x86_64.rpm` and
`foo-1.0-2.el9.x86_64.rpm`) produce two index entries with identical
coordinates. Give each package one version, and let a new version carry a new
version number.

### A package of your own

Build the RPM on a machine that has `rpmbuild` (the `rpm-build` package), then
publish the file it produces. A minimal spec file:

```
Name:           nexspence-demo
Version:        1.0
Release:        1
Summary:        Demo package for the Yum guide
License:        MIT
BuildArch:      noarch

%description
A demo package published to a Nexspence Yum repository.

%install
mkdir -p %{buildroot}/usr/share/nexspence-demo
echo "Hello from Nexspence" > %{buildroot}/usr/share/nexspence-demo/README

%files
/usr/share/nexspence-demo/README
```

```bash
rpmbuild -bb nexspence-demo.spec
curl -u admin:admin123 --fail -w "HTTP %{http_code}\n" \
  -T "$HOME/rpmbuild/RPMS/noarch/nexspence-demo-1.0-1.noarch.rpm" \
  "http://localhost:8081/repository/yum-hosted/Packages/n/nexspence-demo-1.0-1.noarch.rpm"
```

```
Wrote: /root/rpmbuild/RPMS/noarch/nexspence-demo-1.0-1.noarch.rpm
HTTP 201
```

The `Wrote:` line names the `rpmbuild` directory of the account that ran the
build — `$HOME/rpmbuild/RPMS/<arch>/` for a normal account.

The file name and the header agree here, so the index entry is exactly the
package, and the index carries it next to the package published before it:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" packages="2">
  <package type="rpm">
    <name>fuse-common</name>
    <arch>x86_64</arch>
    <version epoch="0" ver="3.10.2" rel="1"/>
    <checksum type="sha256" pkgid="YES">9e778d03985d8927d56daf33728ba17e67e92a697dcc71bbaf372893b646265a</checksum>
    <size package="7447"/>
    <location href="Packages/f/fuse-common-3.10.2-9.el9.x86_64.rpm"/>
  </package>
  <package type="rpm">
    <name>nexspence-demo</name>
    <arch>noarch</arch>
    <version epoch="0" ver="1.0" rel="1"/>
    <checksum type="sha256" pkgid="YES">fa9194963dda644c4ba229b6345ed6f3498b88436d6087cefb048c9c7007b2f2</checksum>
    <size package="6597"/>
    <location href="Packages/n/nexspence-demo-1.0-1.noarch.rpm"/>
  </package>
</metadata>
```

The checksum is the SHA-256 of the RPM you built. `rpmbuild` stamps the build
time into the header, so two builds of the same spec produce different bytes and
a different checksum — the coordinates are the same either way.

### GPG and the package signature

The server stores the RPM byte for byte and does not sign it. The client
verifies the signature of the RPM itself when `gpgcheck=1`, so the outcome
depends on the package:

- a package you signed with a key the client trusts installs;
- a package you did not sign is refused:

```
$ dnf --disablerepo='*' --enablerepo=nexspence install -y nexspence-demo
Nexspence Yum group                             488  B/s | 729  B     00:01
Dependencies resolved.
================================================================================
 Package                Architecture   Version          Repository         Size
================================================================================
Installing:
 nexspence-demo         noarch         1.0-1            nexspence         6.4 k

Transaction Summary
================================================================================
Install  1 Package

Total download size: 6.4 k
Installed size: 0
Downloading Packages:
nexspence-demo-1.0-1.noarch.rpm                 2.3 MB/s | 6.4 kB     00:00
--------------------------------------------------------------------------------
Total                                           644 kB/s | 6.4 kB     00:00
Package nexspence-demo-1.0-1.noarch.rpm is not signed
The downloaded packages were saved in cache until the next successful transaction.
You can remove cached packages by executing 'dnf clean packages'.
Error: GPG check FAILED
```

- an upstream package keeps its own signature, and the Rocky Linux key ships
  with the distribution, so it installs with `gpgcheck=1` as well.

`gpgcheck=0` in the `.repo` file is the setting for repositories that carry
unsigned packages of your own.

### Installing what you published

```
$ dnf --disablerepo='*' --enablerepo=nexspence install -y fuse-common
Nexspence Yum group                             375  B/s | 560  B     00:01
Dependencies resolved.
================================================================================
 Package             Architecture   Version             Repository         Size
================================================================================
Installing:
 fuse-common         x86_64         3.10.2-1            nexspence         7.3 k

Transaction Summary
================================================================================
Install  1 Package

Total download size: 7.3 k
Installed size: 0
Downloading Packages:
fuse-common-3.10.2-9.el9.x86_64.rpm             2.5 MB/s | 7.3 kB     00:00
--------------------------------------------------------------------------------
Total                                           727 kB/s | 7.3 kB     00:00
Running transaction check
Transaction check succeeded.
Running transaction test
Transaction test succeeded.
Running transaction
  Preparing        :                                                        1/1
  Installing       : fuse-common-3.10.2-1.x86_64                            1/1
  Verifying        : fuse-common-3.10.2-1.x86_64                            1/1

Installed:
  fuse-common-3.10.2-1.x86_64                                                   

Complete!
$ rpm -q fuse-common
fuse-common-3.10.2-9.el9.x86_64
```

Two things to read out of that output. The client asked for
`fuse-common-3.10.2-1` — the coordinates from the index, with the release the
server always writes — while `rpm` records
`fuse-common-3.10.2-9.el9.x86_64`, the release from the RPM header. The
installed package is the file you published; the index release is a label the
server cannot fill in correctly.

## 5. Delete an RPM

```bash
curl -u admin:admin123 -X DELETE -w "HTTP %{http_code}\n" \
  http://localhost:8081/repository/yum-hosted/Packages/f/fuse-common-3.10.2-9.el9.x86_64.rpm
```

```
HTTP 204
```

The index loses the entry with the file, because the index is generated from
what is stored:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/yum-hosted/repodata/primary.xml
```

```xml
<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" packages="1">
  <package type="rpm">
    <name>nexspence-demo</name>
    <arch>noarch</arch>
    <version epoch="0" ver="1.0" rel="1"/>
    <checksum type="sha256" pkgid="YES">fa9194963dda644c4ba229b6345ed6f3498b88436d6087cefb048c9c7007b2f2</checksum>
    <size package="6597"/>
    <location href="Packages/n/nexspence-demo-1.0-1.noarch.rpm"/>
  </package>
</metadata>
```

A client that has the old index in its cache still offers the package until it
refreshes: `dnf clean all` drops the cache, and the next `dnf list` fetches the
new index.

## 6. What the server does

- **The metadata is generated.** `repomd.xml`, `primary.xml`, `filelists.xml`
  and `other.xml` are built from the stored RPMs on every request, from one
  snapshot, so the checksums in `repomd.xml` always describe the documents that
  are served. Nothing is stored as an index document and nothing has to be
  rebuilt after an upload or a deletion.
- **The coordinates come from the file name.** The RPM header is not read: the
  name and the version come from the path, the architecture from the last
  dot-separated segment, and the release and the epoch are always `1` and `0`.
- **The checksum and the size come from the stored bytes**, so a client that
  downloads a package can verify it against the index.
- **A path holds one file.** Uploading the same path again replaces the file —
  the default `write_policy` of a hosted repository is `allow`. A repository
  set to `"write_policy": "allow_once"` refuses the second upload with `400`:

  ```json
  {"error":"Repository does not allow updating assets: yum-once"}
  ```

  and one set to `"write_policy": "deny"` refuses every upload with `400`:

  ```json
  {"error":"Repository is read-only: yum-readonly"}
  ```

  The policy lives in the repository's `formatConfig`:

  ```bash
  curl -u admin:admin123 -X POST \
    http://localhost:8081/service/rest/v1/repositories/yum/hosted \
    -H "Content-Type: application/json" \
    -d "{
      \"name\": \"yum-once\",
      \"online\": true,
      \"allowAnonymous\": false,
      \"description\": \"RPMs that are published once\",
      \"blobStoreId\": \"${BLOB_STORE_ID}\",
      \"formatConfig\": { \"write_policy\": \"allow_once\" }
    }"
  ```

- **Read-only proxies.** A proxy refuses `PUT`, `POST`, `PATCH` and `DELETE`
  with `405`:

  ```json
  {"error":"proxy repository is read-only (use a hosted repository to publish)"}
  ```

- **A proxy answers the canonical repodata names too.** An upstream names its
  documents by the checksum of their contents, so the proxy resolves the
  canonical names (`repodata/primary.xml`) through the upstream's own
  `repomd.xml` and serves the document that names. A group merges its members
  at the canonical names, so that is what makes the upstream's packages part of
  the merged index.

- **Group writes.** A group forwards an upload to its first hosted member, so
  publishing through `yum-group` stores the RPM in `yum-hosted`. The group's
  `formatConfig` can name the member that receives the upload with
  `"writable_member"` — it has to be an online hosted member of the same
  format, or the upload answers `409`:

  ```json
  {"error":"writable_member is not an online hosted repository matching group format"}
  ```

  A group without a hosted member answers `405`:

  ```json
  {"error":"group repository has no hosted member — publish directly to a hosted repository"}
  ```

- **404, not error.** A `GET` for a path a repository does not hold answers
  `404`. A wrong *method* answers `405`.

## 7. The proxy and the group against an upstream mirror

### The proxy

The proxy in front of `dl.rockylinux.org` relays the upstream metadata and
caches what it fetched:

```bash
curl -s -o /dev/null -w "%{http_code}\n" \
  http://localhost:8081/repository/yum-proxy/repodata/repomd.xml
```

```
200
```

The document it serves is upstream's own, unchanged:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<repomd xmlns="http://linux.duke.edu/metadata/repo" xmlns:rpm="http://linux.duke.edu/metadata/rpm">
  <revision>9.9</revision>
  <tags>
    <distro cpeid="cpe:/o:rocky:rocky:9.9">Rocky Linux 9.9</distro>
  </tags>
  <data type="primary">
    <checksum type="sha256">707889cc789c4224b686cbe567994bae489dc5c1366f8915b9690ba474279997</checksum>
    <open-checksum type="sha256">ffab2c8cbafd973ceafa37b6d476792c70beca8d705dbcf6b6cc4acf78c4b3b3</open-checksum>
    <location href="repodata/707889cc789c4224b686cbe567994bae489dc5c1366f8915b9690ba474279997-primary.xml.gz"/>
```

A client pointed at the proxy sees the whole upstream repository — 1056
packages from this mirror:

```
$ dnf --disablerepo='*' --enablerepo=nexspence-proxy list --available
Nexspence Yum proxy                              22 MB/s | 5.5 MB     00:00
Available Packages
ModemManager.x86_64                                1.20.2-1.el9                     nexspence-proxy
ModemManager-glib.i686                             1.20.2-1.el9                     nexspence-proxy
ModemManager-glib.x86_64                           1.20.2-1.el9                     nexspence-proxy
NetworkManager.x86_64                              1:1.54.4-3.el9                   nexspence-proxy
NetworkManager-adsl.x86_64                        1:1.54.4-3.el9                   nexspence-proxy
NetworkManager-bluetooth.x86_64                    1:1.54.4-3.el9                   nexspence-proxy
```

The proxy's cache has two speeds. The `repodata/` documents are mutable
metadata: they are revalidated against the upstream after ten minutes
(`proxy_config.metadata_max_age` in seconds changes that). An RPM is immutable
— it is addressed by name and version — so a cached RPM is served without
contacting the upstream again.

### The canonical names

A mirror names its metadata documents by the checksum of their contents —
`repodata/707889cc…-primary.xml.gz` — and the canonical names a hosted
repository serves (`repodata/primary.xml`) are not among them. The proxy
answers the canonical names as well: it reads the upstream's own `repomd.xml`
and serves the document that names, decompressed, because that is what the
canonical name asks for.

```bash
curl -s -o /dev/null -w "%{http_code}\n" \
  http://localhost:8081/repository/yum-proxy/repodata/primary.xml
```

```
200
```

```xml
<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" xmlns:rpm="http://linux.duke.edu/metadata/rpm" packages="1261">
  <package type="rpm">
    <name>ModemManager</name>
    <arch>x86_64</arch>
    <version epoch="0" ver="1.20.2" rel="1.el9"/>
    <checksum type="sha256" pkgid="YES">855e8ab0d2b1ab053d679996cd8f4afa9f6dc7e5a0572e2952056071c554d88e</checksum>
    <summary>Mobile broadband modem management service</summary>
```

The compressed name is answered with the upstream's compressed bytes, and the
checksum-prefixed name the upstream publishes is still relayed as it came — a
client that read the proxy's `repomd.xml` asks for that one.

### The group

A group serves the metadata of its members merged into one document. The merge
is a union: every package of every member appears once, and packages are
deduplicated by their location, the first member winning.

To see the union with more than one member, a second hosted repository and a
group of the two hosted members:

```bash
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/yum/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"yum-hosted2\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Second hosted repository\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/yum/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"yum-group2\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Group of two hosted repositories\",
    \"formatConfig\": { \"member_names\": [\"yum-hosted\", \"yum-hosted2\"] }
  }"
```

```bash
curl -u admin:admin123 --fail -w "HTTP %{http_code}\n" \
  -T basesystem-11-13.el9.0.1.noarch.rpm \
  "http://localhost:8081/repository/yum-hosted2/Packages/b/basesystem-11-13.el9.0.1.noarch.rpm"
```

```
HTTP 201
```

The group builds its **own** `repomd.xml` over the merged documents it serves.
It cannot relay a member's `repomd.xml`: that document carries the checksum of
the member's own single-member copy, while the group serves the union, and
`dnf` treats a mismatch as a hard sync failure. So the group computes the
checksums over the bytes it actually serves:

```bash
curl -s http://localhost:8081/repository/yum-group2/repodata/repomd.xml
```

```xml
<?xml version="1.0" encoding="UTF-8"?>
<repomd xmlns="http://linux.duke.edu/metadata/repo">
  <revision>1795609180</revision>
  <data type="primary">
    <location href="repodata/primary.xml.gz"/>
    <checksum type="sha256">5fe2947854362b9fb6ff8479e6b40b757e93ba49d0316427abba41a3bb4981c4</checksum>
    <open-checksum type="sha256">e943eea2c0ed25cf5e9f78853a751350d0552c787168ddd42cf3dba2785f5472</open-checksum>
    <timestamp>1795609180</timestamp>
    <size>488</size>
    <open-size>1121</open-size>
  </data>
  <data type="filelists">
    <location href="repodata/filelists.xml.gz"/>
    <checksum type="sha256">2d8dec7b8938a9ad113e315f787ae5f5fe4e53ee2efacb9f5dc3e08d3161b9a8</checksum>
    <open-checksum type="sha256">2a1936aded4815c78dc88121a775be91938fcaadc539690af4d279f69873a6bb</open-checksum>
    <timestamp>1795609180</timestamp>
    <size>368</size>
    <open-size>644</open-size>
  </data>
  <data type="other">
    <location href="repodata/other.xml.gz"/>
    <checksum type="sha256">4f561d0516cd467c736e3ec2adadd15fe4df6da1cf6905ded98e1fe84123327a</checksum>
    <open-checksum type="sha256">c965362767e16ea6b5a39b9908ae0cf5aaba54451a5f12ace4efbf4d6e7c02d7</open-checksum>
    <timestamp>1795609180</timestamp>
    <size>366</size>
    <open-size>640</open-size>
  </data>
</repomd>
```

and the merged `primary.xml` of that group carries the packages of both
members — the two published into `yum-hosted` and the one published into
`yum-hosted2`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" packages="3">
  <package type="rpm">
    <name>fuse-common</name>
    <arch>x86_64</arch>
    <version epoch="0" ver="3.10.2" rel="1"/>
    <checksum type="sha256" pkgid="YES">9e778d03985d8927d56daf33728ba17e67e92a697dcc71bbaf372893b646265a</checksum>
    <size package="7447"/>
    <location href="Packages/f/fuse-common-3.10.2-9.el9.x86_64.rpm"/>
  </package>
  <package type="rpm">
    <name>nexspence-demo</name>
    <arch>noarch</arch>
    <version epoch="0" ver="1.0" rel="1"/>
    <checksum type="sha256" pkgid="YES">fa9194963dda644c4ba229b6345ed6f3498b88436d6087cefb048c9c7007b2f2</checksum>
    <size package="6597"/>
    <location href="Packages/n/nexspence-demo-1.0-1.noarch.rpm"/>
  </package>
  <package type="rpm">
    <name>basesystem</name>
    <arch>noarch</arch>
    <version epoch="0" ver="11" rel="1"/>
    <checksum type="sha256" pkgid="YES">9f707e8d1046381c9b4a4df6a6674327df6ffe0298d48e1750972212c97aafad</checksum>
    <size package="6565"/>
    <location href="Packages/b/basesystem-11-13.el9.0.1.noarch.rpm"/>
  </package>
</metadata>
```

A client syncs that group without a checksum error:

```ini
[nexspence-group2]
name=Nexspence Yum group of two hosted members
baseurl=http://localhost:8081/repository/yum-group2/
enabled=1
gpgcheck=0
```

```
$ dnf --disablerepo='*' --enablerepo=nexspence-group2 makecache
Nexspence Yum group of two hosted members       375  B/s | 560  B     00:01
Metadata cache created.
```

The header `X-Nexspence-Source` names the members that answered, which is the
first thing to look at when a group serves the wrong document:

```bash
curl -s -D- -o /dev/null http://localhost:8081/repository/yum-group2/repodata/primary.xml
```

```
X-Nexspence-Source: yum-hosted,yum-hosted2
```

### A group with a proxy member

The group merges its members at the **canonical** repodata paths —
`/repodata/primary.xml` and its compressed flavor — and a proxy member answers
those paths, so the upstream's packages are part of the union.

The group of the hosted repository and the proxy of §1 therefore lists both the
published packages and the whole upstream mirror:

```bash
curl -s -o /dev/null -w "%{http_code}\n" \
  http://localhost:8081/repository/yum-group/repodata/primary.xml
```

```
200
```

```xml
<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" packages="1262">
  <package type="rpm">
    <name>fuse-common</name>
    <arch>x86_64</arch>
    <version epoch="0" ver="3.10.2" rel="1"/>
    <checksum type="sha256" pkgid="YES">9e778d03985d8927d56daf33728ba17e67e92a697dcc71bbaf372893b646265a</checksum>
    <size package="7447"/>
    <location href="Packages/f/fuse-common-3.10.2-9.el9.x86_64.rpm"/>
  </package>
  <package type="rpm">
    <name>ModemManager</name>
    <arch>x86_64</arch>
    <version epoch="0" ver="1.20.2" rel="1.el9"/>
    <checksum type="sha256" pkgid="YES">855e8ab0d2b1ab053d679996cd8f4afa9f6dc7e5a0572e2952056071c554d88e</checksum>
    <summary>Mobile broadband modem management service</summary>
    <description>The ModemManager service manages WWAN modems and provides a consistent API for
interacting with these devices to client applications.</description>
    <url>http://www.freedesktop.org/wiki/Software/ModemManager/</url>
    <time file="1682113661" build="1682113275"/>
    <size package="1274689"/>
    <location href="Packages/m/ModemManager-1.20.2-1.el9.x86_64.rpm"/>
  </package>
```

The header `X-Nexspence-Source` names the members that answered:

```
X-Nexspence-Source: yum-hosted,yum-proxy
```

The client sees the upstream's packages with the upstream's own description:

```
$ dnf --disablerepo='*' --enablerepo=nexspence info ModemManager
Nexspence Yum group                              80 kB/s | 240 kB     00:02
Available Packages
Name         : ModemManager
Version      : 1.20.2
Release      : 1.el9
Architecture : x86_64
Size         : 1.2 M
Source       : None
Repository   : nexspence
Summary      : Mobile broadband modem management service
URL          : http://www.freedesktop.org/wiki/Software/ModemManager/
License      : 
Description  : The ModemManager service manages WWAN modems and provides a consistent API for
             : interacting with these devices to client applications.
```

and installs from it:

```
$ dnf --disablerepo='*' --enablerepo=nexspence install -y tree
Running transaction check
Transaction check succeeded.
Running transaction test
Transaction test succeeded.
Running transaction
  Preparing        :                                                        1/1 
  Installing       : tree-1.8.0-10.el9.x86_64                               1/1 
  Running scriptlet: tree-1.8.0-10.el9.x86_64                               1/1 
  Verifying        : tree-1.8.0-10.el9.x86_64                               1/1 

Installed:
  tree-1.8.0-10.el9.x86_64                                                      

Complete!
```

The group's `repomd.xml` is its own document: the checksums are computed over
the merged documents the group serves, because a member's own `repomd.xml`
describes that member's single-member copy and `dnf` treats the mismatch as a
hard sync failure.

```xml
<?xml version="1.0" encoding="UTF-8"?>
<repomd xmlns="http://linux.duke.edu/metadata/repo">
  <revision>1795659193</revision>
  <data type="primary">
    <location href="repodata/primary.xml.gz"/>
    <checksum type="sha256">033d00cec4de928ec8f68a031bc75c8b58eb0319c7866c612c4984e556b08efa</checksum>
    <open-checksum type="sha256">8c764c5a786c5dae0a38c706503bf3f762a2131ce0bf57a4fadcbdf3cb4a56fc</open-checksum>
    <timestamp>1795659193</timestamp>
    <size>185511</size>
    <open-size>944676</open-size>
  </data>
  <data type="filelists">
    <location href="repodata/filelists.xml.gz"/>
    <checksum type="sha256">236ca62c750cfad725963e4017527eb986944da5ab22f16e77f6b1575b18d973</checksum>
    <open-checksum type="sha256">a0967212e7aec396c8a2b924f2173a02657c48982f392b5b79decaa01ac1cf7f</open-checksum>
    <timestamp>1795659193</timestamp>
    <size>60576</size>
    <open-size>229624</open-size>
  </data>
  <data type="other">
    <location href="repodata/other.xml.gz"/>
    <checksum type="sha256">af09e8039d8e90e3efc3607be5b0444dd4c790d0760d21bacc6e090aa209f91f</checksum>
    <open-checksum type="sha256">85a77e637da6e10a8f984868487a287542c81e9933de4bc2231af32d8a561e58</open-checksum>
    <timestamp>1795659193</timestamp>
    <size>60581</size>
    <open-size>229620</open-size>
  </data>
</repomd>
```

### What the merge keeps

A package is identified by its location, so a package both members hold is
listed once — the first member wins, and the group serves that member's copy.

The merge keeps the descriptive fields of the upstream's own index: the
summary, the description, the URL and the build time of an upstream package
survive the merge. A package published into a hosted repository carries the
coordinates the server derived from its file name, and nothing else — the RPM
header is not read.
## 8. Errors and edge cases

The error bodies are JSON, so a script can read the reason instead of guessing:

| Request | Answer |
|---|---|
| `GET` a path the repository does not hold | `404` — `{"error":"not found: yum-hosted//Packages/n/nosuch-1.0-1.x86_64.rpm"}` |
| `POST` to an `.rpm` path | `405`, no body — only `PUT`, `GET`, `HEAD` and `DELETE` are defined for it |
| `PUT` to a path that does not end in `.rpm` | `405`, no body |
| `PUT`, `POST` or `DELETE` on a proxy | `405` — `{"error":"proxy repository is read-only (use a hosted repository to publish)"}` |
| `GET` on a repository with `allowAnonymous: false` without credentials | `401` — `{"error":"authentication required"}` |
| `PUT` without the write privilege | `403` — `{"error":"access denied"}` |
| `PUT` through a group whose `writable_member` is not an online hosted member | `409` — `{"error":"writable_member is not an online hosted repository matching group format"}` |
| `PUT` through a group without a hosted member | `405` — `{"error":"group repository has no hosted member — publish directly to a hosted repository"}` |
| `PUT` a path that already holds a file, on an `allow_once` repository | `400` — `{"error":"Repository does not allow updating assets: yum-once"}` |
| `PUT` on a `deny` repository | `400` — `{"error":"Repository is read-only: yum-readonly"}` |

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `Status code: 401 for …/repodata/repomd.xml` | The repository is not anonymous | Put `username=` and `password=` in the `.repo` file, or set `"allowAnonymous": true` on the repository |
| `Unknown configuration option: password_ = …` | `password_` is not a dnf option | Use `password=` |
| `Failed to download metadata … Cannot download repomd.xml` | The baseurl is wrong, or the repository does not exist | The baseurl is the repository root, ending in the repository name and a slash |
| `does not have a compatible architecture` | The client runs on another architecture than the packages | Use a client of the same architecture — on Apple Silicon, `--platform linux/amd64` |
| `GPG check FAILED` / `Package … is not signed` | `gpgcheck=1` and the RPM carries no signature | Set `gpgcheck=0`, or sign the RPM and ship the key to the clients |
| `Error: GPG check FAILED` on an upstream package | The key that signed it is not in the client's keyring | Import the key, or use `gpgcheck=0` |
| The index shows the wrong version | The version comes from the file name | Upload the RPM under its own name: `name-version-release.arch.rpm` |
| The index shows `rel="1"` for every package | The release is always `1`; the RPM header is not parsed | Nothing to do — the installed package keeps the release from its header |
| `dnf info` shows no summary, license or description | The server fills the coordinates only | Expected for a package published into a hosted repository — an upstream's own package keeps its description |
| The client offers a package that was deleted | The client has the old index cached | `dnf clean all`, then list again |
| The group shows the hosted packages only | The upstream mirror has no metadata under the canonical names | The proxy resolves them through the upstream's `repomd.xml`; check that the upstream serves a `repomd.xml` at all |
| `Checksum mismatch` from `dnf` | The client read a document that the group no longer serves | `dnf clean all`; the group's `repomd.xml` is rebuilt on every read |
| `405 Method Not Allowed` on an upload | You are publishing into the **proxy** | Publish into the hosted repository; a proxy is read-only |
| `409 Conflict` on an upload through a group | The group's `writable_member` names a member that is not an online hosted repository of the same format | Name a hosted member, or drop the key — the group then writes to its first hosted member |
| `400` on the second upload of the same path | The repository's `write_policy` is `allow_once` | Publish a new version under a new path, or set the policy to `allow` |
| `403 Forbidden` on publish | The user authenticated but lacks the write privilege | The role needs a privilege that allows `write` — see [RBAC and content selectors](../security-rbac.md) |
| `404` on an RPM that was uploaded | The path differs from the one the client asks for | The path decides: the index points at the path you uploaded to |

## Related

- [RBAC and content selectors](../security-rbac.md)
- [Migration from Sonatype Nexus](../nexus-migration.md)
- [Reverse proxy guide](reverse-proxy.md)
- [Alpine guide](alpine.md)
- [APT guide](apt.md)
- [GitHub Actions guide](github-actions.md)
- [API specification](../api-spec.yaml)
