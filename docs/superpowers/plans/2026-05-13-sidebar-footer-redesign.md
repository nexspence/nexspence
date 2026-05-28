# Sidebar Footer Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the cluttered sidebar footer (Documentation link + user block + sign out + version + collapse button) with a clean command bar and move Documentation into the nav as a dedicated DOCS section.

**Architecture:** Pure CSS + JSX change in `Layout.tsx` / `Layout.module.css`. No backend changes. No new files — all changes in the two existing files. The `navScrollArea` wrapper introduced in the recent sticky-fix session stays intact.

**Tech Stack:** React 18, TypeScript strict, CSS Modules

**Spec:** `docs/superpowers/specs/2026-05-13-sidebar-footer-redesign.md`

---

### Task 1: Add new CSS classes

**Files:**
- Modify: `frontend/src/components/Layout.module.css`

- [ ] **Step 1: Add DOCS section label modifier**

In `Layout.module.css`, after the existing `.sectionLabel` block, add:

```css
.sectionLabelDocs {
  color: rgba(34,211,238,0.45);
}
```

- [ ] **Step 2: Add docs nav item modifier**

After the `.navBtn.active` block, add:

```css
.navBtnDocs {
  background: rgba(34,211,238,0.04);
  border-color: rgba(34,211,238,0.12);
  color: rgba(34,211,238,0.8);
}

.navBtnDocs:hover {
  background: rgba(34,211,238,0.08);
  color: rgba(34,211,238,0.95);
}

.navBtnDocs.active {
  background: rgba(34,211,238,0.12);
  border-color: rgba(34,211,238,0.35);
  color: rgba(34,211,238,1);
}
```

- [ ] **Step 3: Add command bar styles**

After the `.divider` block, add:

```css
.commandBar {
  display: flex;
  align-items: center;
  height: 34px;
  border-radius: 9px;
  border: 1px solid rgba(124,92,255,0.22);
  background: rgba(124,92,255,0.05);
  overflow: hidden;
  margin-top: 8px;
  flex-shrink: 0;
  transition: justify-content 0.2s;
}

.commandBarAvatar {
  width: 34px;
  height: 34px;
  background: linear-gradient(135deg, #7c5cff, #22d3ee);
  border: none;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  font-weight: 700;
  color: #fff;
  flex-shrink: 0;
  cursor: pointer;
  transition: opacity 0.15s;
}

.commandBarAvatar:hover {
  opacity: 0.85;
}

.commandBarUser {
  flex: 1;
  padding: 0 8px;
  min-width: 0;
  overflow: hidden;
  transition: opacity 0.15s, max-width 0.2s cubic-bezier(0.23, 1, 0.32, 1), padding 0.2s;
}

.commandBarUserName {
  font-size: 11.5px;
  font-weight: 600;
  color: var(--holo-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  display: block;
}

.commandBarUserRole {
  font-size: 9px;
  color: rgba(124,92,255,0.9);
  display: block;
}

.commandBarSep {
  width: 1px;
  height: 20px;
  background: rgba(124,92,255,0.22);
  flex-shrink: 0;
  transition: opacity 0.15s, max-width 0.15s;
}

.commandBarAction {
  width: 32px;
  height: 34px;
  border: none;
  background: transparent;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  cursor: pointer;
  color: rgba(229,231,235,0.4);
  transition: color 0.15s, background 0.15s, opacity 0.15s, max-width 0.15s;
}

.commandBarAction:hover {
  color: rgba(229,231,235,0.8);
  background: rgba(255,255,255,0.05);
}

.commandBarActionDanger {
  background: rgba(239,68,68,0.05);
  color: rgba(239,68,68,0.65);
}

.commandBarActionDanger:hover {
  background: rgba(239,68,68,0.12);
  color: rgba(239,68,68,0.9);
}
```

- [ ] **Step 4: Add collapse handle styles**

After the `.commandBarActionDanger` block, add:

