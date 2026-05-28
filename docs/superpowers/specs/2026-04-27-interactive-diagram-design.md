# Interactive Architecture Diagram — Design Spec

**Date:** 2026-04-27  
**File:** `docs/architecture-diagram.html` (replaces existing static diagram)  
**Format:** Single self-contained HTML/CSS/JS file — no external dependencies, no npm

---

## Overview

A fully interactive animated architecture diagram of Nexspence. The user selects an action from a sidebar list; a signal animates through the service graph showing exactly which components are involved, in what order, and with what latency. Services (PostgreSQL, LDAP, OIDC, BlobStore, Upstream, Webhook receiver, Trivy) can be toggled off at runtime to show failure paths.

---

## Layout

Three-column layout inside a single viewport:

```
┌─────────────────────────────────────────────────────────────────────┐
│  HEADER: Nexspence logo + title + "Interactive" badge               │
├──────────────┬───────────────────────────────────┬──────────────────┤
│  LEFT 240px  │  CENTER (flex, min 500px)          │  RIGHT 280px     │
│  Control     │  SVG Node Graph                    │  Step Trace      │
│  Panel       │                                    │  Panel           │
└──────────────┴───────────────────────────────────┴──────────────────┘
```

**Left panel:** scrollable, two sections — Actions (12 items) and Services (7 toggles).  
**Center:** SVG canvas, scales with viewport width.  
**Right panel:** step-by-step trace with timestamps, fades in row-by-row as animation plays. Clears when a new action is selected.

---

## Node Graph

### Node layers (top → bottom)

| Layer | Nodes |
|-------|-------|
| **Clients** | Browser/UI · Maven CLI · npm · Docker CLI · pip · Cargo · Helm |
| **HTTP** | Gin API Layer (single node; hover shows middleware chain tooltip: Recovery→Logger→CORS→Metrics→Audit→Auth→RBAC) |
| **Handlers / Services** | Format Handlers · UserService · RepositoryService · TokenService · WebhookService · ScanService |
| **Repository** | ComponentRepo · AssetRepo · UserRepo · AuditRepo |
| **Data** | PostgreSQL · BlobStore |
| **External** | LDAP · OIDC Provider · Upstream Proxy · Webhook Receiver · Trivy |

### Edges

Named edge objects (e.g. `edges.browser_gin`, `edges.userservice_ldap`, `edges.gin_format`). Each edge is an SVG `<line>` with an optional label. Edges are referenced by name in action step definitions.

### Positioning

Node positions are explicit `{ x, y }` percentage coordinates within the SVG viewBox (`0 0 100 100`), hardcoded in the JS node definitions and converted to pixels via `ResizeObserver`. Nodes are arranged in loose horizontal layers (not a strict grid) — each layer has 1–7 nodes spread across the x-axis, with ~18–20 units of vertical separation between layers.

---

## Action Definitions

Each action is a JS object:

```js
{
  id: 'login_ldap',
  label: 'Логин — LDAP',
  icon: '🔐',
  dependsOn: ['ldap', 'postgres'],   // services that must be up for happy path
  steps: [
    { from: 'browser',      to: 'gin',         label: 'POST /login',        ms: 1,    status: 'ok' },
    { from: 'gin',          to: 'userService', label: 'AuthHandler',        ms: 1,    status: 'ok' },
    { from: 'userService',  to: 'ldap',        label: 'ldap.Authenticate()',ms: 12,   status: 'ok' },
    { from: 'ldap',         to: 'userService', label: '→ DN + groups',      ms: 8,    status: 'ok', direction: 'back' },
    { from: 'userService',  to: 'postgres',    label: 'upsert user + roles',ms: 3,    status: 'ok' },
    { from: 'userService',  to: 'gin',         label: 'GenerateToken()',    ms: 1,    status: 'ok', direction: 'back' },
    { from: 'gin',          to: 'browser',     label: '200 + JWT',          ms: 1,    status: 'ok', direction: 'back' },
  ],
  failureSteps: {
    ldap: [
      { from: 'browser',     to: 'gin',        label: 'POST /login',        ms: 1,    status: 'ok' },
      { from: 'gin',         to: 'userService',label: 'AuthHandler',        ms: 1,    status: 'ok' },
      { from: 'userService', to: 'ldap',       label: 'ldap.Authenticate()',ms: 5000, status: 'error', detail: 'timeout' },
      { from: 'gin',         to: 'browser',    label: '401 Unauthorized',   ms: 1,    status: 'error', direction: 'back' },
    ],
    postgres: [ /* similar — user upsert fails */ ],
  }
}
```

