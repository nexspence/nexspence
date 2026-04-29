import { useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, Archive, ArrowRightLeft, CheckCircle, Database, Download, HardDrive, Info, Paperclip, Pause, Pencil, Play, Plus, RefreshCw, Trash2, Upload, Wifi, X } from 'lucide-react'
import { nexusApi, nexspenceApi, ImportRepoStats, ServiceStatus } from '@/api/client'
import { MonitoringView } from '@/pages/MonitoringPage'
import { Select } from '@/components/Select'
import { HoloButton, HoloInput, HoloModal, HoloTabs, HoloCard, HoloTabItem, Wizard } from '@/components/holo'

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

type AdminTab = 'info' | 'blobs' | 'backup' | 'monitoring' | 'migration'
const VALID_TABS: AdminTab[] = ['info', 'blobs', 'backup', 'monitoring', 'migration']

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
  const tab: AdminTab = tabParam && VALID_TABS.includes(tabParam) ? tabParam : 'info'
  const setTab = (t: AdminTab) => {
    const next = new URLSearchParams(searchParams)
    next.set('tab', t)
    setSearchParams(next, { replace: true })
  }

  const [exportBusy, setExportBusy] = useState(false)
  const [importFile, setImportFile]             = useState<File | null>(null)
  const [importTargetName, setImportTargetName] = useState('')
  const [importConflict, setImportConflict]     = useState('skip')
  const [importBusy, setImportBusy]             = useState(false)
  const [importResult, setImportResult]         = useState<{ imported: ImportRepoStats } | null>(null)
  const [importError, setImportError]           = useState<string | null>(null)
  const [restoreBusy, setRestoreBusy] = useState(false)
  const [restoreResult, setRestoreResult] = useState<Record<string, number> | null>(null)
  const [restoreError, setRestoreError] = useState('')
  const fileInputRef = useRef<HTMLInputElement>(null)
  const importFileRef = useRef<HTMLInputElement>(null)
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

  const handleImportRepo = async () => {
    if (!importFile) return
    setImportBusy(true)
    setImportResult(null)
    setImportError(null)
    try {
      const res = await nexspenceApi.importRepo(importFile, importTargetName, importConflict)
      setImportResult(res.data)
    } catch (e: any) {
      setImportError(e.response?.data?.error ?? e.message ?? 'Import failed')
    } finally {
      setImportBusy(false)
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

  const { data: services, isFetching: servicesFetching, refetch: refetchServices } = useQuery<ServiceStatus[]>({
    queryKey: ['systemServices'],
    queryFn: () => nexspenceApi.getServiceStatuses().then(r => r.data),
    staleTime: 30_000,
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
          <div className="holo-section-label" style={{ marginBottom: 4 }}>ADMINISTRATION / SYSTEM</div>
          <h1 style={{ fontSize: 20, fontWeight: 700, margin: '0 0 3px', letterSpacing: '-0.01em', lineHeight: 1.2, background: 'linear-gradient(110deg, #7c5cff, #22d3ee 60%)', WebkitBackgroundClip: 'text', WebkitTextFillColor: 'transparent', backgroundClip: 'text' as const }}>System Admin</h1>
          <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: 0 }}>Server health, blob stores and configuration</p>
        </div>
        <HoloButton onClick={() => { refetchStatus(); refetchBlobs(); refetchServices() }} aria-label="Refresh">
          <RefreshCw size={16} />
        </HoloButton>
      </div>

      {/* Tabs */}
      <HoloTabs
        items={[
          { value: 'info',       label: <><Info size={13} style={{ marginRight: 5 }} />Info</> },
          { value: 'blobs',      label: <><HardDrive size={13} style={{ marginRight: 5 }} />Blob Stores</> },
          { value: 'backup',     label: <><Database size={13} style={{ marginRight: 5 }} />Backup &amp; Restore</> },
          { value: 'monitoring', label: <><Activity size={13} style={{ marginRight: 5 }} />Monitoring</> },
          { value: 'migration',  label: <><ArrowRightLeft size={13} style={{ marginRight: 5 }} />Migration</> },
        ] as HoloTabItem[]}
        value={tab}
        onChange={v => setTab(v as AdminTab)}
      />

      {/* Info */}
      {tab === 'info' && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
          {/* Status card */}
          <HoloCard>
            <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 8 }}>
              <CheckCircle size={14} /> System Status
            </div>
            {statusLoading ? (
              <>
                <div className="holo-skeleton holo-skeleton--text" style={{ width: '70%' }} />
                <div className="holo-skeleton holo-skeleton--text" style={{ width: '60%', marginTop: 8 }} />
              </>
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
              <>
                <div className="holo-skeleton holo-skeleton--text" style={{ width: '75%' }} />
                <div className="holo-skeleton holo-skeleton--text" style={{ width: '55%', marginTop: 8 }} />
                <div className="holo-skeleton holo-skeleton--text" style={{ width: '65%', marginTop: 8 }} />
              </>
            )}
          </HoloCard>
        </div>

        {/* Service Connections */}
        <HoloCard>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
            <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em', display: 'flex', alignItems: 'center', gap: 8 }}>
              <Wifi size={14} /> Service Connections
            </div>
            <HoloButton
              style={{ padding: '4px 8px' }}
              onClick={() => refetchServices()}
              disabled={servicesFetching}
              title="Re-run checks"
            >
              <RefreshCw size={13} style={{ animation: servicesFetching ? 'spin 1s linear infinite' : 'none' }} />
            </HoloButton>
          </div>
          {!services ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <div className="holo-skeleton holo-skeleton--text" style={{ width: '100%' }} />
              <div className="holo-skeleton holo-skeleton--text" style={{ width: '95%' }} />
              <div className="holo-skeleton holo-skeleton--text" style={{ width: '100%' }} />
            </div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {services.map(svc => {
                const color = svc.status === 'ok' ? 'var(--holo-green)' : svc.status === 'error' ? 'var(--holo-red)' : 'rgba(255,255,255,0.25)'
                const glow  = svc.status === 'ok' ? '0 0 5px var(--holo-green)' : svc.status === 'error' ? '0 0 5px var(--holo-red)' : 'none'
                return (
                  <div key={svc.name} style={{ display: 'grid', gridTemplateColumns: '8px 1fr auto', alignItems: 'center', gap: 12, padding: '10px 12px', background: 'rgba(255,255,255,0.03)', borderRadius: 8, border: '1px solid rgba(255,255,255,0.06)' }}>
                    <span style={{ width: 7, height: 7, borderRadius: '50%', background: color, boxShadow: glow, flexShrink: 0, display: 'inline-block' }} />
                    <div style={{ minWidth: 0 }}>
                      <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)', display: 'flex', alignItems: 'center', gap: 6 }}>
                        {svc.name}
                        {svc.name.startsWith('S3') && (
                          <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: 'rgba(245,158,11,0.15)', color: '#f59e0b', fontWeight: 700 }}>S3</span>
                        )}
                      </div>
                      <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const }}>{svc.detail}</div>
                    </div>
                    <div style={{ textAlign: 'right' as const, fontSize: 11, color: 'var(--holo-text-faint)', whiteSpace: 'nowrap' as const }}>
                      {svc.latency_ms != null && <span style={{ color: svc.latency_ms < 50 ? 'var(--holo-green)' : svc.latency_ms < 200 ? 'var(--holo-amber)' : 'var(--holo-red)' }}>{svc.latency_ms}ms</span>}
                      <div style={{ marginTop: 2 }}>{new Date(svc.checked_at).toLocaleTimeString()}</div>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </HoloCard>
        </div>
      )}

      {/* Backup / Restore */}
      {tab === 'backup' && (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>

        {/* ── System Backup & Restore ── */}
        <HoloCard style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 2 }}>
            <Database size={15} style={{ color: 'var(--holo-text-dim)' }} />
            <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--holo-text)' }}>System Backup &amp; Restore</span>
          </div>
          <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: 0 }}>
            Full instance snapshot — all repositories, users, roles, policies, components, assets and blobs.
            Restore is non-destructive; existing records are skipped.
          </p>
          <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' as const, alignItems: 'center' }}>
            <HoloButton variant="primary" icon={<Download size={14} />} onClick={handleExport} disabled={exportBusy}>
              {exportBusy ? 'Exporting…' : 'Export Backup'}
            </HoloButton>
            <HoloButton icon={<Upload size={14} />} onClick={() => fileInputRef.current?.click()} disabled={restoreBusy}>
              {restoreBusy ? 'Restoring…' : 'Restore'}
            </HoloButton>
            <input ref={fileInputRef} type="file" accept=".tar.gz,.tgz" style={{ display: 'none' }} onChange={handleRestore} />
          </div>
          {restoreError && (
            <div role="alert" style={{ background: 'rgba(255,107,107,0.08)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, padding: '10px 14px', color: 'var(--holo-red)', fontSize: 13 }}>
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

        {/* ── Repository Import ── */}
        <HoloCard style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 2 }}>
            <Archive size={15} style={{ color: 'var(--holo-text-dim)' }} />
            <span style={{ fontSize: 14, fontWeight: 600, color: 'var(--holo-text)' }}>Repository Import</span>
          </div>
          <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: 0 }}>
            Import a single repository from a <code style={{ color: 'var(--holo-a)' }}>.tar.gz</code> archive
            exported from this or another Nexspence instance. Users, roles and cleanup policies are not included — only repository metadata, components, assets and blobs.
          </p>

          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            {/* File picker */}
            <div>
              <label style={{ fontSize: 12, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 6 }}>Archive file</label>
              <input
                ref={importFileRef}
                type="file"
                accept=".tar.gz,.tgz"
                style={{ display: 'none' }}
                onChange={e => {
                  setImportFile(e.target.files?.[0] ?? null)
                  setImportResult(null)
                  setImportError(null)
                }}
              />
              <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <HoloButton icon={<Paperclip size={14} />} onClick={() => importFileRef.current?.click()}>
                  Choose archive
                </HoloButton>
                {importFile ? (
                  <span style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--holo-text-faint)', maxWidth: 320, overflow: 'hidden' }}>
                    <Archive size={12} style={{ flexShrink: 0 }} />
                    <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const }}>{importFile.name}</span>
                    <button
                      onClick={() => { setImportFile(null); if (importFileRef.current) importFileRef.current.value = '' }}
                      style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--holo-text-dim)', padding: '0 2px', lineHeight: 1, fontSize: 16, flexShrink: 0 }}
                      title="Clear"
                    ><X size={13} /></button>
                  </span>
                ) : (
                  <span style={{ fontSize: 12, color: 'rgba(229,231,235,0.28)' }}>No file selected</span>
                )}
              </div>
            </div>

            {/* Target name */}
            <div>
              <label style={{ fontSize: 12, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 6 }}>
                Target name <span style={{ color: 'rgba(229,231,235,0.35)' }}>— optional, overrides the name in the archive</span>
              </label>
              <HoloInput
                placeholder="leave blank to use the archived name"
                value={importTargetName}
                onChange={e => setImportTargetName(e.target.value)}
                style={{ width: 300 }}
              />
            </div>

            {/* Conflict mode */}
            <div>
              <label style={{ fontSize: 12, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 6 }}>Conflict mode</label>
              <Select
                value={importConflict}
                onChange={setImportConflict}
                options={[
                  { value: 'skip',   label: 'Skip — add only absent components/assets if repo exists' },
                  { value: 'rename', label: 'Rename — create under target name (fails if name is taken)' },
                ]}
                style={{ width: 360 }}
              />
            </div>

            <div>
              <HoloButton
                variant="primary"
                icon={<Upload size={14} />}
                disabled={!importFile || importBusy}
                onClick={handleImportRepo}
              >
                {importBusy ? 'Importing…' : 'Import Repository'}
              </HoloButton>
            </div>

            {importResult && (
              <div style={{ padding: '10px 14px', borderRadius: 8, background: 'rgba(34,197,94,0.08)', border: '1px solid rgba(34,197,94,0.28)', fontSize: 13, color: 'var(--holo-text)' }}>
                <span style={{ color: 'var(--holo-green)', fontWeight: 600, display: 'block', marginBottom: 4 }}>Import complete</span>
                Imported <strong>{importResult.imported.components}</strong> components and{' '}
                <strong>{importResult.imported.assets}</strong> assets into{' '}
                <code style={{ color: '#93c5fd' }}>{importResult.imported.repository}</code>
                {importResult.imported.blobs > 0 && <>, <strong>{importResult.imported.blobs}</strong> blobs</>}.
              </div>
            )}
            {importError && (
              <div role="alert" style={{ padding: '10px 14px', borderRadius: 8, background: 'rgba(239,68,68,0.08)', border: '1px solid rgba(239,68,68,0.28)', fontSize: 13, color: '#fca5a5' }}>
                {importError}
              </div>
            )}
          </div>
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
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            <div className="holo-skeleton holo-skeleton--block" />
            <div className="holo-skeleton holo-skeleton--block" />
          </div>
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

      {/* Migration */}
      {tab === 'migration' && <MigrationTab />}

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
  const linked = data?.linkedRepositories ?? []
  const bs = data?.store
  const used = bs ? fmtBytes(bs.usedBytes) : '—'
  const quota = bs?.quotaBytes ? fmtBytes(bs.quotaBytes) : 'Unlimited'
  const remaining = data?.quotaRemaining !== undefined ? fmtBytes(data.quotaRemaining) : null
  const canDelete = linked.length === 0

  const [deleteError, setDeleteError] = useState('')
  const [editing, setEditing] = useState(false)
  const [editBucket, setEditBucket]       = useState('')
  const [editRegion, setEditRegion]       = useState('')
  const [editEndpoint, setEditEndpoint]   = useState('')
  const [editAccessKey, setEditAccessKey] = useState('')
  const [editSecretKey, setEditSecretKey] = useState('')
  const [editPath, setEditPath]           = useState('')
  const [editErr, setEditErr]             = useState('')
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

  const editMut = useMutation({
    mutationFn: () => {
      if (!bs) return Promise.reject('no store')
      const secret = editSecretKey || (bs.config?.secret_key as string) || ''
      const config: Record<string, unknown> = bs.type === 's3'
        ? { bucket: editBucket, region: editRegion, endpoint: editEndpoint,
            access_key: editAccessKey, secret_key: secret }
        : { path: editPath }
      return nexusApi.updateBlobStore(bs.type, bs.name, { config, quotaBytes: bs.quotaBytes ?? null })
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['blobstore-usage', name] })
      qc.invalidateQueries({ queryKey: ['blobstores'] })
      setEditing(false)
    },
    onError: (e: unknown) => {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error ?? 'Save failed'
      setEditErr(msg)
    },
  })

  const startEdit = () => {
    const cfg = bs?.config ?? {}
    setEditBucket((cfg.bucket as string) ?? '')
    setEditRegion((cfg.region as string) ?? 'us-east-1')
    setEditEndpoint((cfg.endpoint as string) ?? '')
    setEditAccessKey((cfg.access_key as string) ?? '')
    setEditSecretKey('')
    setEditPath((cfg.path as string) ?? '')
    setEditErr('')
    setEditing(true)
  }

  return (
    <ModalShell title={`Blob Store: ${name}`} onClose={onClose} width={640}>
      {isLoading && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div className="holo-skeleton holo-skeleton--text" style={{ width: '60%' }} />
          <div className="holo-skeleton holo-skeleton--block" />
        </div>
      )}
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
            {bs.type === 's3' && bs.config && (
              <>
                <span style={{ color: 'var(--holo-text-dim)' }}>Endpoint</span>
                <span style={{ color: 'var(--holo-text)', fontFamily: 'monospace', fontSize: 12 }}>
                  {(bs.config.endpoint as string) || 'AWS S3'}
                </span>
                <span style={{ color: 'var(--holo-text-dim)' }}>Bucket</span>
                <span style={{ color: 'var(--holo-text)', fontFamily: 'monospace', fontSize: 12 }}>
                  {(bs.config.bucket as string) || '—'}
                </span>
                <span style={{ color: 'var(--holo-text-dim)' }}>Region</span>
                <span style={{ color: 'var(--holo-text)', fontFamily: 'monospace', fontSize: 12 }}>
                  {(bs.config.region as string) || '—'}
                </span>
              </>
            )}
            {bs.type === 'local' && bs.config && (
              <>
                <span style={{ color: 'var(--holo-text-dim)' }}>Path</span>
                <span style={{ color: 'var(--holo-text)', fontFamily: 'monospace', fontSize: 12 }}>
                  {(bs.config.path as string) || '—'}
                </span>
              </>
            )}
          </div>

          {editing && bs && (
            <div style={{ marginBottom: 16, padding: '12px 14px', background: 'rgba(255,255,255,0.03)', borderRadius: 10, border: '1px solid var(--holo-border)' }}>
              <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase' as const, letterSpacing: '0.05em', marginBottom: 10 }}>Edit Configuration</div>
              {bs.type === 's3' ? (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
                    <div>
                      <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Bucket</label>
                      <HoloInput value={editBucket} onChange={e => setEditBucket(e.target.value)} />
                    </div>
                    <div>
                      <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Region</label>
                      <HoloInput value={editRegion} onChange={e => setEditRegion(e.target.value)} />
                    </div>
                  </div>
                  <div>
                    <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Endpoint</label>
                    <HoloInput value={editEndpoint} onChange={e => setEditEndpoint(e.target.value)} placeholder="leave empty for AWS S3" />
                  </div>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
                    <div>
                      <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Access Key</label>
                      <HoloInput value={editAccessKey} onChange={e => setEditAccessKey(e.target.value)} />
                    </div>
                    <div>
                      <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Secret Key (leave blank to keep)</label>
                      <HoloInput type="password" value={editSecretKey} onChange={e => setEditSecretKey(e.target.value)} placeholder="unchanged" />
                    </div>
                  </div>
                </div>
              ) : (
                <div>
                  <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Path</label>
                  <HoloInput value={editPath} onChange={e => setEditPath(e.target.value)} />
                </div>
              )}
              {editErr && <div style={{ marginTop: 8, color: 'var(--holo-red)', fontSize: 12 }}>{editErr}</div>}
              <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
                <HoloButton variant="primary" disabled={editMut.isPending} onClick={() => editMut.mutate()}>
                  {editMut.isPending ? 'Saving…' : 'Save'}
                </HoloButton>
                <HoloButton onClick={() => { setEditing(false); setEditErr('') }}>Cancel</HoloButton>
              </div>
            </div>
          )}

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
            <div role="alert" style={{ marginTop: 12, background: 'rgba(255,107,107,0.08)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, padding: '8px 12px', color: 'var(--holo-red)', fontSize: 13 }}>
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
            <div style={{ display: 'flex', gap: 8 }}>
              {!editing && bs && (
                <HoloButton icon={<Pencil size={13} />} onClick={startEdit}>Edit Config</HoloButton>
              )}
              <HoloButton onClick={onClose}>Close</HoloButton>
            </div>
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
  const [testResult, setTestResult] = useState<{ ok: boolean; error?: string } | null>(null)
  const [testBusy, setTestBusy] = useState(false)

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

  const handleTest = async () => {
    setTestBusy(true)
    setTestResult(null)
    try {
      const cfg: Record<string, unknown> = type === 'local'
        ? { path }
        : { bucket, region, endpoint, prefix, access_key: accessKey, secret_key: secretKey }
      const res = await nexusApi.testBlobStore(type, cfg)
      setTestResult(res.data)
    } catch {
      setTestResult({ ok: false, error: 'Request failed' })
    } finally {
      setTestBusy(false)
    }
  }

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
            onChange={v => { setType(v as 'local' | 's3'); setTestResult(null) }}
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
        {testResult && (
          <div style={{
            padding: '8px 12px', borderRadius: 8, fontSize: 13,
            background: testResult.ok ? 'rgba(34,197,94,0.08)' : 'rgba(239,68,68,0.08)',
            border: `1px solid ${testResult.ok ? 'rgba(34,197,94,0.3)' : 'rgba(239,68,68,0.3)'}`,
            color: testResult.ok ? 'var(--holo-green)' : 'var(--holo-red)',
          }}>
            {testResult.ok ? 'Connection successful' : `Connection failed: ${testResult.error}`}
          </div>
        )}
        {err && (
          <div role="alert" style={{ background: 'rgba(255,107,107,0.08)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, padding: '8px 12px', color: 'var(--holo-red)', fontSize: 13 }}>
            {err}
          </div>
        )}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 4 }}>
          <HoloButton onClick={onClose}>Cancel</HoloButton>
          <HoloButton onClick={handleTest} disabled={testBusy || !name.trim() || (type === 's3' && !bucket.trim())}>
            {testBusy ? 'Testing…' : 'Test Connection'}
          </HoloButton>
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

// ── Migration tab ─────────────────────────────────────────────────

interface MigrationJobData {
  id: string
  sourceUrl: string
  sourceUser: string
  status: 'pending' | 'running' | 'paused' | 'done' | 'error'
  migrateRepos: boolean
  migrateUsers: boolean
  migrateBlobs: boolean
  migratePolicies: boolean
  repositoriesTotal: number
  repositoriesDone: number
  assetsTotal: number
  assetsDone: number
  errorCount: number
  lastError?: string
  startedAt?: string
  finishedAt?: string
  createdAt: string
  updatedAt: string
}

const MIG_STATUS: Record<string, { bg: string; color: string }> = {
  pending:   { bg: 'rgba(245,158,11,0.15)',  color: '#f59e0b' },
  running:   { bg: 'rgba(59,130,246,0.15)',  color: '#3b82f6' },
  paused:    { bg: 'rgba(107,114,128,0.15)', color: '#9ca3af' },
  done:      { bg: 'rgba(34,197,94,0.15)',   color: '#22c55e' },
  error:     { bg: 'rgba(239,68,68,0.15)',   color: '#ef4444' },
}

function MigrationTab() {
  const qc = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)

  const { data: jobs = [], isLoading, refetch } = useQuery<MigrationJobData[]>({
    queryKey: ['migrationJobs'],
    queryFn: () => nexspenceApi.listMigrationJobs().then(r => r.data),
    refetchInterval: (q) => {
      const list = q.state.data as MigrationJobData[] | undefined
      return list?.some(j => j.status === 'running') ? 3000 : false
    },
  })

  const pauseMut = useMutation({
    mutationFn: (id: string) => nexspenceApi.pauseMigrationJob(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['migrationJobs'] }),
  })
  const resumeMut = useMutation({
    mutationFn: (id: string) => nexspenceApi.resumeMigrationJob(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['migrationJobs'] }),
  })

  const activeJobs = jobs.filter(j => j.status === 'pending' || j.status === 'running' || j.status === 'paused')
  const historyJobs = jobs.filter(j => j.status === 'done' || j.status === 'error')

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <ArrowRightLeft size={15} style={{ color: 'var(--holo-text-dim)' }} />
          <span style={{ fontSize: 15, fontWeight: 600, color: 'var(--holo-text)' }}>Migration from Nexus</span>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <HoloButton onClick={() => refetch()} title="Refresh"><RefreshCw size={14} /></HoloButton>
          <HoloButton variant="primary" onClick={() => setShowCreate(true)}><Plus size={14} /> New Migration</HoloButton>
        </div>
      </div>

      <div style={{ background: 'rgba(124,92,255,0.08)', border: '1px solid rgba(124,92,255,0.2)', borderRadius: 10, padding: '12px 16px', fontSize: 13, color: 'rgba(180,160,255,0.9)', lineHeight: 1.6 }}>
        <strong>How it works:</strong> Nexspence connects to your Nexus instance via its REST API and
        streams repositories, users, roles and all artifacts directly — no downtime required.
        Jobs are pausable and resumable. Requires Nexus admin credentials.
      </div>

      {isLoading ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div className="holo-skeleton holo-skeleton--block" />
          <div className="holo-skeleton holo-skeleton--block" />
        </div>
      ) : activeJobs.length === 0 && historyJobs.length === 0 ? (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12, color: 'var(--holo-text-faint)', fontSize: 14, padding: '48px 0' }}>
          <ArrowRightLeft size={40} style={{ opacity: 0.3 }} />
          <p style={{ margin: 0 }}>No migration jobs yet</p>
          <HoloButton variant="primary" onClick={() => setShowCreate(true)}><Plus size={14} /> Start Migration</HoloButton>
        </div>
      ) : (
        <>
          {activeJobs.length > 0 && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {activeJobs.map(job => <MigrationJobCard key={job.id} job={job} onPause={() => pauseMut.mutate(job.id)} onResume={() => resumeMut.mutate(job.id)} />)}
            </div>
          )}

          {historyJobs.length > 0 && (
            <>
              <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-faint)', textTransform: 'uppercase', letterSpacing: '0.05em', marginTop: 4 }}>
                Migration History
              </div>
              <div style={{ background: 'rgba(255,255,255,0.02)', border: '1px solid var(--holo-border)', borderRadius: 12, overflow: 'hidden' }}>
                <div style={{ display: 'grid', gridTemplateColumns: '3fr 1fr 1fr 1fr 1fr', padding: '10px 16px', background: 'rgba(255,255,255,0.03)', borderBottom: '1px solid var(--holo-border)', fontSize: 11, fontWeight: 600, color: 'var(--holo-text-faint)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                  <div>Source</div>
                  <div>Status</div>
                  <div>Repos</div>
                  <div>Assets</div>
                  <div>Finished</div>
                </div>
                {historyJobs.map(job => {
                  const st = MIG_STATUS[job.status] ?? MIG_STATUS.pending
                  return (
                    <div key={job.id} style={{ display: 'grid', gridTemplateColumns: '3fr 1fr 1fr 1fr 1fr', padding: '11px 16px', borderBottom: '1px solid rgba(255,255,255,0.04)', fontSize: 13, color: 'var(--holo-text)', alignItems: 'center' }}>
                      <div>
                        <div style={{ fontFamily: 'monospace', fontSize: 12, fontWeight: 600, color: 'var(--holo-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{job.sourceUrl}</div>
                        {job.sourceUser && <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 2 }}>{job.sourceUser}</div>}
                      </div>
                      <div>
                        <span style={{ fontSize: 11, fontWeight: 600, padding: '2px 8px', borderRadius: 4, background: st.bg, color: st.color }}>{job.status}</span>
                        {job.errorCount > 0 && <div style={{ fontSize: 11, color: '#ef4444', marginTop: 3 }}>{job.errorCount} errors</div>}
                      </div>
                      <div style={{ fontSize: 13 }}>{job.repositoriesDone}/{job.repositoriesTotal || '?'}</div>
                      <div style={{ fontSize: 13 }}>{job.assetsDone.toLocaleString()}/{job.assetsTotal ? job.assetsTotal.toLocaleString() : '?'}</div>
                      <div style={{ fontSize: 12, color: 'var(--holo-text-faint)' }}>
                        {job.finishedAt ? new Date(job.finishedAt).toLocaleString() : job.updatedAt ? new Date(job.updatedAt).toLocaleString() : '—'}
                      </div>
                    </div>
                  )
                })}
              </div>
            </>
          )}
        </>
      )}

      {showCreate && (
        <CreateMigrationJobModal
          onClose={() => setShowCreate(false)}
          onCreated={() => {
            setShowCreate(false)
            qc.invalidateQueries({ queryKey: ['migrationJobs'] })
          }}
        />
      )}
    </div>
  )
}

