import { useState, useMemo, FormEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { Search, Package, ChevronDown, ChevronUp, ChevronsUpDown, ChevronRight, ExternalLink } from 'lucide-react'
import { nexusApi } from '@/api/client'
import { Select } from '../components/Select'

interface SearchAsset {
  id: string
  path: string
  fileSize: number
  contentType: string
  lastModified: string
}

interface SearchComponent {
  id: string
  repository: string
  format: string
  group: string
  name: string
  version: string
  assets?: SearchAsset[]
}

interface SearchResult { items: SearchComponent[] }

type SortKey = 'name' | 'version' | 'date'
type SortDir = 'asc' | 'desc'

const S = {
  page: { padding: 24, display: 'flex', flexDirection: 'column' as const, gap: 20 },
  header: { marginBottom: 4 },
  title: { fontSize: 20, fontWeight: 700, color: '#dbeafe', margin: '0 0 4px' },
  subtitle: { fontSize: 13, color: 'rgba(229,231,235,0.5)', margin: 0 },
  filterCard: {
    background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)',
    borderRadius: 12, padding: '20px 20px 16px',
  },
  filterGrid: { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))', gap: 12, marginBottom: 14 },
  field: { display: 'flex', flexDirection: 'column' as const, gap: 5 },
  label: { fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.5)', textTransform: 'uppercase' as const, letterSpacing: '0.04em' },
  input: {
    background: 'rgba(255,255,255,0.05)', border: '1px solid rgba(255,255,255,0.1)',
    borderRadius: 8, padding: '8px 12px', color: '#e5e7eb', fontSize: 13, outline: 'none',
  },
  filterFooter: { display: 'flex', alignItems: 'center', gap: 10 },
  searchBtn: {
    background: '#3b82f6', border: 'none', borderRadius: 8,
    padding: '9px 20px', color: '#fff', fontSize: 13, fontWeight: 600,
    cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6,
  },
  clearBtn: {
    background: 'rgba(255,255,255,0.05)', border: '1px solid rgba(255,255,255,0.1)',
    borderRadius: 8, padding: '9px 14px', color: 'rgba(229,231,235,0.6)',
    fontSize: 13, cursor: 'pointer',
  },
  resultsLabel: { fontSize: 12, color: 'rgba(229,231,235,0.4)', marginLeft: 'auto' as const },
  empty: { flex: 1, display: 'flex', flexDirection: 'column' as const, alignItems: 'center', justifyContent: 'center', gap: 12, color: 'rgba(229,231,235,0.4)', fontSize: 14, paddingTop: 48 },

  group: { background: 'rgba(255,255,255,0.02)', border: '1px solid rgba(255,255,255,0.07)', borderRadius: 12, overflow: 'hidden' as const },
  groupHeader: {
    display: 'flex', alignItems: 'center', gap: 10,
    padding: '10px 16px',
    background: 'rgba(255,255,255,0.03)',
    borderBottom: '1px solid rgba(255,255,255,0.07)',
  },
  groupName: { fontSize: 13, fontWeight: 600, color: '#dbeafe' },
  groupCount: { fontSize: 11, color: 'rgba(229,231,235,0.4)', marginLeft: 'auto' as const },

  COLS: '1.5fr 1fr 1fr 2fr 1fr',
  thead: {
    display: 'grid',
    padding: '8px 16px',
    background: 'rgba(255,255,255,0.02)',
    borderBottom: '1px solid rgba(255,255,255,0.06)',
    fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.45)',
    textTransform: 'uppercase' as const, letterSpacing: '0.05em',
    userSelect: 'none' as const,
  },
  th: (active: boolean) => ({
    display: 'flex', alignItems: 'center', gap: 4, cursor: 'pointer',
    color: active ? '#93c5fd' : 'rgba(229,231,235,0.45)',
  }),
  trow: {
    display: 'grid',
    padding: '10px 16px', borderBottom: '1px solid rgba(255,255,255,0.04)',
    fontSize: 13, color: '#e5e7eb', alignItems: 'center',
    cursor: 'pointer',
  },
  expanded: {
    padding: '8px 16px 12px 32px',
    borderBottom: '1px solid rgba(255,255,255,0.04)',
    background: 'rgba(0,0,0,0.15)',
  },
  assetRow: {
    display: 'flex', justifyContent: 'space-between', alignItems: 'center',
    padding: '4px 0', fontSize: 12, gap: 12,
  },
  muted: { fontSize: 12, color: 'rgba(229,231,235,0.4)' },
  path: { fontSize: 11, color: 'rgba(147,197,253,0.85)', fontFamily: 'monospace' as const, wordBreak: 'break-all' as const },
  badge: (color: string) => ({ fontSize: 11, fontWeight: 600 as const, padding: '2px 8px', borderRadius: 4, background: color + '22', color }),
}

