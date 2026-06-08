# Native Install (no Docker)

Run Nexspence directly on Linux, macOS, or Windows. Nexspence is a single binary with
the web UI embedded; it requires an **external PostgreSQL** database (13+).

> Prefer Docker? See [deployment.md](deployment.md). For Kubernetes, see the Helm chart.

## 1. Prerequisites — PostgreSQL

Provision PostgreSQL (any reachable host), then create a database and role:

```sql
CREATE DATABASE nexspence;
CREATE ROLE nexspence WITH LOGIN PASSWORD 'change-me';
GRANT ALL PRIVILEGES ON DATABASE nexspence TO nexspence;
```

The matching `config.yaml` line:

```yaml
database:
  dsn: "postgres://nexspence:change-me@db.example.com:5432/nexspence?sslmode=disable"
```

Nexspence auto-migrates the schema on first start. No manual migration step is needed.

## 2. Linux (.deb / .rpm)

Download the package for your distro from the
[latest release](https://github.com/nexspence/nexspence/releases/latest), then:

```bash
# Debian / Ubuntu
sudo dpkg -i nexspence_*.deb

# RHEL / Fedora / SUSE
sudo rpm -i nexspence-*.rpm        # or: sudo dnf install ./nexspence-*.rpm
```

The package installs `/usr/bin/nexspence`, a default `/etc/nexspence/config.yaml`, a
`nexspence` system user, and the `nexspence.service` systemd unit. It does **not**
auto-start — edit the config first:

```bash
sudo nano /etc/nexspence/config.yaml
#   database.dsn            → your PostgreSQL
#   auth.jwt_secret         → >= 32 random characters
#   bootstrap.admin_password
```

Then enable and start:

```bash
sudo systemctl enable --now nexspence
systemctl status nexspence
journalctl -u nexspence -f       # follow logs
curl -i http://localhost:8081/   # verify
```

Blob storage defaults to `/var/lib/nexspence/data/blobs` (the config's
`storage.local.base_path` `./data/blobs` resolved against the service
`WorkingDirectory` `/var/lib/nexspence`); set `storage.local.base_path` to an absolute
path if you want it elsewhere.

## 3. macOS

Download the `darwin` archive, extract it, and install manually:

```bash
tar xzf nexspence_*_darwin_*.tar.gz
sudo cp nexspence /usr/local/bin/
sudo xattr -dr com.apple.quarantine /usr/local/bin/nexspence   # unsigned binary

sudo mkdir -p /usr/local/etc/nexspence /usr/local/var/nexspence /usr/local/var/log
sudo cp config.yaml.example /usr/local/etc/nexspence/config.yaml
sudo nano /usr/local/etc/nexspence/config.yaml                 # edit DSN, jwt_secret, admin password

sudo cp packaging/launchd/com.nexspence.server.plist /Library/LaunchDaemons/
sudo launchctl load -w /Library/LaunchDaemons/com.nexspence.server.plist
```

Logs: `/usr/local/var/log/nexspence.{out,err}.log`. Stop/remove:
`sudo launchctl unload -w /Library/LaunchDaemons/com.nexspence.server.plist`.

## 4. Windows

Download the `windows` zip, extract it, then from an **Administrator** PowerShell in
the extracted folder:

```powershell
.\packaging\windows\install-service.ps1
notepad C:\ProgramData\Nexspence\config.yaml   # edit DSN, jwt_secret, admin password
Start-Service nexspence
```

The script installs `nexspence.exe` to `C:\Program Files\Nexspence`, seeds
`C:\ProgramData\Nexspence\config.yaml`, and registers the `nexspence` service (manual
start). The binary is unsigned — on first run SmartScreen may warn; choose
**More info → Run anyway**. Remove with `.\packaging\windows\uninstall-service.ps1`.

## 5. Reverse proxy (single node, TLS)

Put a TLS-terminating proxy in front of Nexspence. Artifact uploads can be large and
long-lived, so disable request buffering and raise the body limit to match
`http.max_body_mb` (default `1024`). Set `http.base_url` to the public HTTPS URL.

### nginx

```nginx
server {
    listen 443 ssl;
    server_name repo.example.com;

    ssl_certificate     /etc/ssl/certs/repo.example.com.crt;
    ssl_certificate_key /etc/ssl/private/repo.example.com.key;

    client_max_body_size 1024m;          # match http.max_body_mb

    location / {
        proxy_pass http://127.0.0.1:8081;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        proxy_request_buffering off;       # stream uploads
        proxy_buffering         off;       # stream downloads
        proxy_read_timeout  1800s;         # match http.read_timeout_sec
        proxy_send_timeout  1800s;         # match http.write_timeout_sec
    }
}
```

### Caddy

```caddy
repo.example.com {
    reverse_proxy 127.0.0.1:8081 {
        flush_interval -1                 # stream responses
    }
    request_body {
        max_size 1024MB
    }
}
```

Caddy provisions and renews TLS automatically.

## 6. Load balancer (multi-node)

Run two or more native instances on separate hosts, all pointing at **one** shared
PostgreSQL and **one** shared blob store. This mirrors the Docker HA topology
(`docker-compose.ha.yml`) without containers.

**Requirements:**

- **Shared blob store.** Per-node local disk does **not** work — each node must see the
  same artifacts. Use either:
  - a shared filesystem (NFS/EFS) mounted at the same `storage.local.base_path` on every
    node, or
  - S3 / MinIO (`storage.default_type: s3`) — recommended for HA.
- **One PostgreSQL** shared by all nodes (`database.dsn` identical everywhere).
- Each node runs its own systemd/launchd/Windows service against the shared config.

Auth is stateless (JWT / `nxs_*` tokens), and the UI SPA is served from the binary, so
**no session stickiness is required** — plain round-robin is fine.

### nginx upstream

```nginx
upstream nexspence_backend {
    server 10.0.0.11:8081;
    server 10.0.0.12:8081;
}

server {
    listen 443 ssl;
    server_name repo.example.com;
    ssl_certificate     /etc/ssl/certs/repo.example.com.crt;
    ssl_certificate_key /etc/ssl/private/repo.example.com.key;

    client_max_body_size 1024m;

    location / {
        proxy_pass http://nexspence_backend;
        proxy_set_header Host              $host;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_request_buffering off;
        proxy_buffering         off;
        proxy_read_timeout  1800s;
        proxy_send_timeout  1800s;
    }
}
```

### HAProxy

```haproxy
frontend nexspence_fe
    bind *:443 ssl crt /etc/haproxy/certs/repo.example.com.pem
    default_backend nexspence_be

backend nexspence_be
    balance roundrobin
    option forwardfor
    timeout server 1800s
    server n1 10.0.0.11:8081 check
    server n2 10.0.0.12:8081 check
```

## 7. Docker registry subdomain connector

To pull/push Docker images using per-repository subdomains
(`<repo>.registry.example.com`) on a native install, you need wildcard DNS, a wildcard
TLS cert, and a proxy that forwards the original `Host` header so Nexspence's
subdomain connector can route it. Enable it in config:

```yaml
docker:
  subdomain_connector:
    enabled: true
    base_host: "registry.example.com"
```

### nginx wildcard server

```nginx
server {
    listen 443 ssl;
    server_name ~^(?<repo>.+)\.registry\.example\.com$;

    ssl_certificate     /etc/ssl/certs/wildcard.registry.example.com.crt;
    ssl_certificate_key /etc/ssl/private/wildcard.registry.example.com.key;

    client_max_body_size 1024m;

    location / {
        proxy_pass http://127.0.0.1:8081;
        proxy_set_header Host              $host;   # connector routes on the subdomain
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_request_buffering off;
        proxy_read_timeout  1800s;
    }
}
```

Then `docker login repo-name.registry.example.com` targets that hosted repository
directly. See the in-app docs and `config.yaml.example` for the full
`docker.subdomain_connector.*` options.
