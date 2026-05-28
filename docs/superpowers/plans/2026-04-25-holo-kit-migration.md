# Holo-Kit UI Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the visual layer of the self_nexus React frontend with Holographic Depth (holo-kit) in three phases — Shell first, then content pages, then admin pages — without touching routing, queries, auth, or API contracts.

**Architecture:** Copy `holo-kit/` to `components/holo/`, import `holo.css` globally, then progressively swap inline styles and CSS modules for holo components (`HoloApp`, `HoloCard`, `TiltCard`, `HoloButton`, `HoloInput`, `HoloModal`, `HoloTabs`, `HoloPill`, `HoloText`, `CountUp`). Every page gets a hero header using `holo-section-label` + `<HoloText as="h1">`. TiltCard is used only on leaf grid cards in RepositoriesPage and BrowsePage.

**Tech Stack:** React 18, TypeScript, Vite, CSS Modules, holo-kit (pure CSS + React, no new deps), lucide-react (icons unchanged)

---

## PHASE 1 — Shell

---

### Task 1: Copy holo-kit and import CSS

**Files:**
- Create: `frontend/src/components/holo/holo.css` (copy from holo-kit)
- Create: `frontend/src/components/holo/holo.tsx` (copy from holo-kit)
- Create: `frontend/src/components/holo/index.ts` (copy from holo-kit)
- Modify: `frontend/src/main.tsx`

- [ ] **Step 1: Copy holo-kit files**

```bash
cp frontend/src/holo-kit/holo.css frontend/src/components/holo/holo.css
cp frontend/src/holo-kit/holo.tsx frontend/src/components/holo/holo.tsx
cp frontend/src/holo-kit/index.ts frontend/src/components/holo/index.ts
```

- [ ] **Step 2: Add CSS import to main.tsx**

In `frontend/src/main.tsx`, add the holo CSS import as the first import (before `./index.css`):

```tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import App from './App'
import './components/holo/holo.css'
import './index.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
    },
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
)
```

- [ ] **Step 3: Verify TypeScript compiles**

```bash
cd frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/holo/ frontend/src/main.tsx
git commit -m "feat(ui): install holo-kit and wire CSS"
```

---

### Task 2: Rewrite Layout.module.css with holo tokens

**Files:**
- Modify: `frontend/src/components/Layout.module.css`

- [ ] **Step 1: Replace Layout.module.css**

Full replacement — keep all class names identical (`.root`, `.sidebar`, `.navBtn`, `.active`, etc.), change only values:

```css
.root {
  display: grid;
  grid-template-columns: 260px minmax(0, 1fr);
  min-height: 100vh;
}

.sidebar {
  position: sticky;
  top: 0;
  height: 100vh;
  background: rgba(8,6,18,0.6);
  backdrop-filter: blur(30px);
  border-right: 1px solid rgba(124,92,255,0.15);
  padding: 14px 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  overflow-y: auto;
  z-index: 20;
}

.brand {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 10px 8px 6px;
}

.brandLogo {
  width: 180px;
  height: auto;
  display: block;
}

.sectionLabel {
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--holo-text-faint);
  padding: 0 4px;
  margin-top: 4px;
}

.nav {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.navBtn {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 11px;
  border-radius: 9px;
  border: 1px solid transparent;
  background: transparent;
  color: var(--holo-text-dim);
  font-size: 13px;
  font-weight: 500;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
  text-decoration: none;
  cursor: pointer;
}

.navBtn:hover {
  background: rgba(255,255,255,0.04);
  color: var(--holo-text);
}

.navBtn.active {
  background: linear-gradient(90deg, rgba(124,92,255,0.18), rgba(34,211,238,0.06));
  border-color: rgba(124,92,255,0.30);
  color: var(--holo-text);
  font-weight: 600;
}

.divider {
  border: none;
  border-top: 1px solid rgba(124,92,255,0.15);
  margin: 2px 0;
}

.footer {
  margin-top: auto;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding-top: 10px;
  border-top: 1px solid rgba(124,92,255,0.15);
}

.danger:hover {
  background: rgba(255,107,107,0.10) !important;
  border-color: rgba(255,107,107,0.30) !important;
  color: var(--holo-red) !important;
}

.userInfo {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 10px;
  border-radius: 8px;
  background: rgba(255,255,255,0.025);
  border: 1px solid rgba(124,92,255,0.12);
}

.userName {
  font-size: 12px;
  font-weight: 600;
  color: var(--holo-text);
}

.userRole {
  font-size: 10px;
  font-weight: 500;
  color: var(--holo-a);
  background: rgba(124,92,255,0.12);
  padding: 1px 6px;
  border-radius: 4px;
}

.version {
  text-align: center;
  font-size: 10px;
  color: var(--holo-text-faint);
  padding: 4px 0 0;
}

.main {
  display: flex;
  flex-direction: column;
  min-height: 100vh;
  overflow: hidden;
}
```

- [ ] **Step 2: Verify no TypeScript errors**

