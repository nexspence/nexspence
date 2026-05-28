# Phase 13: Select Component Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace all 10 native `<select>` elements (and the ad-hoc `RepoSelect` component) with a single reusable `Select` component matching the dark glassmorphism theme from `example_drop_down.jpg`.

**Architecture:** One new file `frontend/src/components/Select.tsx` exports a `Select` component and `SelectOption` type. Each page imports and uses it directly — no wrapper components needed. The `badge` and `tag` fields on `SelectOption` accept `ReactNode` so Browse can pass coloured format/type chips.

**Tech Stack:** React 18, TypeScript strict, lucide-react (ChevronDown already used app-wide), inline styles (project convention).

---

## File Map

| Action | File |
|---|---|
| **Create** | `frontend/src/components/Select.tsx` |
| **Modify** | `frontend/src/pages/BrowsePage.tsx` — remove `RepoSelect`, import `Select` |
| **Modify** | `frontend/src/pages/RepositoriesPage.tsx` — toolbar filter + create modal (format + type) |
| **Modify** | `frontend/src/pages/RepositoriesPage.module.css` — remove obsolete `.selectWrapper/.select/.selectIcon` |
| **Modify** | `frontend/src/pages/SearchPage.tsx` — format filter |
| **Modify** | `frontend/src/pages/AuditPage.tsx` — domain + action filters |
| **Modify** | `frontend/src/pages/CleanupPage.tsx` — format |
| **Modify** | `frontend/src/pages/SecurityPage.tsx` — content selector |
| **Modify** | `frontend/src/pages/UsersPage.tsx` — status |

---

## Task 1: Create `Select.tsx`

**Files:**
- Create: `frontend/src/components/Select.tsx`

- [ ] **Step 1: Write the component**

```tsx
// frontend/src/components/Select.tsx
import { CSSProperties, ReactNode, useEffect, useRef, useState } from 'react'
import { ChevronDown } from 'lucide-react'

export interface SelectOption {
  value: string
  label: string
  badge?: ReactNode
  tag?: ReactNode
}

interface SelectProps {
  options: SelectOption[]
  value: string
  onChange: (value: string) => void
  placeholder?: string
  disabled?: boolean
  style?: CSSProperties
}

export function Select({
  options,
  value,
  onChange,
  placeholder = '— Select —',
  disabled,
  style,
}: SelectProps) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const selected = options.find(o => o.value === value)

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div ref={ref} style={{ position: 'relative', ...style }}>
      <button
        type="button"
        disabled={disabled}
        onClick={() => !disabled && setOpen(v => !v)}
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          width: '100%',
          padding: '10px 14px',
          background: open ? 'rgba(20,35,70,0.9)' : 'rgba(15,20,40,0.8)',
          border: `1.5px solid ${open ? '#3b82f6' : 'rgba(255,255,255,0.1)'}`,
          borderRadius: open ? '8px 8px 0 0' : 8,
          boxShadow: open ? '0 0 0 3px rgba(59,130,246,0.12)' : 'none',
          color: selected ? '#e5e7eb' : 'rgba(229,231,235,0.35)',
          fontSize: 13,
          cursor: disabled ? 'not-allowed' : 'pointer',
          opacity: disabled ? 0.5 : 1,
          outline: 'none',
          textAlign: 'left' as const,
          transition: 'border-color 0.15s, background 0.15s',
        }}
      >
        <span style={{ flex: 1 }}>{selected ? selected.label : placeholder}</span>
        {selected?.badge}
        {selected?.tag}
        <ChevronDown
          size={14}
          style={{
            color: 'rgba(229,231,235,0.4)',
            flexShrink: 0,
            transform: open ? 'rotate(180deg)' : 'none',
            transition: 'transform 0.2s',
          }}
        />
      </button>

      {open && (
        <div
          style={{
            position: 'absolute',
            top: '100%',
            left: 0,
            right: 0,
            background: 'rgba(8,13,28,0.98)',
            border: '1.5px solid #3b82f6',
            borderTop: 'none',
            borderRadius: '0 0 8px 8px',
            boxShadow: '0 12px 40px rgba(0,0,0,0.6)',
            zIndex: 200,
            maxHeight: 260,
            overflowY: 'auto' as const,
          }}
        >
          {options.length === 0 && (
            <div style={{ padding: '10px 14px', fontSize: 13, color: 'rgba(229,231,235,0.35)' }}>
              No options
            </div>
          )}
          {options.map(opt => {
            const isSelected = opt.value === value
            return (
              <div
                key={opt.value}
                onClick={() => { onChange(opt.value); setOpen(false) }}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 8,
                  padding: '10px 14px',
                  cursor: 'pointer',
                  fontSize: 13,
                  color: isSelected ? '#93c5fd' : '#e5e7eb',
                  background: isSelected ? 'rgba(59,130,246,0.15)' : 'transparent',
                  borderBottom: '1px solid rgba(255,255,255,0.04)',
                  transition: 'background 0.1s',
                }}
                onMouseEnter={e => {
                  if (!isSelected) (e.currentTarget as HTMLDivElement).style.background = 'rgba(59,130,246,0.10)'
                }}
                onMouseLeave={e => {
                  if (!isSelected) (e.currentTarget as HTMLDivElement).style.background = 'transparent'
                }}
              >
                <span style={{ flex: 1 }}>{opt.label}</span>
                {opt.badge}
                {opt.tag}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Type-check**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors (new file only adds exports, nothing breaks yet).

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/Select.tsx
git commit -m "feat: add Select component — dark glassmorphism dropdown"
```

