import { useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, CheckCircle, Database, Download, HardDrive, Info, Pencil, Plus, RefreshCw, Trash2, Upload, X } from 'lucide-react'
import { nexusApi, nexspenceApi } from '@/api/client'
import { MonitoringView } from '@/pages/MonitoringPage'
import { Select } from '@/components/Select'

interface BlobStore {
  id: string; name: string; type: string; usedBytes: number; quotaBytes?: number; config?: Record<string, unknown>
}
interface LinkedRepo { name: string; format: string; type: string; bytesUsed: number }
interface UsageResp {
  store: BlobStore
  linkedRepositories: LinkedRepo[]
  totalAssetBytes: number
  quotaRemaining?: number
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
  tabBar: { display: 'flex', gap: 4, borderBottom: '1px solid rgba(255,255,255,0.08)', marginBottom: 4 },
  tab: (active: boolean) => ({
    display: 'inline-flex', alignItems: 'center', gap: 7,
    padding: '10px 16px', border: 'none', cursor: 'pointer',
    fontSize: 13, fontWeight: 600,
    background: 'transparent',
    color: active ? '#93c5fd' : 'rgba(229,231,235,0.55)',
    borderBottom: active ? '2px solid #3b82f6' : '2px solid transparent',
    marginBottom: -1,
  }),
}

type AdminTab = 'blobs' | 'backup' | 'monitoring'
const VALID_TABS: AdminTab[] = ['blobs', 'backup', 'monitoring']

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
  const [searchParams, setSearchParams] = useSearchParams()
  const tabParam = searchParams.get('tab') as AdminTab | null
  const tab: AdminTab = tabParam && VALID_TABS.includes(tabParam) ? tabParam : 'blobs'
  const setTab = (t: AdminTab) => {
    const next = new URLSearchParams(searchParams)
    next.set('tab', t)
    setSearchParams(next, { replace: true })
  }

  const [exportBusy, setExportBusy] = useState(false)
  const [restoreBusy, setRestoreBusy] = useState(false)
  const [restoreResult, setRestoreResult] = useState<Record<string, number> | null>(null)
  const [restoreError, setRestoreError] = useState('')
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [editingQuota, setEditingQuota] = useState<string | null>(null) // blob store id
  const [quotaInput, setQuotaInput] = useState('')
  const [detailName, setDetailName] = useState<string | null>(null) // open detail modal for this blob store
  const [createOpen, setCreateOpen] = useState(false)
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

      {/* Tabs */}
      <div style={S.tabBar}>
        <button style={S.tab(tab === 'blobs')} onClick={() => setTab('blobs')}>
          <HardDrive size={14} /> Blob Stores
        </button>
        <button style={S.tab(tab === 'backup')} onClick={() => setTab('backup')}>
          <Database size={14} /> Backup &amp; Restore
        </button>
        <button style={S.tab(tab === 'monitoring')} onClick={() => setTab('monitoring')}>
          <Activity size={14} /> Monitoring
        </button>
      </div>

      {/* Backup / Restore */}
      {tab === 'backup' && (
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
      )}

      {/* Blob stores */}
      {tab === 'blobs' && (
      <div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
          <HardDrive size={15} style={{ color: 'rgba(229,231,235,0.5)' }} />
          <span style={{ fontSize: 15, fontWeight: 600, color: '#dbeafe' }}>Blob Stores</span>
          <span style={{ fontSize: 12, color: 'rgba(229,231,235,0.4)', marginLeft: 4 }}>
            {blobs.length} total
          </span>
          <button style={{ ...S.btn('primary'), marginLeft: 'auto' }} onClick={() => setCreateOpen(true)}>
            <Plus size={14} /> New Blob Store
          </button>
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
                <div
                  key={bs.id}
                  style={{ ...S.trow, cursor: 'pointer' }}
                  onClick={(e) => {
                    // Don't open detail if click is on a button or input
                    const t = e.target as HTMLElement
                    if (t.closest('button') || t.closest('input')) return
                    setDetailName(bs.name)
                  }}
                >
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
      )}

      {/* Monitoring */}
      {tab === 'monitoring' && <MonitoringView />}

      {detailName && <BlobStoreDetailModal name={detailName} onClose={() => setDetailName(null)} />}
      {createOpen && <CreateBlobStoreModal onClose={() => setCreateOpen(false)} />}
    </div>
  )
}

