# Terraform guide — hosted, proxy and group repositories

Nexspence serves the Terraform Registry Protocol v1: providers and modules live in
a repository like any other artifact, and a proxy repository can front
`registry.terraform.io`.

The Terraform client is stricter than the protocol. It looks for the registry at
the *host root* over HTTPS, and it wants a login service before it will store a
token. This guide covers what works today, which part of the flow needs
`terraform login` and which part needs the CLI configuration — they are not the
same thing.

| Part of the flow | Configuration | What it does | Against Nexspence |
|---|---|---|---|
| Authentication | `terraform login`, or a `credentials` block in the CLI configuration | attaches a token to the registry requests | `terraform login` cannot: the discovery document advertises no login service. Put the token in the CLI configuration. |
| Provider installation | the `provider_installation` block in the CLI configuration | tells the client where to install providers from | Works. `direct` needs a registry host in front of the instance, `filesystem_mirror` works against the instance as it is. |
| Module installation | the `source` of the module block | names the archive to download | Works with an archive URL; the registry form needs a registry host in front. |

Everything below assumes an instance on `http://localhost:8091` (the default
port of the Docker Compose quick start is 8081 — replace the host everywhere if
yours differs). The credentials are `admin` / `admin123`.

## 1. Create the repositories

Nexspence ships a helper that creates all three repository types with the right
payloads:

```bash
./scripts/create-terraform-repos.sh
```

It defaults to `http://localhost:8081` — point it at your instance with
`BASE_URL` when it differs:

```bash
BASE_URL=http://localhost:8091 ./scripts/create-terraform-repos.sh
```

That script creates `terraform-hosted`, `terraform-proxy` and `terraform-group` —
the three names this guide uses.

Under the hood it does three `POST` calls against the v1-compatible API. This is
the same thing by hand, and it is worth knowing because the API is what CI
scripts talk to:

```bash
# resolve the blob store id first — a repository must reference an existing one
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8091/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# hosted
curl -u admin:admin123 -X POST \
  http://localhost:8091/service/rest/v1/repositories/terraform/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"terraform-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Terraform Registry hosted repository\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of the public registry
curl -u admin:admin123 -X POST \
  http://localhost:8091/service/rest/v1/repositories/terraform/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"terraform-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Terraform proxy → registry.terraform.io\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://registry.terraform.io\" }
  }"

# group: one URL for both
curl -u admin:admin123 -X POST \
  http://localhost:8091/service/rest/v1/repositories/terraform/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"terraform-group\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Terraform group (hosted + proxy)\",
    \"formatConfig\": { \"member_names\": [\"terraform-hosted\", \"terraform-proxy\"] }
  }"
```

Repository URLs follow one pattern:

```
http://localhost:8091/repository/<repository-name>/
```

## 2. What the server does

### The discovery document

Every Terraform repository serves a discovery document, and the group serves
one of its own:

```bash
curl -s http://localhost:8091/repository/terraform-group/.well-known/terraform.json
```

```json
{"modules.v1":"http://localhost:8091/repository/terraform-group/v1/modules/","providers.v1":"http://localhost:8091/repository/terraform-group/v1/providers/"}
```

Two keys, `providers.v1` and `modules.v1`, each pointing at the repository's own
v1 endpoints. The hosted repository answers the same document, but only to an
authenticated caller — it is created with `"allowAnonymous": false`:

```bash
curl -s -u admin:admin123 \
  http://localhost:8091/repository/terraform-hosted/.well-known/terraform.json
```

The document lives **under the repository path**. The Terraform client asks for
it at the **host root** (`https://<host>/.well-known/terraform.json`), and on a
Nexspence instance the host root answers with the web UI:

```bash
curl -s http://localhost:8091/.well-known/terraform.json | head -c 40
```

```
<!DOCTYPE html>
<html lang="en">
```

That single difference is the reason `terraform login` and `terraform init`
cannot reach a repository directly, and section 6 shows the reverse proxy that
fixes it.

### Version lists

```bash
curl -s -u admin:admin123 \
  http://localhost:8091/repository/terraform-hosted/v1/providers/acme/demo/versions
```

```json
{"versions":[{"version":"1.0.0","protocols":["5.0"],"platforms":null}]}
```

```bash
curl -s -u admin:admin123 \
  http://localhost:8091/repository/terraform-hosted/v1/modules/acme/network/aws/versions
```

```json
{"modules":[{"versions":[{"version":"1.0.0"}]}]}
```

