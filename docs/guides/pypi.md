# PyPI guide — hosted, proxy and group repositories

A working Python package index on Nexspence: create the repositories, publish
with `twine`, install with `pip`, and serve the simple index correctly.

Assumes a fresh instance on `http://localhost:8081`.

## 1. Create the repositories

```bash
BASE_URL=http://localhost:8081 ./scripts/create-pypi-repos.sh
```

That script creates `pypi-hosted`, `pypi-proxy` and `pypi-group` — the three
names this guide uses.

By hand, with the v1-compatible API:

```bash
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# hosted — your own wheels and sdists
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/pypi/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"pypi-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Internal Python packages\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of PyPI
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/pypi/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"pypi-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Proxy to pypi.org\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://pypi.org/simple\" }
  }"

# group — one index URL for both
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/pypi/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"pypi-group\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Group of hosted and proxy\",
    \"formatConfig\": { \"member_names\": [\"pypi-hosted\", \"pypi-proxy\"] }
  }"
```

> The proxy remote URL for PyPI is the **simple index** (`https://pypi.org/simple`),
> not the JSON API — the proxy caches the simple pages and the files behind them.

Repository URLs:

```
http://localhost:8081/repository/<repository-name>/simple/     ← the index
http://localhost:8081/repository/<repository-name>/           ← the upload endpoint
```

The simple index has the trailing `/simple/` suffix; the upload endpoint does
not. That difference is the single most common source of PyPI mistakes.

## 2. Configure the client

### `~/.config/pip/pip.conf` — resolve through the group

```ini
[global]
index-url = http://localhost:8081/repository/pypi-group/simple/
trusted-host = localhost
```

`trusted-host` is required for plain HTTP — pip refuses an insecure index
otherwise.

### Credentials in the URL

```ini
[global]
index-url = http://admin:admin123@localhost:8081/repository/pypi-group/simple/
trusted-host = localhost
```

Or use a token as the password — Nexspence accepts an `nxs_*` API token in the
password position of a Basic authentication:

```ini
[global]
index-url = http://ci-bot:nxs_xxxxxxxxxxxxxxxxxxxx@localhost:8081/repository/pypi-group/simple/
trusted-host = localhost
```

Create the token in the profile panel (the 🔑 icon in the sidebar) — a user
manages their own tokens; there is no admin page for them. Prefer a token over
a password: it is scoped, revocable, and does not leak the account credentials.

### `--extra-index-url` versus `--index-url`

```bash
# only from Nexspence
pip install --index-url http://localhost:8081/repository/pypi-group/simple/ my-package

# PyPI as a fallback, Nexspence first
pip install --index-url http://localhost:8081/repository/pypi-group/simple/ \
            --extra-index-url https://pypi.org/simple my-package
```

`--index-url` replaces the default index; `--extra-index-url` adds to it. pip
picks the highest version across both, so "Nexspence first" does not mean
"Nexspence wins" — pin exact versions if that matters.

## 3. Publish

Build the distribution, then upload it with `twine`:

```bash
python -m build          # or: pip wheel .
twine upload \
  --repository-url http://localhost:8081/repository/pypi-hosted/ \
  --username admin \
  --password admin123 \
  dist/*
```

`twine upload` posts a multipart form to the repository root:

```
POST /repository/pypi-hosted/
  :action=file_upload
  content=@my_package-1.0.0-py3-none-any.whl
  name=my-package
  version=1.0.0
  ...
```

Nexspence reads `:action`, `name`, `version`, `content` and `requires_python`,
stores the file under `/packages/<normalized-name>/<filename>`, and records the
release. `requires_python` is stored per release and survives later uploads of
other files in the same release — a client that does not send it does not strip
it from its siblings.

With an API token:

```bash
twine upload \
  --repository-url http://localhost:8081/repository/pypi-hosted/ \
  --username ci-bot \
  --password nxs_xxxxxxxxxxxxxxxxxxxx \
  dist/*
```

> `twine` prints `HTTP 200: OK` for each uploaded file when the repository
> answers the way PyPI does. Nexspence accepts the upload and answers `200`,
> but `twine` prints no per-file line for it — the command exits `0` and that
> is the whole confirmation. Check what actually landed with
> `curl http://localhost:8081/repository/pypi-hosted/simple/<package>/` (see
> below) rather than with the console output.

## 4. Install and verify

```bash
# install from the group
pip install --index-url http://localhost:8081/repository/pypi-group/simple/ my-package

# the simple index of one package
curl http://localhost:8081/repository/pypi-hosted/simple/my-package/

# the whole index
curl http://localhost:8081/repository/pypi-hosted/simple/

# what the repository holds
curl -u admin:admin123 \
  "http://localhost:8081/service/rest/v1/components?repository=pypi-hosted&format=pypi"
```

## 5. Name normalisation (PEP 503)

Package names are normalised the way PEP 503 prescribes: runs of `-`, `_` and
`.` collapse into a single `-`, and the result is lowercased. `My_Package`,
`my-package` and `my.package` are one and the same package, and they are
stored under the normalised name.

The practical consequence: **the repository name and the package name are
independent.** A repository called `pypi-hosted` can hold a package called
`My_Package`; the client asks for `/simple/my-package/` and gets it. If you
create repositories per team, keep the repository name URL-safe and let the
package name be whatever it is.

Modern pip asks for the PEP 691 JSON representation of the simple page and
falls back to HTML — Nexspence answers both, with the PEP 503 attributes
(`data-requires-python`, `data-yanked`, `data-dist-info-metadata`) carried over
into the HTML form.

## 6. Anonymous access

Proxy and group repositories with `"allowAnonymous": true` serve reads without
credentials — that is the normal setup for a shared build agent. Keep hosted
repositories non-anonymous so publishing requires authentication, and grant
read access through roles — see [security-rbac.md](../security-rbac.md).

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `WARNING: The repository located at ... is not a trusted or secure host` | Plain HTTP index | Add `trusted-host = localhost` to `pip.conf` |
| `404` on `/simple/<pkg>/` but the package was uploaded | The client asked for the repository root instead of `/simple/` | The index URL ends in `/simple/`, the upload URL does not |
| `unsupported action: …` | A client posted an action other than `file_upload` | Use `twine upload`; the legacy action is `file_upload` |
| `missing content file` | The multipart form had no `content` part | `twine upload` sends it; a hand-rolled `curl` needs `-F "content=@file"` |
| `name and version are required` | The form lacked `name` or `version` | Both fields are mandatory |
| `401` on upload | Credentials missing or wrong | `--username`/`--password`, or an `nxs_*` token as the password |
| `403` on upload | Authenticated but no write privilege on that repository | Grant the write privilege to the publishing role |
| `405 Method Not Allowed` | Uploading into a proxy repository | Proxy repositories are read-only |
| `pip` resolves a different version than the one you uploaded | `--extra-index-url` lets pip pick the highest version across indexes | Pin `==` or use only `--index-url` |
| `data-requires-python` missing in the simple page | The upload did not send `requires_python` | Send it; an upload without it does not remove an existing value |

## Related

- [RBAC and content selectors](../security-rbac.md)
- [Migration from Sonatype Nexus](../nexus-migration.md)
- [GitHub Actions guide](github-actions.md)
- [API specification](../api-spec.yaml)
