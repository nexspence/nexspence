import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { FolderOpen, Package, ChevronDown, RefreshCw } from 'lucide-react'
import { nexusApi, apiClient } from '@/api/client'

interface Repository { id: string; name: string; format: string; type: string }
interface Component {
  id: string; name: string; group: string; version: string; format: string
  assets?: { id: string; path: string; fileSize: number; contentType: string }[]
}

const S = {
  page: { padding: 24, display: 'flex', flexDirection: 'column' as const, gap: 20, height: '100%' },
  header: { display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 16 },
  title: { fontSize: 20, fontWeight: 700, color: '#dbeafe', margin: '0 0 4px' },
  subtitle: { fontSize: 13, color: 'rgba(229,231,235,0.5)', margin: 0 },
  toolbar: { display: 'flex', gap: 12, alignItems: 'center' },
  selectWrap: { position: 'relative' as const, display: 'flex', alignItems: 'center' },
  select: {
    appearance: 'none' as const,
    background: 'rgba(255,255,255,0.05)',
    border: '1px solid rgba(255,255,255,0.1)',
    borderRadius: 8, padding: '9px 32px 9px 12px',
    color: '#e5e7eb', fontSize: 13, outline: 'none', cursor: 'pointer', minWidth: 220,
  },
  selectIcon: { position: 'absolute' as const, right: 10, color: 'rgba(229,231,235,0.5)', pointerEvents: 'none' as const },
  iconBtn: {
    background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)',
    borderRadius: 8, padding: 8, color: 'rgba(229,231,235,0.7)', cursor: 'pointer',
    display: 'flex', alignItems: 'center',
  },
  empty: { flex: 1, display: 'flex', flexDirection: 'column' as const, alignItems: 'center', justifyContent: 'center', gap: 12, color: 'rgba(229,231,235,0.4)', fontSize: 14 },
  table: { background: 'rgba(255,255,255,0.02)', border: '1px solid rgba(255,255,255,0.07)', borderRadius: 12, overflow: 'hidden' as const },
  thead: { display: 'grid', gridTemplateColumns: '2fr 1.5fr 1fr 1fr 2fr', padding: '10px 16px', background: 'rgba(255,255,255,0.03)', borderBottom: '1px solid rgba(255,255,255,0.07)', fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.5)', textTransform: 'uppercase' as const, letterSpacing: '0.05em' },
  trow: { display: 'grid', gridTemplateColumns: '2fr 1.5fr 1fr 1fr 2fr', padding: '11px 16px', borderBottom: '1px solid rgba(255,255,255,0.05)', fontSize: 13, color: '#e5e7eb', alignItems: 'center' },
  badge: (color: string) => ({ fontSize: 11, fontWeight: 600 as const, padding: '2px 8px', borderRadius: 4, background: color + '22', color }),
  muted: { color: 'rgba(229,231,235,0.4)', fontSize: 12 },
  path: { fontSize: 12, color: 'rgba(147,197,253,0.85)', fontFamily: 'monospace' as const },
  pager: { display: 'flex', gap: 8, alignItems: 'center', justifyContent: 'center', paddingTop: 4 },
  pgBtn: (disabled: boolean) => ({
    background: disabled ? 'rgba(255,255,255,0.03)' : 'rgba(255,255,255,0.07)',
    border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8,
    padding: '6px 14px', color: disabled ? 'rgba(229,231,235,0.25)' : '#e5e7eb',
    fontSize: 13, cursor: disabled ? 'not-allowed' : 'pointer',
  }),
}

const FORMAT_COLORS: Record<string, string> = {
  maven2: '#f97316', npm: '#ef4444', docker: '#3b82f6', pypi: '#a78bfa',
  go: '#06b6d4', nuget: '#8b5cf6', helm: '#0ea5e9', raw: '#6b7280', apt: '#f59e0b', yum: '#10b981',
}

export default function BrowsePage() {
  const [repoName, setRepoName] = useState('')
  const [page, setPage] = useState(0)
  const limit = 25

  const { data: repos = [] } = useQuery<Repository[]>({
    queryKey: ['repositories'],
    queryFn: () => nexusApi.listRepositories().then(r => r.data),
  })

  const { data: components, isLoading, refetch } = useQuery({
    queryKey: ['components', repoName, page],
    queryFn: () =>
      apiClient.get('/service/rest/v1/components', {
        params: { repository: repoName, limit, offset: page * limit },
      }).then(r => r.data as { items: Component[]; continuationToken: string | null }),
    enabled: !!repoName,
  })

  const items = components?.items ?? []
  const hasNext = !!components?.continuationToken

  return (
    <div style={S.page}>
      <div style={S.header}>
        <div>
          <h1 style={S.title}>Browse</h1>
          <p style={S.subtitle}>
            {repoName ? `${items.length} components loaded` : 'Select a repository to browse'}
          </p>
        </div>
        {repoName && (
          <button style={S.iconBtn} onClick={() => refetch()} title="Refresh">
            <RefreshCw size={16} />
          </button>
        )}
      </div>

      <div style={S.toolbar}>
        <div style={S.selectWrap}>
          <select
            style={S.select}
            value={repoName}
            onChange={e => { setRepoName(e.target.value); setPage(0) }}
          >
            <option value="">— Select repository —</option>
            {repos.map(r => (
              <option key={r.id} value={r.name}>{r.name} ({r.format})</option>
            ))}
          </select>
          <ChevronDown size={14} style={S.selectIcon} />
        </div>
      </div>

      {!repoName ? (
        <div style={S.empty}>
          <FolderOpen size={40} style={{ opacity: 0.3 }} />
          <p>Choose a repository above</p>
        </div>
      ) : isLoading ? (
        <div style={S.empty}>Loading…</div>
      ) : items.length === 0 ? (
        <div style={S.empty}>
          <Package size={40} style={{ opacity: 0.3 }} />
          <p>No components in this repository</p>
        </div>
      ) : (
        <>
          <div style={S.table}>
            <div style={S.thead}>
              <div>Name</div>
              <div>Group</div>
              <div>Version</div>
              <div>Format</div>
              <div>Assets</div>
            </div>
            {items.map(c => {
              const color = FORMAT_COLORS[c.format] ?? '#6b7280'
              const firstAsset = c.assets?.[0]
              return (
                <div key={c.id} style={S.trow}>
                  <div style={{ fontWeight: 600, color: '#dbeafe' }}>{c.name}</div>
                  <div style={S.muted}>{c.group || '—'}</div>
                  <div>{c.version}</div>
                  <div><span style={S.badge(color)}>{c.format}</span></div>
                  <div style={S.path}>
                    {firstAsset
                      ? `${firstAsset.path}${c.assets!.length > 1 ? ` +${c.assets!.length - 1}` : ''}`
                      : '—'}
                  </div>
                </div>
              )
            })}
          </div>

          <div style={S.pager}>
            <button style={S.pgBtn(page === 0)} disabled={page === 0} onClick={() => setPage(p => p - 1)}>
              ← Prev
            </button>
            <span style={S.muted}>Page {page + 1}</span>
            <button style={S.pgBtn(!hasNext)} disabled={!hasNext} onClick={() => setPage(p => p + 1)}>
              Next →
            </button>
          </div>
        </>
      )}
    </div>
  )
}