---

## Task 2: BrowsePage — replace `RepoSelect`

**Files:**
- Modify: `frontend/src/pages/BrowsePage.tsx`

`RepoSelect` is a page-local component (lines ~702–834). We delete it and use `Select` with `badge`/`tag` options built from the repos array.

- [ ] **Step 1: Add import at top of BrowsePage.tsx**

Find the existing imports block and add:
```tsx
import { Select, SelectOption } from '@/components/Select'
```

- [ ] **Step 2: Build the options array**

In `BrowsePage()` component body, after the `repos` variable is derived from `useQuery`, add:

```tsx
const repoOptions: SelectOption[] = (repos ?? []).map(r => ({
  value: r.name,
  label: r.name,
  badge: (
    <span style={{
      fontSize: 10, fontWeight: 600, padding: '1px 6px', borderRadius: 3,
      background: (FORMAT_COLORS[r.format] ?? '#6b7280') + '22',
      color: FORMAT_COLORS[r.format] ?? '#6b7280',
      flexShrink: 0,
    }}>
      {r.format}
    </span>
  ),
  tag: (
    <span style={{ fontSize: 10, color: 'rgba(229,231,235,0.35)', flexShrink: 0 }}>
      {r.type}
    </span>
  ),
}))
```

- [ ] **Step 3: Replace `<RepoSelect>` usage in JSX**

Find the usage:
```tsx
<RepoSelect
  repos={repos}
  value={repoName}
  onChange={setRepoName}
/>
```

Replace with:
```tsx
<Select
  options={repoOptions}
  value={repoName}
  onChange={setRepoName}
  placeholder="— Select repository —"
  style={{ minWidth: 240 }}
/>
```

- [ ] **Step 4: Delete the `RepoSelect` function**

Delete the entire `function RepoSelect(...)` block (the local component, ~130 lines).

- [ ] **Step 5: Type-check**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/BrowsePage.tsx
git commit -m "feat: replace RepoSelect with shared Select component in BrowsePage"
```

---

## Task 3: RepositoriesPage — toolbar filter + create modal

**Files:**
- Modify: `frontend/src/pages/RepositoriesPage.tsx`
- Modify: `frontend/src/pages/RepositoriesPage.module.css`

Three `<select>` elements: toolbar format filter, create modal format, create modal type.

- [ ] **Step 1: Add import**

```tsx
import { Select } from '@/components/Select'
```

- [ ] **Step 2: Replace toolbar format filter**

Find:
```tsx
<div className={styles.selectWrapper}>
  <select
    className={styles.select}
    value={formatFilter}
    onChange={e => setFormatFilter(e.target.value)}
  >
    <option value="">All formats</option>
    {['maven2','npm','docker','pypi','go','nuget','helm','raw','apt','yum'].map(f => (
      <option key={f} value={f}>{f}</option>
    ))}
  </select>
  <ChevronDown size={14} className={styles.selectIcon} />
</div>
```

Replace with:
```tsx
<Select
  options={[
    { value: '', label: 'All formats' },
    ...['maven2','npm','docker','pypi','go','nuget','helm','raw','apt','yum'].map(f => ({ value: f, label: f })),
  ]}
  value={formatFilter}
  onChange={setFormatFilter}
  style={{ minWidth: 140 }}
/>
```

- [ ] **Step 3: Replace create modal Format select**

Find:
```tsx
<select
  className={styles.input}
  value={form.format}
  onChange={e => handleFormatChange(e.target.value)}
>
  {['maven2','npm','docker','pypi','go','nuget','helm','raw','apt','yum','cargo','conan'].map(f => (
    <option key={f} value={f}>{f}</option>
  ))}
</select>
```

Replace with:
```tsx
<Select
  options={['maven2','npm','docker','pypi','go','nuget','helm','raw','apt','yum','cargo','conan'].map(f => ({ value: f, label: f }))}
  value={form.format}
  onChange={handleFormatChange}
