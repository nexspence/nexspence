# Docs Page Redesign — Design Spec

**Date:** 2026-06-09
**Branch:** `feat/docs-page-redesign`
**Scope:** `website/docs/index.html` (single self-contained file) — restructure the documentation page IA, add OS service-setup docs, and promote the Terraform Provider to its own tab.
**Status:** Approved (mockups validated in the visual companion).

## 1. Problem

The current docs page has several illogical spots:

1. **Version selector is a pill row**, and the changelog is just one sidebar item among many — there is no clear "pick a version → see what changed → then read docs" flow.
2. **Terraform Provider is buried** as the last item of the *Advanced* sidebar group, even though it is a distinct, first-class product (an IaC provider, separate repo, published to the Terraform Registry).
3. **No OS service-setup docs.** The Installation section never explains how to run Nexspence as a managed service (systemd / launchd / Windows Service) on different operating systems.

## 2. Goals

- Reframe the page around the flow: **version dropdown → Changelog (default) → top tabs → docs**.
- Promote **Terraform Provider** to its own top-level tab, aligned in the same tab row (not floated to the far right).
- Add **OS-specific "run as a service"** docs to Installation: Linux (systemd), macOS (launchd), Windows (Windows Service via NSSM / `sc.exe`).
- Add **"in vX.Y" feature badges**, but **only on post-1.0 features** (no badge noise on everything that has existed since v1.0.0).
- Preserve everything that already works: EN/RU i18n, the GitHub-API changelog fetch (`nx_releases_v2` cache), the existing per-section content templates, mobile nav.

## 3. Non-Goals (YAGNI)

- **No per-version documentation snapshots.** Docs remain a single source of truth. The version dropdown drives the **changelog only**; docs are "current".
- No new build tooling — the page stays a hand-written, self-contained HTML file with inline `<style>`/`<script>`.
- No backend changes.

## 4. Decisions

### 4.1 Versioning model — Hybrid (changelog-scoped + inline badges)
The version dropdown controls **changelog content only**. Docs are unified, but each article shows an **"in vX.Y"** badge next to its heading **when the feature is post-1.0**. Features present since v1.0.0 get **no badge**.

### 4.2 Information architecture — top category tabs
Replace the always-on sidebar-as-primary-nav with **horizontal top tabs**, left-aligned in a single row:

| Tab | Source group | Sections (left sub-nav inside the tab) |
|-----|--------------|----------------------------------------|
| **Changelog** | `releases` | (full-width, version-driven; default tab) |
| **Getting Started** | `getting-started` | Quick Start, Installation, Native Install |
| **Using** | `using` | Repositories, Format Setup Guides, Browse & Search |
| **Administration** | `admin` | Users & API Tokens, Roles & Privileges, Cleanup Policies, Webhooks |
| **Advanced** | `advanced` | Migration from Nexus, Monitoring, Build Promotion, Content Replication |
| **API** | `reference` | Formats, REST Reference |
| **Terraform Provider** | (pulled out of `advanced`) | Overview, Authentication, Resources, Data Sources, Examples |