const FORMAT_COLORS: Record<string, string> = {
  maven2: '#f97316', npm: '#ef4444', docker: '#3b82f6', pypi: '#a78bfa',
  go: '#06b6d4', nuget: '#8b5cf6', helm: '#0ea5e9', raw: '#6b7280', apt: '#f59e0b', yum: '#10b981',
}

function fmtSize(b: number | undefined) {
  if (!b) return '—'
  if (b < 1024) return b + ' B'
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB'
  return (b / 1024 / 1024).toFixed(1) + ' MB'
}

function fmtDate(s: string | undefined) {
  if (!s) return '—'
  const d = new Date(s)
  return isNaN(d.getTime()) ? '—' : d.toLocaleDateString()
}

function SortIcon({ col, sortKey, sortDir }: { col: SortKey; sortKey: SortKey; sortDir: SortDir }) {
  if (col !== sortKey) return <ChevronsUpDown size={11} style={{ opacity: 0.4 }} />
  return sortDir === 'asc' ? <ChevronUp size={11} /> : <ChevronDown size={11} />
}

const EMPTY_FILTERS = { repository: '', format: '', name: '', group: '', version: '' }

export default function SearchPage() {
  const navigate = useNavigate()
  const [filters, setFilters] = useState(EMPTY_FILTERS)
  const [submitted, setSubmitted] = useState<typeof EMPTY_FILTERS | null>(null)
  const [sortKey, setSortKey] = useState<SortKey>('name')
  const [sortDir, setSortDir] = useState<SortDir>('asc')
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  const { data, isLoading } = useQuery<SearchResult>({
    queryKey: ['search', submitted],
    queryFn: () => {
      const p: Record<string, string> = {}
      if (submitted!.repository) p.repository = submitted!.repository
      if (submitted!.format)     p.format     = submitted!.format
      if (submitted!.name)       p.name       = submitted!.name
      if (submitted!.group)      p.group      = submitted!.group
      if (submitted!.version)    p.version    = submitted!.version
      return nexusApi.search(p).then(r => r.data)
    },
    enabled: !!submitted,
  })

  const allItems = data?.items ?? []

  // Docker digest-alias components (version = "sha256:...") are filtered from the main list
  // but kept in a lookup map so they can appear inside the expanded view of their parent tag.
  const dockerDigests = useMemo(() => {
    const map = new Map<string, SearchComponent[]>()
    for (const c of allItems) {
      if (c.format === 'docker' && c.version?.startsWith('sha256:')) {
        const key = `${c.repository}::${c.name}`
        const arr = map.get(key) ?? []
        arr.push(c)
        map.set(key, arr)
      }
    }
    return map
  }, [allItems])

  const items = useMemo(() =>
    allItems.filter(c => !(c.format === 'docker' && c.version?.startsWith('sha256:')))
  , [allItems])

  const sorted = useMemo(() => {
    return [...items].sort((a, b) => {
      let cmp = 0
      if (sortKey === 'name')    cmp = (a.name ?? '').localeCompare(b.name ?? '')
      if (sortKey === 'version') cmp = (a.version ?? '').localeCompare(b.version ?? '')
      if (sortKey === 'date')    cmp = (a.assets?.[0]?.lastModified ?? '').localeCompare(b.assets?.[0]?.lastModified ?? '')
      return sortDir === 'asc' ? cmp : -cmp
    })
  }, [items, sortKey, sortDir])

  const grouped = useMemo(() => {
    const map = new Map<string, SearchComponent[]>()
    for (const c of sorted) {
      const arr = map.get(c.repository) ?? []
      arr.push(c)
      map.set(c.repository, arr)
    }
    return map
  }, [sorted])

  function handleSort(key: SortKey) {
    if (sortKey === key) setSortDir(d => d === 'asc' ? 'desc' : 'asc')
    else { setSortKey(key); setSortDir('asc') }
  }

  function toggleExpand(id: string) {
    setExpanded(prev => {
      const next = new Set(prev)
      next.has(id) ? next.delete(id) : next.add(id)
      return next
    })
  }

  const handleSubmit = (e: FormEvent) => { e.preventDefault(); setSubmitted({ ...filters }) }
  const handleClear  = () => { setFilters(EMPTY_FILTERS); setSubmitted(null); setExpanded(new Set()) }
  const set = (key: keyof typeof EMPTY_FILTERS) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setFilters(f => ({ ...f, [key]: e.target.value }))

  const theadStyle = { ...S.thead, gridTemplateColumns: S.COLS }
  const trowStyle  = { ...S.trow,  gridTemplateColumns: S.COLS }

  return (
    <div style={S.page}>
      <div style={S.header}>
        <h1 style={S.title}>Search</h1>
        <p style={S.subtitle}>Find artifacts across all repositories</p>
      </div>

      <form style={S.filterCard} onSubmit={handleSubmit}>
        <div style={S.filterGrid}>
          <div style={S.field}>
            <label style={S.label}>Repository</label>
            <input style={S.input} placeholder="any" value={filters.repository} onChange={set('repository')} />
          </div>
          <div style={S.field}>
            <label style={S.label}>Format</label>
            <Select
              options={[
                { value: '', label: 'any' },
                ...['maven2','npm','docker','pypi','go','nuget','helm','raw','apt','yum'].map(f => ({ value: f, label: f })),
              ]}
              value={filters.format}
              onChange={v => setFilters(f => ({ ...f, format: v }))}
            />
          </div>
          <div style={S.field}>
            <label style={S.label}>Name</label>
            <input style={S.input} placeholder="e.g. spring-core" value={filters.name} onChange={set('name')} />
          </div>
          <div style={S.field}>
            <label style={S.label}>Group</label>
            <input style={S.input} placeholder="e.g. org.springframework" value={filters.group} onChange={set('group')} />
          </div>
          <div style={S.field}>
            <label style={S.label}>Version</label>
            <input style={S.input} placeholder="e.g. 1.2.3" value={filters.version} onChange={set('version')} />
          </div>
        </div>
        <div style={S.filterFooter}>
          <button type="submit" style={S.searchBtn}><Search size={14} /> Search</button>
          <button type="button" style={S.clearBtn} onClick={handleClear}>Clear</button>
          {submitted && !isLoading && (
            <span style={S.resultsLabel}>{items.length} result{items.length !== 1 ? 's' : ''} in {grouped.size} repo{grouped.size !== 1 ? 's' : ''}</span>
          )}
        </div>
      </form>

      {!submitted ? (
        <div style={S.empty}>
          <Search size={40} style={{ opacity: 0.3 }} />
          <p>Enter filters and click Search</p>
        </div>
      ) : isLoading ? (
        <div style={S.empty}>Searching…</div>
      ) : items.length === 0 ? (
        <div style={S.empty}>
          <Package size={40} style={{ opacity: 0.3 }} />
          <p>No results matched your filters</p>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {/* Sortable column headers — shown once above all groups */}
          <div style={theadStyle}>
            <div style={S.th(sortKey === 'name')} onClick={() => handleSort('name')}>
              Name <SortIcon col="name" sortKey={sortKey} sortDir={sortDir} />
            </div>
            <div>Group</div>
            <div style={S.th(sortKey === 'version')} onClick={() => handleSort('version')}>
              Version <SortIcon col="version" sortKey={sortKey} sortDir={sortDir} />
            </div>
            <div>Path</div>
            <div style={S.th(sortKey === 'date')} onClick={() => handleSort('date')}>
              Modified <SortIcon col="date" sortKey={sortKey} sortDir={sortDir} />
            </div>
          </div>

          {[...grouped.entries()].map(([repo, comps]) => {
            const fmt = comps[0].format
            const color = FORMAT_COLORS[fmt] ?? '#6b7280'
            return (
              <div key={repo} style={S.group}>
                <div style={S.groupHeader}>
                  <span style={S.badge(color)}>{fmt}</span>
                  <span style={S.groupName}>{repo}</span>
                  <span style={S.groupCount}>{comps.length} component{comps.length !== 1 ? 's' : ''}</span>
                  <button
                    onClick={() => navigate(`/browse?repo=${encodeURIComponent(repo)}`)}
                    style={{ marginLeft: 8, background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(147,197,253,0.7)', display: 'flex', alignItems: 'center', gap: 3, fontSize: 12 }}
                    title="Open in Browse"
                  >
                    <ExternalLink size={12} /> Browse
                  </button>
                </div>
                {comps.map(c => {
                  const firstAsset = c.assets?.[0]
                  const isOpen = expanded.has(c.id)
                  const hasMulti = (c.assets?.length ?? 0) > 1
                  return (
                    <div key={c.id}>
                      <div style={trowStyle} onClick={() => toggleExpand(c.id)}>
                        <div style={{ fontWeight: 500, color: '#dbeafe', display: 'flex', alignItems: 'center', gap: 6 }}>
                          <ChevronRight size={12} style={{ color: 'rgba(229,231,235,0.3)', transform: isOpen ? 'rotate(90deg)' : undefined, transition: 'transform 0.15s', flexShrink: 0 }} />
                          {c.name || '—'}
                        </div>
                        <div style={S.muted}>{c.group || '—'}</div>
                        <div style={{ color: '#a5b4fc', fontFamily: 'monospace', fontSize: 12 }}>{c.version || '—'}</div>
                        <div style={S.path}>{firstAsset?.path ?? '—'}{hasMulti ? ` +${c.assets!.length - 1}` : ''}</div>
                        <div style={S.muted}>{fmtDate(firstAsset?.lastModified)} {firstAsset ? `· ${fmtSize(firstAsset.fileSize)}` : ''}</div>
                      </div>
                      {isOpen && (
                        <div style={S.expanded}>
                          {(c.assets ?? []).map(a => (
                            <div key={a.id} style={S.assetRow}>
                              <span style={S.path}>{a.path}</span>
                              <span style={{ ...S.muted, whiteSpace: 'nowrap' as const }}>{fmtSize(a.fileSize)} · {a.contentType || '—'} · {fmtDate(a.lastModified)}</span>
                            </div>
                          ))}
                          {(() => {
                            const digests = dockerDigests.get(`${c.repository}::${c.name}`) ?? []
                            if (digests.length === 0) return null
                            return (
                              <>
                                <div style={{ ...S.muted, marginTop: 8, marginBottom: 4, fontSize: 11, textTransform: 'uppercase' as const, letterSpacing: '0.05em' }}>Digest aliases</div>
                                {digests.flatMap(d => (d.assets ?? []).map(a => (
                                  <div key={a.id} style={S.assetRow}>
                                    <span style={{ ...S.path, color: 'rgba(147,197,253,0.5)' }}>{a.path}</span>
                                    <span style={{ ...S.muted, whiteSpace: 'nowrap' as const }}>{d.version?.slice(0, 19)}… · {fmtSize(a.fileSize)}</span>
                                  </div>
                                )))}
                              </>
                            )
                          })()}
                        </div>
                      )}
                    </div>
                  )
                })}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
