# CRAN guide — hosted, proxy and group repositories

A working R setup against Nexspence: how to create the repositories, how to
publish a source package, how to point an R session at the repository instead
of CRAN, and how the client reads the index.

Everything below assumes a fresh instance on `http://localhost:8081` (the
default port of the Docker Compose quick start). Replace the host everywhere
if yours differs. The examples were run with R 4.6.

CRAN serves **source** packages. Binary packages (Windows `.zip`, macOS
`.tgz`) are out of scope in this version of the server.

## 1. Create the repositories

Nexspence ships a helper that creates all three repository types with the right
payloads:

```bash
./scripts/create-cran-repos.sh
```

It defaults to `http://localhost:8081` — point it at your instance with
`BASE_URL` when it differs:

```bash
BASE_URL=https://nexus.example.com ./scripts/create-cran-repos.sh
```

That script creates `cran-hosted`, `cran-proxy` (in front of
`https://cran.r-project.org`) and `cran-group` — the three names this guide
uses.

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
  http://localhost:8081/service/rest/v1/repositories/cran/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"cran-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"CRAN hosted repository\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of CRAN
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/cran/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"cran-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"CRAN proxy\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://cran.r-project.org\" }
  }"

# group: one URL for both
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/cran/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"cran-group\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"CRAN group\",
    \"formatConfig\": { \"member_names\": [\"cran-hosted\", \"cran-proxy\"] }
  }"
```

Repository URLs follow one pattern:

```
http://localhost:8081/repository/<repository-name>/
```

## 2. Where the index lives

A CRAN repository serves source packages from one directory. The index and the
tarballs are siblings:

```
/repository/cran-hosted/src/contrib/PACKAGES           ← the index
/repository/cran-hosted/src/contrib/PACKAGES.gz        ← the same index, gzipped
/repository/cran-hosted/src/contrib/<pkg>_<ver>.tar.gz ← the packages
```

The index is **next to** the tarballs, in `src/contrib/`, not above them.

R appends `src/contrib` itself. The URL you hand to R is the repository root:

```
http://localhost:8081/repository/cran-hosted
  → http://localhost:8081/repository/cran-hosted/src/contrib/PACKAGES
```

A path the repository does not keep is a `404`, not an error. A hosted
repository has no `PACKAGES.rds` — Nexspence does not generate that binary
format — and asking for one answers `404`:

```bash
curl -s -o /dev/null -w "%{http_code}\n" -u admin:admin123 \
  http://localhost:8081/repository/cran-hosted/src/contrib/PACKAGES.rds
```

```
404
```

The same is true of a tarball that was never uploaded:

```bash
curl -s -o /dev/null -w "%{http_code}\n" -u admin:admin123 \
  http://localhost:8081/repository/cran-hosted/src/contrib/nosuchpkg_1.0.0.tar.gz
```

```
404
```

A repository that holds no package answers `200` with an empty index, not
`404` — the repository exists, it has nothing in it yet.

## 3. Point the client at the repository

One call, one repository:

```r
install.packages("nexspencedemo",
                 repos = "http://localhost:8081/repository/cran-hosted")
```

The hosted repository of §1 is created with `"allowAnonymous": false`, so this
call needs credentials — see [Credentials](#credentials) below.

Or set it once for the session and let every install use it:

```r
options(repos = c(CRAN = "http://localhost:8081/repository/cran-group"))

install.packages("nexspencedemo")
```

The `repos` URL is the repository **root** — R appends `/src/contrib` itself.
Do not add `/src/contrib` to the URL.

### Credentials

The hosted repository above is created with `"allowAnonymous": false`, so an
anonymous read is refused. R says so and moves on with an empty index:

```
Warning: unable to access index for repository http://localhost:8081/repository/cran-hosted/src/contrib:
  cannot open URL 'http://localhost:8081/repository/cran-hosted/src/contrib/PACKAGES'
```

An install in that state finds nothing to install:

```
Warning message:
package 'nexspencedemo' is not available for this version of R
```

Two ways around it. Put the credentials in the URL:

```r
install.packages("nexspencedemo",
                 repos = "http://admin:admin123@localhost:8081/repository/cran-hosted")
```

…or let the repository serve anonymous reads:

```bash
curl -u admin:admin123 -X PUT \
  http://localhost:8081/service/rest/v1/repositories/cran/hosted/cran-hosted \
  -H "Content-Type: application/json" \
  -d '{"allowAnonymous": true}'
