import { useState, FormEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Search, Package, ChevronDown } from 'lucide-react'
import { nexusApi } from '@/api/client'

interface Asset {
  id: string
  repository: string
  format: string
  path: string
  fileSize: number
  contentType: string
  downloadUrl?: string
  sha256?: string
  lastModified: string
}

interface SearchResult { items: Asset[] }

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
  selectWrap: { position: 'relative' as const },
  select: {
    appearance: 'none' as const, width: '100%',
    background: 'rgba(255,255,255,0.05)', border: '1px solid rgba(255,255,255,0.1)',
    borderRadius: 8, padding: '8px 30px 8px 12px', color: '#e5e7eb', fontSize: 13, outline: 'none',
  },
  selectIcon: { position: 'absolute' as const, right: 10, top: '50%', transform: 'translateY(-50%)', color: 'rgba(229,231,235,0.5)', pointerEvents: 'none' as const },
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
  table: { background: 'rgba(255,255,255,0.02)', border: '1px solid rgba(255,255,255,0.07)', borderRadius: 12, overflow: 'hidden' as const },
  thead: {
    display: 'grid', gridTemplateColumns: '1.5fr 1fr 3fr 1fr 1fr',
    padding: '10px 16px', background: 'rgba(255,255,255,0.03)',
    borderBottom: '1px solid rgba(255,255,255,0.07)',
    fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.5)',
    textTransform: 'uppercase' as const, letterSpacing: '0.05em',
  },
  trow: {
    display: 'grid', gridTemplateColumns: '1.5fr 1fr 3fr 1fr 1fr',
    padding: '11px 16px', borderBottom: '1px solid rgba(255,255,255,0.05)',
    fontSize: 13, color: '#e5e7eb', alignItems: 'center',
  },
  muted: { fontSize: 12, color: 'rgba(229,231,235,0.4)' },
  path: { fontSize: 12, color: 'rgba(147,197,253,0.85)', fontFamily: 'monospace' as const, wordBreak: 'break-all' as const },
  badge: (color: string) => ({ fontSize: 11, fontWeight: 600 as const, padding: '2px 8px', borderRadius: 4, background: color + '22', color }),
}

const FORMAT_COLORS: Record<string, string> = {
  maven2: '#f97316', npm: '#ef4444', docker: '#3b82f6', pypi: '#a78bfa',
  go: '#06b6d4', nuget: '#8b5cf6', helm: '#0ea5e9', raw: '#6b7280', apt: '#f59e0b', yum: '#10b981',
}

function fmtSize(b: number) {
  if (!b) return '—'
  if (b < 1024) return b + ' B'
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB'
  return (b / 1024 / 1024).toFixed(1) + ' MB'
}

const EMPTY_FILTERS = { repository: '', format: '', name: '', group: '', version: '' }

export default function SearchPage() {
  const [filters, setFilters] = useState(EMPTY_FILTERS)
  const [submitted, setSubmitted] = useState<typeof EMPTY_FILTERS | null>(null)

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

  const items = data?.items ?? []

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault()
    setSubmitted({ ...filters })
  }

  const handleClear = () => {
    setFilters(EMPTY_FILTERS)
    setSubmitted(null)
  }

  const set = (key: keyof typeof EMPTY_FILTERS) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setFilters(f => ({ ...f, [key]: e.target.value }))

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
            <div style={S.selectWrap}>
              <select style={S.select} value={filters.format} onChange={set('format')}>
                <option value="">any</option>
                {['maven2','npm','docker','pypi','go','nuget','helm','raw','apt','yum'].map(f => (
                  <option key={f} value={f}>{f}</option>
                ))}
              </select>
              <ChevronDown size={13} style={S.selectIcon} />
            </div>
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
          <button type="submit" style={S.searchBtn}>
            <Search size={14} /> Search
          </button>
          <button type="button" style={S.clearBtn} onClick={handleClear}>Clear</button>
          {submitted && !isLoading && (
            <span style={S.resultsLabel}>{items.length} result{items.length !== 1 ? 's' : ''}</span>
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
        <div style={S.table}>
          <div style={S.thead}>
            <div>Repository</div>
            <div>Format</div>
            <div>Path</div>
            <div>Size</div>
            <div>Modified</div>
          </div>
          {items.map(a => {
            const color = FORMAT_COLORS[a.format] ?? '#6b7280'
            return (
              <div key={a.id} style={S.trow}>
                <div style={{ color: '#dbeafe', fontWeight: 500 }}>{a.repository}</div>
                <div><span style={S.badge(color)}>{a.format}</span></div>
                <div style={S.path}>{a.path}</div>
                <div style={S.muted}>{fmtSize(a.fileSize)}</div>
                <div style={S.muted}>
                  {a.lastModified ? new Date(a.lastModified).toLocaleDateString() : '—'}
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
