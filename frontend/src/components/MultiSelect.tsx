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
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
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

  const selectedLabels = value
    .map(v => options.find(o => o.value === v)?.label)
    .filter(Boolean) as string[]

  const dropStyle: React.CSSProperties = {
    position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 999, marginTop: 4,
    background: 'rgba(10,15,28,0.97)', border: '1px solid rgba(59,130,246,0.4)',
    borderRadius: 8, backdropFilter: 'blur(12px)', maxHeight: 240, display: 'flex', flexDirection: 'column',
  }

  return (
    <div ref={ref} style={{ position: 'relative', userSelect: 'none' }}>
      <div
        onClick={() => setOpen(o => !o)}
        style={{
          minHeight: 36, padding: '6px 10px', background: 'rgba(255,255,255,0.06)',
          border: `1px solid ${open ? 'rgba(59,130,246,0.5)' : 'rgba(255,255,255,0.12)'}`,
          borderRadius: 8, cursor: 'pointer', display: 'flex', alignItems: 'flex-start',
          flexWrap: 'wrap', gap: 4, color: '#e5e7eb', fontSize: 13,
        }}
      >
        {selectedLabels.length === 0 ? (
          <span style={{ color: 'rgba(229,231,235,0.35)', lineHeight: '22px' }}>{placeholder}</span>
        ) : (
          selectedLabels.map(label => (
            <span key={label} style={{
              display: 'flex', alignItems: 'center', gap: 4, padding: '1px 6px',
              background: 'rgba(59,130,246,0.15)', borderRadius: 4, fontSize: 12, color: '#93c5fd',
            }}>
              {label}
              <X size={10} style={{ cursor: 'pointer' }} onClick={e => {
                e.stopPropagation()
                const opt = options.find(o => o.label === label)
                if (opt) toggle(opt.value)
              }} />
            </span>
          ))
        )}
        <ChevronDown size={14} style={{ marginLeft: 'auto', color: 'rgba(229,231,235,0.4)', alignSelf: 'center', flexShrink: 0 }} />
      </div>
      {open && (
        <div style={dropStyle}>
          <div style={{ padding: '6px 8px', borderBottom: '1px solid rgba(255,255,255,0.06)' }}>
            <input
              autoFocus
              placeholder="Filter…"
              value={search}
              onChange={e => setSearch(e.target.value)}
              onClick={e => e.stopPropagation()}
              style={{ width: '100%', background: 'none', border: 'none', outline: 'none', color: '#e5e7eb', fontSize: 13, boxSizing: 'border-box' as const }}
            />
          </div>
          {filtered.length > 0 && (
            <div
              onClick={e => { e.stopPropagation(); toggleAll() }}
              style={{ padding: '6px 12px', fontSize: 12, color: '#3b82f6', cursor: 'pointer', borderBottom: '1px solid rgba(255,255,255,0.06)' }}
            >
              {allSelected ? 'Deselect all' : 'Select all'}
            </div>
          )}
          <div style={{ overflowY: 'auto' as const, flex: 1 }}>
            {filtered.length === 0 ? (
              <div style={{ padding: '8px 12px', fontSize: 13, color: 'rgba(229,231,235,0.35)' }}>No options</div>
            ) : filtered.map(o => (
              <div
                key={o.value}
                onClick={e => { e.stopPropagation(); toggle(o.value) }}
                style={{
                  padding: '7px 12px', fontSize: 13, cursor: 'pointer',
                  color: value.includes(o.value) ? '#93c5fd' : '#e5e7eb',
                  background: value.includes(o.value) ? 'rgba(59,130,246,0.1)' : 'transparent',
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