/>
```

- [ ] **Step 4: Replace create modal Type select**

Find:
```tsx
<select
  className={styles.input}
  value={form.type}
  onChange={e => {
    const t = e.target.value
    setForm(f => ({
      ...f,
      type: t,
      remoteUrl: t === 'proxy' ? (PROXY_DEFAULTS[f.format] ?? '') : '',
    }))
  }}
>
  <option value="hosted">Hosted — store artifacts locally</option>
  <option value="proxy">Proxy — cache from remote registry</option>
  <option value="group">Group — combine multiple repos</option>
</select>
```

Replace with:
```tsx
<Select
  options={[
    { value: 'hosted', label: 'Hosted — store artifacts locally' },
    { value: 'proxy',  label: 'Proxy — cache from remote registry' },
    { value: 'group',  label: 'Group — combine multiple repos' },
  ]}
  value={form.type}
  onChange={t => setForm(f => ({
    ...f,
    type: t,
    remoteUrl: t === 'proxy' ? (PROXY_DEFAULTS[f.format] ?? '') : '',
  }))}
/>
```

- [ ] **Step 5: Remove obsolete CSS**

In `RepositoriesPage.module.css`, delete the three rules:

```css
.selectWrapper { ... }   /* ~5 lines */
.select { ... }          /* ~12 lines */
.select:focus { ... }    /* ~3 lines */
.select option { ... }   /* ~3 lines */
.selectIcon { ... }      /* ~5 lines */
```

- [ ] **Step 6: Remove `ChevronDown` from lucide import if unused**

Check if `ChevronDown` is still referenced anywhere in `RepositoriesPage.tsx` after the replacement. If not, remove it from the import line.

- [ ] **Step 7: Type-check**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/pages/RepositoriesPage.tsx frontend/src/pages/RepositoriesPage.module.css
git commit -m "feat: replace native selects with Select component in RepositoriesPage"
```

---

## Task 4: SearchPage — format filter

**Files:**
- Modify: `frontend/src/pages/SearchPage.tsx`

- [ ] **Step 1: Add import**

```tsx
import { Select } from '@/components/Select'
```

- [ ] **Step 2: Replace the format select**

Find:
```tsx
<div style={S.selectWrap}>
  <select style={S.select} value={filters.format} onChange={set('format')}>
    <option value="">any</option>
    {['maven2','npm','docker','pypi','go','nuget','helm','raw','apt','yum'].map(f => (
      <option key={f} value={f}>{f}</option>
    ))}
  </select>
  <ChevronDown size={13} style={S.selectIcon} />
</div>
```

Replace with (keep same `<div style={S.field}>` wrapper, just replace the inner part):
```tsx
<Select
  options={[
    { value: '', label: 'any' },
    ...['maven2','npm','docker','pypi','go','nuget','helm','raw','apt','yum'].map(f => ({ value: f, label: f })),
  ]}
  value={filters.format}
  onChange={v => setFilters(f => ({ ...f, format: v }))}
/>
```

- [ ] **Step 3: Remove unused styles**

In the `S` object, delete:
```ts
selectWrap: { position: 'relative' as const },
select: { ... },      // appearance, background, border, etc.
selectIcon: { ... },
```

- [ ] **Step 4: Type-check**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/SearchPage.tsx
git commit -m "feat: replace format select with Select component in SearchPage"
```

---

## Task 5: AuditPage — domain + action filters

**Files:**
- Modify: `frontend/src/pages/AuditPage.tsx`

The existing code uses a `handleFilter` helper that wraps `ChangeEvent<HTMLSelectElement>`. With `Select`, `onChange` receives a string directly — simplify the handler.

- [ ] **Step 1: Add import**

```tsx
import { Select } from '@/components/Select'
```

- [ ] **Step 2: Remove the `handleFilter` helper**

Delete:
```tsx
const handleFilter = (setter: (v: string) => void) => (e: React.ChangeEvent<HTMLSelectElement>) => {
  setter(e.target.value)
  setOffset(0)
}
```

- [ ] **Step 3: Replace both selects**

Find:
```tsx
<select style={S.select} value={domain} onChange={handleFilter(setDomain)}>
  {DOMAINS.map(d => <option key={d} value={d}>{d || 'All domains'}</option>)}
</select>
<select style={S.select} value={action} onChange={handleFilter(setAction)}>
  {ACTIONS.map(a => <option key={a} value={a}>{a || 'All actions'}</option>)}
</select>
```

Replace with:
```tsx
<Select
  options={DOMAINS.map(d => ({ value: d, label: d || 'All domains' }))}
  value={domain}
  onChange={v => { setDomain(v); setOffset(0) }}
  style={{ minWidth: 160 }}
