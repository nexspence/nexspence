# Native Local Installation + Cross-Platform Release Artifacts — Design

**Date:** 2026-06-08
**Status:** Approved (design)

## Goal

Let users install and run Nexspence natively (without Docker) on Linux, macOS, and
Windows, with first-class OS service integration and operational docs for running
it behind a reverse proxy or load balancer.

Today Nexspence ships only as a Docker image + a zip of Compose files. This adds
native release artifacts and the docs to run them.

## Scope decisions (settled during brainstorming)

- **Database:** External PostgreSQL stays required. No bundled Postgres, no embedded
  SQLite. Packages document the dependency; install docs cover provisioning the DB.
- **Build tooling:** GoReleaser (single `.goreleaser.yaml`), integrated into the
  existing tag-triggered `release.yml`.
- **OS services:** systemd (Linux), launchd (macOS), Windows service — all three ship.
- **Reverse proxy / LB docs:** single-node reverse proxy (TLS), multi-node load
  balancer, and a Docker subdomain-connector note.

## Non-goals

- Bundling or auto-installing PostgreSQL.
- Embedded/alternative database backends (SQLite, etc.).
- Code signing / notarization of macOS and Windows binaries (artifacts ship
  unsigned; docs note the OS gatekeeper prompts and how to bypass for self-hosted use).
- Native package **repositories** (apt/yum repo hosting, Homebrew tap, winget/Chocolatey
  manifests). Out of scope for this iteration — users download artifacts from GitHub
  Releases. These are noted as possible follow-ups.
- Any change to the application's runtime architecture (handler→service→repository
  layers, auto-migrate on `serve`, embedded frontend) beyond what packaging requires.

---

## 1. Release pipeline — GoReleaser

### 1.1 `.goreleaser.yaml`

New file at repo root. Key configuration:

- **`before` hooks:** the frontend is embedded into the Go binary at build time, so
  before any `go build` GoReleaser must run:
  - `bash -c "cd frontend && npm ci && npm run build"`
  - `go mod download`
- **`builds`:** one build entry.
  - `main: ./cmd/server`
  - `binary: nexspence`
  - `env: [CGO_ENABLED=0]`
  - `ldflags: -s -w -X main.Version={{.Version}}` (matches the Makefile's
    `LDFLAGS`; `main.Version` is the existing variable).
  - `goos: [linux, darwin, windows]`
  - `goarch: [amd64, arm64]`
- **`archives`:** `tar.gz` for linux/darwin, `zip` for windows
  (`format_overrides`). Each archive includes, alongside the binary:
    - `config.yaml.example`
    - the relevant OS service file(s) (see §2)
    - a short `INSTALL.md` (per-OS quickstart, generated/committed under
      `packaging/`)
