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
        onClick={() => setOpen(v => !v)}
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
