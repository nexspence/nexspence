// frontend/src/components/Select.tsx
import { CSSProperties, FocusEvent, ReactNode, useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ChevronDown } from 'lucide-react'
import { menuPlacement, type DropPos } from './menuPlacement'

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

function matches(opt: SelectOption, q: string): boolean {
  const needle = q.toLowerCase()
  return opt.label.toLowerCase().includes(needle) || opt.value.toLowerCase().includes(needle)
}

export function Select({
  options, value, onChange,
  placeholder = '— Select —',
  disabled, style,
}: SelectProps) {
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [dropPos, setDropPos] = useState<DropPos | null>(null)
  const wrapRef = useRef<HTMLDivElement>(null)
  const pillRef = useRef<HTMLElement | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)
  const restoreFocus = useRef(false)
  const selected = options.find(o => o.value === value)

  function bindPill(node: HTMLElement | null) {
    pillRef.current = node
  }

  function placeMenu() {
    const pill = pillRef.current
    if (!pill) return
    setDropPos(menuPlacement(pill.getBoundingClientRect(), window.innerHeight))
  }

  function openMenu() {
    if (disabled || open) return
    placeMenu()
    setOpen(true)
  }

  const closeMenu = useCallback((restore = false) => {
    if (restore) restoreFocus.current = true
    setOpen(false)
  }, [])

  function onWrapBlur(e: FocusEvent<HTMLDivElement>) {
    if (!open) return
    const next = e.relatedTarget as Node | null
    if (!next) return
    if (wrapRef.current?.contains(next) || dropdownRef.current?.contains(next)) return
    closeMenu(false)
  }

  useEffect(() => {
    if (!open) {
      setSearch('')
      if (restoreFocus.current) {
        restoreFocus.current = false
        pillRef.current?.focus()
      }
      return
    }
    inputRef.current?.focus()
    function onScroll(e: Event) {
      if (dropdownRef.current?.contains(e.target as Node)) return
      closeMenu(false)
    }
    function onResize() { closeMenu(false) }
    function onKey(e: KeyboardEvent) { if (e.key === 'Escape') closeMenu(true) }
    function onMouseDown(e: MouseEvent) {
      const t = e.target as Node
      if (
        wrapRef.current && !wrapRef.current.contains(t) &&
        dropdownRef.current && !dropdownRef.current.contains(t)
      ) closeMenu(false)
    }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKey)
    window.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', onResize)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKey)
      window.removeEventListener('scroll', onScroll, true)
      window.removeEventListener('resize', onResize)
    }
  }, [open, closeMenu])

  const visible = search.trim() ? options.filter(o => matches(o, search.trim())) : options

  function pick(v: string) {
    onChange(v)
    closeMenu(true)
  }

  const triggerStyle: CSSProperties = {
    display: 'flex', alignItems: 'center', gap: 8,
    width: '100%', padding: '9px 14px',
    background: open ? 'rgba(124,92,255,0.12)' : 'rgba(124,92,255,0.08)',
    border: `1px solid ${open ? 'rgba(124,92,255,0.5)' : 'rgba(124,92,255,0.35)'}`,
    borderRadius: 999,
    boxShadow: open ? '0 0 0 3px rgba(124,92,255,0.12)' : 'none',
    color: selected && !open ? 'var(--holo-text)' : 'var(--holo-text-faint)',
    cursor: disabled ? 'not-allowed' : open ? 'text' : 'pointer',
    opacity: disabled ? 0.5 : 1,
    textAlign: 'left' as const,
    fontSize: 13,
    boxSizing: 'border-box' as const,
    ...style,
  }

  const dropdown = open && dropPos ? createPortal(
    <div
      ref={dropdownRef}
      className="holo-card"
      role="listbox"
      onMouseDown={e => e.preventDefault()}
      style={{
        position: 'fixed',
        top: dropPos.top,
        bottom: dropPos.bottom,
        left: dropPos.left,
        width: dropPos.width,
        borderRadius: 14,
        zIndex: 100,
        padding: 6,
        display: 'flex', flexDirection: 'column', gap: 2,
        boxShadow: '0 12px 40px var(--holo-shadow-60)',
        maxHeight: dropPos.maxHeight,
        overflowY: 'auto' as const,
      }}
    >
      {visible.length === 0 && (
        <div style={{ padding: '8px 12px', fontSize: 12, color: 'var(--holo-text-faint)' }}>
          {search ? 'No matches' : 'No options'}
        </div>
      )}
      {visible.map(opt => {
        const isSel = opt.value === value
        return (
          <div
            key={opt.value}
            role="option"
            aria-selected={isSel}
            onClick={() => pick(opt.value)}
            style={{
              display: 'flex', alignItems: 'center', gap: 8,
              padding: '7px 12px',
              cursor: 'pointer', fontSize: 13,
              color: isSel ? 'var(--holo-c-violet-300)' : 'var(--holo-text)',
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
              <span style={{ width: 6, height: 6, borderRadius: '50%', background: 'var(--holo-a)', boxShadow: '0 0 6px var(--holo-a)', flexShrink: 0, display: 'inline-block' }} />
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
    <div ref={wrapRef} onBlur={onWrapBlur} style={{ position: 'relative' }}>
      {open ? (
        <div ref={bindPill} style={triggerStyle}>
          <input
            ref={inputRef}
            role="combobox"
            aria-expanded
            aria-autocomplete="list"
            aria-label={placeholder}
            value={search}
            placeholder={selected ? selected.label : placeholder}
            onChange={e => setSearch(e.target.value)}
            onKeyDown={e => {
              if (e.key === 'Enter' && visible.length > 0 && search.trim()) {
                e.preventDefault()
                pick(visible[0].value)
              }
            }}
            style={{
              flex: 1, minWidth: 0, border: 'none', outline: 'none',
              background: 'transparent', padding: 0, fontSize: 13,
              color: 'var(--holo-text)',
            }}
          />
          <button
            type="button"
            aria-label="Close"
            onClick={() => closeMenu(true)}
            style={{
              display: 'flex', alignItems: 'center', padding: 0, border: 'none',
              background: 'transparent', cursor: 'pointer', color: 'var(--holo-text-faint)',
            }}
          >
            <ChevronDown size={14} style={{ transform: 'rotate(180deg)', transition: 'transform 0.2s' }} />
          </button>
        </div>
      ) : (
        <button ref={bindPill} type="button" disabled={disabled} onClick={openMenu} style={triggerStyle}>
          <span style={{ flex: 1 }}>
            {selected ? selected.label : placeholder}
          </span>
          {selected?.badge}
          {selected?.tag}
          <ChevronDown size={14} style={{ color: 'var(--holo-text-faint)', flexShrink: 0, transition: 'transform 0.2s' }} />
        </button>
      )}
      {dropdown}
    </div>
  )
}