The group merges the members: the first member that has a version wins, and the
lists are concatenated rather than shadowed. The group's version document is
served from the members, so a repository that has no `acme/demo` provider does
not hide the one that has.

### The provider download endpoint

```
GET /repository/<repo>/v1/providers/<namespace>/<type>/<version>/download/<os>/<arch>
```

It answers with the document the Terraform client expects, and the `download_url`
points back at the repository:

```bash
curl -s -u admin:admin123 \
  http://localhost:8091/repository/terraform-hosted/v1/providers/acme/demo/1.0.0/download/darwin/arm64
```

```json
{"arch":"arm64","download_url":"http://localhost:8091/repository/terraform-hosted/v1/providers/acme/demo/1.0.0/darwin_arm64.zip","filename":"terraform-provider-demo_1.0.0_darwin_arm64.zip","os":"darwin","sha256_sum":"b302b05538aebf4df973e3bc6de1a0c29b1af72ff388230bb92ecd5f7f9f7817"}
```

The package itself is served at that `download_url`, and the `sha256_sum` is the
digest of the stored blob:

```bash
curl -s -u admin:admin123 -o demo.zip \
  http://localhost:8091/repository/terraform-hosted/v1/providers/acme/demo/1.0.0/darwin_arm64.zip
shasum -a 256 demo.zip
```

One field name is not the one the protocol uses: the document carries
`sha256_sum`, while the client reads `shasum`. A Terraform client that installs
through the registry protocol stops with

```
Error: Failed to install provider
Error while installing registry.example.com/acme/demo v1.0.0: could not query
provider registry for registry.example.com/acme/demo: registry response
includes invalid SHA256 hash "": %!s(<nil>)
```

The proxy repository is not affected by that: it forwards the upstream document,
which already carries `shasums_url`, `shasums_signature_url` and the signing
keys.

### The module download endpoint

```
GET /repository/<repo>/v1/modules/<namespace>/<name>/<provider>/<version>/download
```

It answers `204 No Content` and names the archive in the `X-Terraform-Get`
header, which is what the Terraform client follows:

```bash
curl -s -D- -o /dev/null -u admin:admin123 \
  http://localhost:8091/repository/terraform-hosted/v1/modules/acme/network/aws/1.0.0/download
```

```
HTTP/1.1 204 No Content
X-Terraform-Get: http://localhost:8091/repository/terraform-hosted/v1/modules/acme/network/aws/1.0.0.tar.gz
```

The archive itself is served at that path.

### Publishing into the hosted repository

Two `PUT` endpoints, one per artifact kind:

```bash
# a provider package (a zip that holds the provider executable)
curl -u admin:admin123 -X PUT --data-binary @provider.zip \
  http://localhost:8091/repository/terraform-hosted/v1/providers/acme/demo/1.0.0/upload/darwin/arm64

# a module archive
curl -u admin:admin123 -X PUT --data-binary @module.tar.gz \
  http://localhost:8091/repository/terraform-hosted/v1/modules/acme/network/aws/1.0.0
```

Both answer `{"saved":true}` and are visible in the version lists right away.
The provider is stored under
`/v1/providers/<ns>/<type>/<version>/<os>_<arch>.zip`, the module under
`/v1/modules/<ns>/<name>/<provider>/<version>.tar.gz`.

## 3. `terraform login`, and where the token goes

`terraform login <host>` is a handshake, not a credential prompt against an
arbitrary server:

1. it fetches the discovery document from `https://<host>/.well-known/terraform.json`;
2. it looks for a login service in it — `login.v1` (OAuth) or `tfe.v2` (the
   Terraform Enterprise API);
3. only if one of them is there does it ask for a token and store it.

Against a Nexspence repository the command stops at the first step:

```
$ terraform login localhost:8091
Error: Service discovery failed for localhost:8091

failed to request discovery document: Get
"https://localhost:8091/.well-known/terraform.json": http: server gave HTTP
response to HTTPS client.
```

The client speaks HTTPS and asks at the host root; Nexspence serves the
document under `/repository/<name>/` over plain HTTP. Put a registry host in
front of the instance (section 6) and the same command gets one step further —
and stops again, because the discovery document advertises no login service:

```
Error: Host does not support Terraform tokens API

The given hostname "registry.example.com" does not support creating
Terraform authorization tokens.
```

**Where the token would end up.** When a login service exists, `terraform login`
writes the token in plain text into the credentials file in the CLI
configuration directory — `~/.terraform.d/credentials.tfrc.json` by default:

