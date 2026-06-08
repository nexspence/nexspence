# Native Local Installation + Cross-Platform Release Artifacts — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship native Nexspence installers (Linux `.deb`/`.rpm`, macOS + Windows archives) built by GoReleaser, with systemd/launchd/Windows-service integration and a reverse-proxy / load-balancer install guide.

**Architecture:** A single `.goreleaser.yaml` (frontend built first via a `before` hook so the embedded UI is current) produces binaries, Linux packages (nfpm), archives, and checksums, and takes over GitHub-release creation inside the existing tag-triggered `release.yml`. Packaging source files (service units + nfpm scriptlets) live under a new `packaging/` dir. The service simply runs `nexspence serve --config <path>` — **no application code changes**. External PostgreSQL stays required; docs cover provisioning it.

**Tech Stack:** GoReleaser v2 + nfpm, systemd, launchd, Windows `sc.exe`, GitHub Actions, nginx/Caddy/HAProxy (docs only).

**Spec:** `docs/superpowers/specs/2026-06-08-native-local-install-design.md`

**Reference facts (verified in the codebase):**
- Entrypoint `cmd/server/main.go`; cobra commands `serve` and `migrate`; both accept `--config`/`-c` (default `config.yaml`). `serve` auto-migrates on start.
- Version injected via ldflags: `-X main.Version=...` (`Makefile` `LDFLAGS`, `main.Version` at `cmd/server/main.go:320`).
- Frontend is embedded into the binary at build time (`make build` runs `build-frontend` first).
- Config keys: `http.addr` (`:8081`), `http.base_url`, `http.max_body_mb` (`1024`), `database.dsn`, `storage.local.base_path` (`./data/blobs`), `auth.jwt_secret`, `bootstrap.admin_password`.
- Release repo is **`nexspence/nexspence`** (public), built from `nexspence-core`. `release.yml` authenticates with `secrets.RELEASE_PAT`.

**Working branch:** `feat/native-local-install` (already created; the spec is committed there).

---

## File Structure

**Create:**
- `packaging/systemd/nexspence.service` — systemd unit
- `packaging/launchd/com.nexspence.server.plist` — macOS LaunchDaemon
- `packaging/windows/install-service.ps1` — register Windows service
- `packaging/windows/uninstall-service.ps1` — remove Windows service
- `packaging/nfpm/postinstall.sh` — create user, data dir, perms, daemon-reload
- `packaging/nfpm/preremove.sh` — stop + disable service
- `packaging/nfpm/postremove.sh` — daemon-reload (leave data/config)
- `packaging/INSTALL.md` — per-OS quickstart shipped inside archives
- `.goreleaser.yaml` — build/package/release config
- `docs/install-local.md` — native install + reverse-proxy + load-balancer guide

**Modify:**
- `Makefile` — add `release-snapshot` convenience target
- `.github/workflows/release.yml` — add Go/Node setup + GoReleaser step, remove manual `gh release` step
- `README.md` — native-install pointer
- `docs/deployment.md` — "Native Install (no Docker)" section
- `website/docs/index.html` — inline native-install section (self-contained-docs rule)
- `NEXT_RELEASE.md` — changelog entry

---

## Task 1: Packaging — service files and nfpm scriptlets

**Files:**
- Create: `packaging/systemd/nexspence.service`
- Create: `packaging/launchd/com.nexspence.server.plist`
- Create: `packaging/nfpm/postinstall.sh`
- Create: `packaging/nfpm/preremove.sh`
- Create: `packaging/nfpm/postremove.sh`

- [ ] **Step 1: Create the systemd unit**

Create `packaging/systemd/nexspence.service`:

```ini
[Unit]
Description=Nexspence artifact repository manager
Documentation=https://nexspence.com
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=nexspence
Group=nexspence
ExecStart=/usr/bin/nexspence serve --config /etc/nexspence/config.yaml
WorkingDirectory=/var/lib/nexspence
Restart=on-failure
RestartSec=5

# Hardening
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/nexspence

[Install]
WantedBy=multi-user.target
```

