# Docker / OCI guide — images, charts, signatures

Nexspence speaks the OCI Distribution v2 protocol, so one repository serves
container images, Helm charts pushed with `helm push oci://`, ORAS artifacts and
cosign signatures — with the Referrers API to find the signatures, SBOMs and
attestations attached to a manifest.

Assumes a fresh instance on `http://localhost:8081`.

## 1. Where the registry lives

**The registry is served by the same HTTP server as the web UI — there is no
separate registry port.** Everything a Docker-family client needs is under
`/v2/` on the port you already use for the UI.

Two path shapes are accepted:

| Path | Use it for |
|---|---|
| `/v2/<repository>/…` | Docker, Helm, ORAS, cosign — clients that build the OCI path themselves |
| `/v2/repository/<repository>/…` | scripts that want the repository prefix spelled out |

A Docker client does the translation itself, so you never type the path — you
type the image reference:

```
localhost:8081/<repository>/<image>:<tag>
```

```bash
docker push localhost:8081/docker-dev/myapp:1.0.0
# → PUT /v2/docker-dev/myapp/manifests/1.0.0
```

> Older documentation for this project described a dedicated registry port
> (`5000` natively, `5001` under Docker Compose). No such listener exists —
> the server binds a single port. If you carried that port over from an older
> setup, drop it.

The registry endpoint itself answers the Distribution Spec version check:

```bash
curl -i http://localhost:8081/v2/
# HTTP/1.1 401 Unauthorized
# WWW-Authenticate: Bearer realm="http://localhost:8081/v2/token",service="nexspence-registry"
```

A `401` with that challenge is the **correct** answer, not a misconfiguration:
the ping always challenges so that a client picks the Bearer flow and sends its
credentials. A client that follows the challenge gets its token and continues.

## 2. Create the repositories

```bash
BASE_URL=http://localhost:8081 ./scripts/create-docker-repos.sh
```

That script creates the three repositories this guide uses — `docker-dev`
(hosted), `docker-proxy` (proxy) and `docker-common` (group).

By hand, with the v1-compatible API:

```bash
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# hosted — your own images
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/docker/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"docker-dev\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Internal images\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of Docker Hub
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/docker/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"docker-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Proxy to Docker Hub\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://registry-1.docker.io\" }
  }"

# group
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/docker/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"docker-common\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Group of hosted and proxy\",
    \"formatConfig\": { \"member_names\": [\"docker-dev\", \"docker-proxy\"] }
  }"
```

Image paths:

```
localhost:8081/<repository-name>/<image>:<tag>
```

## 3. Plain HTTP and `insecure-registries`

Docker refuses plain-HTTP registries unless they are listed as insecure — but
the daemon's default allowlist already covers loopback: `127.0.0.0/8` and
`::1/128` are insecure by default. So `localhost:8081` and `127.0.0.1:8081`
work with no daemon configuration at all.

Only a host on a real network needs the entry:

```json
// /etc/docker/daemon.json
{
  "insecure-registries": ["registry.internal.example.com:8081"]
}
```

```bash
sudo systemctl restart docker
```

Then log in:

```bash
docker login localhost:8081 -u admin -p admin123
```

> Put the **host and port** in `insecure-registries`, exactly as the client
> spells them. `localhost:8081` and `localhost` are different entries, and an
> entry without the port does not cover a non-default port.

## 4. Push and pull

```bash
# tag for the hosted repository
docker tag myapp:1.0.0 localhost:8081/docker-dev/myapp:1.0.0

# push
docker push localhost:8081/docker-dev/myapp:1.0.0

# pull through the proxy
docker pull localhost:8081/docker-proxy/nginx:latest

# pull your own image back
docker pull localhost:8081/docker-dev/myapp:1.0.0
```

### Official images and the `library/` namespace

Docker Hub keeps official images under `library/`. A client that pulls `nginx`
from `docker.io` asks for `docker.io/library/nginx` — the client normalises, the
registry does not, and a naive proxy forwards the short name and gets
`401 insufficient_scope` back.

