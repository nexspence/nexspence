import { useQuery } from '@tanstack/react-query'
import { CheckCircle, Database, HardDrive, Info, RefreshCw } from 'lucide-react'
import { nexusApi, nexspenceApi } from '@/api/client'

interface BlobStore {
  id: string; name: string; type: string; usedBytes: number; quotaBytes?: number
}
interface SystemInfo { version: string; edition: string; product: string }

const S = {
  page: { padding: 24, display: 'flex', flexDirection: 'column' as const, gap: 24 },
  header: { display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between' },
  title: { fontSize: 20, fontWeight: 700, color: '#dbeafe', margin: '0 0 4px' },
  subtitle: { fontSize: 13, color: 'rgba(229,231,235,0.5)', margin: 0 },
  grid2: { display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 },
  card: { background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 14, padding: 20 },
  cardTitle: { fontSize: 13, fontWeight: 600, color: 'rgba(229,231,235,0.6)', textTransform: 'uppercase' as const, letterSpacing: '0.06em', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 8 },
  statusRow: { display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 },
  statusDot: (ok: boolean) => ({ width: 8, height: 8, borderRadius: '50%', background: ok ? '#22c55e' : '#ef4444', boxShadow: ok ? '0 0 6px #22c55e66' : '0 0 6px #ef444466', flexShrink: 0 }),
  infoRow: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: '1px solid rgba(255,255,255,0.05)', fontSize: 13 },
  infoKey: { color: 'rgba(229,231,235,0.5)' },
  infoVal: { color: '#dbeafe', fontWeight: 500 },
  table: { background: 'rgba(255,255,255,0.02)', border: '1px solid rgba(255,255,255,0.07)', borderRadius: 12, overflow: 'hidden' as const },
  thead: { display: 'grid', gridTemplateColumns: '2fr 1fr 2fr 1fr', padding: '10px 16px', background: 'rgba(255,255,255,0.03)', borderBottom: '1px solid rgba(255,255,255,0.07)', fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.5)', textTransform: 'uppercase' as const, letterSpacing: '0.05em' },
  trow: { display: 'grid', gridTemplateColumns: '2fr 1fr 2fr 1fr', padding: '11px 16px', borderBottom: '1px solid rgba(255,255,255,0.05)', fontSize: 13, color: '#e5e7eb', alignItems: 'center' },
  muted: { fontSize: 12, color: 'rgba(229,231,235,0.4)' },
  progressBar: { height: 4, borderRadius: 2, background: 'rgba(255,255,255,0.08)', overflow: 'hidden' as const, marginTop: 4, width: '100%' },
  iconBtn: { background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: 8, color: 'rgba(229,231,235,0.7)', cursor: 'pointer', display: 'flex', alignItems: 'center' },
}

function fmtGB(b: number) {
  return (b / 1024 / 1024 / 1024).toFixed(2) + ' GB'
}
function fmtMB(b: number) {
  return (b / 1024 / 1024).toFixed(1) + ' MB'
}
function fmtBytes(b: number) {
  if (b < 1024) return b + ' B'
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB'
  if (b < 1024 * 1024 * 1024) return fmtMB(b)
  return fmtGB(b)
}