- [ ] **Step 2: Create the launchd plist**

Create `packaging/launchd/com.nexspence.server.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.nexspence.server</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/nexspence</string>
        <string>serve</string>
        <string>--config</string>
        <string>/usr/local/etc/nexspence/config.yaml</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>WorkingDirectory</key>
    <string>/usr/local/var/nexspence</string>
    <key>StandardOutPath</key>
    <string>/usr/local/var/log/nexspence.out.log</string>
    <key>StandardErrorPath</key>
    <string>/usr/local/var/log/nexspence.err.log</string>
</dict>
</plist>
```

- [ ] **Step 3: Create the nfpm postinstall script**

Create `packaging/nfpm/postinstall.sh`:

```sh
#!/bin/sh
set -e

# Create dedicated system group + user if missing
if ! getent group nexspence >/dev/null 2>&1; then
    groupadd --system nexspence
fi
if ! getent passwd nexspence >/dev/null 2>&1; then
    useradd --system --gid nexspence --home-dir /var/lib/nexspence \
        --shell /usr/sbin/nologin --comment "Nexspence service" nexspence
fi

# Data directory (matches LocalBlobStore 0750 convention)
mkdir -p /var/lib/nexspence
chown nexspence:nexspence /var/lib/nexspence
chmod 0750 /var/lib/nexspence

# Config holds secrets — readable only by root + service group
if [ -f /etc/nexspence/config.yaml ]; then
    chown root:nexspence /etc/nexspence/config.yaml
    chmod 0640 /etc/nexspence/config.yaml
fi

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
fi

cat <<'BANNER'
────────────────────────────────────────────────────────────
 Nexspence installed.

 Next steps:
   1. Edit /etc/nexspence/config.yaml:
        - database.dsn          (point at your PostgreSQL)
        - auth.jwt_secret       (>= 32 random chars)
        - bootstrap.admin_password
   2. Enable + start the service:
        sudo systemctl enable --now nexspence
   3. Browse to http://localhost:8081

 Docs: https://nexspence.com  →  Native install
────────────────────────────────────────────────────────────
BANNER
```

- [ ] **Step 4: Create the nfpm preremove script**

Create `packaging/nfpm/preremove.sh`:

```sh
#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet nexspence 2>/dev/null; then
        systemctl stop nexspence || true
    fi
    systemctl disable nexspence >/dev/null 2>&1 || true
fi
```

- [ ] **Step 5: Create the nfpm postremove script**

Create `packaging/nfpm/postremove.sh`:

```sh
#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
fi
# NOTE: /var/lib/nexspence (data) and /etc/nexspence/config.yaml are
# intentionally left in place. Remove them manually to fully purge.
```

- [ ] **Step 6: Make the shell scripts executable and verify they parse**

Run:
```bash
chmod +x packaging/nfpm/*.sh
for f in packaging/nfpm/*.sh; do sh -n "$f" && echo "OK: $f"; done
```
Expected: `OK: packaging/nfpm/postinstall.sh`, `OK: packaging/nfpm/preremove.sh`, `OK: packaging/nfpm/postremove.sh` (no syntax errors).

- [ ] **Step 7: Verify the plist is well-formed XML**

Run:
```bash
plutil -lint packaging/launchd/com.nexspence.server.plist 2>/dev/null || xmllint --noout packaging/launchd/com.nexspence.server.plist && echo "plist OK"
```
Expected: `plist OK` (or `... OK` from plutil). If neither tool exists, skip — GoReleaser will still ship the file.

- [ ] **Step 8: Commit**

```bash
git add packaging/systemd packaging/launchd packaging/nfpm
git commit -m "build(packaging): systemd unit, launchd plist, nfpm scriptlets"
```

---

## Task 2: Packaging — Windows service scripts and INSTALL.md