function MigrationJobCard({ job, onPause, onResume }: { job: MigrationJobData; onPause: () => void; onResume: () => void }) {
  const reposPct = job.repositoriesTotal ? Math.round((job.repositoriesDone / job.repositoriesTotal) * 100) : 0
  const assetsPct = job.assetsTotal ? Math.round((job.assetsDone / job.assetsTotal) * 100) : 0
  const st = MIG_STATUS[job.status] ?? MIG_STATUS.pending
  return (
    <HoloCard style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <ArrowRightLeft size={15} style={{ color: 'var(--holo-text-faint)', flexShrink: 0 }} />
        <span style={{ flex: 1, fontSize: 14, fontWeight: 600, color: 'var(--holo-text)', fontFamily: 'monospace', wordBreak: 'break-all' }}>{job.sourceUrl}</span>
        <span style={{ fontSize: 11, fontWeight: 600, padding: '3px 9px', borderRadius: 4, background: st.bg, color: st.color }}>{job.status}</span>
      </div>

      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' as const }}>
        {[
          { label: 'Repos', on: job.migrateRepos },
          { label: 'Users', on: job.migrateUsers },
          { label: 'Policies', on: job.migratePolicies },
          { label: 'Artifacts', on: job.migrateBlobs },
        ].map(s => (
          <span key={s.label} style={{ fontSize: 11, padding: '2px 8px', borderRadius: 4, background: s.on ? 'rgba(59,130,246,0.15)' : 'rgba(255,255,255,0.04)', color: s.on ? '#3b82f6' : 'var(--holo-text-faint)', fontWeight: 600 }}>{s.label}</span>
        ))}
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 12 }}>
        <div style={{ background: 'rgba(255,255,255,0.02)', borderRadius: 8, padding: '10px 12px' }}>
          <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginBottom: 4, textTransform: 'uppercase', letterSpacing: '0.04em' }}>Repositories</div>
          <div style={{ fontSize: 18, fontWeight: 700, color: 'var(--holo-text)' }}>{job.repositoriesDone}<span style={{ fontSize: 13, color: 'var(--holo-text-faint)', fontWeight: 400 }}>/{job.repositoriesTotal || '?'}</span></div>
          <div style={{ height: 4, borderRadius: 2, background: 'rgba(255,255,255,0.08)', overflow: 'hidden', marginTop: 6 }}>
            <div style={{ height: '100%', width: reposPct + '%', background: 'var(--holo-a)', transition: 'width 0.4s' }} />
          </div>
        </div>
        <div style={{ background: 'rgba(255,255,255,0.02)', borderRadius: 8, padding: '10px 12px' }}>
          <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginBottom: 4, textTransform: 'uppercase', letterSpacing: '0.04em' }}>Assets</div>
          <div style={{ fontSize: 18, fontWeight: 700, color: 'var(--holo-text)' }}>{job.assetsDone}<span style={{ fontSize: 13, color: 'var(--holo-text-faint)', fontWeight: 400 }}>/{job.assetsTotal || '?'}</span></div>
          <div style={{ height: 4, borderRadius: 2, background: 'rgba(255,255,255,0.08)', overflow: 'hidden', marginTop: 6 }}>
            <div style={{ height: '100%', width: assetsPct + '%', background: '#22c55e', transition: 'width 0.4s' }} />
          </div>
        </div>
        <div style={{ background: 'rgba(255,255,255,0.02)', borderRadius: 8, padding: '10px 12px' }}>
          <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginBottom: 4, textTransform: 'uppercase', letterSpacing: '0.04em' }}>Errors</div>
          <div style={{ fontSize: 18, fontWeight: 700, color: job.errorCount > 0 ? '#ef4444' : '#22c55e' }}>{job.errorCount}</div>
        </div>
      </div>

      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <span style={{ fontSize: 12, color: 'var(--holo-text-faint)' }}>Started {job.startedAt ? new Date(job.startedAt).toLocaleString() : new Date(job.createdAt).toLocaleString()}</span>
        <div style={{ display: 'flex', gap: 8 }}>
          {job.status === 'running' && <HoloButton onClick={onPause}><Pause size={12} /> Pause</HoloButton>}
          {job.status === 'paused' && <HoloButton onClick={onResume}><Play size={12} /> Resume</HoloButton>}
        </div>
      </div>
    </HoloCard>
  )
}

function CreateMigrationJobModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [form, setForm] = useState({
    sourceUrl: '', username: 'admin', password: '', concurrency: '4',
  })
  const [scope, setScope] = useState({
    migrateRepos: true, migrateUsers: true, migratePolicies: true, migrateBlobs: true,
  })
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm(f => ({ ...f, [k]: e.target.value }))

  const toggleScope = (k: keyof typeof scope) =>
    setScope(s => ({ ...s, [k]: !s[k] }))

  const validateStep = (stepIdx: number): boolean => {
    setError('')
    if (stepIdx === 0) {
      if (!form.sourceUrl.trim()) { setError('Nexus URL is required'); return false }
      if (!form.password.trim()) { setError('Password is required'); return false }
    }
    if (stepIdx === 1) {
      if (!Object.values(scope).some(Boolean)) { setError('Select at least one scope item'); return false }
    }
    return true
  }

  const handleFinish = async () => {
    setError('')
    setLoading(true)
    try {
      await nexspenceApi.createMigrationJob({
        sourceUrl: form.sourceUrl,
        credentials: { username: form.username, password: form.password },
        options: { concurrency: parseInt(form.concurrency) || 4 },
        scope: {
          migrateRepos: scope.migrateRepos,
          migrateUsers: scope.migrateUsers,
          migratePolicies: scope.migratePolicies,
          migrateBlobs: scope.migrateBlobs,
        },
      })
      onCreated()
    } catch (err: unknown) {
      const e = err as { response?: { data?: { error?: string } } }
      setError(e.response?.data?.error ?? 'Failed to create migration job')
    } finally {
      setLoading(false)
    }
  }

  const LABEL = { fontSize: 11, fontWeight: 600 as const, color: 'var(--holo-text-dim)', textTransform: 'uppercase' as const, letterSpacing: '0.04em' }

  const scopeItems: { key: keyof typeof scope; label: string }[] = [
    { key: 'migrateRepos',    label: 'Repositories' },
    { key: 'migrateUsers',    label: 'Users & Roles' },
    { key: 'migratePolicies', label: 'Cleanup Policies' },
    { key: 'migrateBlobs',   label: 'Artifacts (blobs)' },
  ]

  const step1 = (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
        <label style={LABEL}>Nexus URL *</label>
        <HoloInput placeholder="https://nexus.example.com" value={form.sourceUrl} onChange={set('sourceUrl')} autoFocus />
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <label style={LABEL}>Username</label>
          <HoloInput value={form.username} onChange={set('username')} />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <label style={LABEL}>Password *</label>
          <HoloInput type="password" value={form.password} onChange={set('password')} />
        </div>
      </div>
    </div>
  )

  const step2 = (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <label style={LABEL}>Migration Scope</label>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
        {scopeItems.map(({ key, label }) => (
          <label key={key} style={{
            display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer',
            padding: '8px 10px',
            background: scope[key] ? 'rgba(59,130,246,0.1)' : 'rgba(255,255,255,0.03)',
            border: `1px solid ${scope[key] ? 'rgba(59,130,246,0.3)' : 'rgba(255,255,255,0.08)'}`,
            borderRadius: 8, transition: 'background 0.15s, border-color 0.15s', userSelect: 'none',
          }}>
            <input type="checkbox" checked={scope[key]} onChange={() => toggleScope(key)} style={{ accentColor: '#3b82f6', width: 14, height: 14 }} />
            <span style={{ fontSize: 13, color: scope[key] ? 'var(--holo-text)' : 'var(--holo-text-faint)', fontWeight: scope[key] ? 600 : 400 }}>{label}</span>
          </label>
        ))}
      </div>
    </div>
  )

  const step3 = (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
        <label style={LABEL}>Concurrency</label>
        <HoloInput type="number" min={1} max={16} value={form.concurrency} onChange={set('concurrency')} />
      </div>
      <div style={{
        background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(124,92,255,0.15)',
        borderRadius: 10, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 6,
      }}>
        <div style={{ fontSize: 11, fontWeight: 700, color: '#7c5cff', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 4 }}>Summary</div>
        <div style={{ fontSize: 12, color: 'var(--holo-text-dim)' }}>
          <b style={{ color: 'var(--holo-text)' }}>Source:</b> {form.sourceUrl || '—'}
        </div>
        <div style={{ fontSize: 12, color: 'var(--holo-text-dim)' }}>
          <b style={{ color: 'var(--holo-text)' }}>Scope:</b>{' '}
          {scopeItems.filter(i => scope[i.key]).map(i => i.label).join(', ') || 'none'}
        </div>
        <div style={{ fontSize: 12, color: 'var(--holo-text-dim)' }}>
          <b style={{ color: 'var(--holo-text)' }}>Concurrency:</b> {form.concurrency}
        </div>
      </div>
    </div>
  )

  return (
    <Wizard
      steps={[
        { label: 'Source', content: step1 },
        { label: 'Scope', content: step2 },
        { label: 'Options', content: step3 },
      ]}
      onFinish={handleFinish}
      finishLabel="Start Migration"
      onValidateStep={validateStep}
      onClose={onClose}
      loading={loading}
      error={error}
    />
  )
}