// ── BlobStoreDetailModal ─────────────────────────────────────────
function BlobStoreDetailModal({ name, onClose }: { name: string; onClose: () => void }) {
  const qc = useQueryClient()
  const { data, isLoading, error } = useQuery<UsageResp>({
    queryKey: ['blobstore-usage', name],
    queryFn: () => nexusApi.getBlobStoreUsage(name).then(r => r.data),
  })
  const [deleteError, setDeleteError] = useState('')
  const delMut = useMutation({
    mutationFn: () => nexusApi.deleteBlobStore(name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['blobstores'] })
      onClose()
    },
    onError: (err: unknown) => {
      const msg = (err as { response?: { data?: { error?: string } } })?.response?.data?.error ?? 'Delete failed'
      setDeleteError(msg)
    },
  })

  const linked = data?.linkedRepositories ?? []
  const bs = data?.store
  const used = bs ? fmtBytes(bs.usedBytes) : '—'
  const quota = bs?.quotaBytes ? fmtBytes(bs.quotaBytes) : 'Unlimited'
  const remaining = data?.quotaRemaining !== undefined ? fmtBytes(data.quotaRemaining) : null
  const canDelete = linked.length === 0

  return (
    <ModalShell title={`Blob Store: ${name}`} onClose={onClose} width={640}>
      {isLoading && <p style={{ color: 'rgba(229,231,235,0.5)' }}>Loading…</p>}
      {error && <p style={{ color: '#fca5a5' }}>Failed to load usage</p>}
      {bs && (
        <>
          <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '6px 14px', fontSize: 13, marginBottom: 16 }}>
            <span style={{ color: 'rgba(229,231,235,0.5)' }}>Type</span>
            <span style={{ color: '#dbeafe' }}>{bs.type}</span>
            <span style={{ color: 'rgba(229,231,235,0.5)' }}>Used</span>
            <span style={{ color: '#dbeafe' }}>{used}</span>
            <span style={{ color: 'rgba(229,231,235,0.5)' }}>Quota</span>
            <span style={{ color: '#dbeafe' }}>{quota}</span>
            {remaining !== null && (
              <>
                <span style={{ color: 'rgba(229,231,235,0.5)' }}>Remaining</span>
                <span style={{ color: '#dbeafe' }}>{remaining}</span>
              </>
            )}
            <span style={{ color: 'rgba(229,231,235,0.5)' }}>Asset total</span>
            <span style={{ color: '#dbeafe' }}>{fmtBytes(data?.totalAssetBytes ?? 0)} across {linked.length} {linked.length === 1 ? 'repo' : 'repos'}</span>
          </div>

          <div style={{ fontSize: 12, fontWeight: 600, color: 'rgba(229,231,235,0.5)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 8 }}>
            Linked Repositories
          </div>
          {linked.length === 0 ? (
            <div style={{ padding: '20px 16px', background: 'rgba(255,255,255,0.02)', border: '1px dashed rgba(255,255,255,0.08)', borderRadius: 8, textAlign: 'center', color: 'rgba(229,231,235,0.4)', fontSize: 13 }}>
              No repositories use this blob store.
            </div>
          ) : (
            <div style={{ background: 'rgba(255,255,255,0.02)', border: '1px solid rgba(255,255,255,0.07)', borderRadius: 10, overflow: 'hidden' }}>
              <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 1fr 1fr', padding: '8px 14px', background: 'rgba(255,255,255,0.03)', fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.5)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                <div>Name</div><div>Format</div><div>Type</div><div>Used</div>
              </div>
              {linked.map(r => (
                <Link key={r.name} to={`/browse?repo=${encodeURIComponent(r.name)}`} style={{ textDecoration: 'none' }}>
                  <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 1fr 1fr', padding: '10px 14px', borderTop: '1px solid rgba(255,255,255,0.05)', fontSize: 13, color: '#e5e7eb', cursor: 'pointer' }}>
                    <div style={{ color: '#93c5fd', fontWeight: 600 }}>{r.name}</div>
                    <div>{r.format}</div>
                    <div>{r.type}</div>
                    <div>{fmtBytes(r.bytesUsed)}</div>
                  </div>
                </Link>
              ))}
            </div>
          )}

          {deleteError && (
            <div style={{ marginTop: 12, background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, padding: '8px 12px', color: '#fca5a5', fontSize: 13 }}>
              {deleteError}
            </div>
          )}

          <div style={{ marginTop: 20, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <button
              style={{
                display: 'inline-flex', alignItems: 'center', gap: 6,
                padding: '8px 14px', borderRadius: 8, border: 'none', cursor: canDelete && !delMut.isPending ? 'pointer' : 'not-allowed',
                fontSize: 13, fontWeight: 600,
                background: canDelete ? 'rgba(239,68,68,0.15)' : 'rgba(239,68,68,0.05)',
                color: canDelete ? '#fca5a5' : 'rgba(252,165,165,0.4)',
                opacity: canDelete ? 1 : 0.5,
              }}
              disabled={!canDelete || delMut.isPending}
              title={canDelete ? 'Delete this blob store' : 'Detach all repositories first'}
              onClick={() => {
                setDeleteError('')
                if (confirm(`Delete blob store "${name}"? This cannot be undone.`)) delMut.mutate()
              }}
            >
              <Trash2 size={13} />
              {delMut.isPending ? 'Deleting…' : 'Delete'}
            </button>
            <button style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: '8px 16px', color: '#dbeafe', fontSize: 13, fontWeight: 600, cursor: 'pointer' }} onClick={onClose}>
              Close
            </button>
          </div>
        </>
      )}
    </ModalShell>
  )
}