```json
{
  "credentials": {
    "registry.example.com": {
      "token": "nxs_xxxxxxxxxxxxxxxxxxxx"
    }
  }
}
```

That file is not the CLI configuration file. The CLI configuration is
`~/.terraformrc` (a file) and `~/.terraform.d/` (a directory); the credentials
file always lives in the directory, next to the plugins, and it is the only file
`terraform login` writes. Both paths follow `$HOME`, so a CI job that sets `HOME`
keeps its token out of the user's home directory.

**So what authenticates a Terraform client against Nexspence?** Not
`terraform login` — the server does not offer a login service. A token goes into
the CLI configuration as a `credentials` block, and the client sends it as a
bearer token on every registry request:

```hcl
credentials "registry.example.com" {
  token = "nxs_xxxxxxxxxxxxxxxxxxxx"
}
```

Nexspence accepts the `nxs_*` API token as the password of a Basic
authentication, and as a `Bearer` token. Create it in the profile panel (the 🔑
icon in the sidebar) — a user manages their own tokens; there is no admin page
for them.

## 4. `terraform init` and the CLI configuration

The provider installation method is chosen in the CLI configuration, not in the
configuration of the workspace:

```hcl
provider_installation {
  direct {
    include = ["registry.example.com/acme/demo"]
  }
}
```

The `direct` method asks the provider's own host for the version list, which
means service discovery at the host root. Against a bare instance that fails:

```
$ terraform init
- Finding registry.example.com/acme/demo versions matching "1.0.0"...

Error: Failed to query available provider packages

Could not retrieve the list of available versions for provider
registry.example.com/acme/demo: could not connect to registry.example.com:
failed to request discovery document: Get
"https://registry.example.com/.well-known/terraform.json": ...
```

The mirror method works against a plain instance, because it needs no network
and no discovery. Fetch the package from the repository's download endpoint,
lay it out the way a filesystem mirror is laid out, and point the CLI
configuration at the directory:

```bash
BASE=http://localhost:8091
HOST=localhost:8091
MIRROR=$HOME/.terraform/mirror
mkdir -p "$MIRROR/$HOST/acme/demo"

# the download document of the repository gives the package name and the URL
curl -s -u admin:admin123 \
  "$BASE/repository/terraform-hosted/v1/providers/acme/demo/1.0.0/download/darwin/arm64" \
  | tee /tmp/download.json

curl -s -u admin:admin123 -o "$MIRROR/$HOST/acme/demo/terraform-provider-demo_1.0.0_darwin_arm64.zip" \
  "$(python3 -c 'import json;print(json.load(open("/tmp/download.json"))["download_url"])')"
```

```hcl
provider_installation {
  filesystem_mirror {
    path    = "/home/you/.terraform/mirror"
    include = ["localhost:8091/acme/demo"]
  }
}
```

The source address in the workspace names the same host as the mirror
directory:

```hcl
terraform {
  required_providers {
    demo = {
      source  = "localhost:8091/acme/demo"
      version = "1.0.0"
    }
  }
}
```

A provider source address is `<host>/<namespace>/<type>` — three segments, and
no path. The example `source = "localhost:8091/repository/terraform-group/hashicorp/aws"`
that the helper script prints at the end of its run is not a valid source
address; the repository path does not belong in it, and a mirror or a registry
host replaces it.

```bash
$ terraform init
Initializing the backend...
Initializing provider plugins...
- Finding localhost:8091/acme/demo versions matching "1.0.0"...
- Installing localhost:8091/acme/demo v1.0.0...
- Installed localhost:8091/acme/demo v1.0.0 (unauthenticated)

Terraform has been successfully initialized!
```

The mirror holds one package per platform, so Terraform records a checksum for
the platform it ran on and warns that another platform will need its own
`terraform providers lock -platform=linux_amd64` run.

## 5. Installing a module from the repository

A module archive is fetched by the Terraform client from the URL in the
`X-Terraform-Get` header, so the archive has to be reachable without a prompt.
Naming the archive directly works:

```hcl
module "network" {
  source = "http://localhost:8091/repository/terraform-group/v1/modules/acme/network/aws/1.0.0.tar.gz"
}

output "greeting" {
  value = module.network.greeting
}
```

