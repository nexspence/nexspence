# UI Bug-fix & Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix 7 UI areas: unified Glass Pill dropdowns, repositories list view, Browse ghost buttons, Search hover, Security table/modal fixes, Admin Info tab, Profile token layout.

**Architecture:** Pure frontend changes — React components and CSS only. No API, routing, or state logic changes. Task 1 (Select portal) is a prerequisite for Task 6 (Security modal dropdown clip fix). All other tasks are independent.

**Tech Stack:** React 18, TypeScript, Vite, holo-kit CSS (`frontend/src/holo-kit/holo.css`, `holo.tsx`), lucide-react icons, `ReactDOM.createPortal`.

**Verify command (after every task):** `cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit`

---

## Task 1: Glass Pill dropdown — `Select.tsx`

**Files:**
- Modify: `frontend/src/components/Select.tsx`

Replaces the rectangular `holo-input` trigger with a pill shape and renders the dropdown via `createPortal` to `document.body` using `position: fixed`, so it never clips inside modals that have `overflow: hidden`.

- [ ] **Step 1: Replace `Select.tsx` entirely**

```tsx
// frontend/src/components/Select.tsx
import { CSSProperties, ReactNode, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
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
  searchable?: boolean
  style?: CSSProperties
}

export function Select({
  options, value, onChange,
  placeholder = '— Select —',
  disabled, searchable, style,
}: SelectProps) {
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [dropPos, setDropPos] = useState<{ top: number; left: number; width: number } | null>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const selected = options.find(o => o.value === value)

  function openMenu() {
    if (disabled) return
    if (!open && triggerRef.current) {
      const r = triggerRef.current.getBoundingClientRect()
      setDropPos({ top: r.bottom + 4, left: r.left, width: r.width })
    }
    setOpen(v => !v)
  }

  useEffect(() => {
    if (!open) { setSearch(''); return }
    function close() { setOpen(false) }
    function onKey(e: KeyboardEvent) { if (e.key === 'Escape') setOpen(false) }
    function onScroll() { setOpen(false) }
    document.addEventListener('mousedown', (e) => {
      if (triggerRef.current && !triggerRef.current.contains(e.target as Node)) close()
    })
    document.addEventListener('keydown', onKey)
    window.addEventListener('scroll', onScroll, true)
    return () => {
      document.removeEventListener('mousedown', close)
      document.removeEventListener('keydown', onKey)
      window.removeEventListener('scroll', onScroll, true)
    }
  }, [open])

  const visible = searchable && search.trim()
    ? options.filter(o => o.label.toLowerCase().includes(search.toLowerCase()))
    : options

  const triggerStyle: CSSProperties = {
    display: 'flex', alignItems: 'center', gap: 8,
    width: '100%', padding: '9px 14px',
    background: open ? 'rgba(124,92,255,0.12)' : 'rgba(124,92,255,0.08)',
    border: `1px solid ${open ? 'rgba(124,92,255,0.5)' : 'rgba(124,92,255,0.35)'}`,
    borderRadius: 999,
    boxShadow: open ? '0 0 0 3px rgba(124,92,255,0.12)' : 'none',
    color: selected ? 'var(--holo-text)' : 'var(--holo-text-faint)',
    cursor: disabled ? 'not-allowed' : 'pointer',
    opacity: disabled ? 0.5 : 1,
    textAlign: 'left' as const,
    fontSize: 13,
    ...style,
  }

  const dropdown = open && dropPos ? createPortal(
    <div
      className="holo-card"
      style={{
        position: 'fixed',
        top: dropPos.top,
        left: dropPos.left,
        width: dropPos.width,
        borderRadius: 14,
        zIndex: 9999,
        padding: 6,
        display: 'flex', flexDirection: 'column', gap: 2,
        boxShadow: '0 12px 40px rgba(0,0,0,0.7)',
        maxHeight: 280,
        overflowY: 'auto' as const,
      }}
    >
      {searchable && (
        <div style={{ padding: '4px 0 6px', borderBottom: '1px solid rgba(255,255,255,0.06)', marginBottom: 2 }}>
          <input
            autoFocus
            placeholder="Filter…"
            value={search}
            onChange={e => setSearch(e.target.value)}
            className="holo-input"
            style={{ width: '100%', boxSizing: 'border-box' as const, fontSize: 12, padding: '5px 10px' }}
          />
        </div>
      )}
      {visible.length === 0 && (
        <div style={{ padding: '8px 12px', fontSize: 12, color: 'var(--holo-text-faint)' }}>
          {searchable && search ? 'No matches' : 'No options'}
        </div>
      )}
      {visible.map(opt => {
        const isSel = opt.value === value
        return (
          <div
            key={opt.value}
            onClick={() => { onChange(opt.value); setOpen(false) }}
            style={{
              display: 'flex', alignItems: 'center', gap: 8,
              padding: isSel ? '7px 12px' : '8px 12px',
              cursor: 'pointer', fontSize: 13,
              color: isSel ? '#c4b5fd' : 'var(--holo-text)',
              background: isSel ? 'rgba(124,92,255,0.18)' : 'transparent',
              border: isSel ? '1px solid rgba(124,92,255,0.35)' : '1px solid transparent',
              borderRadius: 10,
              fontWeight: isSel ? 600 : 400,
              transition: 'background 0.1s',
            }}
            onMouseEnter={e => {
              if (!isSel) (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.08)'
            }}
            onMouseLeave={e => {
              (e.currentTarget as HTMLDivElement).style.background = isSel ? 'rgba(124,92,255,0.18)' : 'transparent'
            }}
          >
            {isSel && (
              <span style={{ width: 6, height: 6, borderRadius: '50%', background: '#7c5cff', boxShadow: '0 0 6px #7c5cff', flexShrink: 0, display: 'inline-block' }} />
            )}
            <span style={{ flex: 1 }}>{opt.label}</span>
            {opt.badge}
            {opt.tag}
          </div>
        )
      })}
    </div>,
    document.body,
  ) : null

  return (
    <div style={{ position: 'relative' }}>
      <button type="button" ref={triggerRef} disabled={disabled} onClick={openMenu} style={triggerStyle}>
        <span style={{ flex: 1 }}>
          {selected ? selected.label : placeholder}
        </span>
        {selected?.badge}
        {selected?.tag}
        <ChevronDown size={14} style={{ color: 'var(--holo-text-faint)', flexShrink: 0, transform: open ? 'rotate(180deg)' : 'none', transition: 'transform 0.2s' }} />
      </button>
      {dropdown}
    </div>
  )
}
```

