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
  searchable?: boolean
  style?: CSSProperties
}

export function Select({
  options,
  value,
  onChange,
  placeholder = '— Select —',
  disabled,
  searchable,
  style,
}: SelectProps) {
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const ref = useRef<HTMLDivElement>(null)
  const selected = options.find(o => o.value === value)

  useEffect(() => {
    if (!open) { setSearch(''); return }
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

  const visibleOptions = searchable && search.trim()
    ? options.filter(o => o.label.toLowerCase().includes(search.toLowerCase()))
    : options

  return (
    <div ref={ref} style={{ position: 'relative' }}>
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
          borderRadius: open ? 'var(--holo-radius-sm) var(--holo-radius-sm) 0 0' : undefined,
          boxShadow: open ? 'var(--holo-ring)' : 'none',
          borderColor: open ? 'var(--holo-border-strong)' : undefined,
          ...style,
        }}
      >
        <span style={{ flex: 1, color: selected ? 'var(--holo-text)' : 'var(--holo-text-faint)' }}>
          {selected ? selected.label : placeholder}
        </span>
        {selected?.badge}
        {selected?.tag}
        <ChevronDown
          size={14}
          style={{
            color: 'var(--holo-text-faint)',
            flexShrink: 0,
            transform: open ? 'rotate(180deg)' : 'none',
            transition: 'transform 0.2s',
          }}
        />
      </button>

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
            boxShadow: '0 12px 40px rgba(0,0,0,0.6)',
          }}
        >
          {searchable && (
            <div style={{ padding: '6px 8px', borderBottom: '1px solid rgba(255,255,255,0.06)', position: 'sticky', top: 0, background: 'rgba(8,13,28,0.98)', zIndex: 1 }}>
              <input
                autoFocus
                placeholder="Filter…"
                value={search}
                onChange={e => setSearch(e.target.value)}
                className="holo-input"
                style={{ width: '100%', boxSizing: 'border-box' as const }}
              />
            </div>
          )}
          {visibleOptions.length === 0 && (
            <div style={{ padding: '10px 14px', fontSize: 13, color: 'rgba(229,231,235,0.35)' }}>
              {searchable && search.trim() ? 'No matches' : 'No options'}
            </div>
          )}
          {visibleOptions.map(opt => {
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
                  color: isSelected ? 'var(--holo-a)' : 'var(--holo-text)',
                  background: isSelected ? 'rgba(124,92,255,0.15)' : 'transparent',
                  borderBottom: '1px solid rgba(255,255,255,0.04)',
                  transition: 'background 0.1s',
                }}
                onMouseEnter={e => {
                  (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.08)'
                }}
                onMouseLeave={e => {
                  (e.currentTarget as HTMLDivElement).style.background = isSelected ? 'rgba(124,92,255,0.15)' : 'transparent'
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
