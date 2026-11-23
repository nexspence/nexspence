# GitHub Actions guide — publishing to Nexspence from CI

Most teams meet Nexspence through their pipeline, not through the UI. This guide
covers the three pieces you need: a credential that a runner can use, a
workflow that publishes, and a way to verify what landed.

## 1. Give the pipeline its own account

Never use a person's password in CI. Create a dedicated user and issue an API
token for it.

**In the web UI:** **Admin → Security → Users** — create the user and assign
its role. API tokens are per user and are managed by their owner in the
profile panel (the 🔑 icon in the sidebar), not by an admin page.

**From the CLI** (the CLI needs a credential of its own to do this):

```bash
export NXS_URL=http://localhost:8081
export NXS_TOKEN=nxs_xxxxxxxxxxxxxxxxxxxx   # an admin token

nxs user create ci-bot --email ci-bot@example.com --password '…'
```

> `nxs user create` is not idempotent — a second run against the same username
> fails with `conflict: resource already exists` and changes nothing. That also
> means it cannot be used to reset a password: the existing password stays.
> Use the UI or `PUT /service/rest/v1/security/users/{userId}/change-password`
> for that.

### The role needs a privilege — a bare role grants nothing

This is the part that surprises everyone: **assigning `nx-deployer` to the CI
user is not enough.** The built-in roles ship without any privileges attached,
so the account authenticates and is then denied on every repository. A role is
a container; the privilege inside it is what grants access.

Three objects, in this order:

1. **Content Selector** — what the rule covers.
2. **Privilege** — the permission, bound to the selector.
3. **Role** — a set of privileges, assigned to the user.

```bash
# 1. Content Selector — which repository the pipeline may write to
SEL=$(curl -s -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/security/content-selectors \
  -H "Content-Type: application/json" \
  -d '{"name":"ci-maven","description":"CI publish target",
       "expression":"repository == \"maven-hosted\""}' \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# 2. Privilege — the permission itself
PRIV=$(curl -s -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/security/privileges \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"ci-maven-write\",\"description\":\"CI write access\",
       \"type\":\"repository-content-selector\",
       \"contentSelectorId\":\"${SEL}\",
       \"attrs\":{\"actions\":[\"browse\",\"read\",\"write\"]}}" \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# 3. Role — a role of your own, with that privilege attached
ROLE=$(curl -s -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/security/roles \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"ci-publisher\",\"description\":\"CI publishing\",
       \"privileges\":[\"${PRIV}\"]}" \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# 4. Attach the role to the CI user
curl -s -u admin:admin123 -X PUT \
  "http://localhost:8081/service/rest/v1/security/users/ci-bot/roles" \
  -H "Content-Type: application/json" \
  -d "{\"roleIds\":[\"${ROLE}\"]}"
```

The same four objects are available in the UI: **Security → Content Selectors**,
**Security → Privileges**, **Security → Roles**, **Users & Roles → Users**.

> Give the pipeline a role of its own. The built-in roles (`nx-deployer` and
> friends) can be edited, but they are shared: attaching your privilege to one
> of them grants it to every other user of that role as well.

> Content Selectors accept two shapes only — `repository == "<name>"` and
> `path.startsWith("/…")` — optionally combined with `&&`. A selector that
> uses anything else (`format == "…"` on its own, `||`, `!`, `contains()`)
> matches nothing and denies access. See
> [RBAC and content selectors](../security-rbac.md) for the exact list.

Now the user can write to `maven-hosted` and nothing else — and can read
nothing that is not covered by a `read` action.

### Issue the token

An API token belongs to the account that created it — there is no admin path
that mints one for somebody else. So the CI user creates its own token, by
logging in first:

```bash
# the CI user logs in and mints a token for itself
JWT=$(curl -s -X POST "$NXS_URL/api/v1/login" \
  -H "Content-Type: application/json" \
  -d '{"username":"ci-bot","password":"…"}' | jq -r .token)

nxs token create github-actions --url "$NXS_URL" --token "$JWT"
```

The token is printed **once** — copy it. It is stored hashed and can be revoked
without touching the password.

```bash
# what the token can do
nxs token list
```

> `nxs token create` needs a credential of its own: the `--url` and `--token`
> flags, or `NXS_URL` and `NXS_TOKEN` in the environment. Without one it
> answers `invalid token or session expired`.

## 2. Store it as a repository secret

**Settings → Secrets and variables → Actions → New repository secret**

| Name | Value |
|---|---|
| `NEXSPENCE_URL` | `https://nexspence.example.com` |
| `NEXSPENCE_TOKEN` | the `nxs_…` token |

Use **environment secrets** instead of repository secrets when different
pipelines need different privileges — a release pipeline and a nightly build
should not share a credential.

> The token is a write credential. Do not echo it in a workflow log, do not put
> it in a `run:` block as a literal, and do not let a job that runs untrusted
> pull-request code see it.

## 3. Publish with the CLI

The `nxs` CLI reads `NXS_URL` and `NXS_TOKEN` from the environment, so a runner
needs no login step and no config file. Both are also available as global flags
(`nxs --url … --token …`) for a one-off command.

```yaml
name: publish

on:
  push:
    tags: ['v*']

jobs:
  publish:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@v4

      - name: Install nxs
        run: curl -sSfL https://raw.githubusercontent.com/nexspence/nxs/main/install.sh | sh

      - name: Publish
        env:
          NXS_URL: ${{ secrets.NEXSPENCE_URL }}
          NXS_TOKEN: ${{ secrets.NEXSPENCE_TOKEN }}
        run: |
          nxs repo list
          nxs push maven-hosted \
            "com/example/myapp/${{ github.ref_name }}" \
            target/myapp-${{ github.ref_name }}.jar
```