- [ ] **Step 2: Type-check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git -C /home/skensel/AI/self_nexus add frontend/src/components/Select.tsx
git -C /home/skensel/AI/self_nexus commit -m "feat(ui): Select — Glass Pill trigger + portal dropdown"
```

---

## Task 2: Glass Pill dropdown — `MultiSelect.tsx`

**Files:**
- Modify: `frontend/src/components/MultiSelect.tsx`

Same portal pattern as Task 1. Trigger becomes a pill; dropdown renders via `createPortal`.

- [ ] **Step 1: Replace `MultiSelect.tsx` entirely**

```tsx
// frontend/src/components/MultiSelect.tsx
import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ChevronDown, X } from 'lucide-react'

export interface MultiSelectOption {
  value: string
  label: string
}

interface MultiSelectProps {
  options: MultiSelectOption[]
  value: string[]
  onChange: (values: string[]) => void
  placeholder?: string
}

export function MultiSelect({ options, value, onChange, placeholder = '— Select —' }: MultiSelectProps) {
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [dropPos, setDropPos] = useState<{ top: number; left: number; width: number } | null>(null)
  const triggerRef = useRef<HTMLDivElement>(null)

  function openMenu() {
    if (!open && triggerRef.current) {
      const r = triggerRef.current.getBoundingClientRect()
      setDropPos({ top: r.bottom + 4, left: r.left, width: r.width })
    }
    setOpen(v => !v)
  }

  useEffect(() => {
    if (!open) { setSearch(''); return }
    function close(e: MouseEvent) {
      if (triggerRef.current && !triggerRef.current.contains(e.target as Node)) setOpen(false)
    }
    function onKey(e: KeyboardEvent) { if (e.key === 'Escape') setOpen(false) }
    function onScroll() { setOpen(false) }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', onKey)
    window.addEventListener('scroll', onScroll, true)
    return () => {
      document.removeEventListener('mousedown', close)
      document.removeEventListener('keydown', onKey)
      window.removeEventListener('scroll', onScroll, true)
    }
  }, [open])

  const filtered = options.filter(o => o.label.toLowerCase().includes(search.toLowerCase()))
  const allSelected = filtered.length > 0 && filtered.every(o => value.includes(o.value))

  function toggle(v: string) {
    onChange(value.includes(v) ? value.filter(x => x !== v) : [...value, v])
  }
  function toggleAll() {
    if (allSelected) onChange(value.filter(v => !filtered.some(o => o.value === v)))
    else onChange([...value, ...filtered.map(o => o.value).filter(v => !value.includes(v))])
  }

  const selectedEntries = value.map(v => ({ value: v, label: options.find(o => o.value === v)?.label ?? v }))

  const dropdown = open && dropPos ? createPortal(
    <div
      className="holo-card"
      style={{
        position: 'fixed',
        top: dropPos.top,
        left: dropPos.left,
        width: dropPos.width,
        borderRadius: 14,
        zIndex: 9999,
        padding: 0,
        display: 'flex', flexDirection: 'column',
        boxShadow: '0 12px 40px rgba(0,0,0,0.7)',
        maxHeight: 260,
      }}
    >
      <div style={{ padding: '6px 8px', borderBottom: '1px solid rgba(255,255,255,0.06)', flexShrink: 0 }}>
        <input
          autoFocus
          placeholder="Filter…"
          value={search}
          onChange={e => setSearch(e.target.value)}
          onClick={e => e.stopPropagation()}
          className="holo-input"
          style={{ width: '100%', boxSizing: 'border-box' as const, fontSize: 12, padding: '5px 10px' }}
        />
      </div>
      {filtered.length > 0 && (
        <div
          onClick={e => { e.stopPropagation(); toggleAll() }}
          style={{ padding: '6px 14px', fontSize: 12, color: 'var(--holo-a)', cursor: 'pointer', borderBottom: '1px solid rgba(255,255,255,0.06)', flexShrink: 0 }}
        >
          {allSelected ? 'Deselect all' : 'Select all'}
        </div>
      )}
      <div style={{ overflowY: 'auto' as const, flex: 1 }}>
        {filtered.length === 0 ? (
          <div style={{ padding: '8px 14px', fontSize: 13, color: 'var(--holo-text-faint)' }}>No options</div>
        ) : filtered.map(o => {
          const isSel = value.includes(o.value)
          return (
            <div
              key={o.value}
              onClick={e => { e.stopPropagation(); toggle(o.value) }}
              style={{
                padding: isSel ? '7px 14px' : '8px 14px', fontSize: 13, cursor: 'pointer',
                color: isSel ? '#c4b5fd' : 'var(--holo-text)',
                background: isSel ? 'rgba(124,92,255,0.18)' : 'transparent',
                border: isSel ? '1px solid rgba(124,92,255,0.35)' : '1px solid transparent',
                borderRadius: 10, margin: '2px 6px',
                fontWeight: isSel ? 600 : 400,
                display: 'flex', alignItems: 'center', gap: 8,
              }}
              onMouseEnter={e => {
                if (!isSel) (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.08)'
              }}
              onMouseLeave={e => {
                (e.currentTarget as HTMLDivElement).style.background = isSel ? 'rgba(124,92,255,0.18)' : 'transparent'
              }}
            >
              {isSel && <span style={{ width: 6, height: 6, borderRadius: '50%', background: '#7c5cff', boxShadow: '0 0 6px #7c5cff', flexShrink: 0, display: 'inline-block' }} />}
              <span style={{ flex: 1 }}>{o.label}</span>
            </div>
          )
        })}
      </div>
    </div>,
    document.body,
  ) : null

  return (
    <div ref={triggerRef} style={{ position: 'relative', userSelect: 'none' }}>
      <div
        onClick={openMenu}
        style={{
          minHeight: 40, cursor: 'pointer',
          display: 'flex', alignItems: 'flex-start', flexWrap: 'wrap' as const, gap: 4,
          padding: '8px 14px',
          background: open ? 'rgba(124,92,255,0.12)' : 'rgba(124,92,255,0.08)',
          border: `1px solid ${open ? 'rgba(124,92,255,0.5)' : 'rgba(124,92,255,0.35)'}`,
          borderRadius: 999,
          boxShadow: open ? '0 0 0 3px rgba(124,92,255,0.12)' : 'none',
        }}
      >
        {selectedEntries.length === 0 ? (
          <span style={{ color: 'var(--holo-text-faint)', lineHeight: '22px', fontSize: 13 }}>{placeholder}</span>
        ) : selectedEntries.map(entry => (
          <span key={entry.value} style={{
            display: 'flex', alignItems: 'center', gap: 4, padding: '1px 6px',
            background: 'rgba(124,92,255,0.18)', borderRadius: 6, fontSize: 12, color: '#c4b5fd',
            border: '1px solid rgba(124,92,255,0.35)',
          }}>
            {entry.label}
            <X size={10} style={{ cursor: 'pointer' }} onClick={e => { e.stopPropagation(); toggle(entry.value) }} />
          </span>
        ))}
        <ChevronDown size={14} style={{ marginLeft: 'auto', color: 'var(--holo-text-faint)', alignSelf: 'center', flexShrink: 0, transform: open ? 'rotate(180deg)' : 'none', transition: 'transform 0.2s' }} />
      </div>
      {dropdown}
    </div>
  )
}
```

- [ ] **Step 2: Type-check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git -C /home/skensel/AI/self_nexus add frontend/src/components/MultiSelect.tsx
git -C /home/skensel/AI/self_nexus commit -m "feat(ui): MultiSelect — Glass Pill trigger + portal dropdown"
```

