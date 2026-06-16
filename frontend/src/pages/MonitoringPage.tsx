import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Activity, Cpu, Database, Download, HardDrive, RefreshCw, TrendingUp, Upload, Trash2 } from 'lucide-react'
import { MiniChart } from '@/components/holo'
import { nexusApi, apiClient } from '@/api/client'

interface MemStats {
  alloc_bytes: number
  total_alloc_bytes: number
  sys_bytes: number
  gc_cycles: number
}

interface MetricsSnapshot {
  uptime_seconds: number
  requests_total: number
  request_errors: number
  artifacts_stored: number
  bytes_stored: number
  downloads_total: number
  artifacts_deleted: number
  goroutines: number
  memory: MemStats
}

interface DataPoint {
  timestamp: number
  requests_total: number
  request_errors: number
  artifacts_stored: number
  bytes_stored: number
  downloads_total: number
  goroutines: number
}

interface RepoMetric {
  name: string
  format: string
  type: string
  downloads: number
  size_bytes: number
}

const S = {
  page:       { display: 'flex', flexDirection: 'column' as const, gap: 20 },
  header:     { display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', flexWrap: 'wrap' as const, gap: 12 },
  title:      { fontSize: 20, fontWeight: 700, color: '#dbeafe', margin: '0 0 4px' },
  subtitle:   { fontSize: 13, color: 'rgba(229,231,235,0.5)', margin: 0 },
  grid3:      { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: 14 },
  grid2:      { display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 14 },
  card:       { background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 14, padding: 18 },
  cardTitle:  { fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.45)', textTransform: 'uppercase' as const, letterSpacing: '0.06em', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 6 },
  bigNum:     { fontSize: 28, fontWeight: 700, color: '#dbeafe', lineHeight: 1, marginBottom: 4 },
  label:      { fontSize: 12, color: 'rgba(229,231,235,0.45)' },
  row:        { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '7px 0', borderBottom: '1px solid rgba(255,255,255,0.05)', fontSize: 13 },
  rowKey:     { color: 'rgba(229,231,235,0.5)' },
  rowVal:     { color: '#dbeafe', fontWeight: 600, fontVariantNumeric: 'tabular-nums' as const },
  iconBtn:    { background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: 8, color: 'rgba(229,231,235,0.7)', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 },
  badge:      (color: string) => ({ fontSize: 11, fontWeight: 600, padding: '2px 7px', borderRadius: 4, background: color + '20', color }),
  bar:        { height: 6, borderRadius: 3, background: 'rgba(255,255,255,0.07)', overflow: 'hidden' as const, marginTop: 8 },
  sectionHd:  { fontSize: 14, fontWeight: 600, color: '#dbeafe', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 8 },
  toggleBtn:  (active: boolean): React.CSSProperties => ({
    background: active ? 'rgba(59,130,246,0.2)' : 'rgba(255,255,255,0.04)',
    border: `1px solid ${active ? 'rgba(59,130,246,0.5)' : 'rgba(255,255,255,0.08)'}`,
    borderRadius: 6,
    padding: '4px 10px',
    fontSize: 12,
    color: active ? '#93c5fd' : 'rgba(229,231,235,0.5)',
    cursor: 'pointer',
  }),
}

function fmtBytes(b: number) {
  if (b < 1024) return b + ' B'
  if (b < 1024 ** 2) return (b / 1024).toFixed(1) + ' KB'
  if (b < 1024 ** 3) return (b / 1024 ** 2).toFixed(1) + ' MB'
  return (b / 1024 ** 3).toFixed(2) + ' GB'
}

function fmtUptime(s: number) {
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d > 0) return `${d}d ${h}h ${m}m`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m ${Math.floor(s % 60)}s`
}

function fmtNum(n: number) {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K'
  return String(n)
}

function formatTimeLabel(ts: number): string {
  const ageSeconds = Math.floor(Date.now() / 1000) - ts
  if (ageSeconds < 60) return 'now'
  const mins = Math.floor(ageSeconds / 60)
  return `${mins}m ago`
}

function StatCard({ icon: Icon, color, title, value, sub }: {
  icon: React.ElementType; color: string; title: string; value: string; sub?: string
}) {
  return (
    <div style={S.card}>
      <div style={S.cardTitle}>
        <Icon size={13} style={{ color }} />
        {title}
      </div>
      <div style={{ ...S.bigNum, color }}>{value}</div>
      {sub && <div style={S.label}>{sub}</div>}
    </div>
  )
}

const TABS = [
  { id: 'overview', label: 'Overview' },
  { id: 'charts',   label: 'Charts' },
  { id: 'repos',    label: 'Repositories' },
] as const

type TabId = typeof TABS[number]['id']

function OverviewTab({ data, isLoading, refetch, dataUpdatedAt }: {
  data: MetricsSnapshot | undefined
  isLoading: boolean
  refetch: () => void
  dataUpdatedAt: number
}) {
  const m = data
  const errorRate = m && m.requests_total > 0
    ? ((m.request_errors / m.requests_total) * 100).toFixed(1)
    : '0.0'
  const errColor = m && m.request_errors > 0 ? '#f59e0b' : '#22c55e'
  const heapPct = m ? Math.min((m.memory.alloc_bytes / (m.memory.sys_bytes || 1)) * 100, 100) : 0
  const lastUpdate = dataUpdatedAt ? new Date(dataUpdatedAt).toLocaleTimeString() : '—'

  return (
    <>
      <div style={S.header}>
        <div>
          <h1 style={S.title}>Monitoring</h1>
          <p style={S.subtitle}>Live process counters — auto-refreshes every 10 s</p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <span style={{ fontSize: 12, color: 'rgba(229,231,235,0.4)' }}>Updated {lastUpdate}</span>
          <button style={S.iconBtn} onClick={() => refetch()} title="Refresh now">
            <RefreshCw size={14} />
          </button>
        </div>
      </div>

      {isLoading ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div className="holo-skeleton holo-skeleton--block" />
          <div className="holo-skeleton holo-skeleton--block" />
        </div>
      ) : m ? (
        <>
          {/* Top stat cards */}
          <div style={S.grid3}>
            <StatCard icon={Activity}   color="#3b82f6" title="Total Requests"  value={fmtNum(m.requests_total)} sub="since process start" />
            <StatCard icon={Upload}     color="#22c55e" title="Artifacts Stored" value={fmtNum(m.artifacts_stored)} sub={fmtBytes(m.bytes_stored) + ' written'} />
            <StatCard icon={Download}   color="#a78bfa" title="Downloads"        value={fmtNum(m.downloads_total)} sub="artifact fetches" />
            <StatCard icon={Trash2}     color="#f59e0b" title="Artifacts Deleted" value={fmtNum(m.artifacts_deleted)} sub="since process start" />
            <StatCard icon={TrendingUp} color={errColor} title="Request Errors"  value={m.request_errors.toString()} sub={errorRate + '% error rate'} />
            <StatCard icon={Cpu}        color="#06b6d4" title="Goroutines"        value={m.goroutines.toString()} sub={'uptime ' + fmtUptime(m.uptime_seconds)} />
          </div>

          <div style={S.grid2}>
            {/* Memory */}
            <div style={S.card}>
              <div style={S.sectionHd}>
                <HardDrive size={15} style={{ color: 'rgba(229,231,235,0.5)' }} />
                Memory
              </div>
              <div style={{ ...S.row }}>
                <span style={S.rowKey}>Heap allocated</span>
                <span style={S.rowVal}>{fmtBytes(m.memory.alloc_bytes)}</span>
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <div style={{ ...S.bar, flex: 1 }}>
                  <div style={{ height: '100%', width: heapPct + '%', background: heapPct > 80 ? '#ef4444' : '#3b82f6', transition: 'width 0.4s' }} />
                </div>
                <span style={{ fontSize: 11, fontWeight: 600, color: heapPct > 80 ? '#ef4444' : '#3b82f6', whiteSpace: 'nowrap' as const }}>{heapPct.toFixed(0)}%{heapPct > 80 ? ' HIGH' : ' OK'}</span>
              </div>
              <div style={{ ...S.row, marginTop: 4 }}>
                <span style={S.rowKey}>Total allocated</span>
                <span style={S.rowVal}>{fmtBytes(m.memory.total_alloc_bytes)}</span>
              </div>
              <div style={{ ...S.row }}>
                <span style={S.rowKey}>System reserved</span>
                <span style={S.rowVal}>{fmtBytes(m.memory.sys_bytes)}</span>
              </div>
              <div style={{ ...S.row, borderBottom: 'none' }}>
                <span style={S.rowKey}>GC cycles</span>
                <span style={S.rowVal}>{m.memory.gc_cycles}</span>
              </div>
            </div>

            {/* Throughput summary */}
            <div style={S.card}>
              <div style={S.sectionHd}>
                <Database size={15} style={{ color: 'rgba(229,231,235,0.5)' }} />
                Storage Activity
              </div>
              <div style={S.row}>
                <span style={S.rowKey}>Artifacts stored</span>
                <span style={S.rowVal}>{m.artifacts_stored.toLocaleString()}</span>
              </div>
              <div style={S.row}>
                <span style={S.rowKey}>Total bytes written</span>
                <span style={S.rowVal}>{fmtBytes(m.bytes_stored)}</span>
              </div>
              <div style={S.row}>
                <span style={S.rowKey}>Downloads served</span>
                <span style={S.rowVal}>{m.downloads_total.toLocaleString()}</span>
              </div>
              <div style={S.row}>
                <span style={S.rowKey}>Artifacts deleted</span>
                <span style={S.rowVal}>{(m.artifacts_deleted ?? 0).toLocaleString()}</span>
              </div>
              <div style={{ ...S.row, borderBottom: 'none' }}>
                <span style={S.rowKey}>Error rate</span>
                <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                  <span style={S.badge(errColor)}>{errorRate}%</span>
                  <span style={{ fontSize: 11, color: errColor, fontWeight: 600 }}>{m && m.request_errors > 0 ? 'WARN' : 'OK'}</span>
                </span>
              </div>
            </div>
          </div>
        </>
      ) : (
        <p style={{ color: 'rgba(239,68,68,0.7)', fontSize: 14 }}>Failed to load metrics</p>
      )}
    </>
  )
}

function ChartsTab() {
  const { data: history = [] } = useQuery<DataPoint[]>({
    queryKey: ['metrics-history'],
    queryFn: () => apiClient.get<DataPoint[]>('/api/v1/metrics/history').then(r => r.data),
    refetchInterval: 30_000,
  })

  const chartData = history.map((pt, i) => {
    const prev = history[i - 1]
    const dt = prev ? (pt.timestamp - prev.timestamp) || 10 : 10
    return {
      time: formatTimeLabel(pt.timestamp),
      reqPerSec: prev ? Math.max(0, (pt.requests_total - prev.requests_total) / dt) : 0,
      errPct: prev && (pt.requests_total - prev.requests_total) > 0
        ? Math.max(0, ((pt.request_errors - prev.request_errors) / (pt.requests_total - prev.requests_total)) * 100)
        : 0,
      bytesStored: pt.bytes_stored,
    }
  })

  const noData = (
    <div style={{ height: 200, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'rgba(229,231,235,0.3)', fontSize: 13 }}>
      No data yet — collecting samples every 10s
    </div>
  )

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      {/* Requests/sec */}
      <div style={S.card}>
        <div style={S.cardTitle}>Requests / sec</div>
        {chartData.length === 0 ? noData : (
          <MiniChart
            type="line"
            color="#3b82f6"
            ariaLabel="Requests per second"
            data={chartData.map(d => ({ label: d.time, value: d.reqPerSec }))}
            valueFormatter={v => v.toFixed(2)}
          />
        )}
      </div>

      {/* Error rate % */}
      <div style={S.card}>
        <div style={S.cardTitle}>Error Rate %</div>
        {chartData.length === 0 ? noData : (
          <MiniChart
            type="line"
            color="#f59e0b"
            ariaLabel="Error rate percent"
            data={chartData.map(d => ({ label: d.time, value: d.errPct }))}
            valueFormatter={v => `${v.toFixed(1)}%`}
          />
        )}
      </div>

      {/* Storage growth */}
      <div style={S.card}>
        <div style={S.cardTitle}>Storage (bytes)</div>
        {chartData.length === 0 ? noData : (
          <MiniChart
            type="area"
            color="#22c55e"
            ariaLabel="Storage bytes"
            data={chartData.map(d => ({ label: d.time, value: d.bytesStored }))}
          />
        )}
      </div>
    </div>
  )
}

function ReposTab() {
  const [sortBy, setSortBy] = useState<'downloads' | 'size'>('downloads')

  const { data: repos = [] } = useQuery<RepoMetric[]>({
    queryKey: ['metrics-repos'],
    queryFn: () => apiClient.get<RepoMetric[]>('/api/v1/metrics/repos').then(r => r.data),
    refetchInterval: 60_000,
  })

  const sorted = [...repos].sort((a, b) =>
    sortBy === 'downloads' ? b.downloads - a.downloads : b.size_bytes - a.size_bytes
  ).slice(0, 10)

  return (
    <div style={S.card}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 14 }}>
        <div style={{ fontSize: 14, fontWeight: 600, color: '#dbeafe' }}>Top Repositories</div>
        <div style={{ display: 'flex', gap: 6 }}>
          <button style={S.toggleBtn(sortBy === 'downloads')} onClick={() => setSortBy('downloads')}>Downloads</button>
          <button style={S.toggleBtn(sortBy === 'size')} onClick={() => setSortBy('size')}>Storage</button>
        </div>
      </div>

      {repos.length === 0 ? (
        <p style={{ color: 'rgba(229,231,235,0.3)', fontSize: 13 }}>No data yet</p>
      ) : (
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
          <thead>
            <tr>
              <th style={{ textAlign: 'left', padding: '6px 8px', fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.4)', textTransform: 'uppercase', borderBottom: '1px solid rgba(255,255,255,0.08)' }}>Repository</th>
              <th style={{ textAlign: 'left', padding: '6px 8px', fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.4)', textTransform: 'uppercase', borderBottom: '1px solid rgba(255,255,255,0.08)' }}>Format</th>
              <th style={{ textAlign: 'left', padding: '6px 8px', fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.4)', textTransform: 'uppercase', borderBottom: '1px solid rgba(255,255,255,0.08)' }}>Type</th>
              <th style={{ textAlign: 'right', padding: '6px 8px', fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.4)', textTransform: 'uppercase', borderBottom: '1px solid rgba(255,255,255,0.08)' }}>Downloads</th>
              <th style={{ textAlign: 'right', padding: '6px 8px', fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.4)', textTransform: 'uppercase', borderBottom: '1px solid rgba(255,255,255,0.08)' }}>Storage Used</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map(row => (
              <tr key={row.name}>
                <td style={{ padding: '8px 8px', borderBottom: '1px solid rgba(255,255,255,0.05)', color: '#dbeafe' }}>{row.name}</td>
                <td style={{ padding: '8px 8px', borderBottom: '1px solid rgba(255,255,255,0.05)', color: '#dbeafe' }}>{row.format.toUpperCase()}</td>
                <td style={{ padding: '8px 8px', borderBottom: '1px solid rgba(255,255,255,0.05)', color: '#dbeafe' }}>
                  <span style={{ background: 'rgba(59,130,246,0.15)', color: '#93c5fd', padding: '1px 6px', borderRadius: 4, fontSize: 11 }}>{row.type}</span>
                </td>
                <td style={{ padding: '8px 8px', borderBottom: '1px solid rgba(255,255,255,0.05)', color: '#dbeafe', textAlign: 'right' }}>{fmtNum(row.downloads)}</td>
                <td style={{ padding: '8px 8px', borderBottom: '1px solid rgba(255,255,255,0.05)', color: '#dbeafe', textAlign: 'right' }}>{fmtBytes(row.size_bytes)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

export function MonitoringView() {
  const [tab, setTab] = useState<TabId>('overview')

  const { data, isLoading, dataUpdatedAt, refetch } = useQuery<MetricsSnapshot>({
    queryKey: ['metrics'],
    queryFn: () => nexusApi.getMetrics().then(r => r.data),
    refetchInterval: 10_000,
  })

  return (
    <div style={S.page}>
      {/* Tab bar */}
      <div style={{ display: 'flex', gap: 0, borderBottom: '1px solid rgba(255,255,255,0.08)', marginBottom: 4 }}>
        {TABS.map(t => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            style={{
              background: 'none',
              border: 'none',
              borderBottom: tab === t.id ? '2px solid #3b82f6' : '2px solid transparent',
              color: tab === t.id ? '#dbeafe' : 'rgba(229,231,235,0.45)',
              padding: '8px 16px',
              fontSize: 14,
              fontWeight: tab === t.id ? 600 : 400,
              cursor: 'pointer',
              marginBottom: -1,
              transition: 'color 0.15s',
            }}
          >
            {t.label}
          </button>
        ))}
      </div>

      {tab === 'overview' && (
        <OverviewTab
          data={data}
          isLoading={isLoading}
          refetch={refetch}
          dataUpdatedAt={dataUpdatedAt}
        />
      )}

      {tab === 'charts' && <ChartsTab />}

      {tab === 'repos' && <ReposTab />}
    </div>
  )
}
