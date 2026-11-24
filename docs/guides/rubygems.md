# RubyGems guide — a private gem server

A RubyGems repository is two things: a set of `.gem` files and an index that
tells a client which files exist and what they depend on. Nexspence serves
the **compact index** (`/versions`, `/info/<gem>`, `/names`) and the `.gem`
files themselves, so a repository can be a hosted one, a proxy in front of
`rubygems.org`, or a group of the two.

Everything below assumes a fresh instance on `http://localhost:8081` (the
default port of the Docker Compose quick start). Replace the host everywhere
if yours differs.

## 1. Create the repositories

Nexspence ships a helper that creates all three repository types with the
right payloads:

```bash
./scripts/create-rubygems-repos.sh
```

It defaults to `http://localhost:8081` — point it at your instance with
`BASE_URL` when it differs:

```bash
BASE_URL=https://nexus.example.com ./scripts/create-rubygems-repos.sh
```

That script creates `rubygems-hosted`, `rubygems-proxy` and `rubygems-group` —
the three names this guide uses.

Under the hood it does three `POST` calls against the v1-compatible API. This
is the same thing by hand, and it is worth knowing because the API is what CI
scripts talk to:

```bash
# resolve the blob store id first — a repository must reference an existing one
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# hosted — your own gems
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/rubygems/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"rubygems-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"RubyGems hosted repository\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of rubygems.org
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/rubygems/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"rubygems-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"RubyGems proxy → https://rubygems.org\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://rubygems.org\" }
  }"

# group — one source for both
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/rubygems/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"rubygems-group\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"RubyGems group (hosted + proxy)\",
    \"formatConfig\": { \"member_names\": [\"rubygems-hosted\", \"rubygems-proxy\"] }
  }"
```

Repository URLs follow one pattern:

```
http://localhost:8081/repository/<repository-name>/
```

## 2. The endpoints

Every repository answers the same set of paths. The hosted repository serves
them from its own components, the proxy forwards them to `rubygems.org`:

| Endpoint | Method | Used for |
|---|---|---|
| `/versions` | `GET` | the master list: every gem with its versions and the checksum of its `/info` file |
| `/info/<gem>` | `GET` | one gem: its versions, its runtime dependencies, its checksum |
| `/names` | `GET` | the list of gem names |
| `/gems/<name>-<version>[-<platform>].gem` | `GET` | the gem file itself |
| `/api/v1/gems` | `POST` | publish — the body is the `.gem` |
| `/api/v1/gems/yank` | `DELETE` | unpublish one version |

The compact index is generated from the stored components, so it always
reflects what was published:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/rubygems-hosted/versions
```

## 3. Register the source

`gem sources` manages the list of repositories the client reads. The source
URL **must end in a slash** — the client appends the index file name to it,
and without the slash it asks for `/repository/specs.4.8.gz` instead of
`/repository/rubygems-group/specs.4.8.gz`:

```bash
gem sources --add http://localhost:8081/repository/rubygems-group/
```

```
http://localhost:8081/repository/rubygems-group/ added to sources
```

```bash
gem sources --list
```

```
*** CURRENT SOURCES ***

https://rubygems.org/
http://localhost:8081/repository/rubygems-group/
```

The sources live in `~/.gemrc`, so the user who runs the command decides who
sees them.

### A private source

A hosted repository with `"allowAnonymous": false` needs credentials. The
client sends them as HTTP Basic, and takes them from the source URL:

```bash
gem list -r --clear-sources \
  --source http://admin:admin123@localhost:8081/repository/rubygems-hosted/ \
  nexspence-demo
```

The request is authenticated — the server does not answer `401` — but the
result is empty, because this client resolves against the legacy index and
the hosted repository does not serve it (see
[section 7](#7-the-compact-index-and-the-legacy-index)).

The credentials end up in plain text in `~/.gemrc`, and the client redacts
them in its error messages. An `nxs_…` API token works as the password — the
server accepts it in place of the user password — so a CI agent can use a
token instead of the admin password:

```bash
curl -s -u admin:admin123 -X POST \
  http://localhost:8081/api/v1/tokens \
  -H "Content-Type: application/json" \
  -d '{"name": "ci-bot"}'
```

```bash
gem list -r --clear-sources \
  --source http://ci-bot:nxs_xxxxxxxxxxxxxxxxxxxx@localhost:8081/repository/rubygems-hosted/ \
  nexspence-demo