export default function AdminPage() {
  const { data: status, isLoading: statusLoading, refetch: refetchStatus } = useQuery({
    queryKey: ['status'],
    queryFn: () => nexusApi.getStatus().then(r => r.data),
  })

  const { data: blobs = [], isLoading: blobsLoading, refetch: refetchBlobs } = useQuery<BlobStore[]>({
    queryKey: ['blobstores'],
    queryFn: () => nexusApi.listBlobStores().then(r => r.data),
  })

  const { data: info } = useQuery<SystemInfo>({
    queryKey: ['systemInfo'],
    queryFn: () => nexspenceApi.getSystemInfo().then(r => r.data),
  })

  const isOnline = status?.status === 'ok'

  return (
    <div style={S.page}>
      <div style={S.header}>
        <div>
          <h1 style={S.title}>System Admin</h1>
          <p style={S.subtitle}>Server health, blob stores and configuration</p>
        </div>
        <button style={S.iconBtn} onClick={() => { refetchStatus(); refetchBlobs() }} title="Refresh">
          <RefreshCw size={16} />
        </button>
      </div>

      <div style={S.grid2}>
        {/* Status card */}
        <div style={S.card}>
          <div style={S.cardTitle}>
            <CheckCircle size={14} /> System Status
          </div>
          {statusLoading ? (
            <p style={S.muted}>Loading…</p>
          ) : (
            <div style={S.statusRow}>
              <div style={S.statusDot(isOnline)} />
              <span style={{ fontSize: 14, fontWeight: 600, color: isOnline ? '#22c55e' : '#ef4444' }}>
                {isOnline ? 'Online' : 'Offline'}
              </span>
              <span style={{ fontSize: 12, color: 'rgba(229,231,235,0.4)', marginLeft: 4 }}>
                {status?.edition ?? ''}
              </span>
            </div>
          )}
        </div>

        {/* System Info card */}
        <div style={S.card}>
          <div style={S.cardTitle}>
            <Info size={14} /> System Info
          </div>
          {info ? (
            <>
              <div style={S.infoRow}>
                <span style={S.infoKey}>Product</span>
                <span style={S.infoVal}>{info.product}</span>
              </div>
              <div style={S.infoRow}>
                <span style={S.infoKey}>Version</span>
                <span style={S.infoVal}>{info.version}</span>
              </div>
              <div style={{ ...S.infoRow, borderBottom: 'none' }}>
                <span style={S.infoKey}>Edition</span>
                <span style={S.infoVal}>{info.edition}</span>
              </div>
            </>
          ) : (
            <p style={S.muted}>Loading…</p>
          )}
        </div>
      </div>

      {/* Blob stores */}
      <div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
          <HardDrive size={15} style={{ color: 'rgba(229,231,235,0.5)' }} />
          <span style={{ fontSize: 15, fontWeight: 600, color: '#dbeafe' }}>Blob Stores</span>
          <span style={{ fontSize: 12, color: 'rgba(229,231,235,0.4)', marginLeft: 4 }}>
            {blobs.length} total
          </span>
        </div>

        {blobsLoading ? (
          <p style={S.muted}>Loading…</p>
        ) : blobs.length === 0 ? (
          <div style={{ ...S.card, textAlign: 'center' as const, color: 'rgba(229,231,235,0.4)', fontSize: 14 }}>
            <Database size={32} style={{ opacity: 0.3, margin: '0 auto 8px' }} />
            <p>No blob stores configured</p>
          </div>
        ) : (
          <div style={S.table}>
            <div style={S.thead}>
              <div>Name</div>
              <div>Type</div>
              <div>Used</div>
              <div>Quota</div>
            </div>
            {blobs.map(bs => {
              const usedPct = bs.quotaBytes ? Math.min((bs.usedBytes / bs.quotaBytes) * 100, 100) : 0
              const overQuota = bs.quotaBytes && bs.usedBytes > bs.quotaBytes
              const barColor = overQuota ? '#ef4444' : usedPct > 80 ? '#f59e0b' : '#3b82f6'
              return (
                <div key={bs.id} style={S.trow}>
                  <div style={{ fontWeight: 600, color: '#dbeafe' }}>{bs.name}</div>
                  <div>
                    <span style={{ fontSize: 11, fontWeight: 600, padding: '2px 8px', borderRadius: 4, background: 'rgba(59,130,246,0.15)', color: '#93c5fd' }}>
                      {bs.type}
                    </span>
                  </div>
                  <div>
                    <div style={{ fontSize: 13 }}>{fmtBytes(bs.usedBytes)}</div>
                    {bs.quotaBytes && (
                      <div style={S.progressBar}>
                        <div style={{ height: '100%', width: usedPct + '%', background: barColor, transition: 'width 0.3s' }} />
                      </div>
                    )}
                  </div>
                  <div style={S.muted}>
                    {bs.quotaBytes ? fmtBytes(bs.quotaBytes) : 'Unlimited'}
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
