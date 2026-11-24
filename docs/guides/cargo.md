# Cargo guide — a private crate registry

Cargo resolves crates through a **sparse index**: a directory of small JSON
files that the client fetches over HTTP, one file per crate. Nexspence serves
that index, the crates behind it, and the publish API — so a Cargo registry is
a hosted repository, a proxy in front of crates.io, or a group of the two.

Assumes a fresh instance on `http://localhost:8081`.

## 1. Create the repositories

```bash
BASE_URL=http://localhost:8081 ./scripts/create-cargo-repos.sh
```

That creates `cargo-hosted`, `cargo-proxy` (in front of `index.crates.io`) and
`cargo-group`.

By hand, with the v1-compatible API:

```bash
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# hosted — your own crates
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/cargo/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"cargo-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Internal crates\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of crates.io
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/cargo/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"cargo-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Proxy to index.crates.io\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://index.crates.io\" }
  }"

# group — one index for both
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/cargo/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"cargo-group\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Group of hosted and proxy\",
    \"formatConfig\": { \"member_names\": [\"cargo-hosted\", \"cargo-proxy\"] }
  }"
```

## 2. The index URL ends in `/index/`

The sparse index lives under an `index/` prefix, and Cargo builds the URLs it
fetches from the registry URL you configure:

| Request | Answers |
|---|---|
| `<index-url>config.json` | the download and API endpoints |
| `<index-url><prefix>/<name>` | every published version of one crate |

The crate name is split by its length: `serde` is at `se/rd/serde`, a
one-letter name at `1/a`, a two-letter name at `2/ab`, and everything else at
`<first two letters>/<next two letters>/<name>`.

So the registry URL is:

```
sparse+http://localhost:8081/repository/cargo-proxy/index/
```

Two things are easy to get wrong here, and both of them fail the same way —
the client asks for a URL that does not exist:

- **`sparse+`** — without it Cargo uses the git index protocol and tries to
  clone a repository.
- **the trailing `/index/`** — the server serves `config.json` under
  `/index/`, not at the root of the repository.

## 3. Read through the proxy

To use the instance as a mirror of crates.io, replace the crates.io source:

```toml
# ~/.cargo/config.toml
[source.nexspence]
registry = "sparse+http://localhost:8081/repository/cargo-proxy/index/"

[source.crates-io]
replace-with = "nexspence"
```

```bash
cargo add serde
cargo build
```

The proxy answers the index and the crate downloads, so this needs no
credentials — the proxy allows anonymous reads.

## 4. Publish your own crates

Publishing needs a **named registry** and a token. A source replacement is not
enough: Cargo looks the token up under the registry name, and a replacement
source has no registry of its own.

```toml
# ~/.cargo/config.toml
[registries.nexspence]
index = "sparse+http://localhost:8081/repository/cargo-hosted/index/"

[registry]
global-credential-providers = ["cargo:token"]
```

```toml
# ~/.cargo/credentials.toml
[registries.nexspence]
token = "Bearer nxs_xxxxxxxxxxxxxxxxxxxx"
```

Three details make the difference between a working setup and a confusing
error:

- **`global-credential-providers = ["cargo:token"]`** — without a credential
  provider, Cargo refuses a registry whose index says `auth-required: true`:
  *"authenticated registries require a credential-provider to be configured"*.
- **the token carries its scheme** — the value is sent as the `Authorization`
  header as it stands, so it has to start with `Bearer `. A bare token is
  rejected with *"the token does not include an authentication scheme"*.
- **an API token, not a password** — create it in the profile panel (the 🔑
  icon in the sidebar). A password would have to be sent as
  `Basic <base64>`, which Cargo cannot express.

Then:

```bash
cargo publish --registry nexspence
```

The crate lands in the hosted index and can be consumed through the same
registry:

```bash
cargo add your-crate --registry nexspence
```

## 5. Search

```bash
cargo search your-crate --registry nexspence
```

`cargo search` takes a **registry name**, not a URL — the registry has to be
one of the `[registries.*]` entries in `config.toml`.

The search is served by the repository itself, so it finds the crates that
were published to it. A proxy forwards the query upstream, where crates.io
answers on a different host — so searching a proxy is not useful.

## 6. Install a binary

```bash
cargo install --index "sparse+http://localhost:8081/repository/cargo-proxy/index/" ripgrep
```

`cargo install` is only for crates that ship a binary; a library crate is
refused with *"there is nothing to install … because it has no binaries"*.
Use `cargo add` for a library.

## 7. The group

A group repository merges the index of its members, so one registry URL
serves your own crates and crates.io's.

Its `config.json` is worth a look, because it decides what the client does:

```bash
curl -s http://localhost:8081/repository/cargo-group/index/config.json
```

```json
{
  "api": "http://localhost:8081/repository/cargo-hosted",
  "dl": "http://localhost:8081/repository/cargo-hosted/api/v1/crates/{crate}/{version}/download",
  "auth-required": true
}
```

The group currently hands out the **hosted member's** endpoints. That has two
consequences worth planning around:

- `dl` points at the hosted member, so the client downloads from there rather
  than from the group — and Cargo sends its token only to the registry it was
  configured with, so a private hosted member answers `401`.
- `auth-required: true` tells Cargo that the registry needs a token, which
  means a credential provider has to be configured even for reads that the
  group would have allowed anonymously.

Until that is fixed, the recipes above are the ones that work: point the
**proxy** at the source replacement for reads, and the **hosted** repository
at the named registry for publishes.

## 8. Anonymous access

Proxy and group repositories with `"allowAnonymous": true` serve reads without
credentials. A hosted repository with `"allowAnonymous": false` needs them —
and Cargo can only send them to a registry it knows by name.

Keep hosted repositories non-anonymous so that publishing requires
authentication, and grant read access through roles — see
[security-rbac.md](../security-rbac.md).

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `failed to get successful HTTP response from …/config.json … got 405` | The registry URL is missing `/index/` | `sparse+http://host/repository/<name>/index/` |
| Cargo clones a git repository | The `sparse+` prefix is missing | Add it |
| `authenticated registries require a credential-provider to be configured` | The index says `auth-required: true` and no provider is set | `[registry] global-credential-providers = ["cargo:token"]` |
| `the token does not include an authentication scheme` | The token value has no scheme | `token = "Bearer nxs_…"` |
| `no token found for \`nexspence\`` | The token is not under the registry's name | `~/.cargo/credentials.toml` → `[registries.nexspence]` |
| `registry index was not found in any configuration` | A name was used that `config.toml` does not define | Define it under `[registries.<name>]` |
| `there is nothing to install … because it has no binaries` | The crate is a library | `cargo add` it instead |
| `crate … already exists on … index` | The version was published before | Bump the version in `Cargo.toml` |
| `405 Method Not Allowed` on publish | The repository is a proxy | Publish to the hosted repository |
| The download answers `401` | The index advertises a different repository's download URL | Publish to the hosted repository and read from it, or use the proxy for reads |

## Related

- [RBAC and content selectors](../security-rbac.md)
- [GitHub Actions guide](github-actions.md)
- [API specification](../api-spec.yaml)