---

## Task 3: Repositories — list rows + wider modal

**Files:**
- Modify: `frontend/src/pages/RepositoriesPage.tsx`
- Modify: `frontend/src/pages/RepositoriesPage.module.css`
- Modify: `frontend/src/holo-kit/holo.tsx` (add `style` prop to `HoloModal`)

### Step 3a — Add `style` prop to HoloModal

- [ ] **Step 1: Update `HoloModal` in `holo.tsx`**

Find in `frontend/src/holo-kit/holo.tsx` (line ~140):
```tsx
export function HoloModal({ open, onClose, children }: { open: boolean; onClose: () => void; children: React.ReactNode }) {
  if (!open) return null;
  return (
    <div className="holo-overlay" onClick={onClose}>
      <div className="holo-modal" onClick={e => e.stopPropagation()}>{children}</div>
    </div>
  );
}
```

Replace with:
```tsx
export function HoloModal({ open, onClose, children, style }: { open: boolean; onClose: () => void; children: React.ReactNode; style?: React.CSSProperties }) {
  if (!open) return null;
  return (
    <div className="holo-overlay" onClick={onClose}>
      <div className="holo-modal" style={style} onClick={e => e.stopPropagation()}>{children}</div>
    </div>
  );
}
```

### Step 3b — Replace `.grid` CSS with `.list` / `.row`

- [ ] **Step 2: Replace content of `RepositoriesPage.module.css`**

Keep all existing classes (`.page`, `.form`, `.formRow`, `.hint`, `.memberList`, `.memberItem`, etc.) and only replace `.grid` with:

```css
.list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.row {
  display: grid;
  grid-template-columns: 8px 100px 1fr 110px 80px auto;
  align-items: center;
  gap: 14px;
  padding: 11px 16px;
  background: rgba(10, 8, 28, 0.97);
  border: 1px solid rgba(124, 92, 255, 0.2);
  border-radius: 10px;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
}

.row:hover {
  border-color: rgba(124, 92, 255, 0.45);
  background: rgba(124, 92, 255, 0.04);
}
```

(Remove `.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 16px; }`)

### Step 3c — Replace `RepoCard` with `RepoRow` in the TSX

- [ ] **Step 3: In `RepositoriesPage.tsx`, replace the grid render block**

Find:
```tsx
<div className={styles.grid}>
  {filtered.map(repo => (
    <RepoCard
      key={repo.id}
      repo={repo}
      isAdmin={isAdmin}
      storeName={repo.blobStoreId ? storeNameById.get(repo.blobStoreId) : undefined}
      onClick={() => navigate(`/browse?repo=${repo.name}`)}
      onEdit={() => setEditRepo(repo)}
      onDelete={() => {
        if (confirm(`Delete repository "${repo.name}"?`)) {
          deleteMutation.mutate(repo.name)
        }
      }}
    />
  ))}
</div>
```

Replace with:
```tsx
<div className={styles.list}>
  {filtered.map(repo => (
    <RepoRow
      key={repo.id}
      repo={repo}
      isAdmin={isAdmin}
      storeName={repo.blobStoreId ? storeNameById.get(repo.blobStoreId) : undefined}
      onClick={() => navigate(`/browse?repo=${repo.name}`)}
      onEdit={() => setEditRepo(repo)}
      onDelete={() => {
        if (confirm(`Delete repository "${repo.name}"?`)) {
          deleteMutation.mutate(repo.name)
        }
      }}
    />
  ))}
</div>
```

- [ ] **Step 4: Replace the `RepoCard` component with `RepoRow`**

Find the `function RepoCard(...)` component (starts at ~line 211) and replace the entire function with:

