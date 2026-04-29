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
  const dropdownRef = useRef<HTMLDivElement>(null)

  function openMenu() {
    if (!open && triggerRef.current) {
      const r = triggerRef.current.getBoundingClientRect()
      setDropPos({ top: r.bottom + 4, left: r.left, width: r.width })
    }
    setOpen(v => !v)
  }

  useEffect(() => {
    if (!open) { setSearch(''); return }
    function onClose() { setOpen(false) }
    function onKey(e: KeyboardEvent) { if (e.key === 'Escape') setOpen(false) }
    function onMouseDown(e: MouseEvent) {
      const t = e.target as Node
      if (
        triggerRef.current && !triggerRef.current.contains(t) &&
        dropdownRef.current && !dropdownRef.current.contains(t)
      ) setOpen(false)
    }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKey)
    window.addEventListener('scroll', onClose, true)
    window.addEventListener('resize', onClose)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKey)
      window.removeEventListener('scroll', onClose, true)
      window.removeEventListener('resize', onClose)
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
      ref={dropdownRef}
      className="holo-card"
      style={{
        position: 'fixed',
        top: dropPos.top,
        left: dropPos.left,
        width: dropPos.width,
        borderRadius: 14,
        zIndex: 100,
        padding: 0,
        display: 'flex', flexDirection: 'column',
        boxShadow: '0 12px 40px rgba(0,0,0,0.6)',
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
                padding: '7px 14px', fontSize: 13, cursor: 'pointer',
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