// ── CreateBlobStoreModal ──────────────────────────────────────────
function CreateBlobStoreModal({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [type, setType] = useState<'local' | 's3'>('local')
  const [path, setPath] = useState('./data/blobs/')
  const [bucket, setBucket] = useState('')
  const [region, setRegion] = useState('us-east-1')
  const [endpoint, setEndpoint] = useState('')
  const [prefix, setPrefix] = useState('')
  const [accessKey, setAccessKey] = useState('')
  const [secretKey, setSecretKey] = useState('')
  const [quotaGB, setQuotaGB] = useState('')
  const [err, setErr] = useState('')

  const mut = useMutation({
    mutationFn: () => {
      const quotaBytes = quotaGB.trim() === '' ? null : Math.round(parseFloat(quotaGB) * 1024 * 1024 * 1024)
      const config: Record<string, unknown> = type === 'local'
        ? { path }
        : { bucket, region, endpoint, prefix, access_key: accessKey, secret_key: secretKey }
      return nexusApi.createBlobStore(type, { name, config, quotaBytes })
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['blobstores'] })
      onClose()
    },
    onError: (e: unknown) => {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error ?? 'Create failed'
      setErr(msg)
    },
  })

  const inputStyle: React.CSSProperties = {
    width: '100%', background: 'rgba(255,255,255,0.05)', border: '1px solid rgba(255,255,255,0.1)',
    borderRadius: 8, padding: '8px 12px', color: '#e5e7eb', fontSize: 13, outline: 'none',
  }
  const labelStyle: React.CSSProperties = { fontSize: 12, fontWeight: 600, color: 'rgba(229,231,235,0.6)', marginBottom: 4, display: 'block' }

  return (
    <ModalShell title="New Blob Store" onClose={onClose} width={500}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <div>
          <label style={labelStyle}>Name</label>
          <input style={inputStyle} value={name} onChange={e => setName(e.target.value)} placeholder="e.g. fast-ssd" autoFocus />
        </div>
        <div>
          <label style={labelStyle}>Type</label>
          <Select
            value={type}
            onChange={v => setType(v as 'local' | 's3')}
            options={[{ value: 'local', label: 'Local filesystem' }, { value: 's3', label: 'S3-compatible' }]}
          />
        </div>
        {type === 'local' ? (
          <div>
            <label style={labelStyle}>Path</label>
            <input style={inputStyle} value={path} onChange={e => setPath(e.target.value)} placeholder="./data/blobs/fast-ssd" />
          </div>
        ) : (
          <>
            <div>
              <label style={labelStyle}>Bucket</label>
              <input style={inputStyle} value={bucket} onChange={e => setBucket(e.target.value)} />
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
              <div>
                <label style={labelStyle}>Region</label>
                <input style={inputStyle} value={region} onChange={e => setRegion(e.target.value)} />
              </div>
              <div>
                <label style={labelStyle}>Prefix (optional)</label>
                <input style={inputStyle} value={prefix} onChange={e => setPrefix(e.target.value)} />
              </div>
            </div>
            <div>
              <label style={labelStyle}>Endpoint (leave empty for AWS)</label>
              <input style={inputStyle} value={endpoint} onChange={e => setEndpoint(e.target.value)} placeholder="https://minio.example.com" />
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
              <div>
                <label style={labelStyle}>Access Key</label>
                <input style={inputStyle} value={accessKey} onChange={e => setAccessKey(e.target.value)} />
              </div>
              <div>
                <label style={labelStyle}>Secret Key</label>
                <input type="password" style={inputStyle} value={secretKey} onChange={e => setSecretKey(e.target.value)} />
              </div>
            </div>
          </>
        )}
        <div>
          <label style={labelStyle}>Quota (GB, optional)</label>
          <input type="number" min="0" step="0.1" style={inputStyle} value={quotaGB} onChange={e => setQuotaGB(e.target.value)} placeholder="Unlimited" />
        </div>
        {err && (
          <div style={{ background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, padding: '8px 12px', color: '#fca5a5', fontSize: 13 }}>
            {err}
          </div>
        )}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 4 }}>
          <button style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: '8px 16px', color: '#dbeafe', fontSize: 13, fontWeight: 600, cursor: 'pointer' }} onClick={onClose}>Cancel</button>
          <button
            style={{ background: 'rgba(59,130,246,0.2)', border: '1px solid rgba(59,130,246,0.4)', borderRadius: 8, padding: '8px 16px', color: '#93c5fd', fontSize: 13, fontWeight: 600, cursor: name.trim() ? 'pointer' : 'not-allowed', opacity: name.trim() ? 1 : 0.5 }}
            disabled={!name.trim() || mut.isPending}
            onClick={() => { setErr(''); mut.mutate() }}
          >
            {mut.isPending ? 'Creating…' : 'Create'}
          </button>
        </div>
      </div>
    </ModalShell>
  )
}

// ── Shared modal shell ────────────────────────────────────────────
function ModalShell({ title, onClose, width, children }: { title: string; onClose: () => void; width: number; children: React.ReactNode }) {
  return (
    <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)', backdropFilter: 'blur(4px)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 100, padding: 20 }} onClick={onClose}>
      <div
        style={{ width, maxWidth: '100%', maxHeight: '90vh', overflowY: 'auto', background: 'rgba(10,15,28,0.97)', border: '1px solid rgba(59,130,246,0.25)', borderRadius: 14, padding: 24 }}
        onClick={e => e.stopPropagation()}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
          <h2 style={{ fontSize: 16, fontWeight: 700, color: '#dbeafe', margin: 0 }}>{title}</h2>
          <button style={{ background: 'none', border: 'none', color: 'rgba(229,231,235,0.5)', cursor: 'pointer', padding: 4, display: 'flex' }} onClick={onClose}>
            <X size={18} />
          </button>
        </div>
        {children}
      </div>
    </div>
  )
}