```

The token value is shown once, at creation; `DELETE /api/v1/tokens/<id>`
revokes it.

### The hosted repository cannot be registered as a source

The client checks a source before it accepts it: it fetches the source's
`specs.4.8.gz` and refuses the source when that fails. The hosted repository
does not serve the legacy Marshal index (see [section 7](#7-the-compact-index-and-the-legacy-index)),
so the check fails and the source is not added:

```
Error fetching http://localhost:8081/repository/rubygems-hosted/:
	bad response Not Found 404 (http://localhost:8081/repository/rubygems-hosted/specs.4.8.gz)
```

Without credentials the same attempt ends in `401 Unauthorized` instead. The
proxy and the group can be registered — `rubygems.org` has the legacy index,
so the check succeeds.

## 4. Publish

Build the gem first. A minimal gemspec with no runtime dependencies is
enough:

```ruby
# nexspence-demo.gemspec
Gem::Specification.new do |spec|
  spec.name        = "nexspence-demo"
  spec.version     = "1.0.0"
  spec.summary     = "Demo gem for the Nexspence RubyGems guide"
  spec.description = "A tiny gem used to demonstrate publishing and resolving against Nexspence."
  spec.authors     = ["Nexspence"]
  spec.email       = ["docs@nexspence.local"]
  spec.homepage    = "https://example.invalid/nexspence-demo"
  spec.license     = "MIT"

  spec.files         = ["lib/nexspence-demo.rb"]
  spec.require_paths = ["lib"]

  spec.required_ruby_version = ">= 2.6"
end
```

```bash
mkdir -p lib
echo 'module NexspenceDemo; VERSION = "1.0.0"; end' > lib/nexspence-demo.rb

gem build nexspence-demo.gemspec
```

```
Successfully built RubyGem
  Name: nexspence-demo
  Version: 1.0.0
  File: nexspence-demo-1.0.0.gem
```

### The credentials

`gem push` sends the API key as the `Authorization` header. There is no
`--username`/`--password` pair: the key is stored per host in
`~/.gem/credentials`, and the server reads Basic credentials from that
header.

```bash
mkdir -p ~/.gem
printf -- '---\nhttp://localhost:8081/repository/rubygems-hosted: "Basic %s"\n' \
  "$(printf 'admin:admin123' | base64)" > ~/.gem/credentials
chmod 600 ~/.gem/credentials
```

```
---
http://localhost:8081/repository/rubygems-hosted: "Basic YWRtaW46YWRtaW4xMjM="
```

The key of the entry is the host exactly as it is passed to `gem push`, and
the file must be readable by its owner only — the client warns otherwise.

### The push

The host must **not** end in a slash here: the client appends
`/api/v1/gems` to it, and a trailing slash produces a path with an empty
segment, which the server rejects.

```bash
gem push nexspence-demo-1.0.0.gem \
  --host http://localhost:8081/repository/rubygems-hosted
```

```
Pushing gem to http://localhost:8081/repository/rubygems-hosted...
Successfully registered gem: nexspence-demo (1.0.0)
```

The gem is in the index immediately:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/rubygems-hosted/versions
```

```
created_at: 2026-11-24T11:32:52Z
---
nexspence-demo 1.0.0 6f7de6957611760e2b421a4585c8f631
```

The third column is the MD5 of the `/info` file of that gem, and it changes
with every build — the checksum of the `.gem` inside the `/info` line changes
with it.

and the file is downloadable from the path the client expects:

```bash
curl -s -o nexspence-demo-1.0.0.gem \
  -u admin:admin123 \
  http://localhost:8081/repository/rubygems-hosted/gems/nexspence-demo-1.0.0.gem
```

## 5. How the server decides what a version and a platform are

The server does not decide — the gem does. The version and the platform are
read from the gem's own metadata (`metadata.gz` inside the `.gem` archive),
never from the file name and never from a request header. What the server
then does with them:

- **A pure-Ruby gem** (`platform: ruby`) is stored under its plain version —
  `1.0.0`.
- **A platform-specific gem** is stored under the version **with the platform
  appended**: `2.0.0` with `platform: x86_64-linux` becomes `2.0.0-x86_64-linux`.
  The file is named after it
  (`/gems/nexspence-platform-2.0.0-x86_64-linux.gem`) and the compact index
  lists it under that name:

```ruby
# nexspence-platform.gemspec — the same gemspec, with another name, another
# version and the platform line
spec.name        = "nexspence-platform"
spec.version     = "2.0.0"
spec.platform    = "x86_64-linux"
# … the rest of the plain gemspec, including required_ruby_version
```

```bash
gem build nexspence-platform.gemspec
gem push nexspence-platform-2.0.0-x86_64-linux.gem \
  --host http://localhost:8081/repository/rubygems-hosted
```

```
Successfully registered gem: nexspence-platform (2.0.0-x86_64-linux)
```

```
nexspence-platform 2.0.0-x86_64-linux dd4da0e52c7310411f3909596b797014
```

```
2.0.0-x86_64-linux |checksum:3ae74d4dd14fa6a5496a982da32c956072eba2b2075b972a307741cfe89879cf,ruby:>= 2.6,rubygems:>= 0
```

The checksum is the SHA-256 of the `.gem` file, and every build produces a new
one — the `ruby:>= 2.6` part is the `required_ruby_version` of the gemspec,
carried into the index.

- **There is no platform allow-list and no version policy.** A gem that
  carries a platform nobody has heard of is accepted and indexed, and a
  version string that is not a RubyGems version at all is stored verbatim.
  The `gem` client never lets such a version reach the server — `gem build`
  refuses it (`Malformed version number string not-a-version`) — but a
  hand-built `.gem` does, and the server takes it. A `.gem` is a tar archive
  with a gzipped YAML gemspec in it, so one can be assembled by hand:

```bash
python3 - <<'PY'
import tarfile, gzip, io
buf = io.BytesIO()
with tarfile.open(fileobj=buf, mode="w") as tf:
    data = gzip.compress(b"---\nname: nexspence-raw\nversion:\n  version: not-a-version\nplatform: ruby\n")
    ti = tarfile.TarInfo("metadata.gz"); ti.size = len(data); ti.mode = 0o644
    tf.addfile(ti, io.BytesIO(data))
    d = gzip.compress(b"")
    ti = tarfile.TarInfo("data.tar.gz"); ti.size = len(d); ti.mode = 0o644
    tf.addfile(ti, io.BytesIO(d))
open("odd.gem", "wb").write(buf.getvalue())
PY
```

```bash
curl -s -u admin:admin123 -X POST --data-binary @odd.gem \
  http://localhost:8081/repository/rubygems-hosted/api/v1/gems
```

```
Successfully registered gem: nexspence-raw (not-a-version)
```

- **Prereleases are not separated.** `1.0.0.beta` sits in the same
  `/versions` file as the stable versions; there is no prerelease split in
  the compact index.

These are the ways a push fails — none of them is the platform or the
version:

| Push | Answer |
|---|---|
| a body that is not a gem (no `metadata.gz` entry) | `400 {"error":"not a gem: no metadata.gz entry"}` |
| a `metadata.gz` that is not gzip | `400 {"error":"metadata.gz: unexpected EOF"}` |
| a gemspec without a name and a version | `400 {"error":"gemspec metadata: name and version are required"}` |
| no credentials | `401 {"error":"authentication required"}` |
| a push to a **proxy** | `405 {"error":"proxy repository is read-only (use a hosted repository to publish)"}` |
| a body over 256 MB | `413 {"error":"gem exceeds the maximum size"}` |

The first five answers are what the server returns, and the verification
script asserts each of them. The 256 MB limit is the server's own — the
script does not upload a body that large.

A push to a **group** is accepted: the group forwards it to its hosted
member, and the gem lands there.

```bash
gem push nexspence-demo-1.0.0.gem \
  --host http://localhost:8081/repository/rubygems-group
```

```
Pushing gem to http://localhost:8081/repository/rubygems-group...
Successfully registered gem: nexspence-demo (1.0.0)
```

Publishing the same version twice overwrites the stored file — the
repository's write policy decides that, not the format.

## 6. Resolve

### `gem list -r`

```bash
gem list -r --clear-sources \
  --source http://localhost:8081/repository/rubygems-group/ rake
```

```
rake (13.4.2)
```

Against the group this lists what the **proxy** member holds. Against the
hosted repository it lists nothing: the client asks for the legacy index,
which the hosted repository does not serve.

### `gem install`

The same split applies to installing:

```bash
gem install rake --clear-sources \
  --source http://localhost:8081/repository/rubygems-proxy/ --no-document
```

```
Successfully installed rake-13.4.2
1 gem installed
```

The gem is downloaded from `/gems/rake-13.4.2.gem` — the same path the
server stores it under — and the client caches the index under
`~/.gem/specs/<host>/`.

Against the hosted repository the install cannot work, and the error names
the missing file:

```bash
gem install nexspence-demo --clear-sources \
  --source http://localhost:8081/repository/rubygems-hosted/ --no-document
```

```
ERROR:  Could not find a valid gem 'nexspence-demo' (>= 0), here is why:
          Unable to download data from http://localhost:8081/repository/rubygems-hosted/ - bad response Not Found 404 (http://localhost:8081/repository/rubygems-hosted/specs.4.8.gz)
```

Against the group the gem is not found either, because the group's legacy
index comes from the proxy member:

```
ERROR:  Could not find a valid gem 'nexspence-demo' (>= 0) in any repository
```

## 7. The compact index and the legacy index

Two index formats are in play, and they are not the same thing.

**The compact index** is what the server generates:

- `/versions` — the master list. One line per gem: the name, its versions
  separated by commas, and the MD5 of that gem's `/info` file.
- `/info/<gem>` — one line per version:
  `VERSION[-PLATFORM] [DEP:REQ,…]|checksum:SHA256[,ruby:REQ][,rubygems:REQ]`.
  The dependency list holds the **runtime** dependencies only; development
  dependencies never reach a resolver.
- `/names` — the list of gem names.

**The legacy Marshal index** is the older format, three gzipped Marshal files
at the root of the repository:

| File | Contents |
|---|---|
| `specs.4.8.gz` | every released version of every gem |
| `latest_specs.4.8.gz` | the newest released version of every gem |
| `prerelease_specs.4.8.gz` | every prerelease version of every gem |

plus the **quick index** under `quick/Marshal.4.8/<name>-<version>.gemspec.rz`,
which carries the marshalled gemspec of a single version and is what the
client reads to resolve dependencies.

Which of them the client asks for depends on the command — this is what a
run against the stand shows:

```bash
gem sources --add http://localhost:8081/repository/rubygems-group/
```

```
GET http://localhost:8081/repository/rubygems-group/specs.4.8.gz
```

```bash
gem list -r --clear-sources \
  --source http://localhost:8081/repository/rubygems-group/ rake --verbose
```

```
GET http://localhost:8081/repository/rubygems-group/latest_specs.4.8.gz
```

```bash
gem install rake --clear-sources \
  --source http://localhost:8081/repository/rubygems-proxy/ --verbose
```

```
HEAD http://localhost:8081/repository/rubygems-proxy/api/v1/dependencies
GET http://localhost:8081/repository/rubygems-proxy/prerelease_specs.4.8.gz
GET http://localhost:8081/repository/rubygems-proxy/specs.4.8.gz
GET http://localhost:8081/repository/rubygems-proxy/gems/rake-13.4.2.gem
```

These four requests are what the script asserts. The client reads the quick
index (`quick/Marshal.4.8/<gem>-<version>.gemspec.rz`) as well when it needs
the dependencies of a gem and has not cached them yet — that request is
skipped on a second run, so the script does not assert it.

The **hosted** repository answers `404` for all three index files — the
compact index replaced them, and the legacy files are not generated:

```bash
curl -s -o /dev/null -w "%{http_code}\n" -u admin:admin123 \
  http://localhost:8081/repository/rubygems-hosted/specs.4.8.gz
```

```
404
```

The **proxy** relays `rubygems.org`'s copies, so all three answer `200`
there — and the group inherits them, because the hosted member answers `404`
and the proxy member answers `200`.

The practical consequence: the plain `gem` client resolves against the
legacy index, so it can install from the proxy but not from the hosted
repository. The compact index is what the hosted repository serves, and
clients that speak it — Bundler — read `/versions` and `/info/<gem>`.

### Bundler and the missing ETag

Bundler fetches `/versions` first and then `/info/<gem>`, and it validates
the body against the ETag the server sends. A `Gemfile` that points at the
hosted repository is enough to see it:

```ruby
# Gemfile
source 'http://admin:admin123@localhost:8081/repository/rubygems-hosted/'

gem 'nexspence-demo'
```

```bash
bundle install --verbose
```

```
HTTP GET http://admin:admin123@localhost:8081/repository/rubygems-hosted/versions
HTTP 200 OK http://admin:admin123@localhost:8081/repository/rubygems-hosted/versions
The checksum of /versions does not match the checksum provided by the server! Something is wrong (local checksum is "\"7482ed5b1e371147722b7db9c08b1c04\"", was expecting "").
HTTP GET http://admin:admin123@localhost:8081/repository/rubygems-hosted/api/v1/dependencies
HTTP 405 Method Not Allowed http://admin:admin123@localhost:8081/repository/rubygems-hosted/api/v1/dependencies
Fetching source index from http://localhost:8081/repository/rubygems-hosted/
Gem::RemoteFetcher::FetchError: bad response Not Found 404 (http://admin:REDACTED@localhost:8081/repository/rubygems-hosted/specs.4.8.gz)
```

The hosted repository sends no ETag for `/versions`, so Bundler does not
trust the compact index and falls back to the legacy one — which the hosted
repository does not serve either, and the install ends in a `404` on
`specs.4.8.gz`. The proxy relays `rubygems.org`'s ETag, so Bundler resolves
against the proxy. The script asserts each of the four steps above.

## 8. The group

A group repository is an ordered union of its members, and the first member
that answers wins:

- `/info/rake` falls through the hosted member (`404`) to the proxy member
  and answers from `rubygems.org`.
- `/versions` is answered by the hosted member, so the group's compact index
  lists the hosted gems only — the format does not merge the index across
  the members the way Maven merges `maven-metadata.xml`.
- An unknown path is answered by the hosted member with `405 Method Not
  Allowed` — not `404` — and the group relays that instead of asking the
  proxy member. This is what breaks `gem install` through the group: the
  client asks for the quick index and gets

```
GET http://localhost:8081/repository/rubygems-group/quick/Marshal.4.8/rake-13.4.2.gemspec.rz
405 Method Not Allowed
```

Publishing through the group works — the publish is forwarded to the hosted
member — but unpublishing does not: a `DELETE` on a group is refused with
`405 {"error":"group repository is read-only — publish to a member hosted repository"}`.

## 9. Unpublish a version

```bash
gem yank nexspence-demo -v 1.0.0 \
  --host http://localhost:8081/repository/rubygems-hosted
```

```
Yanking gem from http://localhost:8081/repository/rubygems-hosted...
Successfully yanked gem: nexspence-demo (1.0.0)
```

The version disappears from `/versions` and the file is removed.

A platform-specific gem needs its platform-qualified version. `gem yank -v
2.0.0` names a version that does not exist in the registry, and the server
reports success while removing nothing:

```bash
curl -s -u admin:admin123 -X DELETE \
  "http://localhost:8081/repository/rubygems-hosted/api/v1/gems/yank?gem_name=nexspence-platform&version=2.0.0-x86_64-linux"
```

```
Successfully yanked gem: nexspence-platform (2.0.0-x86_64-linux)
```

## 10. Anonymous access

The proxy and the group are created with `"allowAnonymous": true`, so a
build agent can read them without credentials. The hosted repository keeps
`"allowAnonymous": false`: every request to it needs credentials, reads
included. An anonymous read is answered `401`, and so is an anonymous
publish.

```bash
curl -s -o /dev/null -w "%{http_code}\n" \
  http://localhost:8081/repository/rubygems-hosted/versions
```

```
401
```

Grant read access per role instead of turning anonymous access on — see
[security-rbac.md](../security-rbac.md).

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `Error fetching …/specs.4.8.gz` when adding a source | The repository does not serve the legacy index, or the read is not anonymous | The proxy and the group can be registered; the hosted repository cannot, because the client validates a source against `specs.4.8.gz` |
| `Could not find a valid gem … bad response Not Found 404 (…/specs.4.8.gz)` | The client resolves against the legacy index, which the hosted repository does not serve | Install through the proxy, or use a client that speaks the compact index |
| `405 Method Not Allowed` on `/quick/Marshal.4.8/…` | The hosted member answers unknown paths with `405` and the group relays it | Install through the proxy member, or point the source at the proxy |
| `{"error":"path must not contain an empty segment"}` on push | The `--host` URL ends in a slash and the client appends `/api/v1/gems` | Drop the trailing slash from `--host` |
| The client asks for `/repository/specs.4.8.gz` | The source URL does not end in a slash | Add the trailing slash to the source URL |
| `Enter your … credentials` on push | No API key for that host in `~/.gem/credentials` | Add the host entry with a `Basic` key; `gem signin` is not supported by the server |
| `{"error":"authentication required"}` on push | The stored key is missing, wrong, or the file is not readable | Check `~/.gem/credentials` and its permissions (`0600`) |
| `{"error":"proxy repository is read-only"}` on push | The push went to a proxy repository | Push to the hosted repository, or to the group, which forwards it |
| `gem yank` reports success but the version stays | The version carries a platform and the plain version was given | Yank with the platform-qualified version |
| A pushed gem is not in `gem list -r` | The client reads the legacy index, not the compact one | Check `/versions` and `/info/<gem>` directly |
| `gem install` fails after the repository changed | The client caches the index under `~/.gem/specs/<host>/` | Remove that directory to force a refresh |
| `The checksum of /versions does not match the checksum provided by the server!` | The repository serves the compact index without an ETag | Bundler falls back to the legacy index; the hosted repository does not serve that one |

## Related

- [RBAC and content selectors](../security-rbac.md)
- [GitHub Actions guide](github-actions.md)
- [API specification](../api-spec.yaml)