```tsx
function RepoRow({
  repo, isAdmin, storeName, onClick, onEdit, onDelete,
}: {
  repo: Repository
  isAdmin: boolean
  storeName?: string
  onClick?: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  const { data: quota } = useQuery({
    queryKey: ['repoQuota', repo.name],
    queryFn: () => nexspenceApi.getRepositoryQuota(repo.name).then(r => r.data),
    staleTime: 30_000,
  })
  const pct = quota?.percentUsed ?? null

  return (
    <div className={styles.row} onClick={onClick}>
      <span style={{
        width: 7, height: 7, borderRadius: '50%', flexShrink: 0,
        background: repo.online ? 'var(--holo-green)' : 'rgba(255,255,255,0.2)',
        boxShadow: repo.online ? '0 0 5px var(--holo-green)' : 'none',
        display: 'inline-block',
      }} />
      <span style={{
        fontSize: 10, fontWeight: 600, padding: '2px 8px', borderRadius: 4,
        textTransform: 'uppercase' as const, letterSpacing: '0.3px',
        background: (FORMAT_COLORS[repo.format] ?? '#6b7280') + '22',
        color: FORMAT_COLORS[repo.format] ?? '#6b7280',
        whiteSpace: 'nowrap' as const,
      }}>
        {repo.format}
      </span>
      <div style={{ minWidth: 0 }}>
        <div style={{ fontWeight: 600, fontSize: 13, color: 'var(--holo-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const }}>
          {repo.name}
        </div>
        {(repo.description || storeName) && (
          <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const }}>
            {repo.description || (storeName ? `on ${storeName}` : '')}
          </div>
        )}
      </div>
      <HoloPill tone={repo.type === 'hosted' ? 'success' : repo.type === 'proxy' ? 'default' : 'warn'}>
        {TYPE_LABELS[repo.type] ?? repo.type}
      </HoloPill>
      <div>
        <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', fontFamily: 'ui-monospace,monospace', textAlign: 'right' as const }}>
          {quota ? formatBytes(quota.usedBytes) : '—'}
        </div>
        {quota?.quotaBytes != null && (
          <div style={{ height: 3, background: 'rgba(255,255,255,0.08)', borderRadius: 2, overflow: 'hidden', marginTop: 3 }}>
            <div style={{
              height: '100%', borderRadius: 2,
              width: `${Math.min(pct ?? 0, 100)}%`,
              background: (pct ?? 0) >= 90 ? 'var(--holo-red)' : (pct ?? 0) >= 70 ? 'var(--holo-amber)' : 'var(--holo-green)',
            }} />
          </div>
        )}
      </div>
      {isAdmin && (
        <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
          <HoloButton icon={<Settings2 size={14} />} onClick={e => { e.stopPropagation(); onEdit() }} title="Settings" />
          <HoloButton variant="danger" icon={<Trash2 size={14} />} onClick={e => { e.stopPropagation(); onDelete() }} title="Delete" />
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 5: Widen the Create and Edit modals**

In `CreateRepoModal` (around line 432) change:
```tsx
<HoloModal open={true} onClose={onClose}>
```
to:
```tsx
<HoloModal open={true} onClose={onClose} style={{ minWidth: 640 }}>
```

In `EditRepoModal` (around line 701) change:
```tsx
<HoloModal open={true} onClose={onClose}>
```
to:
```tsx
<HoloModal open={true} onClose={onClose} style={{ minWidth: 640 }}>
```

- [ ] **Step 6: Type-check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git -C /home/skensel/AI/self_nexus add frontend/src/holo-kit/holo.tsx frontend/src/pages/RepositoriesPage.tsx frontend/src/pages/RepositoriesPage.module.css
git -C /home/skensel/AI/self_nexus commit -m "feat(ui): Repositories — list rows, no glow, wider create modal"
```

---

## Task 4: Browse — ghost icon action buttons

**Files:**
- Modify: `frontend/src/pages/BrowsePage.tsx`

There are 6 plain `<button style={{ background: 'none', border: 'none', ... }}>` occurrences for Download, Copy link, and Delete actions on tree nodes. Replace them all with a small `GhostBtn` helper component defined at the top of `BrowsePage.tsx`.

- [ ] **Step 1: Add `GhostBtn` component to `BrowsePage.tsx`**

Find the `const S = {` block (line ~420) and add this function immediately after the `S` object (before the first function/component definition):

```tsx
function GhostBtn({ onClick, title, danger = false, children }: {
  onClick: (e: React.MouseEvent) => void
  title?: string
  danger?: boolean
  children: React.ReactNode
}) {
  const [hov, setHov] = useState(false)
  const style: React.CSSProperties = {
    width: 24, height: 24, borderRadius: 6, padding: 0,
    cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
    flexShrink: 0,
    border: danger
      ? `1px solid ${hov ? 'rgba(255,107,107,0.5)' : 'rgba(255,107,107,0.25)'}`
      : `1px solid ${hov ? 'rgba(124,92,255,0.5)' : 'rgba(124,92,255,0.25)'}`,
    background: danger
      ? (hov ? 'rgba(255,107,107,0.18)' : 'rgba(255,107,107,0.07)')
      : (hov ? 'rgba(124,92,255,0.2)' : 'rgba(124,92,255,0.08)'),
    color: danger ? '#ff6b6b' : 'rgba(124,92,255,0.9)',
  }
  return (
    <button type="button" title={title} style={style} onClick={onClick}
      onMouseEnter={() => setHov(true)} onMouseLeave={() => setHov(false)}>
      {children}
    </button>
  )
}
```

- [ ] **Step 2: Replace Download and Copy link buttons on file nodes**

Find (lines ~843–855):
```tsx
<button
  onClick={(e) => { e.stopPropagation(); doDownload() }}
  style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(96,165,250,0.7)', padding: '3px 4px', borderRadius: 4, display: 'flex', alignItems: 'center' }}
  title="Download"
>
  <Download size={12} />
</button>
<button
  onClick={(e) => { e.stopPropagation(); doCopy() }}
  style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(96,165,250,0.7)', padding: '3px 4px', borderRadius: 4, display: 'flex', alignItems: 'center' }}
  title="Copy link"
>
  <Link size={12} />
</button>
```