- **`nfpms`:** one entry producing both `deb` and `rpm`.
  - `package_name: nexspence`
  - `bindir: /usr/bin`
  - `contents:`
    - `config.yaml.example` → `/etc/nexspence/config.yaml` with
      `type: "config|noreplace"` (don't clobber user edits on upgrade)
    - systemd unit → `/lib/systemd/system/nexspence.service`
  - `scripts.postinstall` / `scripts.preremove` / `scripts.postremove`:
    point at scriptlets under `packaging/nfpm/` (see §2.1).
  - `dependencies`: none hard-required (Postgres is remote); install docs state the
    PostgreSQL requirement. (We intentionally do **not** declare a hard `postgresql`
    package dependency, because the DB is typically on another host.)
- **`checksums`:** `checksums.txt`, SHA256.
- **`release`:** GoReleaser owns GitHub-release creation.
  - Notes sourced from `NEXT_RELEASE.md` (passed via `--release-notes` on the CLI,
    so the existing "append Docker section" logic can prepend/append as today).
  - `extra_files`: the existing `nexspence-<tag>.zip` (Compose files + config +
    deploy/keycloak/scripts/docs), so the Docker-based quickstart artifact is still
    attached.

### 1.2 `release.yml` integration

The workflow keeps its current shape; ordering becomes:

1. **Docker job (unchanged):** buildx multi-arch → GHCR, as today.
2. **Build the Compose zip** (the existing "Prepare release assets" step) — kept,
   because GoReleaser attaches it as an `extra_file`.
3. **GoReleaser step:** `goreleaser/goreleaser-action@v6`, `args: release --clean`,
   `GITHUB_TOKEN: ${{ secrets.RELEASE_PAT }}`. Replaces the manual
   `gh release create`/`gh release delete` step (GoReleaser creates the release and
   uploads all binaries, packages, checksums, plus the Compose zip extra-file).
   - Release notes: build the combined notes file first (existing "Build release
     notes" step writes `release-notes.md`), then pass `--release-notes release-notes.md`.
4. **Docs-sync + changelog-reset steps (unchanged):** still run after the release.

Node + Go toolchains must be set up in the release job (the frontend `before` hook
needs Node; today the job only uses Docker). Add `actions/setup-go` and
`actions/setup-node` steps before GoReleaser.

### 1.3 Makefile (optional convenience)

Add `make release-snapshot` → `goreleaser release --snapshot --clean` for local
dry-runs. Non-blocking; nice for testing the config.

---

## 2. Install layout & OS service integration

FHS-conventional paths; dedicated unprivileged user where the OS supports it. The
service in every case runs `nexspence serve --config <path>` — **no application code
change is required**: `serve` already accepts `--config` and auto-migrates on start.

| | Linux (deb/rpm) | macOS | Windows |
|---|---|---|---|
| Binary | `/usr/bin/nexspence` | `/usr/local/bin/nexspence` | `C:\Program Files\Nexspence\nexspence.exe` |
| Config | `/etc/nexspence/config.yaml` | `/usr/local/etc/nexspence/config.yaml` | `C:\ProgramData\Nexspence\config.yaml` |
| Data (blobs) | `/var/lib/nexspence` | `/usr/local/var/nexspence` | `C:\ProgramData\Nexspence\data` |
| Service unit | `/lib/systemd/system/nexspence.service` | `/Library/LaunchDaemons/com.nexspence.server.plist` | `nexspence` Windows service |
| Runs as | `nexspence` system user | `root` daemon (or per-user agent variant) | `NT AUTHORITY\LocalService` |
| Logs | journald (`journalctl -u nexspence`) | `/usr/local/var/log/nexspence.{out,err}.log` | Windows Event Log + file |

Packaging source files live under a new `packaging/` directory:

```
packaging/
├── systemd/nexspence.service
├── launchd/com.nexspence.server.plist
├── windows/install-service.ps1      # registers/starts via sc.exe (or NSSM if bundled)
├── windows/uninstall-service.ps1
├── nfpm/postinstall.sh              # create user, mkdir data dir, set perms, daemon-reload
├── nfpm/preremove.sh                # stop + disable service
├── nfpm/postremove.sh               # daemon-reload; (purge note: leave data + config)
└── INSTALL.md                       # per-OS quickstart (shipped in archives)
```

### 2.1 Linux (systemd via nfpm scriptlets)

- **`nexspence.service`:** `Type=simple`, `ExecStart=/usr/bin/nexspence serve
  --config /etc/nexspence/config.yaml`, `User=nexspence`, `Group=nexspence`,
  `Restart=on-failure`, `WorkingDirectory=/var/lib/nexspence`, basic hardening
  (`NoNewPrivileges`, `ProtectSystem=full`, `ReadWritePaths=/var/lib/nexspence`).
  `WantedBy=multi-user.target`.
- **`postinstall.sh`:** create `nexspence` system user/group if missing;
  `mkdir -p /var/lib/nexspence` owned by `nexspence:nexspence` (mode `0750`, matching
  the LocalBlobStore `0o750` convention); `systemctl daemon-reload`.
  **Does not auto-enable/start** — prints next-steps (set DB DSN, JWT secret, admin
  password in `/etc/nexspence/config.yaml`, then `systemctl enable --now nexspence`).
- **`preremove.sh`:** `systemctl disable --now nexspence` (guard on `systemctl`
  existing / service being active).
- **`postremove.sh`:** `systemctl daemon-reload`; leave `/var/lib/nexspence` and
  `/etc/nexspence/config.yaml` in place (data is precious). Document manual purge.

### 2.2 macOS (launchd)

- **`com.nexspence.server.plist`:** LaunchDaemon with `ProgramArguments`
  = binary + `serve --config …`, `RunAtLoad=true`, `KeepAlive=true`, stdout/stderr
  redirected to log files under `/usr/local/var/log/`.
- Install is manual (the archive ships the plist + `INSTALL.md`): copy binary to
  `/usr/local/bin`, config to `/usr/local/etc/nexspence/`, plist to
  `/Library/LaunchDaemons/`, then `sudo launchctl load -w <plist>`.
- Note Gatekeeper quarantine: `xattr -dr com.apple.quarantine <binary>` for the
  unsigned binary.

### 2.3 Windows (service)

- **`install-service.ps1`:** registers a service named `nexspence` running
  `nexspence.exe serve --config C:\ProgramData\Nexspence\config.yaml`. Primary path
  uses native `sc.exe create … binPath=` (the Go binary handles being run directly;
  a tiny wrapper is unnecessary for `Type=simple`-style always-on processes — if
  Windows SCM control proves flaky we fall back to bundling NSSM, noted in the script).
  Creates `C:\ProgramData\Nexspence\{data}` and copies the example config if none
  exists. Does not auto-start until config is edited.
- **`uninstall-service.ps1`:** `sc.exe stop` + `sc.exe delete`.
- Note SmartScreen prompt for the unsigned `.exe`.

---

## 3. Documentation

### 3.1 New `docs/install-local.md`

Sections:

1. **Overview** — when to use native vs Docker; the external-Postgres requirement.
2. **Prerequisites** — PostgreSQL 13+ reachable; create DB + role
   (`CREATE DATABASE nexspence; CREATE ROLE nexspence …`); the `dsn` config line.
3. **Linux (deb/rpm)** — download from Releases, `dpkg -i` / `rpm -i` / `dnf install`,
   edit `/etc/nexspence/config.yaml` (DSN, `auth.jwt_secret`, bootstrap admin
   password), `systemctl enable --now nexspence`, verify (`curl localhost:8081`,
   `journalctl -u nexspence`).
4. **macOS** — archive extract, place files, `launchctl load`, Gatekeeper note.
5. **Windows** — zip extract, `install-service.ps1`, edit config, `Start-Service`,
   SmartScreen note.
6. **Reverse proxy (single node, TLS)** — nginx and Caddy configs tuned for artifact
   transfer:
   - nginx: `client_max_body_size 0;` (or large, matching `http.max_body_mb`),
     `proxy_request_buffering off;`, `proxy_buffering off;`, long
     `proxy_read_timeout`/`proxy_send_timeout` (matching the 1800s server timeouts),
     `proxy_set_header` for Host/X-Forwarded-*.
   - Caddy: equivalent `reverse_proxy` block with `flush_interval -1` and request
     body limits raised; automatic TLS note.
   - Note: set `http.base_url` to the public HTTPS URL.
7. **Load balancer (multi-node)** — 2+ native instances on separate hosts sharing
   **one** PostgreSQL and **one** blob store (shared filesystem mount or S3 — local
   per-node disk will **not** work). nginx `upstream` round-robin and an HAProxy
   `backend` example. Mirrors `docker-compose.ha.yml` topology natively. Notes:
   - JWT auth is stateless → no session stickiness required for the API/artifacts.
   - The UI SPA is served from the binary; no sticky sessions needed.
   - Each node runs its own systemd/launchd/Windows service pointed at the shared DB.
8. **Docker subdomain connector** — the proxy + wildcard-DNS config needed for the
   existing `SubdomainRewriter` feature on a native install (wildcard cert,
   `*.registry.example.com` → the nexspence upstream, relevant `docker.subdomain_connector.*`
   config keys).

### 3.2 Cross-references and website

- `README.md`: add a short "Native install" pointer to `docs/install-local.md`
  alongside the existing Docker quickstart.
- `docs/deployment.md`: add a "Native (no Docker)" subsection linking to the new doc.
- **Website:** add an inline, self-contained native-install section to `website/`
  docs (per the docs-self-contained rule — no links out to GitHub markdown; website
  and repo updated together). The `release.yml` docs-sync step already rsyncs `docs/`
  to the public repo.

### 3.3 Changelog

- `NEXT_RELEASE.md`: add a Features entry — "Native installers: Linux .deb/.rpm,
  macOS, Windows archives with systemd/launchd/Windows-service integration;
  `docs/install-local.md` reverse-proxy + load-balancer guide."

---

## 4. Verification / success criteria

- `goreleaser release --snapshot --clean` runs locally and produces, under `dist/`:
  binaries for all 6 GOOS/GOARCH combos, a `.deb`, a `.rpm`, `.tar.gz` + `.zip`
  archives, and `checksums.txt`.
- `dpkg-deb -c` / `rpm -qlp` on the built packages show the binary, config at
  `/etc/nexspence/config.yaml`, and the systemd unit at the expected paths.
- Installing the `.deb`/`.rpm` in a Linux VM/container: package installs, creates the
  `nexspence` user + `/var/lib/nexspence`, and (after editing config + pointing at a
  Postgres) `systemctl enable --now nexspence` brings up a working server reachable on
  `:8081` that auto-migrates the DB.
- The tag-triggered `release.yml` still builds + pushes the Docker image, attaches the
  Compose zip, and now also attaches all native artifacts to the same GitHub release.
- `docs/install-local.md` renders correctly and its nginx/Caddy/HAProxy snippets are
  syntactically valid (config lint where practical).

## 5. Open follow-ups (explicitly deferred)

- Hosted apt/yum repositories, Homebrew tap, winget/Chocolatey manifests.
- macOS notarization / Windows Authenticode signing.
- An optional `nexspence` config search-path default (so the service files need no
  `--config` flag) — minor; current explicit `--config` is fine.