`nxs push <repo> <remote-prefix> <local>` uploads a file to
`<repository>/<remote-prefix>/<file>` — the layout a Maven repository expects.

## 4. Publish a Maven artifact

Maven clients speak their own protocol, so use Maven itself:

```yaml
      - name: Set up Maven
        uses: stCarolas/setup-maven@v4
        with:
          maven-version: '3.9'

      - name: Deploy
        env:
          MAVEN_USERNAME: ci-bot
          MAVEN_PASSWORD: ${{ secrets.NEXSPENCE_TOKEN }}
        run: mvn -B -s settings.xml deploy
```

`settings.xml` — note that the token goes into the password field; Nexspence
accepts an `nxs_*` token there:

```xml
<settings>
  <servers>
    <server>
      <id>nexspence</id>
      <username>ci-bot</username>
      <password>${env.MAVEN_PASSWORD}</password>
    </server>
  </servers>
  <mirrors>
    <mirror>
      <id>nexspence</id>
      <url>https://nexspence.example.com/repository/maven-group/</url>
      <mirrorOf>*</mirrorOf>
    </mirror>
  </mirrors>
</settings>
```

The `<id>` in `<server>` must match the `<id>` in your `pom.xml`
`distributionManagement`.

## 5. Publish a Docker image

The registry is served by the same HTTP port as the web UI, so the workflow
pushes to that port:

```yaml
      - name: Log in to the registry
        uses: docker/login-action@v3
        with:
          registry: nexspence.example.com:8081
          username: ci-bot
          password: ${{ secrets.NEXSPENCE_TOKEN }}

      - name: Push the image
        uses: docker/build-push-action@v5
        with:
          push: true
          tags: nexspence.example.com:8081/docker-dev/myapp:${{ github.sha }}
```

The image reference carries the repository name as its first path segment; the
client turns it into `/v2/docker-dev/myapp/manifests/<ref>` on the wire.

> A runner outside the loopback network reaches a plain-HTTP registry only
> after the host is listed in the daemon's `insecure-registries`. On GitHub's
> hosted runners you cannot change the daemon config — put the instance behind
> TLS in that case.

## 6. Publish anything else with the API

For formats without a dedicated client, the v1-compatible REST API is enough.
Nexspence accepts the API token as a Bearer token:

```yaml
      - name: Publish a raw artifact
        env:
          NEXSPENCE_TOKEN: ${{ secrets.NEXSPENCE_TOKEN }}
        run: |
          curl -sSf -X PUT \
            -H "Authorization: Bearer ${NEXSPENCE_TOKEN}" \
            -H "Content-Type: application/octet-stream" \
            --upload-file dist/app.tar.gz \
            "${{ secrets.NEXSPENCE_URL }}/repository/raw-artifacts/releases/app.tar.gz"
```

The same call with `curl -u ci-bot:${NEXSPENCE_TOKEN}` works too — the token is
accepted as the password of a Basic authentication.

## 7. Verify the upload

```yaml
      - name: Verify
        env:
          NEXSPENCE_TOKEN: ${{ secrets.NEXSPENCE_TOKEN }}
        run: |
          curl -sSf -H "Authorization: Bearer ${NEXSPENCE_TOKEN}" \
            "${{ secrets.NEXSPENCE_URL }}/service/rest/v1/components?repository=maven-hosted" \
            | jq '.items | length'
```

`nxs search --repo maven-hosted --q myapp --json` does the same from the CLI.

Note that a hosted repository is created with `"allowAnonymous": false`, so
reading the artifact back without credentials answers `401` — that is the
expected answer, not a failure of the upload. Use the token to read it.

## 8. Hardening the workflow

- **Scope the token.** A token that can only write to one repository is one
  leaked log away from a contained incident.
- **Do not expose the secret to untrusted code.** A workflow that runs
  `pull_request` from a fork cannot read repository secrets — keep the
  publishing workflow on `push`/`release`, and use `pull_request_target` only
  with care.
- **Pin third-party actions to a commit SHA** if your policy requires it.
- **Rotate the token** on a schedule, and immediately after a suspected leak.
  `nxs token delete <id>` revokes it without touching the user.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `401 Unauthorized` | The token was not passed to the step that talks to the server | Set it in `env:` of that step — `env` at the workflow level is not enough for some actions |
| `403 Forbidden` | Authenticated, but the account has no privilege that allows the action | The role needs a privilege — see step 1; a bare `nx-deployer` grants nothing |
| `405 Method Not Allowed` | Publishing into a proxy repository | Proxy repositories are read-only — push to the hosted one |
| `nxs: command not found` | The installer put the binary in a directory that is not on the runner's PATH | Install with `go install` instead, or add the directory to `$GITHUB_PATH` |
| `invalid token or session expired` from `nxs` | The CLI had no credential | Export `NXS_URL`/`NXS_TOKEN`, or pass `--url`/`--token` |
| `401` from `docker push` | The registry needs `insecure-registries` for plain HTTP, or the login used the wrong port | Log in to the registry port, and list it in the daemon config if it is plain HTTP on a non-loopback host |
| The upload succeeds but the artifact is not found | The remote prefix does not match the layout the client expects | For Maven, the prefix must be `<group path>/<artifact>/<version>/` |

## Related

- [Maven guide](maven.md), [npm guide](npm.md), [PyPI guide](pypi.md), [Docker guide](docker.md)
- [RBAC and content selectors](../security-rbac.md)
- [Users and API tokens](../security-rbac.md)