**Files:**
- Create: `packaging/windows/install-service.ps1`
- Create: `packaging/windows/uninstall-service.ps1`
- Create: `packaging/INSTALL.md`

- [ ] **Step 1: Create the Windows install script**

Create `packaging/windows/install-service.ps1`. `$Source` defaults to two levels up from the script, which is the archive root where `nexspence.exe` and `config.yaml.example` live:

```powershell
#Requires -RunAsAdministrator
param(
    [string]$InstallDir = 'C:\Program Files\Nexspence',
    [string]$DataDir    = 'C:\ProgramData\Nexspence',
    [string]$Source     = (Join-Path $PSScriptRoot '..\..')
)
$ErrorActionPreference = 'Stop'

$ConfigPath = Join-Path $DataDir 'config.yaml'
$Exe        = Join-Path $InstallDir 'nexspence.exe'

# Install binary
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Copy-Item (Join-Path $Source 'nexspence.exe') $Exe -Force

# Data directories
New-Item -ItemType Directory -Force -Path (Join-Path $DataDir 'data') | Out-Null

# Seed config from the example if none exists yet
if (-not (Test-Path $ConfigPath)) {
    Copy-Item (Join-Path $Source 'config.yaml.example') $ConfigPath
    Write-Host "Created $ConfigPath - edit database.dsn, auth.jwt_secret, bootstrap.admin_password before starting."
}

# Register the service (manual start; user edits config first)
$bin = "`"$Exe`" serve --config `"$ConfigPath`""
sc.exe create nexspence binPath= $bin start= demand obj= "NT AUTHORITY\LocalService" DisplayName= "Nexspence" | Out-Null
sc.exe description nexspence "Nexspence artifact repository manager" | Out-Null

Write-Host ""
Write-Host "Service 'nexspence' registered. After editing $ConfigPath, run:"
Write-Host "    Start-Service nexspence"
```

- [ ] **Step 2: Create the Windows uninstall script**

Create `packaging/windows/uninstall-service.ps1`:

```powershell
#Requires -RunAsAdministrator
$ErrorActionPreference = 'Continue'
sc.exe stop nexspence | Out-Null
sc.exe delete nexspence | Out-Null
Write-Host "Service 'nexspence' removed. Data in C:\ProgramData\Nexspence was left in place."
```

- [ ] **Step 3: Create the archive INSTALL.md**

Create `packaging/INSTALL.md`:

````markdown
# Nexspence — Native Install

Nexspence needs an external **PostgreSQL** (13+). Provision it first, then create a
database and role:

```sql
CREATE DATABASE nexspence;
CREATE ROLE nexspence WITH LOGIN PASSWORD 'changeme';
GRANT ALL PRIVILEGES ON DATABASE nexspence TO nexspence;
```

Before starting the service, edit your `config.yaml` and set at minimum:
`database.dsn`, `auth.jwt_secret` (>= 32 chars), `bootstrap.admin_password`.
The server auto-migrates the schema on first start. Web UI: `http://localhost:8081`.

## Linux (.deb / .rpm)

Prefer the package — it installs the systemd unit and a `nexspence` user for you:

```bash
sudo dpkg -i nexspence_*.deb      # Debian/Ubuntu
sudo rpm -i  nexspence-*.rpm       # RHEL/Fedora/SUSE
sudo nano /etc/nexspence/config.yaml
sudo systemctl enable --now nexspence
```

Or from this archive (manual): copy `nexspence` to `/usr/bin`, `config.yaml.example`
to `/etc/nexspence/config.yaml`, and `service/nexspence.service` to
`/lib/systemd/system/`, then `systemctl daemon-reload`.

## macOS

