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
    function onScroll() { setOpen(false) }
    function onKey(e: KeyboardEvent) { if (e.key === 'Escape') setOpen(false) }
    function onMouseDown(e: MouseEvent) {
      if (triggerRef.current && !triggerRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKey)
    window.addEventListener('scroll', onScroll, true)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
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
        boxShadow: '0 12px 40px rgba(0,0,0,0.6)',
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
              borderRadius: isSel ? 10 : 8,
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
