# Maven guide — hosted, proxy and group repositories

A working Maven setup against Nexspence: how to create the repositories, how to
publish, how to resolve, and what breaks when it does not.

Everything below assumes a fresh instance on `http://localhost:8081` (the default
port of the Docker Compose quick start). Replace the host everywhere if yours
differs.

## 1. Create the repositories

Nexspence ships a helper that creates all three repository types with the right
payloads:

```bash
./scripts/create-maven-repos.sh
```

It defaults to `http://localhost:8081` — point it at your instance with
`BASE_URL` when it differs:

```bash
BASE_URL=https://nexus.example.com ./scripts/create-maven-repos.sh
```

That script creates `maven-hosted`, `maven-proxy` and `maven-group` — the
three names this guide uses.

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
  http://localhost:8081/service/rest/v1/repositories/maven2/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"maven-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Internal releases\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of Maven Central
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/maven2/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"maven-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Proxy to Maven Central\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://repo1.maven.org/maven2\" }
  }"

# group: one URL for both
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/maven2/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"maven-group\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Group of releases and central\",
    \"formatConfig\": { \"member_names\": [\"maven-hosted\", \"maven-proxy\"] }
  }"
```

Repository URLs follow one pattern:

```
http://localhost:8081/repository/<repository-name>/
```

## 2. Configure the client

### `~/.m2/settings.xml` — resolve through the group

```xml
<settings>
  <mirrors>
    <mirror>
      <id>nexspence</id>
      <name>Nexspence group</name>
      <url>http://localhost:8081/repository/maven-group/</url>
      <mirrorOf>*</mirrorOf>
    </mirror>
  </mirrors>

  <servers>
    <server>
      <id>nexspence</id>
      <username>admin</username>
      <password>admin123</password>
    </server>
  </servers>
</settings>
```

`<mirrorOf>*</mirrorOf>` sends every dependency through Nexspence. If you only
want internal artifacts mirrored, use `external:*` or list the repository IDs
explicitly — `<mirrorOf>*</mirrorOf>` also intercepts the plugin repositories.

### `pom.xml` — publish releases and snapshots separately

```xml
<distributionManagement>
  <repository>
    <id>nexspence</id>
    <url>http://localhost:8081/repository/maven-hosted/</url>
  </repository>
  <snapshotRepository>
    <id>nexspence</id>
    <url>http://localhost:8081/repository/maven-hosted/</url>
  </snapshotRepository>
</distributionManagement>
```

The `<id>` must match the `<server><id>` in `settings.xml` — that is how Maven
knows which credentials to use for which repository.

The helper script creates one hosted repository, so snapshots go to the same
`maven-hosted` as releases here. If you want them separated, create a second
hosted repository with the same payload under the name `maven-snapshots` and
point `<snapshotRepository>` at it — the server does not enforce a version
policy for you, so the separation is a convention your team keeps.

### Use an API token instead of a password

Nexspence accepts an `nxs_*` API token as the password of a Basic
authentication, which is what you want in CI:

```xml
<server>
  <id>nexspence</id>
  <username>ci-bot</username>
  <password>nxs_xxxxxxxxxxxxxxxxxxxx</password>
</server>
```

Create the token in the profile panel (the 🔑 icon in the sidebar) — a user
manages their own tokens; there is no admin page for them. The token is stored
hashed and can be revoked without changing the user's password.

## 3. Publish

```bash
mvn -B deploy
```

Maven `PUT`s the POM and the JAR to the repository path, and Nexspence computes
the checksums itself:

```
PUT /repository/maven-hosted/com/example/myapp/1.0.0/myapp-1.0.0.jar
PUT /repository/maven-hosted/com/example/myapp/1.0.0/myapp-1.0.0.pom
```

You do **not** upload `.sha1`/`.md5`/`.sha256` sidecars — they are served from
the stored digest. If you upload them anyway, they are accepted and ignored.

`maven-metadata.xml` is the mutable index of a version directory. It is generated
by the server; a client that tries to write it is not authoritative.

## 4. Resolve and verify

```bash
# dependency resolution through the group
mvn -B dependency:get -Dartifact=com.example:myapp:1.0.0

# what does the repository actually hold?
curl -u admin:admin123 \
  http://localhost:8081/service/rest/v1/components?repository=maven-hosted

# the metadata index of one artifact
curl -u admin:admin123 \
  http://localhost:8081/repository/maven-hosted/com/example/myapp/maven-metadata.xml
```

## 5. How group repositories resolve

A group repository is an ordered union of its members. Two rules, and they are
different:

- **Component lookups** — hosted members are searched first, then proxy members;
  the first match wins. A `1.0.0` in your hosted repository shadows the same
  version in the proxy.
- **Index documents** — `maven-metadata.xml` is *merged* across the members
  instead of being shadowed by the first member that answers. That is what keeps
  `mvn versions:display-dependency-updates` from missing versions that live
  upstream.

## 6. Anonymous access

A proxy or group repository with `"allowAnonymous": true` can be read without
credentials, which is what you want for a shared build agent. A hosted
repository should keep `"allowAnonymous": false` so that publishing stays
authenticated. Read access is then granted per role — see
[security-rbac.md](../security-rbac.md).

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `401 Unauthorized` on publish | No `<server>` entry for that `<id>`, or the credentials are wrong | Match `<server><id>` with the `<repository><id>` in `pom.xml` |
| `403 Forbidden` on publish | The user authenticated but lacks the write privilege on that repository | The role needs a privilege that allows `write` — see [RBAC and content selectors](../security-rbac.md); a bare `nx-deployer` role carries no privilege until you attach one |
| `405 Method Not Allowed` on push | You are pushing into a **proxy** repository | Proxy repositories are read-only by design — push to the hosted one |
| `404` for a dependency that exists upstream | The proxy is offline or the remote URL is wrong | Check `proxyConfig.remote_url`; the trailing `/maven2` matters |
| Checksum mismatch in the client | A stale `.sha1` was uploaded by an external tool | Checksums are server-computed; delete the sidecar files |
| `maven-metadata.xml` looks stale | A snapshot was published after the metadata was generated | The metadata is refreshed on publish; re-run the deploy |
| `403` on `/service/rest/v1/...` but the UI works | The API authenticates by `Authorization` header, the UI by cookie | Send `-u user:password` or `Authorization: Bearer <token>` |

## Related

- [RBAC and content selectors](../security-rbac.md)
- [Migration from Sonatype Nexus](../nexus-migration.md)
- [GitHub Actions guide](github-actions.md)
- [API specification](../api-spec.yaml)