Replace with:
```tsx
<GhostBtn onClick={e => { e.stopPropagation(); doDownload() }} title="Download">
  <Download size={12} />
</GhostBtn>
<GhostBtn onClick={e => { e.stopPropagation(); doCopy() }} title="Copy link">
  <Link size={12} />
</GhostBtn>
```

- [ ] **Step 3: Replace Delete button on file nodes**

Find (lines ~856–864):
```tsx
{showDelete && onDelete && (
  <button
    onClick={(e) => { e.stopPropagation(); onDelete(node) }}
    style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(239,68,68,0.5)', padding: '3px 4px', borderRadius: 4, display: 'flex', alignItems: 'center' }}
    title="Delete"
  >
    <Trash2 size={12} />
  </button>
)}
```

Replace with:
```tsx
{showDelete && onDelete && (
  <GhostBtn danger onClick={e => { e.stopPropagation(); onDelete(node) }} title="Delete">
    <Trash2 size={12} />
  </GhostBtn>
)}
```

- [ ] **Step 4: Replace the remaining 3 plain buttons (lines ~692–734, ~1436)**

Search for all remaining occurrences of `background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(239,68,68` in `BrowsePage.tsx` and replace each with the appropriate `GhostBtn danger` pattern. Each one looks like:

```tsx
<button
  onClick={e => { e.stopPropagation(); onDelete(node) }}
  style={{ ..., background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(239,68,68,0.6)', ... }}
  title="Delete ..."
>
  <Trash2 size={13} />
</button>
```

Replace with:
```tsx
<GhostBtn danger onClick={e => { e.stopPropagation(); onDelete(node) }} title="Delete ...">
  <Trash2 size={13} />
</GhostBtn>
```

- [ ] **Step 5: Type-check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git -C /home/skensel/AI/self_nexus add frontend/src/pages/BrowsePage.tsx
git -C /home/skensel/AI/self_nexus commit -m "feat(ui): Browse — ghost icon action buttons"
```

---

## Task 5: Search — row hover highlight

**Files:**
- Modify: `frontend/src/pages/SearchPage.tsx`

Add `hoveredCId` state and apply it to the `HoloCard` wrapping each result component.

- [ ] **Step 1: Add `hoveredCId` state**

In `SearchPage` component, find the existing `useState` declarations and add:
```tsx
const [hoveredCId, setHoveredCId] = useState<string | null>(null)
```

- [ ] **Step 2: Apply hover to result `HoloCard`**

Find the `HoloCard` that wraps each result component (around line 378):
```tsx
<HoloCard
  style={{
    padding: 14,
    marginBottom: 8,
    ...(isReturnRow
      ? { outline: '1px solid rgba(59,130,246,0.6)', background: 'rgba(59,130,246,0.08)', transition: 'background 0.6s, outline 0.6s' }
      : {}),
  }}
>
```

Replace with:
```tsx
<HoloCard
  style={{
    padding: 14,
    marginBottom: 8,
    transition: 'border-color 0.15s, background 0.15s',
    ...(isReturnRow
      ? { outline: '1px solid rgba(59,130,246,0.6)', background: 'rgba(59,130,246,0.08)', transition: 'background 0.6s, outline 0.6s' }
      : hoveredCId === c.id
        ? { borderColor: 'rgba(124,92,255,0.4)', background: 'rgba(124,92,255,0.04)' }
        : {}),
  }}
  onMouseEnter={() => setHoveredCId(c.id)}
  onMouseLeave={() => setHoveredCId(null)}
>
```

- [ ] **Step 3: Type-check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git -C /home/skensel/AI/self_nexus add frontend/src/pages/SearchPage.tsx
git -C /home/skensel/AI/self_nexus commit -m "feat(ui): Search — hover highlight on result cards"
```

---

## Task 6: Security — fix Privileges & ContentSelectors tables

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx`

Two bugs: (1) `<td style={{ display: 'flex', ... }}>` on action cells breaks table column widths. (2) No overflow wrapper so wide tables escape their card.

### Fix Privileges table

- [ ] **Step 1: Wrap Privileges table in overflow div + add padding to HoloCard**

Find:
```tsx
<HoloCard style={{ padding: 0 }}>
  <table className="holo-table" style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
    <thead>
      <tr style={{ color: 'var(--holo-text-dim)', textAlign: 'left' as const }}>
        <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Name</th>
        <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Type</th>
        <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Actions</th>
        <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Description</th>
        <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Used in Roles</th>
        {admin && <th style={{ padding: '0 0 10px', fontWeight: 600, width: 80 }}></th>}
      </tr>
    </thead>
```

Replace with:
```tsx
<HoloCard style={{ padding: '0 16px' }}>
  <div style={{ overflowX: 'auto' as const }}>
  <table className="holo-table" style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13, minWidth: 600 }}>
    <thead>
      <tr style={{ color: 'var(--holo-text-dim)', textAlign: 'left' as const }}>
        <th style={{ padding: '12px 0 10px', fontWeight: 600, minWidth: 140 }}>Name</th>
        <th style={{ padding: '12px 8px 10px', fontWeight: 600, minWidth: 130 }}>Type</th>
        <th style={{ padding: '12px 8px 10px', fontWeight: 600, minWidth: 100 }}>Actions</th>
        <th style={{ padding: '12px 8px 10px', fontWeight: 600 }}>Description</th>
        <th style={{ padding: '12px 8px 10px', fontWeight: 600, minWidth: 120 }}>Used in Roles</th>
        {admin && <th style={{ padding: '12px 0 10px', fontWeight: 600, width: 90 }}></th>}
      </tr>
    </thead>
```

Also close the `</div>` before `</HoloCard>`. Find `</table>\n        </HoloCard>` in the Privileges section and replace with `</table>\n        </div>\n        </HoloCard>`.

- [ ] **Step 2: Fix `display: flex` on Privileges action `<td>`**

Find in the Privileges table (line ~764):
```tsx
<td style={{ padding: '9px 0', display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
```

Replace with:
```tsx
<td style={{ padding: '9px 0' }}>
  <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
```

And close the `</div>` before `</td>`:
```tsx
          </>
        )}
      </div>
    </td>