```

R prints the URL it downloads from, credentials included, so keep them out of
the repository URL when the session output is shared. A proxy or group
repository is created with `"allowAnonymous": true` and needs no credentials.

### An `.Rprofile`

`options(repos = ...)` in an `.Rprofile` is the usual place for it — every R
session in that directory then uses the repository instead of CRAN:

```r
options(repos = c(CRAN = "http://localhost:8081/repository/cran-group"))
```

### R in a container

`localhost` inside a container is the container, not the host, so a client
container needs the address the instance answers on. Two of them work:

- **Docker Desktop (macOS, Windows)** — `host.docker.internal` is provided for
  you and points at the host. Use the port the host publishes:

  ```r
  options(repos = c(CRAN = "http://host.docker.internal:8081/repository/cran-group"))
  ```

- **Linux Docker** — the name does not exist unless you map it, or you put the
  container on the compose network of the instance and use the service name:

  ```bash
  docker run --rm --add-host=host.docker.internal:host-gateway ...
  # or
  docker run --rm --network nexspence_default ...
  ```

  ```r
  options(repos = c(CRAN = "http://nexspence:8081/repository/cran-group"))
  ```

  `nexspence_default` is the network and `nexspence:8081` the service of the
  Docker Compose quick start — a project of your own is called what you named
  it, and its network `<project>_default`.

## 4. What the client reads: `available.packages()`

```r
ap <- available.packages(repos = "http://admin:admin123@localhost:8081/repository/cran-hosted")
print(ap)
ap[, c("Package", "Version")]
```

`available.packages()` reads the `PACKAGES` index of every repository in
`repos` and returns one row per package. The columns are the fields of the
index — the server fills in the two it can know and leaves the rest empty:

```
              Package         Version Priority Depends Imports LinkingTo
nexspencedemo "nexspencedemo" "0.2.0" NA       NA      NA      NA
              Suggests Enhances License License_is_FOSS License_restricts_use
nexspencedemo NA       NA       NA      NA              NA
              OS_type Archs MD5sum NeedsCompilation File Published
nexspencedemo NA      NA    NA     NA               NA   NA
              Repository
nexspencedemo "http://admin:admin123@localhost:8081/repository/cran-hosted/src/contrib"
```

The `Repository` column carries the URL the row was read from, credentials
included — the same leak as the download URL below.

and the two fields that carry a value:

```
        Package         Version
"nexspencedemo"         "0.2.0"
```

The row count is one per package **name**: when the index holds two versions
of the same package — the index has one stanza per stored tarball — the row is
the newer of the two.

The version comes from the **file name** of the tarball
(`nexspencedemo_0.2.0.tar.gz`), not from the `DESCRIPTION` inside it: the
server does not open the tarball to build the index, so `Depends`, `Imports`,
`License` and the other fields are `NA`. A client that installs by dependency
therefore sees no dependencies for a hosted package.

R reads the index in a fixed order — `PACKAGES.rds`, then `PACKAGES.gz`, then
the plain `PACKAGES` — and uses the first one it can parse. A `404` on
`PACKAGES.rds` is normal and costs nothing: the client falls through to
`PACKAGES.gz`.

The index is generated from the stored tarballs on every read, so it is never
stale and there is no index document to upload.

## 5. Publish a package

Build the source package first. `R CMD build` produces the
`<pkg>_<version>.tar.gz` file CRAN expects, with the package directory as its
top-level entry:

```bash
R CMD build nexspencedemo
```

```
* checking for file ‘nexspencedemo/DESCRIPTION’ ... OK
* preparing ‘nexspencedemo’:
* checking DESCRIPTION meta-information ... OK
* checking for LF line-endings in source and make files and shell scripts
* checking for empty or unneeded directories
* building ‘nexspencedemo_0.1.0.tar.gz’
```

Publish it with a `PUT` to the path the index will point at:

```bash
curl -u admin:admin123 --fail -T nexspencedemo_0.1.0.tar.gz \
  "http://localhost:8081/repository/cran-hosted/src/contrib/nexspencedemo_0.1.0.tar.gz"
```

The tarball is stored under `src/contrib/` and the index picks it up on the
next read:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/cran-hosted/src/contrib/PACKAGES
```

```
Package: nexspencedemo
Version: 0.1.0
```

