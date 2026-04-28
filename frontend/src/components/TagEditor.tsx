import { useState, KeyboardEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { nexusApi } from '@/api/client'

interface Props {
  componentId: string
  initialTags: string[]
  queryKey: unknown[]
  readOnly?: boolean
}

const S = {
  section: {
    background: 'rgba(124,92,255,0.04)',
    border: '1px solid rgba(124,92,255,0.18)',
    borderRadius: 10,
    padding: '10px 12px',
    marginTop: 12,
  },
  header: {
    display: 'flex', alignItems: 'center', justifyContent: 'space-between',
    marginBottom: 8,
  },
  title: {
    fontSize: 11, fontWeight: 600, letterSpacing: '0.07em',
    textTransform: 'uppercase' as const, color: '#7c5cff',
  },
  chips: { display: 'flex', flexWrap: 'wrap' as const, gap: 5, marginBottom: 8, minHeight: 20 },
  chip: {
    display: 'inline-flex', alignItems: 'center', gap: 4,
    background: 'rgba(124,92,255,0.15)', border: '1px solid rgba(124,92,255,0.35)',
    borderRadius: 5, padding: '2px 8px 2px 9px',
    fontSize: 12, color: '#c4b5fd', fontFamily: 'monospace' as const,
  },
  chipX: {
    cursor: 'pointer', opacity: 0.55, fontSize: 14, lineHeight: 1,
    color: '#a78bfa', border: 'none' as const, background: 'none' as const, padding: '0 0 0 2px',
  },
  inputRow: { display: 'flex', gap: 6 },
  input: {
    flex: 1, background: 'rgba(255,255,255,0.04)',
    border: '1px solid rgba(124,92,255,0.2)', borderRadius: 7,
    padding: '5px 9px', color: '#e2e8f0', fontSize: 12, outline: 'none' as const,
  },
  addBtn: {
    padding: '5px 11px', borderRadius: 7,
    background: 'rgba(124,92,255,0.14)', border: '1px solid rgba(124,92,255,0.3)',
    color: '#a78bfa', fontSize: 12, cursor: 'pointer',
  },
  saveBtn: {
    marginTop: 8, width: '100%', padding: '5px 0', borderRadius: 7,
    background: '#2563eb', border: '1px solid #3b82f6',
    color: '#fff', fontSize: 12, cursor: 'pointer',
  },
  hint: { fontSize: 11, color: '#475569', marginTop: 6 },
  error: { fontSize: 11, color: '#ef4444', marginTop: 4 },
}

export function TagEditor({ componentId, initialTags, queryKey, readOnly }: Props) {
  const [tags, setTags] = useState<string[]>(initialTags ?? [])
  const [input, setInput] = useState('')
  const [dirty, setDirty] = useState(false)
  const qc = useQueryClient()

  const mutation = useMutation({
    mutationFn: (t: string[]) => nexusApi.setComponentTags(componentId, t),
    onSuccess: () => {
      setDirty(false)
      void qc.invalidateQueries({ queryKey })
    },
  })

  function addTag() {
    const v = input.trim()
    if (!v || tags.includes(v)) { setInput(''); return }
    setTags(prev => [...prev, v])
    setInput('')
    setDirty(true)
  }

  function removeTag(tag: string) {
    setTags(prev => prev.filter(t => t !== tag))
    setDirty(true)
  }

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter') { e.preventDefault(); addTag() }
  }

  return (
    <div style={S.section}>
      <div style={S.header}>
        <span style={S.title}>Tags</span>
      </div>
      <div style={S.chips}>
        {tags.map(t => (
          <span key={t} style={S.chip}>
            {t}
            {!readOnly && (
              <button style={S.chipX} onClick={() => removeTag(t)} title="Remove tag">×</button>
            )}
          </span>
        ))}
        {tags.length === 0 && <span style={{ fontSize: 12, color: '#475569' }}>No tags</span>}
      </div>
      {!readOnly && (
        <>
          <div style={S.inputRow}>
            <input
              style={S.input}
              placeholder="Add tag (Enter)"
              value={input}
              onChange={e => setInput(e.target.value)}
              onKeyDown={onKeyDown}
            />
            <button style={S.addBtn} onClick={addTag}>+ Add</button>
          </div>
          {dirty && (
            <button
              style={S.saveBtn}
              disabled={mutation.isPending}
              onClick={() => mutation.mutate(tags)}
            >
              {mutation.isPending ? 'Saving…' : 'Save tags'}
            </button>
          )}
          {mutation.isError && <div style={S.error}>Save failed</div>}
          <div style={S.hint}>
            Press Enter then Save. e.g. <code style={{ color: '#7c5cff' }}>prod</code> or <code style={{ color: '#7c5cff' }}>team:backend</code>
          </div>
        </>
      )}
    </div>
  )
}
