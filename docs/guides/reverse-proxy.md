# Running behind a reverse proxy

Nexspence is meant to sit behind nginx (or Traefik, or a cloud load balancer):
the proxy terminates TLS, the container stays on plain HTTP. This page says what
works out of the box, what needs one setting, and how to check your own instance.

## The short version

- **Most formats work behind a reverse proxy with no configuration at all.** Their
  links are relative, so the client resolves them against the host it already
  reached.
- **Four formats need `http.base_url` set to your public URL**: npm, Cargo,
  NuGet and Conan. Their client protocols require an absolute URL, and there is
  no way around that — the link has to carry a host.
- **`http.trusted_proxies` is not a substitute for `http.base_url`.** It tells the
  server which peers may set `X-Forwarded-For`, so the audit log and the rate
  limiter see the real client address. It does not rewrite the links the server
  hands out.

## Configuration

```yaml
http:
  # The URL clients use to reach this server. Leave it at the default only if
  # clients reach the server under exactly that address.
  base_url: "https://myrepo.ru"

  # Peers allowed to set X-Forwarded-For, as IPs or CIDRs. Empty (the default)
  # trusts nobody, so the audit log and the rate limiter use the real peer
  # address. Set this to your reverse proxy when you run behind one.
  trusted_proxies: ["10.0.0.0/8"]
```

`http.base_url` is the server's own view of itself, not the client's. If it says
`http://localhost:8081` and your clients reach the server through
`https://myrepo.ru`, every absolute link the server mints points at localhost —
and the client cannot follow it.

## Compatibility matrix

| Format | Links the client receives | Behind a reverse proxy |
|---|---|---|
| Alpine (apk) | none (relative paths in `APKINDEX.tar.gz`) | works out of the box |
| APT | relative `Filename:` and hash paths | works out of the box |
| Cargo | absolute (`config.json` `dl` / `api`) | **needs `http.base_url`** |
| Conan | absolute (`upload_urls` / `download_urls`) | **needs `http.base_url`** |
| Conda | relative (`repodata.json` entries) | works out of the box |
| CRAN | none (`PACKAGES` carries no URLs) | works out of the box |
| Docker / OCI | path-only `Location` and `Link` headers | works out of the box |
| Go modules | none (`.info` carries no URLs) | works out of the box |
| Helm | relative (`index.yaml` `urls:`) | works out of the box |
| Hugging Face | relative (`Link` header) | works out of the box |
| Maven | none (`maven-metadata.xml` carries no URLs) | works out of the box |
| npm | absolute (`dist.tarball`) | **needs `http.base_url`** |
| NuGet | absolute (`@id`, `packageContent`) | **needs `http.base_url`** |
| PyPI | relative (simple index `href`) | works out of the box |
| Raw | relative (directory listing links) | works out of the box |
| RubyGems | none (compact index carries no URLs) | works out of the box |
| Terraform | relative (discovery document, `download_url`) | works out of the box |
| Yum / RPM | relative (`location href`) | works out of the box |

## Why some formats need an absolute URL

Not every package manager resolves a relative reference against the page it came
from. Four of them take the link literally:

| Client | What it does with a relative link |
|---|---|
| npm, pnpm, Yarn | treat it as a local file path — `npm error ENOENT … path /repository/npm-proxy/foo/-/foo-1.0.0.tgz` |
| Cargo | rejects it: `URL rejected: No host part in the URL` |
| NuGet | the service index requires `@id` to be absolute with an HTTP/HTTPS scheme |
| Conan | dereferences the returned URLs verbatim |

The rest resolve relative references against the URL they were fetched from, so a
relative link is not merely tolerated but correct: the client and the server are
necessarily on the same host, because the client just fetched the index from it.

Deriving the host from the request's `Host` header would look tempting — it
would make every format work without configuration. It is not done, on purpose:
one request with a forged `Host` header would poison a shared cache entry for
every other client. `http.base_url` and `http.trusted_proxies` exist precisely
to control that trust.

## Checking your own instance

Ask the server for a link and look at the host it carries:

```bash
# npm — needs an absolute link; this is the one to check
curl -s -u admin:admin123 \
  "http://localhost:8081/repository/npm-proxy/lodash" \
  | grep -o '"tarball":"[^"]*"' | head -1

# PyPI — relative is correct here
curl -s -u admin:admin123 \
  "http://localhost:8081/repository/pypi-proxy/simple/requests/" \
  | grep -o 'href="[^"]*"' | head -1

# Helm — relative is correct here
curl -s -u admin:admin123 \
  "http://localhost:8081/repository/helm-proxy/index.yaml" \
  | grep -A2 "urls:" | head -3
```

If a link carries `http://localhost:8081` and your clients do not reach the
server under that address, set `http.base_url` to the public URL and restart.

## Related

- [Format guides](README.md) — one per format, each with the client
  configuration and the troubleshooting notes.
- [The deployment guide](../deployment.md) — including the nginx configuration.
