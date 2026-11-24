# Helm guide — chart repositories and charts over OCI

Helm has two ways to move a chart, and Nexspence serves both:

- the **classic chart repository** — `helm repo add`, an `index.yaml` catalog,
  `helm search repo`, `helm install`;
- the **OCI path** — `helm push oci://…`, the same protocol `docker push`
  speaks.

The two are stored separately, and this guide keeps them apart. A chart
uploaded over OCI is not in the classic index, and a chart uploaded over the
classic protocol is not in the OCI registry.

Assumes a fresh instance on `http://localhost:8081`.

## 1. The two protocols in one table

| | Classic chart repository | OCI |
|---|---|---|
| Repository format | `helm` | `oci` (or `docker`) |
| Publish | `PUT /<chart>-<version>.tgz` or `POST /api/charts` | `helm push oci://<host>/<repo>/<chart>` |
| Consume | `helm repo add` → `index.yaml` | `helm pull oci://…`, `helm install oci://…` |
| URL shape | `/repository/<name>/` | `/v2/<name>/<chart>` |
| Credentials | `--username` / `--password` on `helm repo add` | `helm registry login` |

A `helm` repository is **not** an OCI registry. The server says so:

```
$ helm push mychart-0.1.0.tgz oci://localhost:8081/helm-hosted --plain-http
Error: ... unsupported: "helm-hosted" is a helm repository, not an OCI registry:
push and pull OCI artifacts (docker images, helm push oci://, oras) through an
oci or docker repository
```

So an installation that publishes charts over OCI needs **two** repositories:
one of format `helm` for the classic catalog, and one of format `oci` for the
OCI artifacts.

## 2. Create the repositories

```bash
BASE_URL=http://localhost:8081 ./scripts/create-helm-repos.sh
```

That creates `helm-hosted`, `helm-proxy` and `helm-charts` — the classic
chart repository, a proxy in front of a public chart repository, and the group
that merges them.

The OCI repository for `helm push oci://` is a separate one, of format `oci`:

```bash
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/oci/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"helm-oci\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Charts pushed over OCI\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"
```

## 3. Add the group as a chart repository

```bash
helm repo add nexspence http://localhost:8081/repository/helm-charts/ \
  --username admin --password admin123

helm repo update
helm search repo nexspence
```

For a repository that allows anonymous reads, drop `--username` and
`--password`. For one that does not, they are what make `helm pull` work —
the client sends them with every request to that repository.

### The index carries absolute download URLs

`index.yaml` lists each chart with a full URL, and the server mints it from
`http.base_url` — not from the address the client happened to use:

```yaml
urls:
  - http://localhost:8081/repository/helm-charts/packages/mychart-0.2.0.tgz
```

That default is right for the default install. **If your instance is not on
`http://localhost:8081`, set `http.base_url` to the address your clients use**
— otherwise the client reads the index from one host and downloads the tarball
from another:

```yaml
# config.yaml
http:
  base_url: "https://nexus.example.com"
```

```bash
NEXSPENCE_HTTP_BASE_URL=https://nexus.example.com
```

## 4. Publish a chart the classic way

A chart repository accepts the tarball as a `PUT`:

```bash
helm package mychart
curl -u admin:admin123 -T mychart-0.2.0.tgz \
  http://localhost:8081/repository/helm-hosted/mychart-0.2.0.tgz
```

`helm repo update` and the chart is in the catalog:

```bash
helm search repo nexspence
# NAME                CHART VERSION   APP VERSION   DESCRIPTION
# nexspence/mychart  0.2.0            1.16.0        A Helm chart for MyChart
```

`POST /api/charts` takes the same tarball as a multipart form, if you would
rather not use `curl -T`.

## 5. Publish a chart over OCI

```bash
helm registry login localhost:8081 -u admin -p admin123 --plain-http

helm push mychart-0.1.0.tgz oci://localhost:8081/helm-oci --plain-http
helm pull oci://localhost:8081/helm-oci/mychart --version 0.1.0 --plain-http
```

`--plain-http` is required for every Helm command that talks to a plain-HTTP
registry, and it is needed on `registry login`, `push` and `pull` separately —
a login that worked does not make the next push work without it.

`helm install oci://localhost:8081/helm-oci/mychart --version 0.1.0` pulls
the chart over OCI directly, without a repository in the catalog.

## 6. Consume a chart

```bash
# from the group, through the catalog
helm pull nexspence/mychart --version 0.2.0
helm install myrelease nexspence/mychart --version 0.2.0

# render without a cluster
helm template myrelease nexspence/mychart --version 0.2.0
```

`helm install` needs a cluster to talk to. `helm template` renders the same
manifests and needs nothing — it is the check to run when you only want to
know that the chart and its dependencies resolve.

## 7. How the group merges the index

The group's `index.yaml` is the union of its members' catalogs:

- entries are merged per chart name and version;
- when the same name and version exist in two members, the first member wins
  — hosted before proxy, the same first-match rule the other formats use;
- download URLs are re-rooted at the group, so `helm pull` stays on the group
  URL and the tarball is served through the group rather than through the
  member it came from.

A chart that exists only in the proxy is listed by the group as soon as the
proxy has fetched the upstream index.

## 8. The proxy

```bash
helm repo add upstream http://localhost:8081/repository/helm-proxy/ \
  --username admin --password admin123
helm repo update
helm search repo upstream
```

The proxy reads the upstream `index.yaml`, caches it for the configured time
(`helm.index_cache_ttl`), and rewrites the chart URLs so the tarball is
fetched through the instance and cached there too. A chart that is not yet in
the cached index is resolved on the download path.

Proxy repositories are read-only: a `PUT` answers `405 Method Not Allowed`.

## 9. Anonymous access

Proxy and group repositories with `"allowAnonymous": true` serve their
catalog and their charts without credentials. A hosted repository with
`"allowAnonymous": false` needs them — and so does a group whose chart lives
in that hosted member, because the request ends up there.

Keep hosted repositories non-anonymous so that publishing requires
authentication, and grant read access through roles — see
[security-rbac.md](../security-rbac.md).

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `unsupported: "…" is a helm repository, not an OCI registry` | `helm push oci://` against a repository of format `helm` | Use a repository of format `oci` or `docker` |
| `Error: failed to perform "Exists" on destination: … 400` | The same, seen from the client during the push | As above |
| `open … no such file or directory` after `helm pull` | The target directory does not exist | `mkdir -p` it first — Helm does not create it |
| `failed to fetch http://<wrong-host>/… : 404` | `index.yaml` carries URLs minted from `http.base_url`, which does not match your address | Set `http.base_url` to the address clients use |
| `401 Unauthorized` on `helm pull` | The repository needs credentials and the client has none | Add `--username` / `--password` to `helm repo add` |
| `helm repo add` succeeds but `helm search repo` is empty | The index was cached before the chart was published | `helm repo update` |
| `405 Method Not Allowed` on upload | The repository is a proxy | Push to the hosted repository |
| `helm install` fails with a connection error | There is no cluster to talk to | `helm template` renders the same manifests without one |
| Helm refuses the connection | The registry is plain HTTP | Add `--plain-http` to `registry login`, `push` and `pull` |

## Related

- [Docker / OCI guide](docker.md) — the same registry, for images and for `oras`
- [RBAC and content selectors](../security-rbac.md)
- [GitHub Actions guide](github-actions.md)
- [API specification](../api-spec.yaml)