`POST` works as well — it is the Nexus-compatible verb — and takes the same
path:

```bash
curl -u admin:admin123 --fail -X POST \
  -T nexspencedemo_0.1.0.tar.gz \
  "http://localhost:8081/repository/cran-hosted/src/contrib/nexspencedemo_0.1.0.tar.gz"
```

An upload to the repository root is stored under `src/contrib/` all the same:

```bash
curl -u admin:admin123 --fail -T nexspencedemo_0.2.0.tar.gz \
  "http://localhost:8081/repository/cran-hosted/"
```

That works because `curl --upload-file` appends the local file name to a URL
that ends in `/`, so the request lands on
`/repository/cran-hosted/nexspencedemo_0.2.0.tar.gz` and the server moves it
under `src/contrib/`. A request to the repository root *without* a file name
in the path answers `400`:

```json
{"error":"expected a *.tar.gz path or a multipart 'file' field"}
```

A multipart `POST` is the other way in — the file name then comes from the
form part:

```bash
curl -u admin:admin123 -X POST \
  -F "file=@nexspencedemo_0.2.0.tar.gz" \
  "http://localhost:8081/repository/cran-hosted/"
```

A package is removed with `DELETE`, and the index loses the stanza with it:

```bash
curl -u admin:admin123 -X DELETE \
  http://localhost:8081/repository/cran-hosted/src/contrib/nexspencedemo_0.1.0.tar.gz
```

```
204
```

What is left in the index is what the client sees from here on:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/cran-hosted/src/contrib/PACKAGES
```

```
Package: nexspencedemo
Version: 0.2.0
```

## 6. What the server does

- **Index generation.** `PACKAGES` is derived from the tarballs stored under
  `/src/contrib/`, one stanza per stored file, `Package:` and `Version:` only.
  The index document itself is not stored — it is generated per request from
  the list of assets, so it is always in step with the content.
- **Coordinates.** The package name and the version are taken from the file
  name `name_version.tar.gz`. The `DESCRIPTION` inside the tarball is not
  parsed, so a file name and its `DESCRIPTION` can disagree — the file name
  wins.
- **One file per version.** A version is identified by name and version, so a new
  version of a package is a new file name. By default — the `write_policy` of a
  hosted repository is `allow` — uploading the same file name again replaces
  the stored tarball. A repository set to `"write_policy": "allow_once"`
  ("Disable redeploy") refuses it with `400`:

  ```json
  {"error":"Repository does not allow updating assets: cran-hosted"}
  ```
- **Read-only proxies.** A proxy refuses `PUT`, `POST`, `PATCH` and `DELETE`
  with `405`:

  ```json
  {"error":"proxy repository is read-only (use a hosted repository to publish)"}
  ```

- **Group writes.** A group forwards an upload to its first hosted member, so
  publishing through `cran-group` stores the tarball in `cran-hosted`. The
  group's `formatConfig` can name the member that receives the upload with
  `"writable_member"` — it has to be an online hosted member of the same
  format, or the upload answers `409`:

  ```json
  {"error":"writable_member is not an online hosted repository matching group format"}
  ```

  A group without a hosted member answers `405`:

  ```json
  {"error":"group repository has no hosted member — publish directly to a hosted repository"}
  ```
- **404, not error.** A `GET` for a path a hosted repository does not keep — a
  `PACKAGES.rds` on a hosted repository, a tarball that was never uploaded —
  answers `404`. A wrong *method* answers `405`.

## 7. The proxy and the group against an upstream mirror

The proxy sits in front of `https://cran.r-project.org` and relays the
upstream index, caching what it fetched:

```bash
curl -s -o /dev/null -w "%{http_code}\n" \
  http://localhost:8081/repository/cran-proxy/src/contrib/PACKAGES
```

```
200
```

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/assets?repository=cran-proxy \
  | grep -o '"path":"[^"]*"'
```

```
"path":"/src/contrib/PACKAGES"
"path":"/src/contrib/PACKAGES.rds"
"path":"/src/contrib/R6_2.6.1.tar.gz"
```

The set depends on what the clients asked for — a client that falls back to
`PACKAGES.gz` caches that document as well. The proxy relays the upstream
`PACKAGES.rds` as it is, so a client that reads the binary index gets
upstream's own document.

The group merges the members' `PACKAGES` documents into one index — the hosted
packages *and* the upstream ones — so a client can use a single URL. After the
`DELETE` of §5 the hosted repository holds one version of the package, and the
merged index carries that stanza:

```bash
curl -s http://localhost:8081/repository/cran-group/src/contrib/PACKAGES \
  | grep -A1 "^Package: nexspencedemo"