### Full action list (12)

1. **Логин — пароль** (`POST /login` → bcrypt → JWT)
2. **Логин — LDAP** (→ `ldap.Authenticate` → upsert → JWT)
3. **Логин — OIDC/SSO** (Auth Code + PKCE → `/oidc/callback` → JWT via fragment)
4. **Загрузка артефакта** (`PUT /repository/...` → Format Handler → BlobStore + Postgres)
5. **Скачивание артефакта** (`GET /repository/...` → hosted → BlobStore → stream)
6. **Proxy cache miss** (`GET /repository/...` → proxy → upstream fetch → cache in BlobStore → stream)
7. **Docker pull** (`GET /v2/` auth challenge → manifest → layer blobs)
8. **API Token** (create via `POST /api/v1/auth/tokens` → use as `Bearer nxs_…`)
9. **Webhook delivery** (`artifact.published` → WebhookService.Dispatch → HTTP POST to receiver)
10. **Cleanup Policy run** (scheduler → CleanupService → asset scan → BlobStore delete + Postgres delete)
11. **Поиск компонентов** (`GET /service/rest/v1/search` → Postgres full-text tsvector)
12. **Vulnerability scan** (`POST /api/v1/components/:id/scan` → Trivy → component.extra update)

---

## Animation Engine

### Per-step sequence

Steps execute sequentially with a configurable inter-step delay (default: 600ms).

For each step:
1. Source node gets `state: active` → CSS ring pulse animation + blue glow border
2. SVG `<circle>` travels along the edge path using `<animateMotion>` (duration = step `ms` clamped to 200–1200ms for readability)
3. Target node activates
4. A row fades into the right trace panel: `[icon] [label] [+Xms]`
5. On `status: error` — dot is red, target node goes red with `UNREACHABLE` badge, trace row is red

### Return path

Steps with `direction: 'back'` send the dot in reverse along the same edge. Response dots are slightly smaller and use a dimmer color (ok=green, error=red).

### Replay

Clicking the same action while it's running restarts it from step 1. A "⏸ Running…" indicator shows in the action list during playback.

---

## Service Toggle Behaviour

Toggles live in the left panel under a "Сервисы" section. Each has:
- Color dot (green = up, red = down)
- Service label + click to toggle

When a service is set to **DOWN**:
- Its graph node immediately changes to the `error` visual state (red border, ✕ badge, `UNREACHABLE` label)
- Edges connected to it dim to red
- If the active action `dependsOn` this service → `failureSteps[service]` path is used on next play. If multiple `dependsOn` services are down simultaneously, the first one listed in `dependsOn` that is currently down takes precedence.
- Actions that don't touch this service play normally

Services: **PostgreSQL · LDAP · OIDC Provider · BlobStore · Upstream Proxy · Webhook Receiver · Trivy**

---

## Visual Style

Follows the VMSManager K3S dark glassmorphism theme from the existing `architecture-diagram.html`:

| Token | Value |
|-------|-------|
| Background | `#070b14` + radial blue/purple gradients |
| Glass cards | `rgba(255,255,255,0.04)` + `backdrop-filter: blur(12px)` |
| Primary blue | `#3b82f6` |
| OK green | `#22c55e` |
| Error red | `#ef4444` |
| Warning amber | `#f59e0b` |
| Border | `rgba(255,255,255,0.07)` normal · `rgba(59,130,246,0.35)` active |
| Border radius | 14px cards, 8px elements |

Node active state: `box-shadow: 0 0 0 3px rgba(59,130,246,0.5)` + pulsing ring keyframe.  
Node error state: `box-shadow: 0 0 12px rgba(239,68,68,0.4)` + red border.

---

## Implementation Notes

- **No frameworks** — vanilla JS, no npm, no bundler
- **State object**: `const state = { action: null, services: { postgres: true, ldap: true, ... }, running: false }`
- **Graph drawn programmatically**: nodes and edges are JS objects; SVG elements created via `document.createElementNS`
- **Positions**: `{ x: 50, y: 15 }` as percentage of SVG viewBox (`0 0 100 100`), converted to px on resize via `ResizeObserver`
- **animateMotion path**: computed from source/target node center coordinates at render time
- **Trace panel**: `<div>` rows appended with `requestAnimationFrame`-scheduled fade-in, cleared on action change

---

## File

Replaces `docs/architecture-diagram.html` in-place. The existing static diagram (686 lines) is discarded and rewritten from scratch.
