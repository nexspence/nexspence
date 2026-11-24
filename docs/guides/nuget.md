# NuGet guide — a private package feed

A NuGet feed is an HTTP service with a **v3 service index** at its root. The
index advertises where the packages live (the flat container), where the
metadata lives (the registration hive) and where to push. Nexspence serves all
three, so a NuGet feed is a hosted repository, a proxy in front of nuget.org,
or a group of the two.

Assumes a fresh instance on `http://localhost:8081`.

## 1. Create the repositories

```bash
BASE_URL=http://localhost:8081 ./scripts/create-nuget-repos.sh
```

That creates `nuget-hosted`, `nuget-proxy` (in front of `api.nuget.org`) and
`nuget-group`.

By hand, with the v1-compatible API:

```bash
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# hosted — your own packages
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/nuget/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"nuget-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Internal packages\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of nuget.org
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/nuget/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"nuget-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Proxy to api.nuget.org\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://api.nuget.org/v3/index.json\" }
  }"

# group — one feed for both
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/nuget/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"nuget-group\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Group of hosted and proxy\",
    \"formatConfig\": { \"member_names\": [\"nuget-hosted\", \"nuget-proxy\"] }
  }"
```

## 2. The service index

Every repository answers `GET /index.json` — that is the URL you register as a
source:

```bash
curl -s http://localhost:8081/repository/nuget-group/index.json
```

```json
{
  "version": "3.0.0",
  "resources": [
    { "@type": "PackageBaseAddress/3.0.0", "@id": "http://localhost:8081/repository/nuget-group/v3/flatcontainer/" },
    { "@type": "RegistrationsBaseUrl/3.0.0", "@id": "http://localhost:8081/repository/nuget-group/v3/registration/" },
    { "@type": "PackagePublish/2.0.0", "@id": "http://localhost:8081/repository/nuget-group/v2/package" },
    { "@type": "LegacyGallery/2.0.0", "@id": "http://localhost:8081/repository/nuget-group/v2/" }
  ]
}
```

The four resource types map onto the endpoints the client uses:

| Resource | Endpoint | Used for |
|---|---|---|
| `PackageBaseAddress/3.0.0` | `GET /v3/flatcontainer/<id>/<version>/<id>.<version>.nupkg` | downloading the package |
| `RegistrationsBaseUrl/3.0.0` | `GET /v3/registration/<id>/index.json` | metadata, versions, `listed` |
| `PackagePublish/2.0.0` | `PUT /v2/package` | pushing |
| `LegacyGallery/2.0.0` | `GET /v2/FindPackagesById()?id=…` | the older v2 OData query |

The flat container path is **lowercased**, and the server accepts the original
casing too — `nexspence.demo` and `Nexspence.Demo` both answer.

## 3. Register the source

NuGet refuses plain HTTP by default. A source on `http://` needs
`allowInsecureConnections="true"`, either in `NuGet.Config` or as a CLI flag:

```bash
dotnet nuget add source \
  http://localhost:8081/repository/nuget-group/index.json \
  --name nexspence \
  --allow-insecure-connections
```

```bash
dotnet nuget list source
```

```xml
<!-- NuGet.Config -->
<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <packageSources>
    <clear />
    <add key="nexspence" value="http://localhost:8081/repository/nuget-group/index.json"
         allowInsecureConnections="true" />
  </packageSources>
</configuration>
```

Without `allowInsecureConnections` the client stops before it reaches the
server:

> You are performing a "push" operation with source "HTTP":
> http://localhost:8081/repository/nuget-hosted/index.json. For NuGet to work
> with HTTP sources, either set the value of the "allowInsecureConnections" to
> true in NuGet.Config or pass "--allow-insecure-connections" to the command.

Put the config next to the project and restore with it:

```bash
dotnet add package Newtonsoft.Json --version 13.0.3
dotnet build
```

The group serves the package from its proxy member and caches it, so the
upstream is only contacted once.

## 4. Publish your own packages

Build the package:

```bash
dotnet new classlib --name Nexspence.Demo --output demo
cd demo
dotnet pack -c Release -o ./out
```

Push it. **Push by source name, not by URL** — the client resolves the source
name to a URL *and* its credentials, and a bare URL carries neither:

```bash
dotnet nuget add source \
  http://localhost:8081/repository/nuget-hosted/index.json \
  --name nexspence-hosted \
  --username admin --password admin123 \
  --store-password-in-clear-text \
  --allow-insecure-connections

dotnet nuget push ./out/Nexspence.Demo.1.0.0.nupkg --source nexspence-hosted
```