```bash
sudo cp nexspence /usr/local/bin/
sudo xattr -dr com.apple.quarantine /usr/local/bin/nexspence   # unsigned binary
sudo mkdir -p /usr/local/etc/nexspence /usr/local/var/nexspence /usr/local/var/log
sudo cp config.yaml.example /usr/local/etc/nexspence/config.yaml
sudo nano /usr/local/etc/nexspence/config.yaml
sudo cp service/com.nexspence.server.plist /Library/LaunchDaemons/
sudo launchctl load -w /Library/LaunchDaemons/com.nexspence.server.plist
```

## Windows

Extract the zip, then from an **Administrator** PowerShell in the extracted folder:

```powershell
.\packaging\windows\install-service.ps1
notepad C:\ProgramData\Nexspence\config.yaml
Start-Service nexspence
```

The binary is unsigned — SmartScreen may warn on first run; choose **More info →
Run anyway**.
````

- [ ] **Step 4: Verify the PowerShell scripts parse**

Run (skip if `pwsh` is unavailable — they are validated in CI on a Windows runner only if added later; parse-check is best-effort locally):
```bash
command -v pwsh >/dev/null && pwsh -NoProfile -Command "[void][System.Management.Automation.Language.Parser]::ParseFile('packaging/windows/install-service.ps1',[ref]\$null,[ref]\$null); [void][System.Management.Automation.Language.Parser]::ParseFile('packaging/windows/uninstall-service.ps1',[ref]\$null,[ref]\$null); 'PS OK'" || echo "pwsh not installed - skipping parse check"
```
Expected: `PS OK` or the skip message.

- [ ] **Step 5: Commit**

```bash
git add packaging/windows packaging/INSTALL.md
git commit -m "build(packaging): windows service scripts + archive INSTALL.md"
```

---

## Task 3: GoReleaser config + Makefile snapshot target

**Files:**
- Create: `.goreleaser.yaml`
- Modify: `Makefile`

- [ ] **Step 1: Create `.goreleaser.yaml`**

Create `.goreleaser.yaml` at the repo root. `release.github` targets the public
`nexspence/nexspence` repo; `mode: replace` mirrors the old delete-then-create flow.
The Compose zip produced by the workflow's "Prepare release assets" step is attached
via `extra_files`:

```yaml
version: 2

project_name: nexspence

before:
  hooks:
    - go mod download
    - bash -c "cd frontend && npm ci && npm run build"

builds:
  - id: nexspence
    main: ./cmd/server
    binary: nexspence
    env:
      - CGO_ENABLED=0
    flags:
      - -trimpath
    ldflags:
      - -s -w -X main.Version={{ .Version }}
    goos:
      - linux
      - darwin
      - windows
    goarch:
      - amd64
      - arm64

archives:
  - id: default
    ids:
      - nexspence
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
    files:
      - config.yaml.example
      - packaging/INSTALL.md
      - packaging/systemd/nexspence.service
      - packaging/launchd/com.nexspence.server.plist
      - packaging/windows/install-service.ps1
      - packaging/windows/uninstall-service.ps1

nfpms:
  - id: packages
    package_name: nexspence
    ids:
      - nexspence
    vendor: Nexspence
    homepage: https://nexspence.com
    maintainer: Nexspence <noreply@nexspence.com>
    description: Free, open-source universal artifact repository manager.
    license: AGPL-3.0
    formats:
      - deb
      - rpm
    bindir: /usr/bin
    contents:
      - src: config.yaml.example
        dst: /etc/nexspence/config.yaml
        type: "config|noreplace"
      - src: packaging/systemd/nexspence.service
        dst: /lib/systemd/system/nexspence.service
    scripts:
      postinstall: packaging/nfpm/postinstall.sh
      preremove: packaging/nfpm/preremove.sh
      postremove: packaging/nfpm/postremove.sh

checksum:
  name_template: "checksums.txt"
  algorithm: sha256

changelog:
  disable: true

release:
  github:
    owner: nexspence
    name: nexspence
  mode: replace
  extra_files:
    - glob: ./release-assets/nexspence-*.zip
```

- [ ] **Step 2: Add a Makefile snapshot target**

