# Running behind a reverse proxy

Nexspence is meant to sit behind nginx (or Traefik, or a cloud load balancer):
the proxy terminates TLS, the container stays on plain HTTP. This page says what
works out of the box, what needs one setting, and how to check your own instance.

## The short version

- **Most formats work behind a reverse proxy with no configuration at all.** Their
  links are relative, so the client resolves them against the host it already
  reached.
- **npm works behind a reverse proxy when the proxy is trusted.** With the proxy's
  address in `http.trusted_proxies`, the server takes the public host from the
  proxy's `X-Forwarded-Host`, so npm's absolute tarball links name the host the
  client actually reached. Without a trusted proxy npm still needs
  `http.base_url` set to your public URL.
- **Four formats need `http.base_url` set to your public URL**: Cargo, NuGet,
  Conan and Hugging Face. Their client protocols require an absolute URL, and
  there is no way around that — the link has to carry a host. A trusted proxy
  does not help them: their absolute links are built from `http.base_url` alone.
- **`http.trusted_proxies` says whose forwarded headers are believed.** It tells
  the server which peers may set `X-Forwarded-For`, so the audit log and the rate
  limiter see the real client address, and which peers may set
  `X-Forwarded-Host` and `X-Forwarded-Proto`, so npm's absolute links name the
  public host.

## Configuration

```yaml
http:
  # The URL clients use to reach this server. Leave it at the default only if
  # clients reach the server under exactly that address. It is also the
  # fallback for npm's absolute tarball links, and the only source of the
  # absolute links of Cargo, Conan, NuGet and Hugging Face.
  base_url: "https://myrepo.ru"

  # Peers allowed to set X-Forwarded-For, as IPs or CIDRs. Empty (the default)
  # trusts nobody, so the audit log and the rate limiter use the real peer
  # address. Set this to your reverse proxy when you run behind one.
  # A trusted peer also speaks for the client about the public host: the
  # server believes that peer's X-Forwarded-Host and X-Forwarded-Proto, which
  # is what puts the right host into npm's absolute tarball links.
  trusted_proxies: ["10.0.0.0/8"]
```

`http.base_url` is the server's own view of itself, not the client's. If it says
`http://localhost:8081` and your clients reach the server through
`https://myrepo.ru`, every absolute link the server mints points at localhost —
and the client cannot follow it. npm is the one format that can be told
otherwise: behind a trusted proxy it takes the host from the proxy instead —
see [npm and a trusted proxy](#npm-and-a-trusted-proxy).

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
| Hugging Face | absolute (`Link` header) | **needs `http.base_url`** |
| Maven | none (`maven-metadata.xml` carries no URLs) | works out of the box |
| npm | absolute (`dist.tarball`) | works with a trusted proxy (`http.trusted_proxies`); otherwise **needs `http.base_url`** |
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
| Hugging Face | `huggingface_hub` hands the `Link` target straight to its HTTP client, and neither `httpx` nor `fetch` resolves a relative reference against the request URL — the request fails with "Request URL is missing an 'http://' or 'https://' protocol" |

The rest resolve relative references against the URL they were fetched from, so a
relative link is not merely tolerated but correct: the client and the server are
necessarily on the same host, because the client fetched the index from it.

Deriving the host from the request's `Host` header would look tempting — it
would make every format work without configuration. It is not done, on purpose:
one request with a forged `Host` header would poison a shared cache entry for
every other client. The host comes from `http.base_url`, or from the
`X-Forwarded-Host` of a proxy listed in `http.trusted_proxies` — the proxy's
word about the host, spoken by a peer that is known to be the proxy.
`http.base_url` and `http.trusted_proxies` exist precisely to control that
trust.

## npm and a trusted proxy

npm is the one format that hands out an absolute URL and can still name the
host the client actually reached. The public URL of a request is built like
this:

- the host from the proxy's `X-Forwarded-Host`, but only when the request came
  from a peer listed in `http.trusted_proxies`;
- the scheme from the proxy's `X-Forwarded-Proto`, and only `http` or `https`
  are believed;
- `http.base_url` for whatever the proxy did not say — and for every request
  that did not come through a trusted proxy.

`http.trusted_proxies` is what makes the proxy's word believable. What is
checked is the address the request came from, not anything the request says
about it: a client that reaches the instance directly, around the proxy, is not
a trusted peer, and its `X-Forwarded-Host` is ignored whatever it contains.

The request's own `Host` header is never believed, not even from a trusted
proxy. The host in a link is the one thing a client must not be able to choose:
the packument is cached, so one request that named a host of its choosing would
send every later client to that host instead of to this instance.

When more than one proxy sits in front of the instance, the **rightmost** value
of `X-Forwarded-Host` is the one taken: a proxy that overwrites the header
leaves one value, and a proxy that appends to it puts its own view last — the
client's own claim, if it sent one, is to the left of it.

The proxy has to say it, and the instance has to trust it:

```nginx
server {
    listen 443 ssl;
    server_name repo.example.com;

    location / {
        proxy_pass http://127.0.0.1:8081;

        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host  $host;   # the host the client used
    }
}
```

`$host` carries no port; a public URL that has one wants `$http_host` instead.

And the instance has to list the proxy's address — the peer as the server sees
it — in `http.trusted_proxies`:

```yaml
http:
  trusted_proxies: ["10.0.0.0/8"]     # the proxy's address as the server sees it
```

```bash
NEXSPENCE_HTTP_TRUSTED_PROXIES="10.0.0.0/8"    # the same list, as an environment variable
```

Both are needed, and neither is enough on its own: a header the proxy does not
send changes nothing, and a header the instance does not believe changes
nothing either.

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

For npm there is a second answer to the same question. Ask through the proxy,
from the outside, and look at the host the tarball carries:

```bash
# npm through the proxy — the tarball names the host the client used
curl -s -u admin:admin123 \
  "https://repo.example.com/repository/npm-proxy/lodash" \
  | grep -o '"tarball":"[^"]*"' | head -1
```

The proxy's request must carry `X-Forwarded-Host: repo.example.com`, and the
packument's `dist.tarball` must name that host. If it names the configured
`http.base_url` instead, the proxy's address is not in `http.trusted_proxies`,
or the proxy does not set the header.

## Related

- [Format guides](README.md) — one per format, each with the client
  configuration and the troubleshooting notes.
- [The deployment guide](../deployment.md) — including the nginx configuration.