/>
<Select
  options={ACTIONS.map(a => ({ value: a, label: a || 'All actions' }))}
  value={action}
  onChange={v => { setAction(v); setOffset(0) }}
  style={{ minWidth: 140 }}
/>
```

- [ ] **Step 4: Remove `S.select` from the styles object**

Delete the `select:` entry from the `S` const if it exists. (If `S.select` is only used for these two dropdowns, delete it.)

- [ ] **Step 5: Type-check**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/AuditPage.tsx
git commit -m "feat: replace domain/action selects with Select component in AuditPage"
```

---

## Task 6: CleanupPage — format

**Files:**
- Modify: `frontend/src/pages/CleanupPage.tsx`

- [ ] **Step 1: Add import**

```tsx
import { Select } from '@/components/Select'
```

- [ ] **Step 2: Replace format select**

Find:
```tsx
<select style={S.select} value={form.format} onChange={set('format')}>
  {FORMATS.map(f => <option key={f} value={f}>{f === '*' ? 'All formats' : f}</option>)}
</select>
```

Replace with:
```tsx
<Select
  options={FORMATS.map(f => ({ value: f, label: f === '*' ? 'All formats' : f }))}
  value={form.format}
  onChange={v => setForm(f => ({ ...f, format: v }))}
/>
```

- [ ] **Step 3: Remove `S.select` if unused**

Check the `S` const — if `select:` is only used for this one dropdown, delete it.

- [ ] **Step 4: Type-check**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/CleanupPage.tsx
git commit -m "feat: replace format select with Select component in CleanupPage"
```

---

## Task 7: SecurityPage — content selector

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx`

The content selector dropdown also shows a CEL expression preview and an error when no selectors exist — these stay unchanged (they're below the dropdown).

- [ ] **Step 1: Add import**

```tsx
import { Select } from '@/components/Select'
```

- [ ] **Step 2: Replace content selector select**

Find:
```tsx
<select
  style={{ ...S.input }}
  value={form.contentSelectorId}
  onChange={e => setForm(f => ({ ...f, contentSelectorId: e.target.value }))}
>
  <option value="">— select a content selector —</option>
  {selectors.map(s => (
    <option key={s.id} value={s.id}>{s.name}</option>
  ))}
</select>
```

Replace with:
```tsx
<Select
  options={[
    { value: '', label: '— select a content selector —' },
    ...selectors.map(s => ({ value: s.id, label: s.name })),
  ]}
  value={form.contentSelectorId}
  onChange={v => setForm(f => ({ ...f, contentSelectorId: v }))}
/>
```

The CEL preview block and "no selectors" error below the dropdown stay unchanged.

- [ ] **Step 3: Type-check**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx
git commit -m "feat: replace content selector select with Select component in SecurityPage"
```

---

## Task 8: UsersPage — status

**Files:**
- Modify: `frontend/src/pages/UsersPage.tsx`

The status dropdown uses `className={styles.input}` (CSS module). With `Select`, we drop the className and let the component style itself.

- [ ] **Step 1: Add import**

```tsx
import { Select } from '@/components/Select'
```

- [ ] **Step 2: Replace status select**

Find:
```tsx
<select className={styles.input} value={form.status} onChange={e => setForm(f => ({ ...f, status: e.target.value }))}>
  <option value="active">Active</option>
  <option value="disabled">Disabled</option>
</select>
```

Replace with:
```tsx
<Select
  options={[
    { value: 'active',   label: 'Active' },
    { value: 'disabled', label: 'Disabled' },
  ]}
  value={form.status}
  onChange={v => setForm(f => ({ ...f, status: v }))}
/>
```

- [ ] **Step 3: Type-check**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/UsersPage.tsx
git commit -m "feat: replace status select with Select component in UsersPage"
```

---

## Task 9: Final verification

- [ ] **Step 1: Full type-check**

```bash
cd frontend && npx tsc --noEmit
```

Expected: 0 errors.

- [ ] **Step 2: Production build**

```bash
cd frontend && npm run build 2>&1 | tail -10
```

Expected: Build succeeded, no errors. Bundle size should be similar to before.

- [ ] **Step 3: Verify no native `<select>` remains**

```bash
grep -r '<select' frontend/src --include='*.tsx' | grep -v node_modules
```

Expected: empty output (no `<select>` elements remain).

- [ ] **Step 4: Update task_plan.md**

In `task_plan.md`, mark Phase 13 as `complete` and add completed tasks checkmarks.

- [ ] **Step 5: Update progress.md**

Add session entry for Phase 13 completion.

- [ ] **Step 6: Final commit**

```bash
git add task_plan.md progress.md
git commit -m "docs: mark Phase 13 complete — unified Select component across app"
```