- Clicking a non-Changelog tab shows a **contextual left sub-nav** (the group's items) + content (the existing `tpl-*` templates).
- **Terraform** is removed from the `advanced` group and becomes its own tab — `margin-left:auto` is **not** used (tabs stay in one aligned row).

### 4.3 Version dropdown
Replaces `.ver-bar` pills with a `.ver-dd` dropdown:
- Button: `Version: vX.Y.Z [latest]  ⌄`.
- Menu: each release as `version + tag (latest/minor/patch) + date`, plus an "All releases on GitHub →" footer link.
- Selecting a version updates the Changelog pane. Reuses existing `fetchReleases()` + `parseReleaseBody()` + `nx_releases_v2` cache — only the selector UI changes.

### 4.4 Installation — method tabs + OS sub-tabs + service setup
Within Installation:
- **Method tabs**: Docker Compose · Helm / Kubernetes · Native binary (existing `tpl-install` Docker/Helm content is preserved).
- **Native binary** gets **OS sub-tabs**: Linux (deb/rpm/tar.gz) · macOS (tar.gz) · Windows (zip).
- Each OS adds a **"Run as a service"** block, verified against the real binary (`nexspence serve --config <path>`, env prefix `NEXSPENCE_`):
  - **Linux** — a `systemd` unit at `/etc/systemd/system/nexspence.service` + `systemctl daemon-reload && systemctl enable --now nexspence` + `journalctl -u nexspence -f`.
  - **macOS** — a `launchd` plist at `/Library/LaunchDaemons/com.nexspence.server.plist` + `launchctl load -w`.
  - **Windows** — Windows Service via **NSSM** (recommended) and built-in `sc.exe` alternative, with a note that a bare console binary is not a real service.
- Implementation lands in the `tpl-native-install` template (or a consolidated Installation template).

### 4.5 Terraform Provider tab content
The existing `tpl-terraform` content is moved into the new tab and expanded to sub-sections:
- **Overview** — `required_providers` + `provider "nexspence"` block, a first resource, normal `terraform init` install. Header badge: `Provider v0.2.0` + `on Terraform Registry` (it is **published**, not pre-release).
- **Authentication** — token (`nxs_*`, preferred) vs username+password; attribute/env table (`NEXSPENCE_URL/TOKEN/USERNAME/PASSWORD`).
- **Resources (10)** — `nexspence_repository`, `_blobstore`, `_content_selector`, `_privilege`, `_role`, `_user`, `_cleanup_policy`, `_routing_rule`, `_webhook`, `_promotion_rule` + a worked RBAC example.
- **Data Sources (2)** — `nexspence_repository`, `nexspence_repositories` + an example with `output`.
- **Examples & local development** — pointer to `deploy/terraform-example/` and a `~/.terraformrc` dev-override for developing the provider itself.

## 5. Verified feature → release map (badge source)

Badges are written **only** for post-1.0 features. The verified map (from git tags):

| Section | Badge |
|---------|-------|
| Quick Start, Repositories, Browse & Search, Users & API Tokens, RBAC, Cleanup, Webhooks, Migration, base 12 formats, REST API | *(v1.0.0 — no badge)* |
| Installation → Native install (cross-platform artifacts) | `in v1.13.0` |
| Format Setup Guides → Conda + Terraform formats | `in v1.7.0` |
| RBAC → Access Map | `in v1.6.0` (sub-feature note only) |
| Content Replication | `in v1.3.0` |
| Build Promotion | `in v1.8.1` |
| Monitoring (Prometheus `/metrics` + Grafana) | `in v1.9.0` |
| Terraform Provider | `Provider v0.2.0` |

Release dates: v1.0.0 (2026-04-30), v1.3.0 (2026-05-06), v1.6.0 (2026-05-07), v1.7.0 (2026-05-08), v1.8.1 (2026-05-15), v1.9.0 (2026-05-26), v1.13.0 (2026-06-08).

## 6. Implementation Notes

- All work is in `website/docs/index.html`. Reuse existing CSS tokens (`--bg`, `--blue`, `--purple`, `--glass`, …) and component classes (`.cb`, `.doc-table`, `.step-item`, `.inst-ptab`, `.inst-vchip`, `.alert-info`, `.dsb-link`).
- New CSS: `.ver-dd` / `.ver-menu` (dropdown), `.doc-tabs` / `.doc-tab` (top tabs), `.dsb-badge` + `.vbadge-inline` (feature badges), `.os-row` / `.os-chip` / `.os-pnl` (OS sub-tabs).
- JS: convert `SIDEBAR_GROUPS` into the tab model; render the active tab's items as the contextual sub-nav. Keep `SECTIONS` + `tpl-*` templates. Pull `terraform` out of the `advanced` group into a standalone tab entry. Replace `.ver-bar` rendering with the dropdown.
- **i18n parity is mandatory**: every new string gets both `en` and `ru` entries in `TRANSLATIONS` (tab labels, OS tab labels, service-block captions, Terraform sub-section headings). Follow the existing `data-i18n` pattern.
- **Mobile**: the existing mobile nav drawer / bottom nav must keep working — top tabs collapse into the mobile drawer the same way the sidebar groups do today.
- **Accuracy**: keep the service-config examples matching the real binary (`nexspence`, `serve`, `-c/--config`, `NEXSPENCE_*` env, artifacts `nexspence_<ver>_<os>_<arch>` tar.gz/zip + deb/rpm).

## 7. Verification

- Visual: page matches the approved mockups (`.superpowers/brainstorm/.../docs-ia-v2.html`, `install-os-tabs.html`, `terraform-tab.html`).
- Tabs render in one aligned row; Terraform Provider is a top tab, no right-side gap.
- Version dropdown opens, lists versions, and switches the Changelog pane.
- Badges appear only on post-1.0 sections; v1.0.0 sections have none.
- Installation → Native → each OS shows its service block with correct commands.
- Terraform tab: all 5 sub-sections render; resource/data-source counts (10 / 2) correct.
- EN ⇄ RU switch leaves no untranslated strings.
- No broken internal links / breadcrumbs; deep-link behavior (if any) preserved.

## 8. Out of scope / follow-ups

- Live-fetching the provider version badge from the Terraform Registry (currently static `v0.2.0`) — optional later.
- Any change to the marketing landing (`website/index.html`).
