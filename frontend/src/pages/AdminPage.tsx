import { useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle, Database, Download, HardDrive, Info, Pencil, RefreshCw, Upload, X } from 'lucide-react'
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
  btn: (variant: 'primary' | 'danger') => ({
    display: 'inline-flex', alignItems: 'center', gap: 7,
    padding: '8px 16px', borderRadius: 8, border: 'none', cursor: 'pointer',
    fontSize: 13, fontWeight: 600,
    background: variant === 'primary' ? 'rgba(59,130,246,0.18)' : 'rgba(239,68,68,0.15)',
    color: variant === 'primary' ? '#93c5fd' : '#fca5a5',
  }),
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
  const [exportBusy, setExportBusy] = useState(false)
  const [restoreBusy, setRestoreBusy] = useState(false)
  const [restoreResult, setRestoreResult] = useState<Record<string, number> | null>(null)
  const [restoreError, setRestoreError] = useState('')
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [editingQuota, setEditingQuota] = useState<string | null>(null) // blob store id
  const [quotaInput, setQuotaInput] = useState('')
  const qc = useQueryClient()

  const handleExport = async () => {
    setExportBusy(true)
    try {
      const res = await nexspenceApi.exportBackup()
      const url = URL.createObjectURL(res.data as Blob)
      const a = document.createElement('a')
      const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)
      a.href = url
      a.download = `nexspence-backup-${ts}.tar.gz`
      a.click()
      URL.revokeObjectURL(url)
    } finally {
      setExportBusy(false)
    }
  }

  const handleRestore = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    setRestoreResult(null)
    setRestoreError('')
    setRestoreBusy(true)
    try {
      const res = await nexspenceApi.restoreBackup(file)
      setRestoreResult(res.data.restored)
    } catch (err: unknown) {
      const msg = (err as { response?: { data?: { error?: string } } })?.response?.data?.error ?? 'Restore failed'
      setRestoreError(msg)
    } finally {
      setRestoreBusy(false)
      if (fileInputRef.current) fileInputRef.current.value = ''
    }
  }

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

  const quotaMut = useMutation({
    mutationFn: ({ bs, gb }: { bs: BlobStore; gb: string }) => {
      const bytes = gb.trim() === '' ? null : Math.round(parseFloat(gb) * 1024 * 1024 * 1024)
      return nexusApi.updateBlobStore(bs.type, bs.name, { quotaBytes: bytes })
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['blobstores'] }); setEditingQuota(null) },
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

      {/* Backup / Restore */}
      <div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
          <Database size={15} style={{ color: 'rgba(229,231,235,0.5)' }} />
          <span style={{ fontSize: 15, fontWeight: 600, color: '#dbeafe' }}>Backup &amp; Restore</span>
        </div>
        <div style={{ ...S.card, display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' as const, alignItems: 'center' }}>
            <button style={S.btn('primary')} onClick={handleExport} disabled={exportBusy}>
              <Download size={14} />
              {exportBusy ? 'Exporting…' : 'Export backup'}
            </button>
            <button style={S.btn('primary')} onClick={() => fileInputRef.current?.click()} disabled={restoreBusy}>
              <Upload size={14} />
              {restoreBusy ? 'Restoring…' : 'Restore from backup'}
            </button>
            <input ref={fileInputRef} type="file" accept=".tar.gz,.tgz" style={{ display: 'none' }} onChange={handleRestore} />
          </div>
          <p style={{ ...S.muted, margin: 0 }}>
            Export creates a full <code style={{ color: '#93c5fd' }}>.tar.gz</code> archive of all repositories, users, roles, policies, components, assets and blobs.
            Restore is non-destructive — existing records are skipped.
          </p>
          {restoreError && (
            <div style={{ background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, padding: '10px 14px', color: '#fca5a5', fontSize: 13 }}>
              {restoreError}
            </div>
          )}
          {restoreResult && (
            <div style={{ background: 'rgba(34,197,94,0.08)', border: '1px solid rgba(34,197,94,0.25)', borderRadius: 8, padding: '10px 14px', fontSize: 13 }}>
              <span style={{ color: '#86efac', fontWeight: 600, marginBottom: 6, display: 'block' }}>Restore complete</span>
              <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' as const }}>
                {Object.entries(restoreResult).map(([k, v]) => (
                  <span key={k} style={{ color: 'rgba(229,231,235,0.7)' }}>
                    <span style={{ color: '#dbeafe', fontWeight: 600 }}>{v}</span> {k}
                  </span>
                ))}
              </div>
            </div>
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
              const isEditing = editingQuota === bs.id
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
                  <div>
                    {isEditing ? (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        <input
                          type="number" min="0" step="0.1" autoFocus
                          value={quotaInput}
                          onChange={e => setQuotaInput(e.target.value)}
                          placeholder="GB"
                          style={{ width: 72, background: 'rgba(255,255,255,0.07)', border: '1px solid rgba(59,130,246,0.5)', borderRadius: 6, padding: '3px 7px', color: '#e5e7eb', fontSize: 12, outline: 'none' }}
                          onKeyDown={e => {
                            if (e.key === 'Enter') quotaMut.mutate({ bs, gb: quotaInput })
                            if (e.key === 'Escape') setEditingQuota(null)
                          }}
                        />
                        <span style={{ fontSize: 11, color: 'rgba(229,231,235,0.4)' }}>GB</span>
                        <button
                          style={{ background: 'rgba(34,197,94,0.15)', border: '1px solid rgba(34,197,94,0.3)', borderRadius: 6, padding: '3px 8px', color: '#22c55e', fontSize: 11, cursor: 'pointer' }}
                          onClick={() => quotaMut.mutate({ bs, gb: quotaInput })}
                        >Save</button>
                        <button
                          style={{ background: 'none', border: 'none', color: 'rgba(229,231,235,0.4)', cursor: 'pointer', padding: 2 }}
                          onClick={() => setEditingQuota(null)}
                        ><X size={12} /></button>
                      </div>
                    ) : (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        <span style={overQuota ? { color: '#ef4444', fontSize: 12 } : S.muted}>
                          {bs.quotaBytes ? fmtBytes(bs.quotaBytes) : 'Unlimited'}
                        </span>
                        <button
                          title="Edit quota"
                          style={{ background: 'none', border: 'none', color: 'rgba(229,231,235,0.3)', cursor: 'pointer', padding: 2, display: 'flex', alignItems: 'center' }}
                          onClick={() => {
                            setEditingQuota(bs.id)
                            setQuotaInput(bs.quotaBytes ? (bs.quotaBytes / 1024 / 1024 / 1024).toFixed(1) : '')
                          }}
                        ><Pencil size={11} /></button>
                      </div>
                    )}
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