In `Makefile`, under the `# ── Build ───` section (after the `docker-build` target), add:

```makefile
.PHONY: release-snapshot
release-snapshot: ## Local dry-run of the release artifacts (no publish)
	goreleaser release --snapshot --clean
```

- [ ] **Step 3: Install GoReleaser locally (if needed) and validate the config**

Run:
```bash
command -v goreleaser >/dev/null || go install github.com/goreleaser/goreleaser/v2@latest
goreleaser check
```
Expected: `goreleaser check` prints `1 configuration file(s) validated` (or
`config is valid`) with no errors. Fix any schema errors it reports before continuing.

- [ ] **Step 4: Build a local snapshot and inspect artifacts**

Run (requires Node + Go toolchains; the `before` hook builds the frontend):
```bash
goreleaser release --snapshot --clean
ls dist/
```
Expected: `dist/` contains 6 binary dirs (linux/darwin/windows × amd64/arm64),
`.tar.gz` + `.zip` archives, a `*.deb`, a `*.rpm`, and `checksums.txt`.

- [ ] **Step 5: Verify package contents**

Run (skip the tool you don't have; at least one of these works on most dev machines):
```bash
dpkg-deb -c dist/*.deb | grep -E 'usr/bin/nexspence|etc/nexspence/config.yaml|lib/systemd/system/nexspence.service'
rpm -qlp dist/*.rpm 2>/dev/null | grep -E '/usr/bin/nexspence|/etc/nexspence/config.yaml|/lib/systemd/system/nexspence.service' || true
```
Expected: all three paths appear in the `.deb` listing (and the `.rpm` listing if `rpm` is installed).

- [ ] **Step 6: Verify the snapshot binary reports a version**

Run:
```bash
./dist/nexspence_*_$(go env GOOS)_$(go env GOARCH)*/nexspence --help | head -5
```
Expected: cobra help text for `nexspence` (confirms the binary built and the embedded UI compiled without error).

- [ ] **Step 7: Commit**

```bash
git add .goreleaser.yaml Makefile
git commit -m "build: add GoReleaser config + make release-snapshot target"
```

---

## Task 4: Wire GoReleaser into the release workflow

**Files:**
- Modify: `.github/workflows/release.yml`

- [ ] **Step 1: Add Go + Node setup steps before the release**

In `.github/workflows/release.yml`, insert these steps **immediately after** the
`- name: Build release notes` step (so toolchains exist before GoReleaser's frontend
`before` hook runs):

```yaml
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Set up Node
        uses: actions/setup-node@v4
        with:
          node-version: '20'
          cache: npm
          cache-dependency-path: frontend/package-lock.json
```

- [ ] **Step 2: Replace the manual release step with GoReleaser**

In `.github/workflows/release.yml`, delete the entire `- name: Create GitHub Release`
step (the one running `gh release delete` then `gh release create`) and replace it with:

```yaml
      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          version: '~> v2'
          args: release --clean --release-notes=release-notes.md
        env:
          GITHUB_TOKEN: ${{ secrets.RELEASE_PAT }}
```

Leave the `- name: Sync docs and README to nexspence repo` and
`- name: Reset changelog for next release` steps unchanged — they still run after GoReleaser.

- [ ] **Step 3: Verify the workflow YAML is valid**

Run:
```bash
python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/release.yml')); print('YAML OK')"
```
Expected: `YAML OK`.

- [ ] **Step 4: Confirm the ordering invariant**

Run:
```bash
grep -nE 'Prepare release assets|Build release notes|Set up Go|Set up Node|Run GoReleaser|Create GitHub Release' .github/workflows/release.yml
```
Expected: `Prepare release assets` and `Build release notes` appear **before**
`Set up Go`/`Set up Node`/`Run GoReleaser`; there is **no** `Create GitHub Release`
line remaining. (The Compose zip from "Prepare release assets" must exist before
GoReleaser attaches it via `extra_files`.)

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: build native artifacts via GoReleaser in release workflow"
```

---

## Task 5: Native install + reverse-proxy + load-balancer guide

**Files:**
- Create: `docs/install-local.md`

- [ ] **Step 1: Create `docs/install-local.md`**

Create `docs/install-local.md`:

````markdown
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

Blob storage defaults to `/var/lib/nexspence` (the service `WorkingDirectory`); set
`storage.local.base_path` to an absolute path if you want it elsewhere.

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
````

- [ ] **Step 2: Verify nginx snippets are syntactically valid (best-effort)**

Run (skip if `nginx` isn't installed locally; CI/manual review covers it otherwise):
```bash
command -v nginx >/dev/null && echo "nginx present - manual config test recommended in a real server context" || echo "nginx not installed - skipping"
```
Expected: one of the two messages. (Full `nginx -t` requires a complete server context; the snippets are validated during manual review.)

- [ ] **Step 3: Verify internal consistency of the doc**

Run:
```bash
grep -nE '1024m|1800s|http://localhost:8081|database.dsn|auth.jwt_secret' docs/install-local.md | head
```
Expected: matches showing the body-size (`1024m`), timeout (`1800s`), and config-key references are present and consistent with `config.yaml.example`.

- [ ] **Step 4: Commit**

```bash
git add docs/install-local.md
git commit -m "docs: native install + reverse-proxy + load-balancer guide"
```

---

## Task 6: Cross-references — README, deployment.md, website, changelog

**Files:**
- Modify: `README.md`
- Modify: `docs/deployment.md`
- Modify: `website/docs/index.html`
- Modify: `NEXT_RELEASE.md`

- [ ] **Step 1: Add a native-install pointer to README.md**

In `README.md`, after the `### Docker Compose Profiles` section and **before**
`## CLI Tool — `nxs`` (around line 158), insert:

```markdown
### Native Install (no Docker)

Prefer running on bare metal? Download the `.deb`/`.rpm` (Linux) or the macOS/Windows
archive from the [latest release](https://github.com/nexspence/nexspence/releases/latest).
Each ships with systemd / launchd / Windows-service integration. Full walkthrough —
including reverse-proxy (nginx/Caddy) and multi-node load-balancer setups — in
[docs/install-local.md](docs/install-local.md). Requires an external PostgreSQL.

```

- [ ] **Step 2: Add a Native Install section to docs/deployment.md**

In `docs/deployment.md`, insert this section **immediately before** the
`## Kubernetes (Helm)` heading (around line 104):

```markdown
## Native Install (no Docker)

Run Nexspence directly on Linux (`.deb`/`.rpm`), macOS, or Windows with systemd /
launchd / Windows-service integration. Requires an external PostgreSQL. See the full
guide — including reverse-proxy and multi-node load-balancer configs — in
[install-local.md](install-local.md).

```

- [ ] **Step 3: Verify the README and deployment.md anchors landed correctly**

Run:
```bash
grep -nE 'Native Install' README.md docs/deployment.md
grep -nE 'install-local.md' README.md docs/deployment.md
```
Expected: a `Native Install` heading and an `install-local.md` link appear in both files.

- [ ] **Step 4: Add an inline Native Install section to the website docs**

The website docs are a single self-contained HTML file (`website/docs/index.html`,
~165 KB) with a multi-group sidebar and EN/RU i18n — content must be inline, not linked
out to GitHub (per the docs-self-contained rule). First read the file to learn its
section + sidebar + i18n markup pattern:

```bash
grep -nE 'id="(deployment|docker|kubernetes|install)' website/docs/index.html | head
```

Then add a new "Native Install" docs section that mirrors the prose of
`docs/install-local.md` (prerequisites, per-OS install, reverse proxy, load balancer,
Docker subdomain connector), following the exact section/sidebar/i18n structure already
used by the neighboring Deployment/Docker sections. Add the matching sidebar nav entry
and EN/RU strings the same way the existing sections do.

- [ ] **Step 5: Verify the website HTML still parses and the section is present**

Run:
```bash
python3 -c "import html.parser,sys
class P(html.parser.HTMLParser):
    pass
P().feed(open('website/docs/index.html',encoding='utf-8').read()); print('HTML parsed')"
grep -ciE 'native install' website/docs/index.html
```
Expected: `HTML parsed` and a count `>= 1` for the new section.

- [ ] **Step 6: Add the changelog entry**

In `NEXT_RELEASE.md`, under the `### ✨ Features` heading, add:

```markdown
- **Native installers** — Linux `.deb`/`.rpm`, macOS & Windows archives built by GoReleaser, with systemd / launchd / Windows-service integration. New [docs/install-local.md](docs/install-local.md) covers native setup plus reverse-proxy (nginx/Caddy) and multi-node load-balancer configs. Requires external PostgreSQL.
```

- [ ] **Step 7: Verify the changelog entry**

Run:
```bash
grep -nE 'Native installers' NEXT_RELEASE.md
```
Expected: one match under the Features heading.

- [ ] **Step 8: Commit**

```bash
git add README.md docs/deployment.md website/docs/index.html NEXT_RELEASE.md
git commit -m "docs: cross-link native install in README/deployment/website + changelog"
```

---

## Final verification

- [ ] **Step 1: Full repo build still works**

Run:
```bash
make build && ./bin/nexspence --help | head -3
```
Expected: frontend + backend build succeed; cobra help prints. (Confirms packaging
changes did not break the normal build.)

- [ ] **Step 2: Go tests still pass**

Run:
```bash
go test ./... 2>&1 | tail -20
```
Expected: all packages `ok` / no failures (no Go source was changed, so this is a regression guard).

- [ ] **Step 3: GoReleaser snapshot end-to-end**

Run:
```bash
goreleaser check && goreleaser release --snapshot --clean && ls dist/*.deb dist/*.rpm dist/checksums.txt
```
Expected: config valid; `dist/` holds the `.deb`, `.rpm`, archives for all 6
GOOS/GOARCH combos, and `checksums.txt`.

- [ ] **Step 4: Push the branch and open a PR**

```bash
git push -u origin feat/native-local-install
gh pr create --repo nexspence-core --title "Native local installation + cross-platform release artifacts" --body "Implements docs/superpowers/specs/2026-06-08-native-local-install-design.md"
```
(Per repo policy: branch + PR, never push to `main` directly. Adjust `--repo` to the
actual core repo slug; the `nxs`/`gh` availability caveat from project memory applies —
if `gh` is unavailable, open the PR via the web UI.)

---

## Notes for the implementer

- **No Go source changes.** The service runs `nexspence serve --config <path>`; `serve`
  already accepts `--config` and auto-migrates. If you find yourself editing
  `cmd/server` or `internal/`, stop — it's out of scope for this plan.
- **The frontend `before` hook is load-bearing.** Without `npm ci && npm run build`
  before `go build`, GoReleaser binaries embed a stale/empty UI. Don't remove it.
- **Release repo is `nexspence/nexspence`.** GoReleaser's `release.github` block and the
  workflow's `RELEASE_PAT` both target the public repo, built from `nexspence-core`.
- **Git-clean check.** A real `goreleaser release` (not `--snapshot`) refuses to run if
  the working tree is dirty. The frontend `before` hook writes the embedded UI build
  output — confirm that output directory is gitignored (it must already be, since
  `make build` doesn't dirty the repo). If CI fails with "git is currently in a dirty
  state", the fix is to gitignore the generated embed dir, **not** to add `--skip=validate`.
- **Deferred (do not implement here):** hosted apt/yum repos, Homebrew tap,
  winget/Chocolatey, code signing/notarization, a config search-path default. These are
  listed in the spec's follow-ups.
