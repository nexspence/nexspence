import { useMemo, useState } from 'react'
import { HoloButton, HoloInput } from '@/components/holo'

export interface PreviewRepo {
  name: string
  format: string
  type: string
}

export function isHostedRepo(r: PreviewRepo) {
  return r.type === 'hosted'
}

/** Hosted packages and proxy caches; groups have none of their own. */
export function isBlobSourceRepo(r: PreviewRepo) {
  return isHostedRepo(r) || r.type === 'proxy'
}

/** Drop names the current scope cannot use (groups when only artifacts run). */
export function scopedRepoSelection(repos: PreviewRepo[], selected: string[], blobSourcesOnly: boolean) {
  if (!blobSourcesOnly) return selected
  const keep = new Set(repos.filter(isBlobSourceRepo).map(r => r.name))
  return selected.filter(n => keep.has(n))
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
  if (opts.migrateBlobs && !opts.migrateRepos && opts.previewRepoCount === 0) {
    return 'No hosted or proxy repositories to copy artifacts from'
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
  blobSourcesOnly = false,
}: {
  repos: PreviewRepo[]
  selected: string[]
  onChange: (names: string[]) => void
  /** Artifacts-only: groups are listed but cannot be chosen. */
  blobSourcesOnly?: boolean
}) {
  const [q, setQ] = useState('')
  const blobNames = useMemo(() => repos.filter(isBlobSourceRepo).map(r => r.name), [repos])
  const blobSet = useMemo(() => new Set(blobNames), [blobNames])
  const canSelect = (r: PreviewRepo) => !blobSourcesOnly || isBlobSourceRepo(r)
  const selectedSet = useMemo(() => new Set(selected), [selected])
  const selectedSelectable = blobSourcesOnly ? selected.filter(n => blobSet.has(n)) : selected
  const needle = q.trim().toLowerCase()
  const visible = needle
    ? repos.filter(r =>
        r.name.toLowerCase().includes(needle) ||
        r.format.toLowerCase().includes(needle) ||
        r.type.toLowerCase().includes(needle))
    : repos

  const toggle = (r: PreviewRepo) => {
    if (!canSelect(r)) return
    onChange(selectedSet.has(r.name)
      ? selectedSelectable.filter(n => n !== r.name)
      : [...selectedSelectable, r.name])
  }

  const selectVisible = () => {
    const next = new Set(selectedSelectable)
    for (const r of visible) {
      if (canSelect(r)) next.add(r.name)
    }
    onChange([...next])
  }

  const clearAll = () => onChange([])

  const hiddenSelected = needle
    ? selectedSelectable.filter(n => !visible.some(r => r.name === n)).length
    : 0

  const selectedGroup = !blobSourcesOnly && repos.some(r => r.type === 'group' && selectedSet.has(r.name))

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
        <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>
          Repositories
        </span>
        <span style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>
          {blobSourcesOnly
            ? `${selectedSelectable.length} of ${blobNames.length} selected`
            : `${selected.length} of ${repos.length} selected`}
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
        <HoloButton type="button" onClick={clearAll}>None</HoloButton>
      </div>
      {hiddenSelected > 0 && (
        <div style={{ fontSize: 12, color: 'var(--holo-text-faint)', lineHeight: 1.45 }}>
          {hiddenSelected} selected {hiddenSelected === 1 ? 'is' : 'are'} hidden by the filter
        </div>
      )}
      <div style={{ maxHeight: 200, overflowY: 'auto', border: '1px solid var(--holo-border)', borderRadius: 8, padding: '6px 8px' }}>
        {visible.length === 0 ? (
          <div style={{ fontSize: 12, color: 'var(--holo-text-faint)', padding: 8 }}>No repositories match.</div>
        ) : visible.map(r => {
          const enabled = canSelect(r)
          return (
            <label
              key={r.name}
              style={{
                display: 'flex', alignItems: 'center', gap: 8, fontSize: 13,
                color: enabled ? 'var(--holo-text)' : 'var(--holo-text-faint)',
                cursor: enabled ? 'pointer' : 'default',
                padding: '3px 2px', fontFamily: 'monospace',
                opacity: enabled ? 1 : 0.55,
              }}
            >
              <input
                type="checkbox"
                checked={enabled && selectedSet.has(r.name)}
                disabled={!enabled}
                onChange={() => toggle(r)}
              />
              {r.name} <span style={{ opacity: 0.7, fontFamily: 'inherit' }}>({r.format}/{r.type})</span>
            </label>
          )
        })}
      </div>
      {blobSourcesOnly && (
        <div style={{ fontSize: 12, color: 'var(--holo-text-faint)', lineHeight: 1.45 }}>
          Artifacts copy from hosted repositories and proxy caches. Groups have none of their own.
        </div>
      )}
      {selectedGroup && (
        <div style={{ fontSize: 12, color: 'var(--holo-text-faint)', lineHeight: 1.45 }}>
          Groups have no artifacts of their own; selecting one also migrates its members.
        </div>
      )}
    </div>
  )
}