```bash
$ terraform init
Initializing the backend...
Initializing modules...
Downloading http://localhost:8091/repository/terraform-group/v1/modules/acme/network/aws/1.0.0.tar.gz for network...
- network in .terraform/modules/network

Terraform has been successfully initialized!

$ terraform apply -auto-approve
Outputs:

greeting = "hello from the nexspence module"
```

Two rules come out of that:

- **The module files belong at the root of the archive.** Terraform does not
  strip a single top-level directory out of a module archive. An archive that
  contains `my-module-1.0.0/main.tf` is unpacked to
  `.terraform/modules/network/my-module-1.0.0/main.tf`, the module has no
  configuration of its own, and every output of it is missing.
- **The URL has to be readable by the client.** The group repository is created
  with `"allowAnonymous": true`, so the archive is served without credentials.
  The hosted repository is not, and the Terraform client sends no credentials
  when it fetches an archive — it answers `401` and the install fails with
  `bad response code: 401`.

The registry form of the same source — `source = "<host>/acme/network/aws"` with
a `version` — needs the discovery document at the host root, so it only works
behind the reverse proxy of section 6.

## 6. Putting a registry host in front of Nexspence

The Terraform client addresses a registry by host, not by path. A deployment
that wants the client to speak the registry protocol needs a host of its own
that:

- serves `https://<host>/.well-known/terraform.json` and answers with the
  discovery document of the repository, with the `providers.v1` and `modules.v1`
  URLs pointing at that host;
- forwards `/repository/<name>/...` to the Nexspence instance;
- terminates TLS.

A reverse proxy in front of the instance is enough. With that in place the proxy
repository works end to end with the real client:

```
$ terraform init
Initializing the backend...
Initializing provider plugins...
- Finding registry.example.com/hashicorp/null versions matching "3.2.4"...
- Installing registry.example.com/hashicorp/null v3.2.4...
- Installed registry.example.com/hashicorp/null v3.2.4 (signed by HashiCorp)

Terraform has been successfully initialized!
```

Two things still do not work there, and they are properties of the server, not
of the proxy:

- **the hosted repository's download document** is rejected by the client,
  because it carries `sha256_sum` where the protocol's field is `shasum`, and
  no `shasums_url` at all — see section 2;
- **the proxy's module download endpoint** answers `204` but drops the
  `X-Terraform-Get` header of the upstream answer, so the client cannot find
  the archive.

The provider binary behind the proxy is fetched from `releases.hashicorp.com`:
the proxied download document keeps the upstream `download_url`, so the proxy
caches the document, not the binary.

## 7. Authentication

A proxy or a group repository with `"allowAnonymous": true` can be read without
credentials, which is what you want for a shared build agent. A hosted
repository should keep `"allowAnonymous": false` so that publishing stays
authenticated. Read access is then granted per role — see
[security-rbac.md](../security-rbac.md).

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `Service discovery failed for <host>` | The client asks for the discovery document at the host root over HTTPS; Nexspence serves it under `/repository/<name>/` | Put a registry host with its own `/.well-known/terraform.json` in front (section 6), or use the mirror method |
| `http: server gave HTTP response to HTTPS client` | The client only speaks HTTPS; a plain-HTTP instance cannot be discovered | Terminate TLS in front of the instance |
| `Host does not support Terraform tokens API` | The discovery document advertises neither `login.v1` nor `tfe.v2`, so there is no login service to hand a token to | Configure the token in the CLI configuration instead (`credentials` block) |
| `registry response includes invalid SHA256 hash ""` | The hosted download document carries `sha256_sum`, not the protocol's `shasum`, and no `shasums_url` | Install through the mirror method, or fix the document in the server |
| `failed to get download URL for "<module>": 204 No Content` | The proxy's module download endpoint drops the upstream `X-Terraform-Get` header | Use the hosted repository's module endpoint, or a direct archive URL |
| `bad response code: 401` while a module is downloaded | The archive URL points into a repository that is not anonymously readable | Use the group repository, or make the archive readable |
| A module installs but has no outputs | The archive wraps the module in a top-level directory that Terraform does not strip | Put the module's `.tf` files at the root of the archive |
| `provider ... was not found in any of the search locations` | The provider source address has more than three segments | A source address is `<host>/<namespace>/<type>` — the repository path does not belong in it |
| `405 Method Not Allowed` on a publish | You are publishing into a **proxy** repository | Proxy repositories are read-only; publish into the hosted one |

## Related

- [RBAC and content selectors](../security-rbac.md)
- [GitHub Actions guide](github-actions.md)
- [API specification](../api-spec.yaml)