```bash
cd frontend && npx tsc --noEmit
```

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/Layout.module.css
git commit -m "feat(ui): update Layout.module.css with holo tokens"
```

---

### Task 3: Wrap Layout in HoloApp and migrate ProfileModal

**Files:**
- Modify: `frontend/src/components/Layout.tsx`

- [ ] **Step 1: Add holo imports to Layout.tsx**

Add to the existing imports at the top of `frontend/src/components/Layout.tsx`:

```tsx
import { HoloApp, HoloModal, HoloButton, HoloInput } from '@/components/holo'
```

- [ ] **Step 2: Wrap root div with HoloApp**

Find the `return (` block in the `Layout` default export function. Wrap the outer `<div className={styles.root}>` with `<HoloApp>`:

```tsx
  return (
    <HoloApp>
      <div className={styles.root}>
        {/* existing aside + main — unchanged */}
        <aside className={styles.sidebar}>
          {/* ... all existing sidebar content unchanged ... */}
        </aside>
        <main className={styles.main}>
          <Outlet />
        </main>
        {profileOpen && <ProfileModal onClose={() => setProfileOpen(false)} />}
      </div>
    </HoloApp>
  )
```

- [ ] **Step 3: Replace ProfileModal overlay/modal with HoloModal**

In the `ProfileModal` function, replace the return statement's outer structure:

**Before:**
```tsx
  return (
    <div style={S.overlay} onClick={onClose}>
      <div style={S.modal} onClick={e => e.stopPropagation()}>
        {/* children */}
      </div>
    </div>
  )
```

**After:**
```tsx
  return (
    <HoloModal open={true} onClose={onClose}>
      {/* children — same as before, only the outer wrapper changes */}
    </HoloModal>
  )
```

- [ ] **Step 4: Replace ProfileModal buttons with HoloButton**

In `ProfileModal`, replace every `S.btn(...)` call:

```tsx
// S.btn('primary') → variant="primary"
// Before:
<button style={S.btn('primary')} onClick={create} disabled={creating || !name.trim()}>
  <Plus size={14} />{creating ? 'Creating…' : 'Create'}
</button>

// After:
<HoloButton variant="primary" icon={<Plus size={14} />} onClick={create} disabled={creating || !name.trim()}>
  {creating ? 'Creating…' : 'Create'}
</HoloButton>

// S.btn('danger') → variant="danger"
// Before:
<button style={S.btn('danger')} onClick={() => del.mutate(t.id)}>
  <Trash2 size={13} />
</button>

// After:
<HoloButton variant="danger" icon={<Trash2 size={13} />} onClick={() => del.mutate(t.id)} />

// S.btn('ghost') → default variant
// Before:
<button style={{ ...S.btn('ghost'), marginTop: 8, fontSize: 12 }} onClick={() => setNewToken(null)}>Dismiss</button>

// After:
<HoloButton style={{ marginTop: 8 }} onClick={() => setNewToken(null)}>Dismiss</HoloButton>

// Close button
// Before:
<button style={S.closeBtn} onClick={onClose}><X size={18} /></button>

// After:
<button style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--holo-text-dim)', padding: 4, display: 'flex' }} onClick={onClose}><X size={18} /></button>
```

- [ ] **Step 5: Replace ProfileModal input with HoloInput**

```tsx
// Before:
<input
  style={S.input}
  placeholder="Token name"
  value={name}
  onChange={e => setName(e.target.value)}
  onKeyDown={e => e.key === 'Enter' && create()}
/>

// After:
<HoloInput
  style={{ flex: 1 }}
  placeholder="Token name"
  value={name}
  onChange={e => setName(e.target.value)}
  onKeyDown={e => e.key === 'Enter' && create()}
/>
```

- [ ] **Step 6: Replace ProfileModal card/row inline styles with holo equivalents**

In the token list area of `ProfileModal`, update the card containers to use holo class names. The `S.card` style object → `className="holo-card"` with inline padding, `S.mono` → `className="holo-mono"`:

```tsx
// S.card div
<div style={S.card}>  →  <div className="holo-card" style={{ padding: 16 }}>

// New token success card  
<div style={{ ...S.card, background: 'rgba(34,197,94,0.06)', border: '1px solid rgba(34,197,94,0.3)' }}>
→
<div className="holo-card" style={{ padding: 16, background: 'rgba(94,255,184,0.08)', border: '1px solid rgba(94,255,184,0.25)' }}>

// Token value code block — update colors to holo palette
<code style={{ ...S.mono, fontSize: 12, ... color: '#a5b4fc' }}>
→
<code className="holo-mono" style={{ fontSize: 12, background: 'rgba(0,0,0,0.3)', padding: '8px 12px', borderRadius: 8, display: 'block', wordBreak: 'break-all', color: 'var(--holo-a)' }}>

// Token list title color
style={{ fontSize: 13, fontWeight: 600, color: '#dbeafe', marginBottom: 10 }}
→
style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)', marginBottom: 10 }}
```

- [ ] **Step 7: Replace profile icon button in sidebar footer**

```tsx
// Before:
<button
  title="API Tokens & Profile"
  onClick={() => setProfileOpen(true)}
  style={{ background: 'rgba(59,130,246,0.12)', border: '1px solid rgba(59,130,246,0.25)', borderRadius: 7, padding: '5px 7px', cursor: 'pointer', color: '#3b82f6', display: 'flex', alignItems: 'center', flexShrink: 0 }}
>
  <Key size={14} />
</button>

// After:
<button
  title="API Tokens & Profile"
  onClick={() => setProfileOpen(true)}
  style={{ background: 'rgba(124,92,255,0.12)', border: '1px solid rgba(124,92,255,0.25)', borderRadius: 7, padding: '5px 7px', cursor: 'pointer', color: 'var(--holo-a)', display: 'flex', alignItems: 'center', flexShrink: 0 }}
>
  <Key size={14} />
</button>
```

- [ ] **Step 8: Verify**

```bash
cd frontend && npx tsc --noEmit
```

- [ ] **Step 9: Start dev server and visually verify Phase 1 shell**

```bash
cd frontend && npm run dev
```

Check:
- Sidebar has purple-tinted active nav state (gradient background)
- ProfileModal opens with holographic top-edge gradient line
- No console errors

- [ ] **Step 10: Commit**

```bash
git add frontend/src/components/Layout.tsx
git commit -m "feat(ui): migrate Layout shell and ProfileModal to holo-kit"
```

---

### Task 4: Migrate LoginPage

**Files:**
- Modify: `frontend/src/pages/LoginPage.module.css`
- Modify: `frontend/src/pages/LoginPage.tsx`

- [ ] **Step 1: Rewrite LoginPage.module.css**

Full replacement of `frontend/src/pages/LoginPage.module.css`:

```css
.container {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--holo-bg);
}

.card {
  width: 400px;
  background: linear-gradient(135deg, rgba(124,92,255,0.18), rgba(34,211,238,0.08), rgba(255,92,240,0.10));
  border: 1px solid var(--holo-border);
  border-radius: 18px;
  padding: 40px 36px;
  backdrop-filter: blur(20px);
  text-align: center;
  position: relative;
  overflow: hidden;
  max-width: 92vw;
}