```

### Fix ContentSelectors table

- [ ] **Step 3: Same overflow fix for ContentSelectors table**

Find the ContentSelectors `HoloCard`:
```tsx
<HoloCard style={{ padding: 0 }}>
  <table className="holo-table" style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
    <thead>
      <tr style={{ color: 'var(--holo-text-dim)', textAlign: 'left' as const }}>
        <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Name</th>
        <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Scope</th>
        <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Privilege</th>
        <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Description</th>
        {admin && <th style={{ padding: '0 0 10px', width: 80 }}></th>}
      </tr>
    </thead>
```

Replace with:
```tsx
<HoloCard style={{ padding: '0 16px' }}>
  <div style={{ overflowX: 'auto' as const }}>
  <table className="holo-table" style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13, minWidth: 500 }}>
    <thead>
      <tr style={{ color: 'var(--holo-text-dim)', textAlign: 'left' as const }}>
        <th style={{ padding: '12px 0 10px', fontWeight: 600, minWidth: 140 }}>Name</th>
        <th style={{ padding: '12px 8px 10px', fontWeight: 600, minWidth: 180 }}>Scope</th>
        <th style={{ padding: '12px 8px 10px', fontWeight: 600, minWidth: 130 }}>Privilege</th>
        <th style={{ padding: '12px 8px 10px', fontWeight: 600 }}>Description</th>
        {admin && <th style={{ padding: '12px 0 10px', width: 90 }}></th>}
      </tr>
    </thead>