```css
.collapseHandle {
  height: 3px;
  border-radius: 2px;
  background: rgba(124,92,255,0.13);
  margin: 4px 20px 0;
  position: relative;
  overflow: hidden;
  flex-shrink: 0;
  cursor: pointer;
  transition: margin 0.2s cubic-bezier(0.23, 1, 0.32, 1), background 0.15s;
}

.collapseHandle::after {
  content: '';
  position: absolute;
  left: 50%;
  transform: translateX(-50%);
  width: 30px;
  height: 100%;
  background: rgba(124,92,255,0.42);
  border-radius: 2px;
  transition: background 0.15s;
}

.collapseHandle:hover {
  background: rgba(124,92,255,0.2);
}

.collapseHandle:hover::after {
  background: rgba(124,92,255,0.65);
}
```

- [ ] **Step 5: Add collapsed-state overrides for command bar**

Find the `.root.collapsed .sidebar` block area and add after it:

```css
.root.collapsed .commandBar {
  justify-content: center;
}

.root.collapsed .commandBarUser {
  opacity: 0;
  max-width: 0;
  padding: 0;
}

.root.collapsed .commandBarSep {
  opacity: 0;
  max-width: 0;
}

.root.collapsed .commandBarAction {
  opacity: 0;
  max-width: 0;
}

.root.collapsed .collapseHandle {
  margin: 4px 6px 0;
}
```

- [ ] **Step 6: Update `.version` to match spec**

Find the existing `.version` rule and replace with:

```css
.version {
  text-align: center;
  font-size: 8px;
  color: rgba(229,231,235,0.17);
  padding: 3px 0 2px;
  overflow: hidden;
  max-height: 20px;
  transition: opacity 0.15s, max-height 0.2s, padding 0.2s;
}
```

The existing `.root.collapsed .version` rule stays unchanged (already hides it correctly).

- [ ] **Step 7: Verify TypeScript build passes**

```bash
cd frontend && npx tsc --noEmit
```

Expected: no errors (CSS-only change, no TS involved yet).

- [ ] **Step 8: Commit**

```bash
git add frontend/src/components/Layout.module.css
git commit -m "style(sidebar): add command bar, docs section, collapse handle CSS"
```

---

### Task 2: Restructure Layout.tsx

**Files:**
- Modify: `frontend/src/components/Layout.tsx`

- [ ] **Step 1: Update imports — remove unused icons, keep needed ones**

Find the lucide-react import line:

```tsx
import {
  Home, Search, FolderOpen, Trash2,
  Settings, Shield, FileText, LogOut,
  Key, Plus, X, Copy, Check,
  ChevronLeft, ChevronRight, BookOpen,
} from 'lucide-react'
```

Replace with (remove `ChevronLeft`, `ChevronRight`; keep everything else):

```tsx
import {
  Home, Search, FolderOpen, Trash2,
  Settings, Shield, FileText, LogOut,
  Key, Plus, X, Copy, Check,
  BookOpen,
} from 'lucide-react'
```

- [ ] **Step 2: Move Documentation NavLink into navScrollArea, below system nav**

Inside the `<div className={styles.navScrollArea}>`, after the `{admin && (...)}` block, add the DOCS section:

```tsx
          {/* Docs */}
          <hr className={styles.divider} />
          <span className={`${styles.sectionLabel} ${styles.sectionLabelDocs}`}>Docs</span>
          <nav className={styles.nav}>
            <NavLink
              to="/docs"
              title={collapsed ? 'Documentation' : undefined}
              className={({ isActive }) =>
                `${styles.navBtn} ${styles.navBtnDocs}${isActive ? ' ' + styles.active : ''}`
              }
            >
              <BookOpen size={16} />
              <span className={styles.navLabel}>Documentation</span>
            </NavLink>
          </nav>
```

- [ ] **Step 3: Replace the entire footer div with the command bar**

Find and delete the existing `{/* Footer */}` block (everything from `<div className={styles.footer}>` to its closing `</div>`).

Replace with:

