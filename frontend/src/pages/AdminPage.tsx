import { useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, CheckCircle, Database, Download, HardDrive, Info, Pencil, Plus, RefreshCw, Trash2, Upload, X } from 'lucide-react'
import { nexusApi, nexspenceApi } from '@/api/client'
import { MonitoringView } from '@/pages/MonitoringPage'
import { Select } from '@/components/Select'
import { HoloButton, HoloInput, HoloModal, HoloText, HoloTabs, HoloCard, HoloTabItem } from '@/components/holo'

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
    <div style={{ padding: 24, display: 'flex', flexDirection: 'column', gap: 24 }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between' }}>
        <div style={{ marginBottom: 20 }}>
          <div className="holo-section-label" style={{ marginBottom: 6 }}>ADMINISTRATION / SYSTEM</div>
          <h1 style={{ fontSize: 40, fontWeight: 700, margin: '0 0 4px', letterSpacing: '-0.04em', lineHeight: 1 }}>
            <HoloText>System Admin</HoloText>
          </h1>
          <p style={{ fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 }}>Server health, blob stores and configuration</p>
        </div>
        <HoloButton onClick={() => { refetchStatus(); refetchBlobs() }} title="Refresh">
          <RefreshCw size={16} />
        </HoloButton>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
        {/* Status card */}
        <HoloCard>
          <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 8 }}>
            <CheckCircle size={14} /> System Status
          </div>
          {statusLoading ? (
            <p style={{ fontSize: 12, color: 'var(--holo-text-faint)' }}>Loading…</p>
          ) : (
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 }}>
              <div style={{ width: 8, height: 8, borderRadius: '50%', background: isOnline ? '#22c55e' : '#ef4444', boxShadow: isOnline ? '0 0 6px #22c55e66' : '0 0 6px #ef444466', flexShrink: 0 }} />
              <span style={{ fontSize: 14, fontWeight: 600, color: isOnline ? '#22c55e' : '#ef4444' }}>
                {isOnline ? 'Online' : 'Offline'}
              </span>
              <span style={{ fontSize: 12, color: 'var(--holo-text-faint)', marginLeft: 4 }}>
                {status?.edition ?? ''}
              </span>
            </div>
          )}
        </HoloCard>

        {/* System Info card */}
        <HoloCard>
          <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 8 }}>
            <Info size={14} /> System Info
          </div>
          {info ? (
            <>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: '1px solid rgba(255,255,255,0.05)', fontSize: 13 }}>
                <span style={{ color: 'var(--holo-text-dim)' }}>Product</span>
                <span style={{ color: 'var(--holo-text)', fontWeight: 500 }}>{info.product}</span>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: '1px solid rgba(255,255,255,0.05)', fontSize: 13 }}>
                <span style={{ color: 'var(--holo-text-dim)' }}>Version</span>
                <span style={{ color: 'var(--holo-text)', fontWeight: 500 }}>{info.version}</span>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: 'none', fontSize: 13 }}>
                <span style={{ color: 'var(--holo-text-dim)' }}>Edition</span>
                <span style={{ color: 'var(--holo-text)', fontWeight: 500 }}>{info.edition}</span>
              </div>
            </>
          ) : (
            <p style={{ fontSize: 12, color: 'var(--holo-text-faint)' }}>Loading…</p>
          )}
        </HoloCard>
      </div>

      {/* Tabs */}
      <HoloTabs
        items={[
          { value: 'blobs',      label: <><HardDrive size={13} style={{ marginRight: 5 }} />Blob Stores</> },
          { value: 'backup',     label: <><Database size={13} style={{ marginRight: 5 }} />Backup &amp; Restore</> },
          { value: 'monitoring', label: <><Activity size={13} style={{ marginRight: 5 }} />Monitoring</> },
        ] as HoloTabItem[]}
        value={tab}
        onChange={v => setTab(v as AdminTab)}
      />

      {/* Backup / Restore */}
      {tab === 'backup' && (
      <div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
          <Database size={15} style={{ color: 'var(--holo-text-dim)' }} />
          <span style={{ fontSize: 15, fontWeight: 600, color: 'var(--holo-text)' }}>Backup &amp; Restore</span>
        </div>
        <HoloCard style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' as const, alignItems: 'center' }}>
            <HoloButton variant="primary" icon={<Download size={14} />} onClick={handleExport} disabled={exportBusy}>
              {exportBusy ? 'Exporting…' : 'Export backup'}
            </HoloButton>
            <HoloButton variant="primary" icon={<Upload size={14} />} onClick={() => fileInputRef.current?.click()} disabled={restoreBusy}>
              {restoreBusy ? 'Restoring…' : 'Restore from backup'}
            </HoloButton>
            <input ref={fileInputRef} type="file" accept=".tar.gz,.tgz" style={{ display: 'none' }} onChange={handleRestore} />
          </div>
          <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: 0 }}>
            Export creates a full <code style={{ color: 'var(--holo-a)' }}>.tar.gz</code> archive of all repositories, users, roles, policies, components, assets and blobs.
            Restore is non-destructive — existing records are skipped.
          </p>
          {restoreError && (
            <div style={{ background: 'rgba(255,107,107,0.08)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, padding: '10px 14px', color: 'var(--holo-red)', fontSize: 13 }}>
              {restoreError}
            </div>
          )}
          {restoreResult && (
            <div style={{ background: 'rgba(34,197,94,0.08)', border: '1px solid rgba(34,197,94,0.25)', borderRadius: 8, padding: '10px 14px', fontSize: 13 }}>
              <span style={{ color: 'var(--holo-green)', fontWeight: 600, marginBottom: 6, display: 'block' }}>Restore complete</span>
              <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' as const }}>
                {Object.entries(restoreResult).map(([k, v]) => (
                  <span key={k} style={{ color: 'rgba(229,231,235,0.7)' }}>
                    <span style={{ color: 'var(--holo-text)', fontWeight: 600 }}>{v}</span> {k}
                  </span>
                ))}
              </div>
            </div>
          )}
        </HoloCard>
      </div>
      )}

      {/* Blob stores */}
      {tab === 'blobs' && (
      <div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
          <HardDrive size={15} style={{ color: 'var(--holo-text-dim)' }} />
          <span style={{ fontSize: 15, fontWeight: 600, color: 'var(--holo-text)' }}>Blob Stores</span>
          <span style={{ fontSize: 12, color: 'var(--holo-text-faint)', marginLeft: 4 }}>
            {blobs.length} total
          </span>
          <HoloButton variant="primary" icon={<Plus size={14} />} style={{ marginLeft: 'auto' }} onClick={() => setCreateOpen(true)}>New Blob Store</HoloButton>
        </div>

        {blobsLoading ? (
          <p style={{ fontSize: 12, color: 'var(--holo-text-faint)' }}>Loading…</p>
        ) : blobs.length === 0 ? (
          <div style={{ background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 14, padding: 20, textAlign: 'center' as const, color: 'var(--holo-text-faint)', fontSize: 14 }}>
            <Database size={32} style={{ opacity: 0.3, margin: '0 auto 8px' }} />
            <p>No blob stores configured</p>
          </div>
        ) : (
          <div style={{ background: 'rgba(255,255,255,0.02)', border: '1px solid var(--holo-border)', borderRadius: 12, overflow: 'hidden' }}>
            <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 2fr 1fr', padding: '10px 16px', background: 'rgba(255,255,255,0.03)', borderBottom: '1px solid var(--holo-border)', fontSize: 11, fontWeight: 600, color: 'var(--holo-text-faint)', textTransform: 'uppercase' as const, letterSpacing: '0.05em' }}>
              <div>Name</div>
              <div>Type</div>
              <div>Used</div>
              <div>Quota</div>
            </div>
            {blobs.map(bs => {
              const usedPct = bs.quotaBytes ? Math.min((bs.usedBytes / bs.quotaBytes) * 100, 100) : 0
              const overQuota = bs.quotaBytes && bs.usedBytes > bs.quotaBytes
              const barColor = overQuota ? '#ef4444' : usedPct > 80 ? '#f59e0b' : 'var(--holo-a)'
              const isEditing = editingQuota === bs.id
              return (
                <div
                  key={bs.id}
                  style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 2fr 1fr', padding: '11px 16px', borderBottom: '1px solid rgba(255,255,255,0.05)', fontSize: 13, color: 'var(--holo-text)', alignItems: 'center', cursor: 'pointer' }}
                  onClick={(e) => {
                    // Don't open detail if click is on a button or input
                    const t = e.target as HTMLElement
                    if (t.closest('button') || t.closest('input')) return
                    setDetailName(bs.name)
                  }}
                >
                  <div style={{ fontWeight: 600, color: 'var(--holo-text)' }}>{bs.name}</div>
                  <div>
                    <span style={{ fontSize: 11, fontWeight: 600, padding: '2px 8px', borderRadius: 4, background: 'rgba(124,92,255,0.15)', color: 'var(--holo-a)' }}>
                      {bs.type}
                    </span>
                  </div>
                  <div>
                    <div style={{ fontSize: 13 }}>{fmtBytes(bs.usedBytes)}</div>
                    {bs.quotaBytes && (
                      <div style={{ height: 4, borderRadius: 2, background: 'rgba(255,255,255,0.08)', overflow: 'hidden', marginTop: 4, width: '100%' }}>
                        <div style={{ height: '100%', width: usedPct + '%', background: barColor, transition: 'width 0.3s' }} />
                      </div>
                    )}
                  </div>
                  <div>
                    {isEditing ? (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        <HoloInput
                          type="number" min="0" step="0.1" autoFocus
                          value={quotaInput}
                          onChange={e => setQuotaInput(e.target.value)}
                          placeholder="GB"
                          style={{ width: 72 }}
                          onKeyDown={e => {
                            if (e.key === 'Enter') quotaMut.mutate({ bs, gb: quotaInput })
                            if (e.key === 'Escape') setEditingQuota(null)
                          }}
                        />
                        <span style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>GB</span>
                        <button
                          style={{ background: 'rgba(34,197,94,0.15)', border: '1px solid rgba(34,197,94,0.3)', borderRadius: 6, padding: '3px 8px', color: '#22c55e', fontSize: 11, cursor: 'pointer' }}
                          onClick={() => quotaMut.mutate({ bs, gb: quotaInput })}
                        >Save</button>
                        <button
                          style={{ background: 'none', border: 'none', color: 'var(--holo-text-faint)', cursor: 'pointer', padding: 2 }}
                          onClick={() => setEditingQuota(null)}
                        ><X size={12} /></button>
                      </div>
                    ) : (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        <span style={overQuota ? { color: '#ef4444', fontSize: 12 } : { fontSize: 12, color: 'var(--holo-text-faint)' }}>
                          {bs.quotaBytes ? fmtBytes(bs.quotaBytes) : 'Unlimited'}
                        </span>
                        <button
                          title="Edit quota"
                          style={{ background: 'none', border: 'none', color: 'var(--holo-text-faint)', cursor: 'pointer', padding: 2, display: 'flex', alignItems: 'center' }}
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
      {isLoading && <p style={{ color: 'var(--holo-text-dim)' }}>Loading…</p>}
      {error && <p style={{ color: 'var(--holo-red)' }}>Failed to load usage</p>}
      {bs && (
        <>
          <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '6px 14px', fontSize: 13, marginBottom: 16 }}>
            <span style={{ color: 'var(--holo-text-dim)' }}>Type</span>
            <span style={{ color: 'var(--holo-text)' }}>{bs.type}</span>
            <span style={{ color: 'var(--holo-text-dim)' }}>Used</span>
            <span style={{ color: 'var(--holo-text)' }}>{used}</span>
            <span style={{ color: 'var(--holo-text-dim)' }}>Quota</span>
            <span style={{ color: 'var(--holo-text)' }}>{quota}</span>
            {remaining !== null && (
              <>
                <span style={{ color: 'var(--holo-text-dim)' }}>Remaining</span>
                <span style={{ color: 'var(--holo-text)' }}>{remaining}</span>
              </>
            )}
            <span style={{ color: 'var(--holo-text-dim)' }}>Asset total</span>
            <span style={{ color: 'var(--holo-text)' }}>{fmtBytes(data?.totalAssetBytes ?? 0)} across {linked.length} {linked.length === 1 ? 'repo' : 'repos'}</span>
          </div>

          <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-faint)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 8 }}>
            Linked Repositories
          </div>
          {linked.length === 0 ? (
            <div style={{ padding: '20px 16px', background: 'rgba(255,255,255,0.02)', border: '1px dashed var(--holo-border)', borderRadius: 8, textAlign: 'center', color: 'var(--holo-text-faint)', fontSize: 13 }}>
              No repositories use this blob store.
            </div>
          ) : (
            <div style={{ background: 'rgba(255,255,255,0.02)', border: '1px solid var(--holo-border)', borderRadius: 10, overflow: 'hidden' }}>
              <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 1fr 1fr', padding: '8px 14px', background: 'rgba(255,255,255,0.03)', fontSize: 11, fontWeight: 600, color: 'var(--holo-text-faint)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                <div>Name</div><div>Format</div><div>Type</div><div>Used</div>
              </div>
              {linked.map(r => (
                <Link key={r.name} to={`/browse?repo=${encodeURIComponent(r.name)}`} style={{ textDecoration: 'none' }}>
                  <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 1fr 1fr', padding: '10px 14px', borderTop: '1px solid rgba(255,255,255,0.05)', fontSize: 13, color: 'var(--holo-text)', cursor: 'pointer' }}>
                    <div style={{ color: 'var(--holo-a)', fontWeight: 600 }}>{r.name}</div>
                    <div>{r.format}</div>
                    <div>{r.type}</div>
                    <div>{fmtBytes(r.bytesUsed)}</div>
                  </div>
                </Link>
              ))}
            </div>
          )}

          {deleteError && (
            <div style={{ marginTop: 12, background: 'rgba(255,107,107,0.08)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, padding: '8px 12px', color: 'var(--holo-red)', fontSize: 13 }}>
              {deleteError}
            </div>
          )}

          <div style={{ marginTop: 20, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <HoloButton
              variant="danger"
              disabled={!canDelete || delMut.isPending}
              title={canDelete ? 'Delete this blob store' : 'Detach all repositories first'}
              onClick={() => {
                setDeleteError('')
                if (confirm(`Delete blob store "${name}"? This cannot be undone.`)) delMut.mutate()
              }}
            >
              <Trash2 size={13} />
              {delMut.isPending ? 'Deleting…' : 'Delete'}
            </HoloButton>
            <HoloButton onClick={onClose}>Close</HoloButton>
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

  return (
    <ModalShell title="New Blob Store" onClose={onClose} width={500}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <div>
          <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Name</label>
          <HoloInput value={name} onChange={e => setName(e.target.value)} placeholder="e.g. fast-ssd" autoFocus />
        </div>
        <div>
          <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Type</label>
          <Select
            value={type}
            onChange={v => setType(v as 'local' | 's3')}
            options={[{ value: 'local', label: 'Local filesystem' }, { value: 's3', label: 'S3-compatible' }]}
          />
        </div>
        {type === 'local' ? (
          <div>
            <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Path</label>
            <HoloInput value={path} onChange={e => setPath(e.target.value)} placeholder="./data/blobs/fast-ssd" />
          </div>
        ) : (
          <>
            <div>
              <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Bucket</label>
              <HoloInput value={bucket} onChange={e => setBucket(e.target.value)} />
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
              <div>
                <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Region</label>
                <HoloInput value={region} onChange={e => setRegion(e.target.value)} />
              </div>
              <div>
                <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Prefix (optional)</label>
                <HoloInput value={prefix} onChange={e => setPrefix(e.target.value)} />
              </div>
            </div>
            <div>
              <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Endpoint (leave empty for AWS)</label>
              <HoloInput value={endpoint} onChange={e => setEndpoint(e.target.value)} placeholder="https://minio.example.com" />
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
              <div>
                <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Access Key</label>
                <HoloInput value={accessKey} onChange={e => setAccessKey(e.target.value)} />
              </div>
              <div>
                <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Secret Key</label>
                <HoloInput type="password" value={secretKey} onChange={e => setSecretKey(e.target.value)} />
              </div>
            </div>
          </>
        )}
        <div>
          <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', marginBottom: 4, display: 'block' }}>Quota (GB, optional)</label>
          <HoloInput type="number" min="0" step="0.1" value={quotaGB} onChange={e => setQuotaGB(e.target.value)} placeholder="Unlimited" />
        </div>
        {err && (
          <div style={{ background: 'rgba(255,107,107,0.08)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, padding: '8px 12px', color: 'var(--holo-red)', fontSize: 13 }}>
            {err}
          </div>
        )}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 4 }}>
          <HoloButton onClick={onClose}>Cancel</HoloButton>
          <HoloButton variant="primary" disabled={!name.trim() || mut.isPending} onClick={() => { setErr(''); mut.mutate() }}>
            {mut.isPending ? 'Creating…' : 'Create'}
          </HoloButton>
        </div>
      </div>
    </ModalShell>
  )
}

// ── Shared modal shell ────────────────────────────────────────────
function ModalShell({ title, onClose, width, children }: { title: string; onClose: () => void; width: number; children: React.ReactNode }) {
  return (
    <HoloModal open={true} onClose={onClose}>
      <div style={{ width, maxWidth: '100%' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
          <h2 style={{ fontSize: 16, fontWeight: 700, color: 'var(--holo-text)', margin: 0 }}>{title}</h2>
          <HoloButton onClick={onClose} style={{ padding: 4 }}><X size={18} /></HoloButton>
        </div>
        {children}
      </div>
    </HoloModal>
  )
}