```

Close `</div>` before `</HoloCard>` in the ContentSelectors section.

- [ ] **Step 4: Fix `display: flex` on ContentSelectors action `<td>`**

Find in ContentSelectors table (line ~1018):
```tsx
<td style={{ padding: '9px 0', display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
```

Replace with:
```tsx
<td style={{ padding: '9px 0' }}>
  <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
```

And add `</div>` before `</td>`.

- [ ] **Step 5: Type-check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git -C /home/skensel/AI/self_nexus add frontend/src/pages/SecurityPage.tsx
git -C /home/skensel/AI/self_nexus commit -m "fix(ui): Security tables — fix td flex bug + overflow wrapper"
```

---

## Task 7: Security — transfer list for Role privileges

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx`

Replace `MultiSelect` in `RoleModal` with a two-panel transfer list. Applies to both Create and Edit role flows (same `RoleModal` component).

- [ ] **Step 1: Add `PrivilegeTransferList` component to `SecurityPage.tsx`**

Add this function before `RolesTab` (before line ~107):

```tsx
function PrivilegeTransferList({
  allPrivs, selectedIds, onChange,
}: {
  allPrivs: Privilege[]
  selectedIds: string[]
  onChange: (ids: string[]) => void
}) {
  const [leftSearch, setLeftSearch] = useState('')
  const [rightSearch, setRightSearch] = useState('')

  const available = allPrivs.filter(p =>
    !selectedIds.includes(p.id) &&
    (!leftSearch || p.name.toLowerCase().includes(leftSearch.toLowerCase()))
  )
  const selected = allPrivs.filter(p =>
    selectedIds.includes(p.id) &&
    (!rightSearch || p.name.toLowerCase().includes(rightSearch.toLowerCase()))
  )

  function add(id: string) { onChange([...selectedIds, id]) }
  function remove(id: string) { onChange(selectedIds.filter(x => x !== id)) }
  function addAll() { onChange([...new Set([...selectedIds, ...available.map(p => p.id)])]) }
  function removeAll() { onChange(selectedIds.filter(id => !selected.some(p => p.id === id))) }

  const panelStyle: React.CSSProperties = {
    border: '1px solid rgba(124,92,255,0.2)', borderRadius: 10, overflow: 'hidden', flex: 1,
  }
  const headerStyle: React.CSSProperties = {
    padding: '6px 10px', fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)',
    textTransform: 'uppercase' as const, letterSpacing: '0.4px',
    borderBottom: '1px solid rgba(255,255,255,0.06)', background: 'rgba(0,0,0,0.2)',
  }
  const listStyle: React.CSSProperties = { maxHeight: 160, overflowY: 'auto' as const }
  const itemBase: React.CSSProperties = {
    padding: '6px 10px', fontSize: 12, cursor: 'pointer',
    borderBottom: '1px solid rgba(255,255,255,0.03)',
  }
  const arrowBtn: React.CSSProperties = {
    width: 28, height: 28, display: 'flex', alignItems: 'center', justifyContent: 'center',
    borderRadius: 8, border: '1px solid rgba(124,92,255,0.2)',
    background: 'rgba(124,92,255,0.1)', color: 'var(--holo-a)', cursor: 'pointer', fontSize: 14,
  }

  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 28px 1fr', gap: 8, alignItems: 'start' }}>
      <div style={panelStyle}>
        <div style={headerStyle}>Available ({available.length})</div>
        <div style={{ padding: '4px 6px', borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
          <input
            placeholder="Filter…"
            value={leftSearch}
            onChange={e => setLeftSearch(e.target.value)}
            className="holo-input"
            style={{ width: '100%', boxSizing: 'border-box' as const, fontSize: 11, padding: '4px 8px' }}
          />
        </div>
        <div style={listStyle}>
          {available.map(p => (
            <div key={p.id} style={{ ...itemBase, color: 'var(--holo-text)' }}
              onClick={() => add(p.id)}
              onMouseEnter={e => (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.08)'}
              onMouseLeave={e => (e.currentTarget as HTMLDivElement).style.background = 'transparent'}
            >{p.name}</div>
          ))}
          {available.length === 0 && (
            <div style={{ ...itemBase, color: 'var(--holo-text-faint)' }}>
              {leftSearch ? 'No matches' : 'All selected'}
            </div>
          )}
        </div>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 6, paddingTop: 42 }}>
        <button type="button" style={arrowBtn} onClick={addAll} title="Add all">→</button>
        <button type="button" style={arrowBtn} onClick={removeAll} title="Remove all">←</button>
      </div>

      <div style={panelStyle}>
        <div style={headerStyle}>Selected ({selected.length})</div>
        <div style={{ padding: '4px 6px', borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
          <input
            placeholder="Filter…"
            value={rightSearch}
            onChange={e => setRightSearch(e.target.value)}
            className="holo-input"
            style={{ width: '100%', boxSizing: 'border-box' as const, fontSize: 11, padding: '4px 8px' }}
          />
        </div>
        <div style={listStyle}>
          {selected.map(p => (
            <div key={p.id} style={{ ...itemBase, color: '#c4b5fd', background: 'rgba(124,92,255,0.12)', display: 'flex', alignItems: 'center', gap: 6 }}
              onClick={() => remove(p.id)}
              onMouseEnter={e => (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.2)'}
              onMouseLeave={e => (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.12)'}
            >
              <span style={{ width: 6, height: 6, borderRadius: '50%', background: '#7c5cff', flexShrink: 0, display: 'inline-block' }} />
              {p.name}
            </div>
          ))}
          {selected.length === 0 && (
            <div style={{ ...itemBase, color: 'var(--holo-text-faint)' }}>None selected</div>
          )}
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Replace `MultiSelect` in `RoleModal` with `PrivilegeTransferList`**

In `RoleModal` (around line 78–88), find:
```tsx
<div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', marginTop: 4 }}>Privileges</div>
{loadingPrivs ? (
  <div style={emptyStyle}>Loading privileges…</div>
) : (
  <MultiSelect
    options={allPrivs.map(p => ({ value: p.id, label: p.name }))}
    value={selectedPrivIds}
    onChange={onPrivToggle}
    placeholder="Search and select privileges…"
  />
)}
```

Replace with:
```tsx
<div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', marginTop: 4 }}>Privileges</div>
{loadingPrivs ? (
  <div style={emptyStyle}>Loading privileges…</div>
) : (
  <PrivilegeTransferList
    allPrivs={allPrivs}
    selectedIds={selectedPrivIds}
    onChange={onPrivToggle}
  />
)}
```

- [ ] **Step 3: Widen the Role modal**

`RoleModal` uses `HoloModal open={true} onClose={onCancel}` (line ~73). Update:
```tsx
<HoloModal open={true} onClose={onCancel} style={{ minWidth: 640 }}>
```

- [ ] **Step 4: Remove unused `MultiSelect` import if no longer used in `SecurityPage.tsx`**

Check if `MultiSelect` is still imported. If not used elsewhere, remove its import line:
```tsx
import { MultiSelect } from '../components/MultiSelect'
```

- [ ] **Step 5: Type-check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git -C /home/skensel/AI/self_nexus add frontend/src/pages/SecurityPage.tsx
git -C /home/skensel/AI/self_nexus commit -m "feat(ui): Security roles — transfer list for privilege selection"
```

---

## Task 8: System Admin — "Info" tab

**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

Move System Status + System Info cards from the top of the page into a new first tab called "Info".

- [ ] **Step 1: Update `AdminTab` type, `VALID_TABS`, and default**

Find:
```tsx
type AdminTab = 'blobs' | 'backup' | 'monitoring'
const VALID_TABS: AdminTab[] = ['blobs', 'backup', 'monitoring']
```

Replace with:
```tsx
type AdminTab = 'info' | 'blobs' | 'backup' | 'monitoring'
const VALID_TABS: AdminTab[] = ['info', 'blobs', 'backup', 'monitoring']
```

Find:
```tsx
AdminTab = tabParam && VALID_TABS.includes(tabParam) ? tabParam : 'blobs'
```

Replace with:
```tsx
AdminTab = tabParam && VALID_TABS.includes(tabParam) ? tabParam : 'info'
```

- [ ] **Step 2: Add "Info" as first item in `HoloTabs`**

Find:
```tsx
<HoloTabs
  items={[
    { value: 'blobs',      label: <><HardDrive size={13} style={{ marginRight: 5 }} />Blob Stores</> },
    { value: 'backup',     label: <><Database size={13} style={{ marginRight: 5 }} />Backup &amp; Restore</> },
    { value: 'monitoring', label: <><Activity size={13} style={{ marginRight: 5 }} />Monitoring</> },
  ] as HoloTabItem[]}
  value={tab}
  onChange={v => setTab(v as AdminTab)}
/>
```

Replace with:
```tsx
<HoloTabs
  items={[
    { value: 'info',       label: <><Info size={13} style={{ marginRight: 5 }} />Info</> },
    { value: 'blobs',      label: <><HardDrive size={13} style={{ marginRight: 5 }} />Blob Stores</> },
    { value: 'backup',     label: <><Database size={13} style={{ marginRight: 5 }} />Backup &amp; Restore</> },
    { value: 'monitoring', label: <><Activity size={13} style={{ marginRight: 5 }} />Monitoring</> },
  ] as HoloTabItem[]}
  value={tab}
  onChange={v => setTab(v as AdminTab)}
/>
```

- [ ] **Step 3: Move Status + Info cards into `{tab === 'info' && ...}` block**

In `AdminPage.tsx`, find and **cut** the entire block starting with:
```tsx
<div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
```
and ending with its closing `</div>` (it contains the System Status `HoloCard` and the System Info `HoloCard`). This block appears at roughly lines 133–178, just before `{/* Tabs */}`.

After the `<HoloTabs .../>` closing tag (and before `{/* Backup / Restore */}`), add:
```tsx
{tab === 'info' && (
  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
    {/* PASTE the two HoloCard blocks cut above — verbatim, no content changes */}
  </div>
)}
```

Paste the two `<HoloCard>` blocks (System Status and System Info) inside this conditional exactly as they were.

- [ ] **Step 4: Type-check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git -C /home/skensel/AI/self_nexus add frontend/src/pages/AdminPage.tsx
git -C /home/skensel/AI/self_nexus commit -m "feat(ui): System Admin — Info tab with Status + System Info"
```

---

## Task 9: Profile modal — token list layout

**Files:**
- Modify: `frontend/src/components/Layout.tsx`

Fix: token list grows without bound when many tokens exist. Add `maxHeight` + scroll. Improve individual token row layout.

- [ ] **Step 1: Update `S` styles object in `ProfileModal`**

Find in `ProfileModal` the `S` object (around line 60):
```tsx
const S = {
  header:   { display: 'flex', alignItems: 'center', justifyContent: 'space-between' },
  title:    { fontSize: 16, fontWeight: 700, color: 'var(--holo-text)', display: 'flex', alignItems: 'center', gap: 8 },
  row:      { display: 'flex', alignItems: 'center', gap: 10, padding: '8px 0', borderBottom: '1px solid rgba(255,255,255,0.06)' },
  empty:    { color: 'var(--holo-text-dim)', fontSize: 13, padding: '12px 0' },
  mono:     { fontFamily: 'ui-monospace,monospace' },
  scroll:   { overflowY: 'auto' as const, display: 'flex', flexDirection: 'column' as const, gap: 16 },
}
```

Replace with:
```tsx
const S = {
  header:    { display: 'flex', alignItems: 'center', justifyContent: 'space-between' },
  title:     { fontSize: 16, fontWeight: 700, color: 'var(--holo-text)', display: 'flex', alignItems: 'center', gap: 8 },
  tokenList: { maxHeight: 240, overflowY: 'auto' as const, display: 'flex', flexDirection: 'column' as const },
  row:       { display: 'flex', alignItems: 'flex-start', gap: 10, padding: '10px 0', borderBottom: '1px solid rgba(255,255,255,0.06)', minWidth: 0 },
  rowMeta:   { flex: 1, minWidth: 0 },
  rowName:   { fontWeight: 600, fontSize: 13, color: 'var(--holo-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const },
  rowDates:  { fontSize: 11, color: 'var(--holo-text-dim)', marginTop: 2, lineHeight: 1.4 },
  empty:     { color: 'var(--holo-text-dim)', fontSize: 13, padding: '12px 0' },
  mono:      { fontFamily: 'ui-monospace,monospace' },
}
```

- [ ] **Step 2: Update token list render**

Find the token list card (around line 108):
```tsx
<div className="holo-card" style={{ padding: 16 }}>
  <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)', marginBottom: 10 }}>Your API Tokens</div>
  {isLoading
    ? <div style={S.empty}>Loading…</div>
    : tokens.length === 0
      ? <div style={S.empty}>No tokens yet</div>
      : tokens.map(t => (
        <div key={t.id} style={S.row}>
          <Key size={13} style={{ color: 'var(--holo-a)', flexShrink: 0 }} />
          <div style={{ flex: 1 }}>
            <div style={{ color: 'var(--holo-text)', fontWeight: 600, fontSize: 13 }}>{t.name}</div>
            <div style={{ fontSize: 11, color: 'var(--holo-text-dim)' }}>
              Created {new Date(t.createdAt).toLocaleDateString()}
              {t.lastUsedAt && ` · Last used ${new Date(t.lastUsedAt).toLocaleDateString()}`}
              {t.expiresAt && ` · Expires ${new Date(t.expiresAt).toLocaleDateString()}`}
            </div>
          </div>
          <HoloButton variant="danger" icon={<Trash2 size={13} />} onClick={() => del.mutate(t.id)} />
        </div>
      ))
  }
</div>
```

Replace with:
```tsx
<div className="holo-card" style={{ padding: 16 }}>
  <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)', marginBottom: 10 }}>
    Your API Tokens {tokens.length > 0 && <span style={{ fontWeight: 400, color: 'var(--holo-text-faint)', fontSize: 12 }}>({tokens.length})</span>}
  </div>
  {isLoading
    ? <div style={S.empty}>Loading…</div>
    : tokens.length === 0
      ? <div style={S.empty}>No tokens yet</div>
      : (
        <div style={S.tokenList}>
          {tokens.map(t => (
            <div key={t.id} style={S.row}>
              <Key size={13} style={{ color: 'var(--holo-a)', flexShrink: 0, marginTop: 2 }} />
              <div style={S.rowMeta}>
                <div style={S.rowName}>{t.name}</div>
                <div style={S.rowDates}>
                  Created {new Date(t.createdAt).toLocaleDateString()}
                  {t.lastUsedAt && ` · Last used ${new Date(t.lastUsedAt).toLocaleDateString()}`}
                  {t.expiresAt && ` · Expires ${new Date(t.expiresAt).toLocaleDateString()}`}
                </div>
              </div>
              <HoloButton variant="danger" icon={<Trash2 size={13} />} onClick={() => del.mutate(t.id)} style={{ flexShrink: 0 }} />
            </div>
          ))}
        </div>
      )
  }
</div>
```

- [ ] **Step 3: Type-check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git -C /home/skensel/AI/self_nexus add frontend/src/components/Layout.tsx
git -C /home/skensel/AI/self_nexus commit -m "fix(ui): Profile modal — token list bounded height + better row layout"
```

---

## Final verification

- [ ] **Run full TypeScript build**

```bash
cd /home/skensel/AI/self_nexus/frontend && npm run build
```

Expected: `✓ built in X.XXs` — no TypeScript errors, no Vite errors.

- [ ] **Visual smoke-test** (dev server at http://localhost:5174)

| Page | Check |
|------|-------|
| Any page | All `<Select>` dropdowns open as pill with rounded-dot selected item; dropdown works inside modals without clipping |
| Repositories | List rows, name+desc stacked, no glow on hover, Create modal ≥640px wide |
| Browse | Download/Copy/Delete buttons show ghost style with hover |
| Search | Hovering a result card shows purple border highlight |
| Security → Privileges | Table columns aligned, Delete button stays in last column |
| Security → Content Selectors | Same table fix |
| Security → Roles → Create/Edit | Transfer list shows Available and Selected panels |
| System Admin | Default tab is "Info" showing Status + Info cards; other tabs intact |
| Profile | Token list scrolls at max-height 240px; long names truncate |
