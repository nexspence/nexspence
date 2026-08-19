# Image Scanning — Supplying Trivy

Starting with this version, nexspence no longer includes Trivy. Scanning of
Docker and OCI images stops working after the upgrade until you complete one of
the procedures below. Scanning of Maven, npm, PyPI and Cargo packages is
unaffected and needs nothing from you.

Why: Trivy is third-party software and no longer ships inside the nexspence
image. Nexspence carries no scanner binary and no wrapper for one — you supply
the binary, nexspence runs it.

[Trivy](https://trivy.dev) is an open-source vulnerability scanner by Aqua
Security. Nexspence executes it against stored Docker/OCI images and shows the
CVEs it finds on the **Security → CVE Scan** page.

## Which procedure applies to you

| You run nexspence via | Go to |
|---|---|
| Kubernetes (Helm chart) | [Kubernetes (Helm)](#kubernetes-helm) |
| docker-compose | [docker-compose](#docker-compose) |
| plain `docker run` | [Plain docker run](#plain-docker-run) |
| from source / native install (`.deb`, `.rpm`, macOS, Windows) | [From source or native install](#from-source-or-native-install) |

## Kubernetes (Helm)

The chart does the whole job for you. Setting `scanning.enabled: true` renders
an *initContainer* — a helper container Kubernetes runs before nexspence starts —
named `trivy-copy`. It copies the Trivy binary out of the `aquasec/trivy` image
into a small shared volume, and the chart points nexspence at that copy via
environment variables. You never touch the binary yourself.

Add this to your values file (these are the chart's actual keys and defaults —
only `enabled` needs changing):

```yaml
scanning:
  enabled: true
  image:
    repository: aquasec/trivy
    # Pin to a digest for reproducible pulls, e.g. "0.70.0@sha256:...", same
    # convention as the top-level image.tag.
    tag: "0.70.0"
    pullPolicy: IfNotPresent
  # The binary is ~150 MB; the vulnerability database it downloads on first use
  # lands in the existing cache volume.
  volumeSize: 300Mi
```

Then upgrade the release:

```bash
helm upgrade nexspence deploy/helm/nexspence \
  -f your-values.yaml \
  --namespace nexspence
```

Verify the pod restarted with the initContainer and the scanner probe passed:

```bash
kubectl -n nexspence get pods
# the nexspence pod shows "Init:0/1" briefly, then Running

kubectl -n nexspence logs deploy/nexspence | grep "image scanner"
# expect one JSON log record at startup containing "image scanner" with state=ready
```

Then do the UI check in [Checking that it worked](#checking-that-it-worked).

## docker-compose

The shipped `docker-compose.yml` is already wired for this. You do exactly two
things:

1. Set `NEXSPENCE_SCAN_TRIVY_ENABLED=true` — either export it in your shell or
   add this line to a `.env` file next to `docker-compose.yml`:

   ```
   NEXSPENCE_SCAN_TRIVY_ENABLED=true
   ```

2. Start with the `scanning` profile:

   ```bash
   docker compose --profile scanning up -d
   ```

The profile adds a one-shot service that copies the binary into a shared volume
before nexspence starts. For reference, this is what the shipped file contains
(you do not need to add any of it):

```yaml
  # Nexspence ships no scanner. This copies Trivy into a shared volume so the
  # application can exec it. Opt-in: `docker compose --profile scanning up`.
  trivy-init:
    profiles: ["scanning"]
    image: aquasec/trivy:0.70.0
    entrypoint: ["sh", "-c", "cp /usr/local/bin/trivy /opt/trivy/trivy && chmod 0755 /opt/trivy/trivy"]
    volumes:
      - trivy_bin:/opt/trivy
    restart: "no"
```

and on the `nexspence` service:

```yaml
    environment:
      # Image scanning is off unless the `scanning` profile supplied a binary.
      NEXSPENCE_SCAN_TRIVY_ENABLED: "${NEXSPENCE_SCAN_TRIVY_ENABLED:-false}"
      NEXSPENCE_SCAN_TRIVY_BIN: "/opt/trivy/trivy"
    volumes:
      - trivy_bin:/opt/trivy:ro
```

**HA compose users:** `docker-compose.ha.yml` does not include the `scanning`
profile. Adapt the same pattern by hand: add the `trivy_bin` volume and the
`trivy-init` one-shot service exactly as above, then add the two environment
variables and the read-only `trivy_bin:/opt/trivy:ro` mount to the shared
`&nexspence_base` YAML anchor so both replicas get them.

## Plain docker run

Create a volume and copy the Trivy binary into it from the official image (this
is the reliable way to get a binary that runs inside the nexspence container —
see [Traps](#traps) for why a host-installed binary usually will not):

```bash
docker volume create nexspence_trivy
docker run --rm -v nexspence_trivy:/out --entrypoint sh aquasec/trivy:0.70.0 \
  -c 'cp /usr/local/bin/trivy /out/trivy && chmod 0755 /out/trivy'
```

Then run nexspence with the volume mounted read-only and the two scan variables
set. A complete minimal run (adjust the database DSN to your PostgreSQL):

```bash
docker run -d --name nexspence \
  -p 8081:8081 -p 5001:5000 \
  -e NEXSPENCE_DATABASE_DSN="postgres://nexspence:nexspence@your-postgres-host:5432/nexspence?sslmode=disable" \
  -e NEXSPENCE_STORAGE_LOCAL_BASE_PATH=/app/data/blobs \
  -e NEXSPENCE_SCAN_TRIVY_ENABLED=true \
  -e NEXSPENCE_SCAN_TRIVY_BIN=/opt/trivy/trivy \
  -v nexspence_blobs:/app/data/blobs \
  -v nexspence_secrets:/app/secrets \
  -v nexspence_trivy:/opt/trivy:ro \
  ghcr.io/nexspence/nexspence:latest
```

If you already have a `docker run` command you use, keep it and add only these
three lines to it:

```
  -e NEXSPENCE_SCAN_TRIVY_ENABLED=true \
  -e NEXSPENCE_SCAN_TRIVY_BIN=/opt/trivy/trivy \
  -v nexspence_trivy:/opt/trivy:ro \
```

## From source or native install

When nexspence runs directly on the host (built from source, or installed from
a `.deb`/`.rpm`/macOS/Windows package), there is no container boundary — any
normally installed Trivy works. Install it with your package manager:

```bash
# macOS
brew install trivy

# Debian / Ubuntu (official Aqua Security repository)
sudo apt-get install -y wget gnupg
wget -qO - https://aquasecurity.github.io/trivy-repo/deb/public.key | \
  gpg --dearmor | sudo tee /usr/share/keyrings/trivy.gpg > /dev/null
echo "deb [signed-by=/usr/share/keyrings/trivy.gpg] https://aquasecurity.github.io/trivy-repo/deb generic main" | \
  sudo tee /etc/apt/sources.list.d/trivy.list
sudo apt-get update && sudo apt-get install -y trivy

# RHEL / Fedora
cat <<'EOF' | sudo tee /etc/yum.repos.d/trivy.repo
[trivy]
name=Trivy repository
baseurl=https://aquasecurity.github.io/trivy-repo/rpm/releases/$basearch/
gpgcheck=1
gpgkey=https://aquasecurity.github.io/trivy-repo/rpm/public.key
enabled=1
EOF
sudo dnf install -y trivy
```

Then turn image scanning on in the config file — `/etc/nexspence/config.yaml`
for a `.deb`/`.rpm` install, or the `config.yaml` next to the binary when you
run from source:

```yaml
scan:
  trivy:
    enabled: true
```

`scan.trivy.bin` defaults to `trivy`, which is resolved through `PATH`, so a
package-manager install is found automatically. If you put the binary somewhere
unusual, set `scan.trivy.bin` to its absolute path.

The config file is read only at startup — there is no hot reload — so restart
nexspence to apply the change:

```bash
sudo systemctl restart nexspence    # .deb / .rpm install
# from source: stop and start your own nexspence process
```

Then continue with [Checking that it worked](#checking-that-it-worked).

## Checking that it worked

Open the web UI, go to **Security → CVE Scan**. Under the **Scan** button there
is a status line. Success looks like this (version and path vary with how you
supplied the binary):

```
Trivy 0.70.0 — /opt/trivy/trivy
```

and the Scan button is active. Any other message means one of the steps above
is incomplete:

| Status line | What it means | What to revisit |
|---|---|---|
| `Image scanning is disabled by the administrator` | `scan.trivy.enabled` is still `false` — nexspence has not even looked for a binary. | The enable switch: `NEXSPENCE_SCAN_TRIVY_ENABLED=true` (compose / docker run), `scanning.enabled: true` (Helm), or `scan.trivy.enabled: true` (config file). |
| `Trivy not found: looked for …` | Scanning is enabled but there is no file at the configured location (or no `trivy` on `PATH`). | The supply step: did `trivy-init` / `trivy-copy` run? Is the volume mounted at the path `scan.trivy.bin` points to? |
| `Trivy found at … but will not run: …` | A file is there but it does not execute: wrong architecture, wrong libc, truncated download, or missing execute permission. | Replace the binary with one copied from the `aquasec/trivy` image — see [Traps](#traps). |

Without the UI, ask the API as an admin:

```bash
curl -s -H "Authorization: Bearer <admin token>" \
  http://localhost:8081/api/v1/security/scanner
# {"state":"ready","version":"0.70.0","path":"/opt/trivy/trivy","message":"Trivy 0.70.0 — /opt/trivy/trivy"}
```

Nexspence also writes one log record at startup containing `image scanner`
with the current state (`state=ready` when everything works), so
`docker compose logs nexspence | grep "image scanner"` (or
`journalctl -u nexspence | grep "image scanner"`) answers the question too.

With scanning already enabled, the binary probe re-runs at most once every 60
seconds, so supplying or replacing the binary shows up within a minute without
a restart. Changing configuration — including `scan.trivy.enabled` itself — is
picked up only at startup: restart nexspence after any config edit.

## Traps

**A host binary works in the container only if it is the official static
build.** Trivy binaries from community distribution packages, Homebrew, or
macOS are built for a different environment than the nexspence container and
fail there with the `Trivy found at … but will not run` status. If you want to
mount a binary from the host instead of copying it out of the `aquasec/trivy`
image, use Aqua Security's official static Linux build: the tarball from
[Trivy's GitHub releases](https://github.com/aquasecurity/trivy/releases)
(`trivy_<version>_Linux-64bit.tar.gz`), or an install from Aqua's own apt/yum
repositories, which ship the same static build. When in doubt, copy from the
`aquasec/trivy` image — that binary is known to run in the container as uid 1000.

**Budget ~150 MB for the binary.** The Trivy binary itself is around 150 MB;
the Helm chart sizes its volume at 300 Mi for headroom. The vulnerability
database (a further few hundred MB) goes to the cache directory
(`TRIVY_CACHE_DIR=/app/.cache/trivy` inside the image), not to the binary volume.

**The first scan is slow.** On first use Trivy downloads its vulnerability
database, which typically takes 1–3 minutes on top of the scan itself.
Subsequent scans reuse the cached database and are fast. A first scan that sits
in "running" for a couple of minutes is normal, not stuck.

## The vulnerability database

Nothing needs to be present at first start — the database is fetched by the
first scan, not at boot. Three setups:

**1. Defaults (most deployments).** Do nothing. Trivy downloads its database
from its own default location (`ghcr.io`) on first scan and refreshes it as
needed. Requires outbound HTTPS to `ghcr.io`.

**2. Through a nexspence proxy repository.** `trivy-db` is an ordinary OCI
artifact, so a nexspence Docker/OCI *proxy* repository in front of `ghcr.io`
works as the source. Create one (e.g. named `ghcr-proxy` with remote
`https://ghcr.io`), then point Trivy at it:

```yaml
scan:
  trivy:
    enabled: true
    db_repository:
      - nexspence.example.com/repository/ghcr-proxy/aquasecurity/trivy-db:2
    java_db_repository:
      - nexspence.example.com/repository/ghcr-proxy/aquasecurity/trivy-java-db:1
```

As environment variables the lists are comma-separated:
`NEXSPENCE_SCAN_TRIVY_DB_REPOSITORY=host/repo/a:2,host/repo/b:2`.

**3. Fully air-gapped.** On a machine with internet access, copy the database
artifacts into a nexspence *hosted* Docker/OCI repository with `oras`, `crane`
or `skopeo`:

```bash
# with oras
oras cp ghcr.io/aquasecurity/trivy-db:2 nexspence.example.com/repository/trivy-mirror/trivy-db:2
oras cp ghcr.io/aquasecurity/trivy-java-db:1 nexspence.example.com/repository/trivy-mirror/trivy-java-db:1

# or with crane
crane copy ghcr.io/aquasecurity/trivy-db:2 nexspence.example.com/repository/trivy-mirror/trivy-db:2
crane copy ghcr.io/aquasecurity/trivy-java-db:1 nexspence.example.com/repository/trivy-mirror/trivy-java-db:1
```

Mirror both: the main database covers OS and language packages, and the Java
database is fetched separately the first time an image containing Java
artifacts is scanned — without a mirror for it, that scan tries `ghcr.io` and
fails in an air-gapped network.

Then point at the mirror and stop Trivy from trying to update over the
internet:

```yaml
scan:
  trivy:
    enabled: true
    db_repository:
      - nexspence.example.com/repository/trivy-mirror/trivy-db:2
    java_db_repository:
      - nexspence.example.com/repository/trivy-mirror/trivy-java-db:1
    skip_db_update: false   # first scan pulls from the mirrors above
```

Once the cache is populated (or if you seed the cache directory by hand), set
`skip_db_update: true` so Trivy never attempts a refresh; re-seed the mirror
and flip it back to refresh.

## Where the switch lives

| Setting | config.yaml key | Environment variable | Helm value |
|---|---|---|---|
| Enable image scanning | `scan.trivy.enabled` | `NEXSPENCE_SCAN_TRIVY_ENABLED` | `scanning.enabled` (sets the env var to `true` for you) |
| Path to the binary | `scan.trivy.bin` | `NEXSPENCE_SCAN_TRIVY_BIN` | set automatically to `/opt/trivy/trivy` when `scanning.enabled` is on |
| DB source (list) | `scan.trivy.db_repository` | `NEXSPENCE_SCAN_TRIVY_DB_REPOSITORY` (comma-separated) | via config file / env |
| Java DB source (list) | `scan.trivy.java_db_repository` | `NEXSPENCE_SCAN_TRIVY_JAVA_DB_REPOSITORY` (comma-separated) | via config file / env |
| Skip DB refresh | `scan.trivy.skip_db_update` | `NEXSPENCE_SCAN_TRIVY_SKIP_DB_UPDATE` | via config file / env |
| Cache directory | `scan.trivy.cache_dir` | `NEXSPENCE_SCAN_TRIVY_CACHE_DIR` | via config file / env (image default: `TRIVY_CACHE_DIR=/app/.cache/trivy`) |

Every `config.yaml` key maps to its environment variable as
`NEXSPENCE_<SECTION>_<KEY>` (uppercase, dots become underscores).

## Rolling back

If you need image scanning working right now and cannot supply a binary yet,
pin the previous image tag: **v1.38.0** is the last release whose image still
bundles Trivy.

```bash
# docker / compose
image: ghcr.io/nexspence/nexspence:v1.38.0

# Helm
helm upgrade nexspence deploy/helm/nexspence --set image.tag=v1.38.0 --namespace nexspence
```

Note for compose users: the shipped `docker-compose.yml` builds the image from
source with a `build:` block — to pin a version, replace that block on the
`nexspence` service with the `image:` line above.

No data is lost either way: scan results already in the database stay visible
in the UI whether or not a scanner binary is currently available, and rolling
forward again later requires nothing beyond this document's procedures.
