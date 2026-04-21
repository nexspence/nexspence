import { useQuery } from '@tanstack/react-query'
import { Activity, Cpu, Database, Download, HardDrive, RefreshCw, TrendingUp, Upload, Trash2 } from 'lucide-react'
import { nexusApi } from '@/api/client'

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

const S = {
  page:       { padding: 24, display: 'flex', flexDirection: 'column' as const, gap: 20 },
  header:     { display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', flexWrap: 'wrap' as const, gap: 12 },
  title:      { fontSize: 20, fontWeight: 700, color: '#dbeafe', margin: '0 0 4px' },
  subtitle:   { fontSize: 13, color: 'rgba(229,231,235,0.5)', margin: 0 },
  grid3:      { display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 14 },
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

export default function MonitoringPage() {
  const { data, isLoading, dataUpdatedAt, refetch } = useQuery<MetricsSnapshot>({
    queryKey: ['metrics'],
    queryFn: () => nexusApi.getMetrics().then(r => r.data),
    refetchInterval: 10_000,
  })

  const m = data
  const errorRate = m && m.requests_total > 0
    ? ((m.request_errors / m.requests_total) * 100).toFixed(1)
    : '0.0'
  const errColor = m && m.request_errors > 0 ? '#f59e0b' : '#22c55e'
  const heapPct = m ? Math.min((m.memory.alloc_bytes / m.memory.sys_bytes) * 100, 100) : 0

  const lastUpdate = dataUpdatedAt ? new Date(dataUpdatedAt).toLocaleTimeString() : '—'

  return (
    <div style={S.page}>
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
        <p style={{ color: 'rgba(229,231,235,0.4)', fontSize: 14 }}>Loading…</p>
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
              <div style={S.bar}>
                <div style={{ height: '100%', width: heapPct + '%', background: heapPct > 80 ? '#ef4444' : '#3b82f6', transition: 'width 0.4s' }} />
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
                <span style={S.rowVal}>{m.artifacts_deleted.toLocaleString()}</span>
              </div>
              <div style={{ ...S.row, borderBottom: 'none' }}>
                <span style={S.rowKey}>Error rate</span>
                <span style={S.badge(errColor)}>{errorRate}%</span>
              </div>
            </div>
          </div>
        </>
      ) : (
        <p style={{ color: 'rgba(239,68,68,0.7)', fontSize: 14 }}>Failed to load metrics</p>
      )}
    </div>
  )
}
