# Holographic Depth — UI Kit for self_nexus

A complete component kit implementing the **V5 Holographic Depth** direction. Drop into your existing React/Vite/TypeScript frontend without breaking any logic.

## What's included

| File          | Purpose                                                                 |
| ------------- | ----------------------------------------------------------------------- |
| `holo.css`    | Design tokens (colors, radii, gradients), shell, all base styles        |
| `holo.tsx`    | React components: `HoloApp`, `HoloCard`, `TiltCard`, `HoloButton`, etc. |
| `index.ts`    | Barrel export                                                           |
| `README.md`   | This file — install + migration instructions for Claude Code            |

## Visual language

- **Palette**: deep violet-black `#080612` background, holographic gradient `#7c5cff → #22d3ee → #ff5cf0` for accents.
- **Surfaces**: glass cards with backdrop-blur 20px, hairline gradient edges.
- **Motion**: `holoShift` 5s gradient sweep on accents; 3D `perspective+rotateX/Y` tilt on hover; cubic-bezier `(0.2, 0.9, 0.2, 1.2)`.
- **Type**: Geist (display) + Geist Mono (numbers/labels). Tabular-nums for all numeric data.
- **Background**: three radial gradient blobs + 2-px scanline overlay (built into `.holo-app::before/::after`).

---

# Instructions for Claude Code

> **Mission**: Apply the Holographic Depth UI to the existing self_nexus React frontend (Vite + TypeScript + react-router-dom + @tanstack/react-query + Zustand authStore + lucide-react). Replace the visual layer **only** — preserve all routing, queries, mutations, auth flow, and API contracts.

## Step 1 — Install the kit

1. Copy this folder to `frontend/src/components/holo/`.
2. In `frontend/src/main.tsx`, add the CSS import at the top of the imports:
   ```ts
   import './components/holo/holo.css';
   ```

## Step 2 — Wrap the app shell

In `frontend/src/components/Layout.tsx`, replace the outer `<div className={styles.root}>` with:

```tsx
import { HoloApp } from '@/components/holo';

return (
  <HoloApp>
    <div className={styles.root}>
      {/* existing aside + main */}
    </div>
  </HoloApp>
);
```

This adds the holographic background gradient + scanlines under everything. Existing CSS modules still work — they sit on top.

## Step 3 — Update Layout.module.css

Replace background colors / borders to use Holo tokens:

```css
.root        { background: transparent; color: var(--holo-text); }
.sidebar     { background: rgba(8,6,18,0.6); backdrop-filter: blur(30px); border-right: 1px solid rgba(124,92,255,0.15); }
.brand       { /* unchanged structure, just colors */ }
.navBtn      { color: var(--holo-text-dim); border-radius: 9px; transition: background 0.15s, color 0.15s; }
.navBtn:hover{ background: rgba(255,255,255,0.04); color: var(--holo-text); }
.navBtn.active {
  color: var(--holo-text);
  background: linear-gradient(90deg, rgba(124,92,255,0.18), rgba(34,211,238,0.06));
  border: 1px solid rgba(124,92,255,0.30);
}
.userInfo    { /* same structure */ }
.divider     { border-color: rgba(124,92,255,0.15); }
.danger      { color: var(--holo-red); }
.version     { color: var(--holo-text-faint); }
```

The existing brand `<img src={logo}>` stays — palette adapts around it.

## Step 4 — Replace inline button styles

Anywhere you see:

```tsx
<button style={S.btn('primary')}>Create</button>
<button style={S.btn('ghost')}>Cancel</button>
<button style={S.btn('danger')}>Delete</button>
```

Substitute:

```tsx
import { HoloButton } from '@/components/holo';

<HoloButton variant="primary">Create</HoloButton>
<HoloButton>Cancel</HoloButton>
<HoloButton variant="danger">Delete</HoloButton>
```

`HoloButton` accepts an `icon` prop for lucide icons:

```tsx
<HoloButton variant="primary" icon={<Plus size={14} />}>New repository</HoloButton>
```

## Step 5 — Replace `S.modal` / `S.overlay` patterns

In `Layout.tsx`'s `ProfileModal` and similar:

```tsx
import { HoloModal } from '@/components/holo';

<HoloModal open={profileOpen} onClose={() => setProfileOpen(false)}>
  {/* existing children */}
</HoloModal>
```

The visible modal chrome (overlay + container + top gradient edge) is built in.

## Step 6 — Wrap top-level page cards in TiltCard (the hero feature)

This is the signature visual. On `RepositoriesPage`, `BrowsePage` and dashboard-like surfaces, wrap each repo / artifact card:

