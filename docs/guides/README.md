# Format guides

End-to-end guides for using Nexspence as a package registry: create the
repositories, point the client at them, publish, consume, and debug what breaks.

Each guide is self-contained. Every command in them has been checked against the
implementation and the repository's own helper scripts
(`scripts/create-<format>-repos.sh`, `scripts/seed-packages.sh`).

| Guide | What it covers |
|---|---|
| [Maven](maven.md) | Hosted, proxy and group repositories · `settings.xml` · `mvn deploy` · checksum and `maven-metadata.xml` handling |
| [npm](npm.md) | `.npmrc` · `npm publish` and the raw packument API · scoped packages · registry search |
| [PyPI](pypi.md) | `pip.conf` · `twine upload` · the simple index · PEP 503 name normalisation |
| [Docker / OCI](docker.md) | The registry path under `/v2/` · `insecure-registries` · `docker push` · Helm over OCI · cosign signatures and the Referrers API |
| [Helm](helm.md) | Chart repositories and `index.yaml` · `helm repo add` · charts over OCI · how a group merges the index |
| [Go modules](go.md) | `GOPROXY` · the trailing slash · `,direct` · the checksum database · publishing a module |
| [Cargo](cargo.md) | The sparse index · source replacement · `cargo publish` and the credential provider |
| [NuGet](nuget.md) | The v3 service index · `allowInsecureConnections` · `dotnet nuget push` and delete |
| [Raw](raw.md) | Arbitrary paths · `curl --upload-file` · the browse tree · cleanup policies and quotas |
| [APT](apt.md) | Suites and `dists/<suite>/` · the apt source line · signing · what a deletion does to the index |
| [Conan](conan.md) | `conan remote add` and `conan remote login` · `conan upload` and `conan install` · the v2 revisions protocol and the v1 REST endpoints · group and proxy behaviour |
| [Conda](conda.md) | Channels and platform subdirs · `repodata.json` · `.condarc` and `--override-channels` · publishing · how a group merges the index |
| [Terraform](terraform.md) | Hosted, proxy and group repositories · the discovery document · `terraform login` and the CLI configuration · installing providers and modules |
| [GitHub Actions](github-actions.md) | API tokens in CI · publishing with the `nxs` CLI, Maven, Docker and the REST API |

## Repository URL patterns

Every format is served under one prefix, so the URL shape is the same everywhere:

```
http://<host>:<port>/repository/<repository-name>/
```

| Format | Client URL | Notes |
|---|---|---|
| Maven 2/3 | `/repository/<name>/` | the Maven 2 layout: `group/artifact/version/file` |
| npm | `/repository/<name>/` | the package path follows, `PUT /<package>` |
| PyPI | `/repository/<name>/simple/` | the index; uploads go to `/repository/<name>/` |
| Docker | `:8081/<name>/` | the registry shares the web port, under `/v2/` on the wire |
| Helm | `/repository/<name>/` | chart repositories, `helm repo add`; OCI charts live under `/v2/<name>/` |
| Go modules | `/repository/<name>/` | `GOPROXY` — the URL needs a trailing slash |
| Cargo | `/repository/<name>/index/` | the sparse index lives under `index/` |
| NuGet | `/repository/<name>/index.json` | the v3 service index |
| Raw | `/repository/<name>/` | any path you like |
| APT | `/repository/<name>/dists/<suite>/` | the suite index; packages live under `pool/` |
| Yum/RPM, Conan, Conda, Terraform, Alpine, RubyGems, R/CRAN, Hugging Face | `/repository/<name>/` | format-specific paths below that prefix |

## Creating repositories

Every format has a helper script that creates a hosted, a proxy and a group
repository with the right payloads:

```bash
BASE_URL=http://localhost:8081 ./scripts/create-npm-repos.sh
```

The scripts speak the v1-compatible API, so the same thing by hand is a `POST`
to `/service/rest/v1/repositories/<format>/<type>`.

## Authentication

Package clients authenticate with HTTP Basic authentication. Nexspence accepts:

- **username and password** — the user's own credentials;
- **an `nxs_*` API token as the password** — the recommended form for CI;
- **a Bearer token** — `Authorization: Bearer <jwt or nxs_* token>`.

A proxy or group repository with `allowAnonymous: true` serves reads without
credentials. See [RBAC and content selectors](../security-rbac.md).
