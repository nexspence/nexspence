import { useMemo, useState } from 'react'
import { HoloButton, HoloInput } from '@/components/holo'

export interface PreviewRepo {
  name: string
  format: string
  type: string
}

/** Block a Repositories/Artifacts job that would otherwise copy the whole instance. */
export function validateMigrationRepoScope(opts: {
  migrateRepos: boolean
  migrateBlobs: boolean
  previewed: boolean
  previewRepoCount: number
  selectedCount: number
}): string | null {
  if (!opts.migrateRepos && !opts.migrateBlobs) return null
  if (!opts.previewed) {
    return 'Test the connection to pick repositories, or uncheck Repositories and Artifacts'
  }
  if (opts.previewRepoCount > 0 && opts.selectedCount === 0) {
    return 'Select at least one repository, or uncheck Repositories and Artifacts'
  }
  return null
}

/** Tick a subset of the source repositories a migration will touch. */
export function MigrationRepoPicker({
  repos,
  selected,
  onChange,
}: {
  repos: PreviewRepo[]
  selected: string[]
  onChange: (names: string[]) => void
}) {
  const [q, setQ] = useState('')
  const selectedSet = useMemo(() => new Set(selected), [selected])
  const needle = q.trim().toLowerCase()
  const visible = needle
    ? repos.filter(r =>
        r.name.toLowerCase().includes(needle) ||
        r.format.toLowerCase().includes(needle) ||
        r.type.toLowerCase().includes(needle))
    : repos

  const toggle = (name: string) => {
    onChange(selectedSet.has(name) ? selected.filter(n => n !== name) : [...selected, name])
  }

  const selectVisible = () => {
    const next = new Set(selected)
    for (const r of visible) next.add(r.name)
    onChange([...next])
  }

  const clearVisible = () => {
    const drop = new Set(visible.map(r => r.name))
    onChange(selected.filter(n => !drop.has(n)))
  }

  const selectedGroup = repos.some(r => r.type === 'group' && selectedSet.has(r.name))

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
        <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>
          Repositories
        </span>
        <span style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>
          {selected.length} of {repos.length} selected
        </span>
      </div>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <HoloInput
          style={{ flex: 1 }}
          placeholder="Filter by name, format or type"
          value={q}
          onChange={e => setQ(e.target.value)}
          aria-label="Filter repositories"
        />
        <HoloButton type="button" onClick={selectVisible}>All</HoloButton>
        <HoloButton type="button" onClick={clearVisible}>None</HoloButton>
      </div>
      <div style={{ maxHeight: 200, overflowY: 'auto', border: '1px solid var(--holo-border)', borderRadius: 8, padding: '6px 8px' }}>
        {visible.length === 0 ? (
          <div style={{ fontSize: 12, color: 'var(--holo-text-faint)', padding: 8 }}>No repositories match.</div>
        ) : visible.map(r => (
          <label
            key={r.name}
            style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text)', cursor: 'pointer', padding: '3px 2px', fontFamily: 'monospace' }}
          >
            <input
              type="checkbox"
              checked={selectedSet.has(r.name)}
              onChange={() => toggle(r.name)}
            />
            {r.name} <span style={{ opacity: 0.7, fontFamily: 'inherit' }}>({r.format}/{r.type})</span>
          </label>
        ))}
      </div>
      {selectedGroup && (
        <div style={{ fontSize: 12, color: 'var(--holo-text-faint)', lineHeight: 1.45 }}>
          Groups have no artifacts of their own; selecting one also migrates its members.
        </div>
      )}
    </div>
  )
}