.card::before {
  content: '';
  position: absolute;
  top: 0; left: 0; right: 0;
  height: 1px;
  background: var(--holo-gradient);
  background-size: 200% 200%;
  animation: holoShift 5s ease infinite;
}

.logo {
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 28px;
}

.logoImg {
  width: 220px;
  height: auto;
  display: block;
}

.form {
  display: flex;
  flex-direction: column;
  gap: 16px;
  text-align: left;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.label {
  font-size: 12px;
  font-weight: 500;
  color: var(--holo-text-dim);
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.error {
  background: rgba(255,107,107,0.12);
  border: 1px solid rgba(255,107,107,0.30);
  border-radius: var(--holo-radius-sm);
  padding: 10px 12px;
  color: var(--holo-red);
  font-size: 13px;
}

.divider {
  display: flex;
  align-items: center;
  gap: 12px;
  color: var(--holo-text-faint);
  font-size: 12px;
  font-weight: 500;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.divider::before,
.divider::after {
  content: '';
  flex: 1;
  height: 1px;
  background: rgba(124,92,255,0.2);
}
```

- [ ] **Step 2: Update LoginPage.tsx — wrap with HoloApp and replace form elements**

Add import at top of `frontend/src/pages/LoginPage.tsx`:

```tsx
import { HoloApp, HoloButton, HoloInput } from '@/components/holo'
```

Wrap the root div with `<HoloApp>` (LoginPage sits outside Layout, so it needs its own shell):

```tsx
  return (
    <HoloApp>
      <div className={styles.container}>
        <div className={styles.card}>
          <div className={styles.logo}>
            <img src={logo} alt="Nexspence" className={styles.logoImg} />
          </div>

          <form onSubmit={handleSubmit} className={styles.form}>
            <div className={styles.field}>
              <label className={styles.label}>Username</label>
              <HoloInput
                type="text"
                value={username}
                onChange={e => setUsername(e.target.value)}
                autoComplete="username"
                autoFocus
                required
              />
            </div>
            <div className={styles.field}>
              <label className={styles.label}>Password</label>
              <HoloInput
                type="password"
                value={password}
                onChange={e => setPassword(e.target.value)}
                autoComplete="current-password"
                required
              />
            </div>

            {error && <div className={styles.error}>{error}</div>}
            {oidcError && (
              <div className={styles.error} role="alert">
                SSO login failed: {oidcError}
              </div>
            )}

            <HoloButton variant="primary" type="submit" disabled={loading} style={{ width: '100%', justifyContent: 'center' }}>
              {loading ? 'Signing in…' : 'Sign in'}
            </HoloButton>

            {authConfig?.oidcEnabled && (
              <>
                <div className={styles.divider}>or</div>
                <HoloButton
                  type="button"
                  icon={<KeyRound size={16} />}
                  onClick={handleOIDC}
                  style={{ width: '100%', justifyContent: 'center' }}
                >
                  Sign in with {authConfig.oidcDisplayName}
                </HoloButton>
              </>
            )}
          </form>
        </div>
      </div>
    </HoloApp>
  )
```

- [ ] **Step 3: Verify and check /login visually**

```bash
cd frontend && npx tsc --noEmit
```

Then in dev server, navigate to `/login`. Check:
- Deep violet-black background (`#080612`)
- Card has gradient top edge line
- Submit button has holographic gradient animation
- OIDC button (if configured) uses holo style

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/LoginPage.tsx frontend/src/pages/LoginPage.module.css
git commit -m "feat(ui): migrate LoginPage to holo-kit"
```

---

## PHASE 2 — Content Pages

---

### Task 5: Migrate RepositoriesPage

**Files:**
- Modify: `frontend/src/pages/RepositoriesPage.tsx`
- Modify: `frontend/src/pages/RepositoriesPage.module.css`

- [ ] **Step 1: Add holo imports to RepositoriesPage.tsx**

```tsx
import { TiltCard, HoloCard, HoloButton, HoloInput, HoloPill, HoloText, HoloModal, CountUp } from '@/components/holo'
```

- [ ] **Step 2: Add page hero at top of RepositoriesPage JSX**

Find the return statement in `RepositoriesPage`. Replace the existing header block:

```tsx
// Before:
<div className={styles.header}>
  <div>
    <h1 className={styles.title}>Repositories</h1>
    <p className={styles.subtitle}>{repos.length} total</p>
  </div>
  <div className={styles.actions}>
    <button className={styles.iconBtn} onClick={() => refetch()} title="Refresh">
      <RefreshCw size={16} />
    </button>
    {isAdmin && (
      <button className={styles.createBtn} onClick={() => setShowCreate(true)}>
        <Plus size={16} />
        Create Repository
      </button>
    )}
  </div>
</div>

// After:
<div style={{ marginBottom: 8 }}>
  <div className="holo-section-label" style={{ marginBottom: 6 }}>WORKSPACE / REPOSITORIES</div>
  <div style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', gap: 16 }}>
    <div>
      <h1 style={{ fontSize: 40, fontWeight: 700, margin: '0 0 4px', letterSpacing: '-0.04em', lineHeight: 1 }}>
        <HoloText>Repositories</HoloText>
      </h1>
      <p style={{ fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 }}>{repos.length} total</p>
    </div>
    <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
      <HoloButton icon={<RefreshCw size={15} />} onClick={() => refetch()} title="Refresh" />
      {isAdmin && (
        <HoloButton variant="primary" icon={<Plus size={15} />} onClick={() => setShowCreate(true)}>
          Create Repository
        </HoloButton>
      )}
    </div>
  </div>
</div>
```

- [ ] **Step 3: Replace search input and format filter in toolbar**

```tsx
// Before:
<div className={styles.toolbar}>
  <input
    className={styles.search}
    placeholder="Filter by name…"
    value={filter}
    onChange={e => setFilter(e.target.value)}
  />
  <Select ... />
</div>

// After:
<div style={{ display: 'flex', gap: 12 }}>
  <HoloInput
    style={{ flex: 1 }}
    placeholder="Filter by name…"
    value={filter}
    onChange={e => setFilter(e.target.value)}
  />
  <Select ... />  {/* Select styling handled in Task 9 */}
</div>
```

- [ ] **Step 4: Wrap RepoCard with TiltCard + HoloCard**

In the `RepoCard` function, replace the outer `<div className={styles.card} onClick={onClick}>` with `TiltCard + HoloCard`:

```tsx
// Before:
return (
  <div className={styles.card} onClick={onClick} style={{ cursor: 'pointer' }}>
    <div className={styles.cardHeader}>
      ...
    </div>
    <div className={styles.cardName}>{repo.name}</div>
    ...
  </div>
)

// After:
return (
  <TiltCard intensity={10} style={{ borderRadius: 14 }} onClick={onClick}>
    <HoloCard edge style={{ padding: 16, cursor: 'pointer' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
        <span style={{ fontSize: 11, fontWeight: 600, padding: '2px 8px', borderRadius: 4, textTransform: 'uppercase', letterSpacing: '0.3px', background: (FORMAT_COLORS[repo.format] ?? '#6b7280') + '22', color: FORMAT_COLORS[repo.format] ?? '#6b7280' }}>
          {repo.format}
        </span>
        <HoloPill tone={repo.type === 'hosted' ? 'success' : repo.type === 'proxy' ? 'default' : 'warn'}>
          {TYPE_LABELS[repo.type] ?? repo.type}
        </HoloPill>
        <span style={{ width: 7, height: 7, borderRadius: '50%', background: repo.online ? 'var(--holo-green)' : 'rgba(255,255,255,0.2)', boxShadow: repo.online ? '0 0 6px var(--holo-green)' : 'none', display: 'inline-block', marginLeft: 'auto', flexShrink: 0 }} />
        {repo.allowAnonymous && (
          <HoloPill>anon</HoloPill>
        )}
      </div>
      <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--holo-text)', wordBreak: 'break-word', marginBottom: 4 }}>
        {repo.name}
      </div>
      {repo.description && (
        <div style={{ fontSize: 12, color: 'var(--holo-text-dim)', lineHeight: 1.4 }}>{repo.description}</div>
      )}
      {repo.type !== 'group' && storeName && (
        <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 4 }}>
          on <span style={{ color: 'var(--holo-b)', fontWeight: 500 }}>{storeName}</span>
        </div>
      )}
      {quota != null && (
        <div style={{ marginTop: 8 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11, color: 'var(--holo-text-dim)', marginBottom: 4 }}>
            <span className="holo-mono">{formatBytes(quota.usedBytes)} used</span>
            {quota.quotaBytes != null && <span className="holo-mono">{formatBytes(quota.quotaBytes)}</span>}
          </div>
          {quota.quotaBytes != null && (
            <div style={{ height: 3, background: 'rgba(255,255,255,0.08)', borderRadius: 2, overflow: 'hidden' }}>
              <div style={{ height: '100%', borderRadius: 2, transition: 'width 0.3s ease', width: `${Math.min(pct ?? 0, 100)}%`, background: (pct ?? 0) >= 90 ? 'var(--holo-red)' : (pct ?? 0) >= 70 ? 'var(--holo-amber)' : 'var(--holo-green)' }} />
            </div>
          )}
        </div>
      )}
      {isAdmin && (
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 12, paddingTop: 10, borderTop: '1px solid rgba(255,255,255,0.06)' }}>
          <HoloButton icon={<Settings2 size={14} />} onClick={e => { e.stopPropagation(); onEdit() }} title="Settings" />
          <HoloButton variant="danger" icon={<Trash2 size={14} />} onClick={e => { e.stopPropagation(); onDelete() }} title="Delete" />
        </div>
      )}
    </HoloCard>
  </TiltCard>
)
```

- [ ] **Step 5: Replace modal overlays in CreateRepoModal and EditRepoModal**

Both modals use `styles.modalOverlay` + `styles.modal`. Replace the outer wrapper in each modal:

```tsx
// Before (in CreateRepoModal and EditRepoModal):
return (
  <div className={styles.modalOverlay} onClick={onClose}>
    <div className={styles.modal} onClick={e => e.stopPropagation()}>
      <h2 className={styles.modalTitle}>...</h2>
      ...
    </div>
  </div>
)

