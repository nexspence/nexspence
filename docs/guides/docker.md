# Docker / OCI guide — images, charts, signatures

Nexspence speaks the OCI Distribution v2 protocol, so one repository serves
container images, Helm charts pushed with `helm push oci://`, ORAS artifacts and
cosign signatures — with the Referrers API to find the signatures, SBOMs and
attestations attached to a manifest.

Assumes a fresh instance on `http://localhost:8081`.

## 1. The port

The registry listens on its own port, separate from the web UI:

| Deployment | Registry port |
|---|---|
| Native install | `5000` |
| Docker Compose (default) | `5001` on the host — `5000` inside the container |

The Compose file maps `5001:5000`, so from the host you talk to `localhost:5001`.
If you run Nexspence natively, the port is `5000` directly.

The Docker API is also reachable under the web port for clients that cannot use
a separate port:

```
http://localhost:8081/repository/<repository-name>/<image>:<tag>
```

## 2. Create the repositories

```bash
BASE_URL=http://localhost:8081 ./scripts/create-docker-repos.sh
```

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
    \"name\": \"docker-hosted\",
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
    \"name\": \"docker-public\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Group of hosted and proxy\",
    \"formatConfig\": { \"member_names\": [\"docker-hosted\", \"docker-proxy\"] }
  }"
```

Image paths:

```
localhost:5001/<repository-name>/<image>:<tag>
```

## 3. Tell Docker that the registry is not HTTPS

Docker refuses plain HTTP registries unless they are listed as insecure:

```json
// /etc/docker/daemon.json
{
  "insecure-registries": ["localhost:5001"]
}
```

```bash
sudo systemctl restart docker
```

Then log in:

```bash
docker login localhost:5001 -u admin -p admin123
```

> `docker login` against a plain-HTTP registry needs the same
> `insecure-registries` entry, otherwise the client refuses the connection
> before any credential is sent.

## 4. Push and pull

```bash
# tag for the hosted repository
docker tag myapp:1.0.0 localhost:5001/docker-hosted/myapp:1.0.0

# push
docker push localhost:5001/docker-hosted/myapp:1.0.0

# pull through the proxy
docker pull localhost:5001/docker-proxy/nginx:latest

# pull your own image back
docker pull localhost:5001/docker-hosted/myapp:1.0.0
```

### Official images and the `library/` namespace

Docker Hub keeps official images under `library/`. A client that pulls `nginx`
from `docker.io` asks for `docker.io/library/nginx` — the client normalises, the
registry does not, and a naive proxy forwards the short name and gets
`401 insufficient_scope` back.

Nexspence applies the same fix Docker does: the short name is tried first, and
the `library/` form is used when the upstream does not know the short one. So
`docker pull localhost:5001/docker-proxy/nginx:latest` works.

## 5. The `docker` and `oci` format labels

One implementation, two labels:

- `docker` — container images
- `oci` — Helm charts pushed with `helm push oci://`, ORAS artifacts, cosign
  signatures, SBOMs

A repository created with the `oci` label accepts everything the `docker` label
does, plus the artifact types. Use `docker` for images and `oci` when the
repository also holds charts or signed artifacts.

## 6. Helm charts over OCI

```bash
helm registry login localhost:5001 -u admin -p admin123

helm push mychart-1.0.0.tgz oci://localhost:5001/docker-hosted
helm pull oci://localhost:5001/docker-hosted/mychart --version 1.0.0
```

## 7. Signatures, SBOMs and attestations

Push a cosign signature and it lands next to the image, discoverable through the
Referrers API:

```bash
cosign sign --key cosign.key \
  localhost:5001/docker-hosted/myapp:1.0.0
```

```bash
# what is attached to this manifest?
curl -u admin:admin123 \
  "http://localhost:5001/v2/docker-hosted/myapp/referrers/sha256:<digest>"

# only signatures
curl -u admin:admin123 \
  "http://localhost:5001/v2/docker-hosted/myapp/referrers/sha256:<digest>?artifactType=application/vnd.dev.cosign.simplesigning.v1+json"
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

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `Error response from daemon: Get "https://localhost:5001/v2/": http: server gave HTTP response to HTTPS client` | Plain HTTP registry | Add `localhost:5001` to `insecure-registries` and restart Docker |
| `unauthorized: authentication required` | Push without credentials, or the user lacks write access | `docker login`, and grant the write privilege on that repository |
| `403 Forbidden` on pull | Anonymous access disabled and no credentials were sent | `docker login`, or enable `allowAnonymous` for read-only use |
| `405 Method Not Allowed` | Pushing into a proxy repository | Proxy repositories are read-only |
| `401 insufficient_scope` from the upstream | Official image short name not resolved | Use the `library/` form, or rely on the built-in fallback |
| `blob unknown` on push | The blob was never uploaded | Let the client complete the upload session; do not reuse an expired upload URL |
| `429 Too Many Requests` | The rate limit is per client address | See the rate-limit settings in `config.yaml.example` |
| Helm push fails with `401` | `helm registry login` was not run, or used a different host | Log in to the exact host and port you push to |

## Related

- [Docker subdomain connector](../docker-subdomain-connector.md) — serve images without the port and the `/repository/` prefix
- [Image scanning](../scanning.md)
- [RBAC and content selectors](../security-rbac.md)
- [GitHub Actions guide](github-actions.md)
- [API specification](../api-spec.yaml)