```

```
Package: nexspencedemo
Version: 0.2.0
```

In R:

```r
options(repos = c(CRAN = "http://localhost:8081/repository/cran-group"))

ap <- available.packages()
ap[ap[, "Package"] %in% c("nexspencedemo", "R6"), c("Package", "Version", "Repository")]
```

```
              Package         Version
nexspencedemo "nexspencedemo" "0.2.0"
R6            "R6"            "2.6.1"
              Repository
nexspencedemo "http://localhost:8081/repository/cran-group/src/contrib"
R6            "http://localhost:8081/repository/cran-group/src/contrib"
```

Installing through the group works for both kinds of package. Both commands
download from the group URL; the group answers from the member that holds the
file — the hosted one for your own packages, the proxy one for the upstream's:

```r
install.packages("nexspencedemo")   # the group's hosted member
install.packages("R6")              # the group's proxy member, from cran.r-project.org
```

```
trying URL 'http://localhost:8081/repository/cran-group/src/contrib/R6_2.6.1.tar.gz'
Content type 'application/x-gzip' length 64507 bytes (62 KB)
==================================================
downloaded 62 KB

* installing *source* package ‘R6’ ...
** this is package ‘R6’ version ‘2.6.1’
** package ‘R6’ successfully unpacked and MD5 sums checked
** using staged installation
** R
** byte-compile and prepare package for lazy loading
** help
*** installing help indices
*** copying figures
** building package indices
** testing if installed package can be loaded from temporary location
** testing if installed package can be loaded from final location
** testing if installed package keeps a record of temporary installation path
* DONE (R6)
```

The group cannot merge the binary `PACKAGES.rds` — there is no way to merge an
R serialization — so it refuses it with a `502` instead of handing the client a
member's document that would hide the other member's packages:

```bash
curl -s http://localhost:8081/repository/cran-group/src/contrib/PACKAGES.rds
```

```json
{"error":"members of group \"cran-group\" could not be merged into one index, so the answer would be incomplete: cran: PACKAGES.rds is a binary format the group cannot merge; client falls back to PACKAGES.gz"}
```

R treats a failed download of `PACKAGES.rds` exactly like a failed read and
falls through to `PACKAGES.gz`, which the group does merge.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `unable to access index for repository …` | The hosted repository is not anonymous | Put the credentials in the URL, or set `"allowAnonymous": true` on it |
| `package ‘x’ is not available for this version of R` | The package is not in the index | `available.packages()` — check that the name is listed; the index only knows what was uploaded |
| `404` on `PACKAGES.rds` | The repository does not keep that document | Expected on a hosted repository; R falls back to `PACKAGES.gz` |
| `502` on `PACKAGES.rds` of a group | The binary index cannot be merged | Expected; R falls back to the merged `PACKAGES.gz` |
| The version in the index is not the one in the `DESCRIPTION` | The version comes from the file name | Name the tarball `<pkg>_<version>.tar.gz` |
| `Depends` is `NA` | The index carries `Package` and `Version` only | The server does not parse the `DESCRIPTION` in this version |
| `405 Method Not Allowed` on upload | You are publishing into the **proxy** | Publish into `cran-hosted`; a proxy is read-only |
| `409 Conflict` on an upload through a group | The group's `writable_member` names a member that is not an online hosted repository of the same format | Name a hosted member, or drop the key — the group then writes to its first hosted member |
| `401 Unauthorized` on every fetch | The repository is not anonymous | Send `-u user:password`, or put the credentials in the URL |
| `403 Forbidden` on publish | The user authenticated but lacks the write privilege | The role needs a privilege that allows `write` — see [RBAC and content selectors](../security-rbac.md) |
| The download goes to the wrong host | `repos` already contains `/src/contrib` | Give R the repository root; R appends `/src/contrib` itself |
| `403` on `/service/rest/v1/...` but the UI works | The API authenticates by `Authorization` header, the UI by cookie | Send `-u user:password` or `Authorization: Bearer <token>` |

## Related

- [RBAC and content selectors](../security-rbac.md)
- [Migration from Sonatype Nexus](../nexus-migration.md)
- [API specification](../api-spec.yaml)
