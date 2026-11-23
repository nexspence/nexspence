# GitHub Actions guide — publishing to Nexspence from CI

Most teams meet Nexspence through their pipeline, not through the UI. This guide
covers the three pieces you need: a credential that a runner can use, a
workflow that publishes, and a way to verify what landed.

## 1. Create a token for the pipeline

Never use a person's password in CI. Create a dedicated user, give it only the
privileges it needs, and issue an API token for it.

In the web UI: **Admin → Security → Users** — create the user, then
**Admin → Security → Tokens** — issue a token for it. From the CLI:

```bash
nxs user create ci-bot --email ci-bot@example.com --password '…'
nxs role assign ci-bot nx-developer
nxs token create github-actions
```

The token is printed **once** — copy it. It is stored hashed and can be revoked
without touching the password.

```bash
# what the token can do
nxs token list
```

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
needs no login step and no config file.

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
          nxs push maven-releases \
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
      <url>https://nexspence.example.com/repository/maven-public/</url>
      <mirrorOf>*</mirrorOf>
    </mirror>
  </mirrors>
</settings>
```

The `<id>` in `<server>` must match the `<id>` in your `pom.xml`
`distributionManagement`.

## 5. Publish a Docker image

The registry runs on its own port, so the workflow pushes to that port:

```yaml
      - name: Log in to the registry
        uses: docker/login-action@v3
        with:
          registry: nexspence.example.com:5001
          username: ci-bot
          password: ${{ secrets.NEXSPENCE_TOKEN }}

      - name: Push the image
        uses: docker/build-push-action@v5
        with:
          push: true
          tags: nexspence.example.com:5001/docker-hosted/myapp:${{ github.sha }}
```

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
            "${{ secrets.NEXSPENCE_URL }}/repository/raw-hosted/releases/app.tar.gz"
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
            "${{ secrets.NEXSPENCE_URL }}/service/rest/v1/components?repository=maven-releases" \
            | jq '.items | length'
```

`nxs search --repo maven-releases --q myapp --json` does the same from the CLI.

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
| `403 Forbidden` | Authenticated, but the role lacks the privilege on that repository | Grant the write privilege to the role of the CI user |
| `405 Method Not Allowed` | Publishing into a proxy repository | Proxy repositories are read-only — push to the hosted one |
| `nxs: command not found` | The installer put the binary in a directory that is not on the runner's PATH | Install with `go install` instead, or add the directory to `$GITHUB_PATH` |
| `401` from `docker push` | The registry needs `insecure-registries` for plain HTTP, or the login used the wrong port | Log in to the registry port, and list it in the daemon config if it is plain HTTP |
| The upload succeeds but the artifact is not found | The remote prefix does not match the layout the client expects | For Maven, the prefix must be `<group path>/<artifact>/<version>/` |

## Related

- [Maven guide](maven.md), [npm guide](npm.md), [PyPI guide](pypi.md), [Docker guide](docker.md)
- [RBAC and content selectors](../security-rbac.md)
- [Users and API tokens](../security-rbac.md)