```tsx
import { TiltCard, HoloCard, HoloPill, CountUp } from '@/components/holo';

{repos.map(r => (
  <TiltCard key={r.id} intensity={10} style={{ borderRadius: 14 }}>
    <HoloCard edge style={{ padding: 18 }}>
      <h3 style={{ fontSize: 15, fontWeight: 600, margin: 0 }}>{r.name}</h3>
      <div className="holo-mono" style={{ fontSize: 11, color: 'var(--holo-text-dim)' }}>{r.format}</div>
      <HoloPill tone={r.healthy ? 'success' : 'warn'}>{r.healthy ? 'healthy' : 'degraded'}</HoloPill>
      <div style={{ fontSize: 28 }}><CountUp to={r.itemCount} /></div>
    </HoloCard>
  </TiltCard>
))}
```

`TiltCard` should wrap **leaf cards only** (don't tilt entire page sections — feels broken).

## Step 7 — Page hero pattern

Every page top should follow:

```tsx
<div style={{ marginBottom: 32 }}>
  <div className="holo-section-label" style={{ marginBottom: 10 }}>
    WORKSPACE / {breadcrumb.toUpperCase()}
  </div>
  <h1 style={{ fontSize: 56, fontWeight: 600, margin: 0, letterSpacing: '-0.04em', lineHeight: 1 }}>
    <HoloText>{pageTitle}</HoloText>
  </h1>
  <div style={{ fontSize: 14, color: 'var(--holo-text-dim)', marginTop: 10 }}>{pageDescription}</div>
</div>
```

Apply to: `RepositoriesPage`, `BrowsePage`, `SearchPage`, `UsersPage`, `AdminPage`, `AuditPage`, `SecurityPage`, `MigrationPage`, `CleanupPage`.

## Step 8 — Tabs (AdminPage etc.)

Replace tab strips with:

```tsx
import { HoloTabs } from '@/components/holo';

<HoloTabs
  value={tab}
  onChange={setTab}
  items={[
    { value: 'roles', label: 'Roles' },
    { value: 'webhooks', label: 'Webhooks' },
    { value: 'privileges', label: 'Privileges' },
  ]}
/>
```

## Step 9 — Tables

Add `className="holo-table"` to existing `<table>` elements. The CSS handles header / row / hover treatments. Don't touch row data — only swap badge cells:

```tsx
<td><HoloPill tone="success">healthy</HoloPill></td>
```

## Step 10 — Inputs

Replace inline-styled inputs:

```tsx
import { HoloInput } from '@/components/holo';

<HoloInput placeholder="Search…" value={q} onChange={e => setQ(e.target.value)} />
```

`Select`, `MultiSelect` components: keep existing logic, but apply trigger styling: `className="holo-input"` plus dropdown panel using `.holo-card`.

## Step 11 — LoginPage

Update `LoginPage.module.css`:

- `.root`: `background: var(--holo-bg);` (the `<HoloApp>` shell isn't on /login yet — add `<HoloApp>` wrapper to `LoginPage` directly)
- `.card`: replace with `className="holo-card holo-card--accent"`, padding 32, max-width 400
- Title: `<HoloText as="h1">{title}</HoloText>`
- Submit button: `<HoloButton variant="primary" type="submit" style={{ width: '100%' }}>Sign in</HoloButton>`

## Step 12 — DON'T

- Don't change `App.tsx` routing.
- Don't change `apiClient`, `authStore`, or any `useQuery`/`useMutation` calls.
- Don't change file structure — only edit existing files + add `components/holo/`.
- Don't replace lucide-react icons; they work fine on dark holographic backgrounds.
- Don't add new dependencies — the kit is pure CSS + React.

## Step 13 — Verify

After applying, run `npm run dev` and check:

1. `/login` renders with holographic gradient title + glass card
2. `/repositories` shows tiltable repo cards with `HoloText` heading
3. Sidebar nav active state uses gradient background
4. Profile modal opens with gradient top edge
5. No console errors. No lost functionality (token CRUD, OIDC logout, navigation all work).

## Token reference

```css
--holo-bg: #080612;          /* page background */
--holo-text: #f4f0ff;        /* primary text */
--holo-text-dim: rgba(244,240,255,0.55);
--holo-text-faint: rgba(244,240,255,0.30);

--holo-a: #7c5cff;  /* purple */
--holo-b: #22d3ee;  /* cyan */
--holo-c: #ff5cf0;  /* magenta */

--holo-green: #5effb8;
--holo-amber: #ffc857;
--holo-red:   #ff6b6b;

--holo-radius: 14px;
--holo-radius-sm: 10px;
--holo-radius-pill: 999px;

--holo-gradient: linear-gradient(110deg, #7c5cff 0%, #22d3ee 35%, #ff5cf0 70%, #7c5cff 100%);
--holo-shadow-glow: 0 8px 30px rgba(124,92,255,0.35);
--holo-ring: 0 0 0 3px rgba(124,92,255,0.30);
```

Use these directly in any custom inline styles for consistency.