```
PUT http://localhost:8081/repository/nuget-hosted/v2/package/
Created http://localhost:8081/repository/nuget-hosted/v2/package/ 213ms
```

The package is now in the hosted repository:

```bash
curl -s http://localhost:8081/repository/nuget-hosted/v3/flatcontainer/nexspence.demo/index.json
```

```json
{ "versions": ["1.0.0"] }
```

and it can be consumed through the group:

```bash
dotnet add package Nexspence.Demo --version 1.0.0
```

### Credentials

The hosted repository takes **HTTP Basic** credentials — the same ones every
other client uses. An API token works as the password:

```bash
dotnet nuget add source \
  http://localhost:8081/repository/nuget-hosted/index.json \
  --name nexspence-hosted \
  --username admin --password nxs_xxxxxxxxxxxxxxxxxxxx \
  --store-password-in-clear-text \
  --allow-insecure-connections
```

Create a token in the profile panel (the 🔑 icon in the sidebar) or through
`POST /api/v1/tokens`. The token's value is shown once, at creation.

On macOS, `--store-password-in-clear-text` is what makes the password
readable by the CLI; without it the CLI encrypts it into the system keychain
and the token is unusable.

### What does not work

- **`--api-key`** — the client sends the value as the `X-NuGet-ApiKey` header,
  and the server does not read that header. A push with `--api-key` alone is
  answered `401 Unauthorized`.
- **`--source <url>`** — a URL carries no credentials, so the client loads the
  service index anonymously and the hosted repository answers `401`. Register
  the source with `--username`/`--password` and push by name.
- **pushing to a group** — a group repository is read-only and answers `400`
  on `PUT /v2/package`. Push to the hosted member.
- **credentials in the URL** (`http://admin:pass@host/…`) — the NuGet client
  does not send them, and the service index comes back `401`.

## 5. Delete a package

```bash
dotnet nuget delete Nexspence.Demo 1.0.0 \
  --source nexspence-hosted \
  --non-interactive
```

```
DELETE http://localhost:8081/repository/nuget-hosted/v2/package/Nexspence.Demo/1.0.0
NoContent 28ms
```

The version disappears from the flat container, and the registration hive stops
listing it. Deleting a version that does not exist answers `404`.

The client warns that no API key was found — that warning is harmless, the
source's credentials are used instead.

## 6. The group

A group repository merges its members, so one feed serves your own packages
and nuget.org's. `dotnet add package` against the group finds both.

Two details are worth knowing:

- **the group's download links point at the member that has the package.** A
  proxied package downloads from the proxy, a hosted package from the hosted
  member. Both are reachable through the group, but the link in the
  registration metadata names the member, not the group.
- **the group is read-only.** Pushing to it answers `400`; push to the hosted
  member instead.

## 7. Anonymous access

Proxy and group repositories with `"allowAnonymous": true` serve reads without
credentials. A hosted repository with `"allowAnonymous": false` needs them —
which is what makes the hosted repository the right place to publish.

Keep hosted repositories non-anonymous so that publishing requires
authentication, and grant read access through roles — see
[security-rbac.md](../security-rbac.md).

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `For NuGet to work with HTTP sources … set allowInsecureConnections` | The source is plain HTTP | `--allow-insecure-connections`, or `allowInsecureConnections="true"` in `NuGet.Config` |
| `Failed to load the service index … 401 (Unauthorized)` | The hosted repository was addressed by URL, without credentials | Register the source with `--username`/`--password` and use its name |
| `Unable to load the service index … 400` on `PUT /v2/package` | The package body is not a valid `.nupkg` | `dotnet pack` and push the `.nupkg` file |
| Push answers `401` | No credentials were sent | `--api-key` is not supported — use a registered source |
| Push answers `400` | The repository is a group or a proxy | Push to the hosted repository |
| `warn : The API key is not specified` on delete | The client wants an API key it does not need | Ignore it; the source's credentials are used |
| `NU1301` during restore | The source is unreachable or the package is not in any member | Check the repository names in the group and the proxy's upstream |
| The package downloads from the hosted member, not the group | The registration metadata names the owning member | Expected — the group merges, it does not re-host |

## Related

- [RBAC and content selectors](../security-rbac.md)
- [GitHub Actions guide](github-actions.md)
- [API specification](../api-spec.yaml)