// After:
return (
  <HoloModal open={true} onClose={onClose}>
    <h2 style={{ fontSize: 17, fontWeight: 700, color: 'var(--holo-text)', margin: 0 }}>...</h2>
    ...
  </HoloModal>
)
```

Also in both modals, replace form elements:
- `<input className={styles.input}` → `<HoloInput`
- `<button className={styles.submitBtn}` → `<HoloButton variant="primary" type="submit"`
- `<button className={styles.cancelBtn}` → `<HoloButton type="button"`
- `<div className={styles.error}` → `<div style={{ background: 'rgba(255,107,107,0.12)', border: '1px solid rgba(255,107,107,0.3)', borderRadius: 10, padding: '10px 12px', color: 'var(--holo-red)', fontSize: 13 }}`
- `<label className={styles.label}` → `<label style={{ fontSize: 12, fontWeight: 500, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.4px' }}`

- [ ] **Step 6: Simplify RepositoriesPage.module.css**

Remove the modal, button, and input styles from `RepositoriesPage.module.css` — they are now handled by holo components. Keep only layout-level classes: `.page`, `.grid`, `.empty`, `.emptyIcon`. The CSS module no longer needs `.card`, `.cardHeader`, `.formatBadge`, `.typeBadge`, `.cardName`, `.cardDesc`, `.cardFooter`, `.settingsBtn`, `.deleteBtn`, `.quotaBar`, `.quotaText`, `.quotaTrack`, `.quotaFill`, `.modal*`, `.form*`, `.input`, `.label`, `.submit*`, `.cancel*`, `.toolbar`, `.search`, `.header`, `.title`, `.subtitle`, `.actions`, `.iconBtn`, `.createBtn`, `.member*`, `.hint`, `.type_*`:

```css
.page {
  padding: 24px;
  height: 100%;
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 16px;
}

.empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  color: var(--holo-text-dim);
  font-size: 14px;
}

.emptyIcon {
  opacity: 0.3;
}
```

- [ ] **Step 7: Verify and check /repositories visually**

```bash
cd frontend && npx tsc --noEmit
```

In dev server at `/repositories`:
- Page title shows holographic gradient text
- Repo cards have glass effect and 3D tilt on hover
- Create button has animated gradient
- No console errors

- [ ] **Step 8: Commit**

```bash
git add frontend/src/pages/RepositoriesPage.tsx frontend/src/pages/RepositoriesPage.module.css
git commit -m "feat(ui): migrate RepositoriesPage to holo-kit (TiltCard + hero)"
```

---

### Task 6: Migrate BrowsePage

**Files:**
- Modify: `frontend/src/pages/BrowsePage.tsx`

BrowsePage is 1775 lines. Apply transformations systematically by searching for patterns.

- [ ] **Step 1: Add holo imports**

```tsx
import { TiltCard, HoloCard, HoloButton, HoloInput, HoloPill, HoloText, HoloModal } from '@/components/holo'
```

- [ ] **Step 2: Add page hero**

At the top of the BrowsePage JSX return, before the repo selector / tree area, add:

```tsx
<div style={{ marginBottom: 24 }}>
  <div className="holo-section-label" style={{ marginBottom: 6 }}>WORKSPACE / BROWSE</div>
  <h1 style={{ fontSize: 40, fontWeight: 700, margin: '0 0 4px', letterSpacing: '-0.04em', lineHeight: 1 }}>
    <HoloText>Browse</HoloText>
  </h1>
  <p style={{ fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 }}>Explore repository contents</p>
</div>
```

- [ ] **Step 3: Apply systematic replacements throughout BrowsePage**

For every occurrence of the patterns below, apply the corresponding replacement:

| Pattern (search) | Replacement |
|---|---|
| `background: 'rgba(255,255,255,0.03)'` in card-like divs | `className="holo-card"` + remove inline bg/border/radius |
| `border: '1px solid rgba(255,255,255,0.08)'` | remove (handled by `holo-card`) |
| `background: '#3b82f6'` (primary button) | `<HoloButton variant="primary">` |
| `background: 'rgba(239,68,68,0.15)'` (danger button) | `<HoloButton variant="danger">` |
| `<input style={{...` (search/filter inputs) | `<HoloInput` |
| `background: 'rgba(34,197,94,` (success pill/badge) | `<HoloPill tone="success">` |
| `background: 'rgba(239,68,68,` (danger pill/badge) | `<HoloPill tone="danger">` |
| `background: 'rgba(245,158,11,` (warn pill/badge) | `<HoloPill tone="warn">` |
| Modal overlays: `position: 'fixed'` + `display: 'flex'` + `alignItems: 'center'` pattern | `<HoloModal open={...} onClose={...}>` |

- [ ] **Step 4: Wrap artifact/folder cards with TiltCard**

For leaf item cards in the tree/grid view (files, images, packages) — cards that represent individual artifacts — wrap with `<TiltCard intensity={8}><HoloCard edge>`. Do NOT tilt the entire left panel or tree navigation.

- [ ] **Step 5: Update color references to holo tokens**

```
'#dbeafe' → 'var(--holo-text)'
'rgba(229,231,235,0.5)' → 'var(--holo-text-dim)'
'rgba(229,231,235,0.35)' → 'var(--holo-text-faint)'
'#3b82f6' (accent color) → 'var(--holo-a)'
'#22c55e' (success color) → 'var(--holo-green)'
'#ef4444' (danger color) → 'var(--holo-red)'
'#f59e0b' (warn color) → 'var(--holo-amber)'
```

- [ ] **Step 6: Verify**

```bash
cd frontend && npx tsc --noEmit
```

In dev server at `/browse`: tree renders, artifact cards tilt on hover, no console errors.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/pages/BrowsePage.tsx
git commit -m "feat(ui): migrate BrowsePage to holo-kit (TiltCard + hero)"
```

---

### Task 7: Migrate SearchPage

**Files:**
- Modify: `frontend/src/pages/SearchPage.tsx`

- [ ] **Step 1: Add holo imports**

```tsx
import { HoloCard, HoloButton, HoloInput, HoloPill, HoloText, HoloModal } from '@/components/holo'
```

- [ ] **Step 2: Add page hero**

```tsx
<div style={{ marginBottom: 24 }}>
  <div className="holo-section-label" style={{ marginBottom: 6 }}>WORKSPACE / SEARCH</div>
  <h1 style={{ fontSize: 40, fontWeight: 700, margin: '0 0 4px', letterSpacing: '-0.04em', lineHeight: 1 }}>
    <HoloText>Search</HoloText>
  </h1>
  <p style={{ fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 }}>Find artifacts across all repositories</p>
</div>
```

- [ ] **Step 3: Replace search input**

Replace the main search `<input>` with `<HoloInput style={{ flex: 1 }} ...>`.

- [ ] **Step 4: Replace result cards with flat HoloCard (no TiltCard)**

SearchPage shows list results — use flat `<HoloCard>` without `TiltCard`:

```tsx
// Each result row / component card:
<HoloCard style={{ padding: 14, marginBottom: 8 }}>
  {/* existing content unchanged */}
</HoloCard>
```

- [ ] **Step 5: Replace badges and buttons**

Apply the same token substitution from Task 6 Step 5. Replace all inline button and badge styles with `<HoloButton>` and `<HoloPill tone="...">`.

- [ ] **Step 6: Verify**

```bash
cd frontend && npx tsc --noEmit
```

- [ ] **Step 7: Commit**

```bash
git add frontend/src/pages/SearchPage.tsx
git commit -m "feat(ui): migrate SearchPage to holo-kit"
```

---

### Task 8: Update Select and MultiSelect

**Files:**
- Modify: `frontend/src/components/Select.tsx`
- Modify: `frontend/src/components/MultiSelect.tsx`

- [ ] **Step 1: Update Select trigger button**

In `Select.tsx`, replace the trigger `<button>` inline styles with holo equivalents:

```tsx
<button
  type="button"
  disabled={disabled}
  onClick={() => setOpen(v => !v)}
  className="holo-input"
  style={{
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    width: '100%',
    cursor: disabled ? 'not-allowed' : 'pointer',
    opacity: disabled ? 0.5 : 1,
    textAlign: 'left' as const,
    borderRadius: open ? 'var(--holo-radius-sm) var(--holo-radius-sm) 0 0' : 'var(--holo-radius-sm)',
    boxShadow: open ? 'var(--holo-ring)' : 'none',
    borderColor: open ? 'var(--holo-border-strong)' : undefined,
    ...style,
  }}
>
  <span style={{ flex: 1 }}>{selected ? selected.label : placeholder}</span>
  {selected?.badge}
  {selected?.tag}
  <ChevronDown size={14} style={{ color: 'var(--holo-text-faint)', flexShrink: 0, transform: open ? 'rotate(180deg)' : 'none', transition: 'transform 0.2s' }} />
</button>
```

- [ ] **Step 2: Update Select dropdown panel**

Replace the dropdown `<div>` inline styles with holo card:

```tsx
{open && (
  <div
    className="holo-card"
    style={{
      position: 'absolute',
      top: '100%',
      left: 0,
      right: 0,
      borderRadius: '0 0 var(--holo-radius-sm) var(--holo-radius-sm)',
      borderTop: 'none',
      zIndex: 200,
      maxHeight: 260,
      overflowY: 'auto' as const,
    }}
  >
```

- [ ] **Step 3: Update Select search input inside dropdown**

```tsx
<input
  autoFocus
  placeholder="Filter…"
  value={search}
  onChange={e => setSearch(e.target.value)}
  className="holo-input"
  style={{ width: '100%', boxSizing: 'border-box' as const }}
/>
```

- [ ] **Step 4: Update Select option hover colors**

```tsx
color: isSelected ? 'var(--holo-a)' : 'var(--holo-text)',
background: isSelected ? 'rgba(124,92,255,0.15)' : 'transparent',
// onMouseEnter:
(e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.08)'
```

- [ ] **Step 5: Apply same pattern to MultiSelect.tsx**

Open `MultiSelect.tsx` and apply the same trigger → `className="holo-input"`, dropdown → `className="holo-card"`, option colors → holo-a substitution.

- [ ] **Step 6: Verify**

```bash
cd frontend && npx tsc --noEmit
```

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/Select.tsx frontend/src/components/MultiSelect.tsx
git commit -m "feat(ui): update Select/MultiSelect to holo styles"
```

---

## PHASE 3 — Admin Pages

---

### Task 9: Migrate UsersPage

**Files:**
- Modify: `frontend/src/pages/UsersPage.tsx`
- Modify: `frontend/src/pages/UsersPage.module.css`

- [ ] **Step 1: Add holo imports**

```tsx
import { HoloButton, HoloInput, HoloPill, HoloText, HoloModal, HoloTabs } from '@/components/holo'
```

- [ ] **Step 2: Add page hero**

```tsx
<div style={{ marginBottom: 24 }}>
  <div className="holo-section-label" style={{ marginBottom: 6 }}>SYSTEM / USERS</div>
  <h1 style={{ fontSize: 40, fontWeight: 700, margin: '0 0 4px', letterSpacing: '-0.04em', lineHeight: 1 }}>
    <HoloText>Users</HoloText>
  </h1>
  <p style={{ fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 }}>Manage user accounts and role assignments</p>
</div>
```

- [ ] **Step 3: Replace user table with holo-table**

Add `className="holo-table"` to the `<table>` element. Replace status badge cells with `<HoloPill>`:

```tsx
<table className="holo-table">
  <thead>
    <tr>
      <th>Username</th>
      <th>Name</th>
      <th>Email</th>
      <th>Roles</th>
      <th>Status</th>
      <th></th>
    </tr>
  </thead>
  <tbody>
    {users.map(u => (
      <tr key={u.id}>
        <td>{u.username}</td>
        <td>{u.firstName} {u.lastName}</td>
        <td style={{ color: 'var(--holo-text-dim)' }}>{u.email}</td>
        <td>{/* role chips → HoloPill */}</td>
        <td>
          <HoloPill tone={u.status === 'active' ? 'success' : 'warn'}>{u.status}</HoloPill>
        </td>
        <td>{/* action buttons → HoloButton */}</td>
      </tr>
    ))}
  </tbody>
</table>
```

- [ ] **Step 4: Replace role chips in AssignRolesModal with HoloPill**

In `AssignRolesModal`, role chip spans → `<HoloPill>` elements:

```tsx
{selectedRoles.map(id => {
  const role = roles.find(r => r.id === id)
  if (!role) return null
  return (
    <HoloPill key={id} tone="default" style={{ cursor: 'pointer' }} onClick={() => toggle(id)}>
      <Shield size={10} /> {role.name} <XCircle size={10} />
    </HoloPill>
  )
})}
```

Role filter input → `<HoloInput>`. Modal wrapper → `<HoloModal>`.

- [ ] **Step 5: Replace all buttons and inputs throughout**

- All `S.btn('primary')` / `.createBtn` / `.submitBtn` → `<HoloButton variant="primary">`
- All `S.btn('danger')` / `.deleteBtn` → `<HoloButton variant="danger">`
- All `S.btn('ghost')` / `.cancelBtn` → `<HoloButton>`
- All `<input className={styles.input}` → `<HoloInput`
- All modal overlays → `<HoloModal>`

- [ ] **Step 6: Simplify UsersPage.module.css**

Keep only layout-level classes. Remove all button, input, modal, card, table classes now handled by holo components.

- [ ] **Step 7: Verify**

```bash
cd frontend && npx tsc --noEmit
```

- [ ] **Step 8: Commit**

```bash
git add frontend/src/pages/UsersPage.tsx frontend/src/pages/UsersPage.module.css
git commit -m "feat(ui): migrate UsersPage to holo-kit"
```

---

### Task 10: Migrate SecurityPage

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx`

SecurityPage is 1242 lines with tabs (Roles, Privileges, Content Selectors, API Tokens, Users, CVE Scan, Webhooks).

- [ ] **Step 1: Add holo imports**

```tsx
import { HoloButton, HoloInput, HoloPill, HoloText, HoloModal, HoloTabs } from '@/components/holo'
```

- [ ] **Step 2: Add page hero**

```tsx
<div style={{ marginBottom: 24 }}>
  <div className="holo-section-label" style={{ marginBottom: 6 }}>SYSTEM / SECURITY</div>
  <h1 style={{ fontSize: 40, fontWeight: 700, margin: '0 0 4px', letterSpacing: '-0.04em', lineHeight: 1 }}>
    <HoloText>Security</HoloText>
  </h1>
  <p style={{ fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 }}>Roles, privileges, content selectors, and API tokens</p>
</div>
```

- [ ] **Step 3: Replace tab strip with HoloTabs**

Find the tab bar (currently rendered as `<div>` of `<button>` elements with active state styling). Replace with `<HoloTabs>`:

```tsx
const TAB_ITEMS = [
  { value: 'roles', label: 'Roles' },
  { value: 'privileges', label: 'Privileges' },
  { value: 'selectors', label: 'Content Selectors' },
  { value: 'tokens', label: 'API Tokens' },
  ...(isAdmin ? [
    { value: 'users', label: 'Users' },
    { value: 'scan', label: 'CVE Scan' },
    { value: 'webhooks', label: 'Webhooks' },
  ] : []),
]

<HoloTabs
  value={tab}
  onChange={t => setTab(t as typeof tab)}
  items={TAB_ITEMS}
/>
```

- [ ] **Step 4: Apply systematic replacements**

Same as Task 6 Step 3–5: replace all inline button styles, inputs, modal overlays, status badges, and color references with holo equivalents. Add `className="holo-table"` to all `<table>` elements.

- [ ] **Step 5: Verify**

```bash
cd frontend && npx tsc --noEmit
```

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx
git commit -m "feat(ui): migrate SecurityPage to holo-kit (HoloTabs)"
```

---

### Task 11: Migrate AdminPage

**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

AdminPage uses the `S` inline style object and has tabs: `blobs | backup | monitoring`.

- [ ] **Step 1: Add holo imports**

```tsx
import { HoloButton, HoloInput, HoloPill, HoloText, HoloCard, HoloModal, HoloTabs } from '@/components/holo'
```

- [ ] **Step 2: Add page hero**

Replace the current `S.header` + `S.title` block:

```tsx
<div style={{ marginBottom: 24 }}>
  <div className="holo-section-label" style={{ marginBottom: 6 }}>SYSTEM / ADMIN</div>
  <h1 style={{ fontSize: 40, fontWeight: 700, margin: '0 0 4px', letterSpacing: '-0.04em', lineHeight: 1 }}>
    <HoloText>System Admin</HoloText>
  </h1>
  <p style={{ fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 }}>Blob stores, backup, and monitoring</p>
</div>
```

- [ ] **Step 3: Replace tab strip with HoloTabs**

```tsx
// Before: S.tabBar + S.tab(active) inline style buttons
// After:
<HoloTabs
  value={tab}
  onChange={t => setTab(t as AdminTab)}
  items={[
    { value: 'blobs', label: 'Blob Stores' },
    { value: 'backup', label: 'Backup & Restore' },
    { value: 'monitoring', label: 'Monitoring' },
  ]}
/>
```

- [ ] **Step 4: Replace S.card with HoloCard**

Every `<div style={S.card}>` → `<HoloCard style={{ padding: 20 }}>`.

- [ ] **Step 5: Replace S.btn with HoloButton**

Every `S.btn('primary')` → `<HoloButton variant="primary">`, `S.btn('danger')` → `<HoloButton variant="danger">`.

- [ ] **Step 6: Replace custom table grid with holo-table or inline flex**

AdminPage uses a grid-based table (`S.thead`, `S.trow`). Convert to a proper `<table className="holo-table">`:

```tsx
<table className="holo-table">
  <thead>
    <tr>
      <th>Blob Store</th>
      <th>Type</th>
      <th>Usage</th>
      <th>Actions</th>
    </tr>
  </thead>
  <tbody>
    {blobStores.map(bs => (
      <tr key={bs.id}>
        <td style={{ fontWeight: 500 }}>{bs.name}</td>
        <td><HoloPill>{bs.type}</HoloPill></td>
        <td className="holo-mono">{formatBytes(bs.usedBytes)}</td>
        <td>
          <HoloButton icon={<Pencil size={13} />} onClick={() => setEditStore(bs)} />
          <HoloButton variant="danger" icon={<Trash2 size={13} />} onClick={() => deleteStore(bs.id)} />
        </td>
      </tr>
    ))}
  </tbody>
</table>
```

- [ ] **Step 7: Replace modal overlays**

All `position: 'fixed'` modal wrappers → `<HoloModal>`.

- [ ] **Step 8: Verify**

```bash
cd frontend && npx tsc --noEmit
```

- [ ] **Step 9: Commit**

```bash
git add frontend/src/pages/AdminPage.tsx
git commit -m "feat(ui): migrate AdminPage to holo-kit (HoloTabs + HoloCard)"
```

---

### Task 12: Migrate AuditPage

**Files:**
- Modify: `frontend/src/pages/AuditPage.tsx`

- [ ] **Step 1: Add holo imports**

```tsx
import { HoloButton, HoloInput, HoloPill, HoloText } from '@/components/holo'
```

- [ ] **Step 2: Add page hero**

```tsx
<div style={{ marginBottom: 24 }}>
  <div className="holo-section-label" style={{ marginBottom: 6 }}>SYSTEM / AUDIT</div>
  <h1 style={{ fontSize: 40, fontWeight: 700, margin: '0 0 4px', letterSpacing: '-0.04em', lineHeight: 1 }}>
    <HoloText>Audit Log</HoloText>
  </h1>
  <p style={{ fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 }}>Security and change events</p>
</div>
```

- [ ] **Step 3: Replace filter inputs and Export button**

```tsx
// Date and username filter inputs → HoloInput
<HoloInput type="date" value={from} onChange={e => setFrom(e.target.value)} />
<HoloInput type="date" value={to} onChange={e => setTo(e.target.value)} />
<HoloInput placeholder="Username…" value={username} onChange={e => setUsername(e.target.value)} />

// Export button — IMPORTANT: keep the fetch+blob download logic unchanged, only change the button element
<HoloButton variant="primary" icon={<Download size={14} />} onClick={handleExport} disabled={exporting}>
  {exporting ? 'Exporting…' : 'Export NDJSON'}
</HoloButton>
```

- [ ] **Step 4: Replace audit table**

```tsx
<table className="holo-table">
  <thead>
    <tr>
      <th>Time</th><th>User</th><th>Action</th><th>Entity</th><th>Path</th><th>IP</th>
    </tr>
  </thead>
  <tbody>
    {events.map(e => (
      <tr key={e.id}>
        <td className="holo-mono" style={{ fontSize: 11, color: 'var(--holo-text-dim)' }}>{formatTime(e.timestamp)}</td>
        <td style={{ fontWeight: 500 }}>{e.username}</td>
        <td>
          <HoloPill tone={e.action === 'DELETE' ? 'danger' : e.action === 'LOGIN' ? 'success' : 'default'}>
            {e.action}
          </HoloPill>
        </td>
        <td style={{ color: 'var(--holo-text-dim)' }}>{e.entityName}</td>
        <td className="holo-mono" style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>{e.context?.path ?? ''}</td>
        <td className="holo-mono" style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>{e.ipAddress}</td>
      </tr>
    ))}
  </tbody>
</table>
```

- [ ] **Step 5: Verify Export flow unchanged**

The Export button must use `fetch` + `blob` + `<a download>` — NOT `window.location.href` (would bypass JWT). Confirm the `handleExport` function body is not touched.

- [ ] **Step 6: Verify TypeScript**

```bash
cd frontend && npx tsc --noEmit
```

- [ ] **Step 7: Commit**

```bash
git add frontend/src/pages/AuditPage.tsx
git commit -m "feat(ui): migrate AuditPage to holo-kit"
```

---

### Task 13: Migrate CleanupPage

**Files:**
- Modify: `frontend/src/pages/CleanupPage.tsx`

- [ ] **Step 1: Add holo imports**

```tsx
import { HoloButton, HoloInput, HoloPill, HoloText, HoloCard, HoloModal } from '@/components/holo'
```

- [ ] **Step 2: Add page hero**

```tsx
<div style={{ marginBottom: 24 }}>
  <div className="holo-section-label" style={{ marginBottom: 6 }}>SYSTEM / CLEANUP</div>
  <h1 style={{ fontSize: 40, fontWeight: 700, margin: '0 0 4px', letterSpacing: '-0.04em', lineHeight: 1 }}>
    <HoloText>Cleanup Policies</HoloText>
  </h1>
  <p style={{ fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 }}>Configure and run artifact retention policies</p>
</div>
```

- [ ] **Step 3: Apply systematic replacements**

Same token substitution pattern as Task 6: inline card styles → `<HoloCard>`, buttons → `<HoloButton>`, inputs → `<HoloInput>`, badges → `<HoloPill>`, modals → `<HoloModal>`, color references → holo tokens.

- [ ] **Step 4: Verify**

```bash
cd frontend && npx tsc --noEmit
```

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/CleanupPage.tsx
git commit -m "feat(ui): migrate CleanupPage to holo-kit"
```

---

### Task 14: Migrate MigrationPage

**Files:**
- Modify: `frontend/src/pages/MigrationPage.tsx`

- [ ] **Step 1: Add holo imports**

```tsx
import { HoloButton, HoloInput, HoloPill, HoloText, HoloCard } from '@/components/holo'
```

- [ ] **Step 2: Add page hero**

```tsx
<div style={{ marginBottom: 24 }}>
  <div className="holo-section-label" style={{ marginBottom: 6 }}>SYSTEM / MIGRATION</div>
  <h1 style={{ fontSize: 40, fontWeight: 700, margin: '0 0 4px', letterSpacing: '-0.04em', lineHeight: 1 }}>
    <HoloText>Migration</HoloText>
  </h1>
  <p style={{ fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 }}>Import artifacts from Nexus Repository</p>
</div>
```

- [ ] **Step 3: Apply systematic replacements**

Inline card styles → `<HoloCard>`, buttons → `<HoloButton>`, inputs → `<HoloInput>`, progress/status badges → `<HoloPill tone="success|warn|danger">`, color references → holo tokens.

- [ ] **Step 4: Verify**

```bash
cd frontend && npx tsc --noEmit
```

- [ ] **Step 5: Final full verification — run dev server**

```bash
cd frontend && npm run dev
```

Check all routes:
1. `/login` — holographic background, glass card, gradient top edge, animated gradient submit button
2. `/repositories` — holographic sidebar active state, TiltCard on repo cards, CountUp on numbers
3. `/browse` — TiltCard on artifact cards, holo-table for file listings
4. `/search` — flat HoloCard results, HoloPill badges
5. `/users` — holo-table, HoloPill role chips
6. `/security` — HoloTabs, holo-table
7. `/admin` — HoloTabs (blobs/backup/monitoring), HoloCard sections
8. `/audit` — holo-table, HoloPill action badges, Export still works (fetch+blob)
9. `/cleanup` — HoloCard policy cards, HoloPill format badges
10. `/migration` — HoloCard config sections, HoloPill status badges

Also verify:
- OIDC logout flow works (calls `/api/v1/auth/oidc/logout` then redirects)
- API token CRUD in ProfileModal works
- No console errors on any page

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/MigrationPage.tsx
git commit -m "feat(ui): migrate MigrationPage to holo-kit"
```

- [ ] **Step 7: Final commit tagging the migration complete**

```bash
git add -A
git commit -m "feat(ui): complete holo-kit UI migration — all pages holographic depth"
```