```tsx
        {/* Command bar */}
        {user && (
          <div className={styles.commandBar}>
            <button
              className={styles.commandBarAvatar}
              onClick={() => setProfileOpen(true)}
              title="Profile"
            >
              {(user.firstName || user.username || '?')[0].toUpperCase()}
            </button>
            <div className={styles.commandBarUser}>
              <span className={styles.commandBarUserName}>
                {user.firstName || user.username}
              </span>
              <span className={styles.commandBarUserRole}>
                {user.roles?.includes('nx-admin') ? 'Admin' : user.roles?.length === 0 ? 'No access' : 'User'}
              </span>
            </div>
            <div className={styles.commandBarSep} />
            <button
              className={styles.commandBarAction}
              onClick={() => setProfileOpen(true)}
              title="API Tokens & Profile"
            >
              <Key size={12} />
            </button>
            <div className={styles.commandBarSep} />
            <button
              className={`${styles.commandBarAction} ${styles.commandBarActionDanger}`}
              title="Sign Out"
              onClick={async () => {
                if (isOIDC()) {
                  try {
                    const res = await apiClient.get('/api/v1/auth/oidc/logout')
                    logout()
                    window.location.href = res.data.logout_url
                  } catch {
                    logout()
                  }
                } else {
                  logout()
                }
              }}
            >
              <LogOut size={12} />
            </button>
          </div>
        )}
        <span className={styles.version}>Nexspence v{systemInfo?.version ?? '…'}</span>
        <div
          className={styles.collapseHandle}
          onClick={toggleCollapse}
          role="button"
          tabIndex={0}
          title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          onKeyDown={e => (e.key === 'Enter' || e.key === ' ') && toggleCollapse()}
        />
```

- [ ] **Step 4: Verify TypeScript — no errors**

```bash
cd frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/Layout.tsx
git commit -m "feat(sidebar): command bar footer, Documentation moved to DOCS nav section"
```

---

### Task 3: Remove dead CSS

**Files:**
- Modify: `frontend/src/components/Layout.module.css`

- [ ] **Step 1: Identify and remove unused classes**

The following CSS classes are no longer referenced in `Layout.tsx` after Task 2:

- `.footer`
- `.collapseBtn` and `.collapseBtn:hover`
- `.userInfo` and `.root.collapsed .userInfo`
- `.userInfoText` and `.root.collapsed .userInfoText`
- `.userName`
- `.userRole`
- `.danger` and `.danger:hover`
- `.profileBtn:focus-visible`

Remove each of these blocks from `Layout.module.css`.

- [ ] **Step 2: Verify no broken class references**

```bash
grep -n "styles\." frontend/src/components/Layout.tsx | grep -E "footer|collapseBtn|userInfo|userInfoText|userName|userRole|\.danger|profileBtn"
```

Expected: no output (all removed classes no longer referenced).

- [ ] **Step 3: TypeScript build — final check**

```bash
cd frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/Layout.module.css
git commit -m "style(sidebar): remove dead CSS after footer refactor"
```

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Task |
|---|---|
| Documentation → DOCS nav section with cyan label | Task 2, Step 2 |
| Documentation nav item cyan styling | Task 1, Step 2 |
| Command bar: avatar + name/role + sep + key + sep + logout | Task 2, Step 3 |
| Avatar click → ProfileModal | Task 2, Step 3 |
| Key icon click → ProfileModal | Task 2, Step 3 |
| Logout icon red, OIDC-aware | Task 2, Step 3 |
| Collapsed: command bar → avatar only | Task 1, Step 5 |
| Version text 8px, rgba(.17) | Task 1, Step 6 |
| Version hidden in collapsed mode | Already exists in CSS (`.root.collapsed .version`) |
| Collapse handle strip (3px, pill) | Task 1, Step 4 + Task 2, Step 3 |
| Handle margin expanded vs collapsed | Task 1, Step 5 |
| Remove old collapse button | Task 2, Step 3 (footer replacement) + Task 3 |
| ChevronLeft/Right removed from imports | Task 2, Step 1 |
| Dead CSS removed | Task 3 |

All spec requirements covered. ✓
