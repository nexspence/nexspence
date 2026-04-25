import { useEffect, useRef, useState } from 'react'
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
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false)
        setSearch('')
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  const filtered = options.filter(o => o.label.toLowerCase().includes(search.toLowerCase()))
  const allSelected = filtered.length > 0 && filtered.every(o => value.includes(o.value))

  function toggle(v: string) {
    onChange(value.includes(v) ? value.filter(x => x !== v) : [...value, v])
  }

  function toggleAll() {
    if (allSelected) {
      onChange(value.filter(v => !filtered.some(o => o.value === v)))
    } else {
      const toAdd = filtered.map(o => o.value).filter(v => !value.includes(v))
      onChange([...value, ...toAdd])
    }
  }

  const selectedEntries = value.map(v => ({
    value: v,
    label: options.find(o => o.value === v)?.label ?? v,
  }))

  const dropStyle: React.CSSProperties = {
    position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 999, marginTop: 4,
    borderRadius: 'var(--holo-radius-sm)', maxHeight: 240, display: 'flex', flexDirection: 'column',
    boxShadow: '0 12px 40px rgba(0,0,0,0.6)',
  }

  return (
    <div ref={ref} style={{ position: 'relative', userSelect: 'none' }}>
      <div
        onClick={() => setOpen(o => !o)}
        className="holo-input"
        style={{
          minHeight: 36, cursor: 'pointer', display: 'flex', alignItems: 'flex-start',
          flexWrap: 'wrap', gap: 4,
          borderColor: open ? 'var(--holo-border-strong)' : undefined,
          boxShadow: open ? 'var(--holo-ring)' : 'none',
        }}
      >
        {selectedEntries.length === 0 ? (
          <span style={{ color: 'var(--holo-text-faint)', lineHeight: '22px' }}>{placeholder}</span>
        ) : (
          selectedEntries.map(entry => (
            <span key={entry.value} style={{
              display: 'flex', alignItems: 'center', gap: 4, padding: '1px 6px',
              background: 'rgba(124,92,255,0.15)', borderRadius: 4, fontSize: 12, color: 'var(--holo-a)',
            }}>
              {entry.label}
              <X size={10} style={{ cursor: 'pointer' }} onClick={e => {
                e.stopPropagation()
                toggle(entry.value)
              }} />
            </span>
          ))
        )}
        <ChevronDown size={14} style={{ marginLeft: 'auto', color: 'var(--holo-text-faint)', alignSelf: 'center', flexShrink: 0 }} />
      </div>
      {open && (
        <div className="holo-card" style={dropStyle}>
          <div style={{ padding: '6px 8px', borderBottom: '1px solid rgba(255,255,255,0.06)' }}>
            <input
              autoFocus
              placeholder="Filter…"
              value={search}
              onChange={e => setSearch(e.target.value)}
              onClick={e => e.stopPropagation()}
              className="holo-input"
              style={{ width: '100%', boxSizing: 'border-box' as const }}
            />
          </div>
          {filtered.length > 0 && (
            <div
              onClick={e => { e.stopPropagation(); toggleAll() }}
              style={{ padding: '6px 12px', fontSize: 12, color: 'var(--holo-a)', cursor: 'pointer', borderBottom: '1px solid rgba(255,255,255,0.06)' }}
            >
              {allSelected ? 'Deselect all' : 'Select all'}
            </div>
          )}
          <div style={{ overflowY: 'auto' as const, flex: 1 }}>
            {filtered.length === 0 ? (
              <div style={{ padding: '8px 12px', fontSize: 13, color: 'var(--holo-text-faint)' }}>No options</div>
            ) : filtered.map(o => (
              <div
                key={o.value}
                onClick={e => { e.stopPropagation(); toggle(o.value) }}
                style={{
                  padding: '7px 12px', fontSize: 13, cursor: 'pointer',
                  color: value.includes(o.value) ? 'var(--holo-a)' : 'var(--holo-text)',
                  background: value.includes(o.value) ? 'rgba(124,92,255,0.15)' : 'transparent',
                }}
                onMouseEnter={e => {
                  (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.08)'
                }}
                onMouseLeave={e => {
                  (e.currentTarget as HTMLDivElement).style.background = value.includes(o.value) ? 'rgba(124,92,255,0.15)' : 'transparent'
                }}
              >
                {o.label}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