Nexspence applies the same fix Docker does: the short name is tried first, and
the `library/` form is used when the upstream does not know the short one. So
`docker pull localhost:8081/docker-proxy/nginx:latest` works.

## 5. The `docker` and `oci` format labels

One implementation, two labels:

- `docker` — container images
- `oci` — Helm charts pushed with `helm push oci://`, ORAS artifacts, cosign
  signatures, SBOMs

A repository created with the `oci` label accepts everything the `docker` label
does, plus the artifact types. Use `docker` for images and `oci` when the
repository also holds charts or signed artifacts.

## 6. Helm charts over OCI

Helm refuses plain-HTTP registries unless you say so, and it has no loopback
allowlist — `--plain-http` is required for every Helm command that talks to an
HTTP registry:

```bash
helm registry login localhost:8081 -u admin -p admin123 --plain-http

helm push mychart-1.0.0.tgz oci://localhost:8081/docker-dev --plain-http
helm pull oci://localhost:8081/docker-dev/mychart --version 1.0.0 --plain-http
```

> `helm registry login` and `helm push`/`helm pull` need the flag separately.
> A login that worked does not make the following push work without it.

## 7. Signatures, SBOMs and attestations

Push a cosign signature and it lands next to the image, discoverable through the
Referrers API:

```bash
cosign sign --key cosign.key \
  localhost:8081/docker-dev/myapp:1.0.0
```

```bash
# what is attached to this manifest?
curl -u admin:admin123 \
  "http://localhost:8081/v2/docker-dev/myapp/referrers/sha256:<digest>"

# only signatures
curl -u admin:admin123 \
  "http://localhost:8081/v2/docker-dev/myapp/referrers/sha256:<digest>?artifactType=application/vnd.dev.cosign.simplesigning.v1+json"
```

The Referrers API answers with `artifactType` filtering, so a client can ask
for exactly the artifact type it understands instead of parsing the whole list.

Cross-repository blob mounts are supported: a blob already present in another
repository can be mounted instead of uploaded again, and the access check against
the source repository is performed first.

## 8. Anonymous access

The Docker registry implements the token flow that the Docker client expects:
`/v2/token` issues an anonymous bearer token for credential-less pulls, and the
per-repository RBAC decides whether the pull is allowed. With
`"allowAnonymous": true` on the repository, pulls work without `docker login`;
pushes never do.

A hosted repository with `"allowAnonymous": false` answers an anonymous pull
with `401`, not `403` — the client has no credentials at all, so it is asked to
authenticate rather than told it may not.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `Error response from daemon: Get "https://localhost:8081/v2/": http: server gave HTTP response to HTTPS client` | Plain HTTP registry on a non-loopback host | Add the host:port to `insecure-registries` and restart Docker — loopback needs no entry |
| `connection refused` on `localhost:5001` | An older port mapping | There is no separate registry port; use the web port |
| `unauthorized: authentication required` | Push without credentials, or the user lacks write access | `docker login`, and grant the write privilege on that repository |
| `403 Forbidden` on pull | Anonymous access disabled and no credentials were sent | `docker login`, or enable `allowAnonymous` for read-only use |
| `405 Method Not Allowed` | Pushing into a proxy repository | Proxy repositories are read-only |
| `401 insufficient_scope` from the upstream | Official image short name not resolved | Use the `library/` form, or rely on the built-in fallback |
| `blob unknown` on push | The blob was never uploaded | Let the client complete the upload session; do not reuse an expired upload URL |
| `429 Too Many Requests` | The rate limit is per client address | See the rate-limit settings in `config.yaml.example` |
| Helm push fails with `401` | `helm registry login` was not run, or used a different host | Log in to the exact host and port you push to |
| Helm fails with a TLS or scheme error | Helm insists on HTTPS by default | Add `--plain-http` to `registry login`, `push` and `pull` |

## Related

- [Docker subdomain connector](../docker-subdomain-connector.md) — serve images without the port and the `/repository/` prefix
- [Image scanning](../scanning.md)
- [RBAC and content selectors](../security-rbac.md)
- [GitHub Actions guide](github-actions.md)
- [API specification](../api-spec.yaml)
