import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import axios from 'axios'
import { Shield, RefreshCw, Webhook, AlertTriangle, CheckCircle, Loader, Trash2, Plus, Bug } from 'lucide-react'
import { nexusApi, apiClient } from '@/api/client'
import { UsersTab } from './UsersPage'
import { useAuthStore } from '@/store/authStore'
import { Select } from '../components/Select'
import { MultiSelect } from '../components/MultiSelect'

/* ─── Types ─────────────────────────────────────────────── */
interface Role { id: string; name: string; description: string; privileges: string[]; roles: string[]; readOnly: boolean; source?: string }
interface CVEFinding { id: string; severity: string; pkgName: string; installedVersion: string; fixedVersion?: string; title?: string }
interface ScanSummary { critical: number; high: number; medium: number; low: number; unknown: number; total: number }
interface ScanResult { scannedAt: string; imageRef: string; status: string; error?: string; summary: ScanSummary; findings: CVEFinding[] }
interface WebhookDef { id: string; name: string; url: string; events: string[]; active: boolean; secret?: string }
interface Privilege {
  id: string
  name: string
  description: string
  type: 'wildcard' | 'repository-view' | 'repository-admin' | 'application' | 'script'
  attrs: Record<string, unknown>
  contentSelectorId?: string
  readOnly: boolean
}

/* ─── Styles ─────────────────────────────────────────────── */
const S = {
  page:     { padding: 24, display: 'flex', flexDirection: 'column' as const, gap: 20 },
  header:   { display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', flexWrap: 'wrap' as const, gap: 12 },
  title:    { fontSize: 20, fontWeight: 700, color: '#dbeafe', margin: '0 0 4px' },
  subtitle: { fontSize: 13, color: 'rgba(229,231,235,0.5)', margin: 0 },
  tabs:     { display: 'flex', gap: 4, borderBottom: '1px solid rgba(255,255,255,0.08)', marginBottom: 4 },
  tab:      (active: boolean) => ({
    padding: '8px 16px', fontSize: 13, fontWeight: 600, cursor: 'pointer', border: 'none',
    background: 'none', color: active ? '#3b82f6' : 'rgba(229,231,235,0.5)',
    borderBottom: active ? '2px solid #3b82f6' : '2px solid transparent',
  }),
  card:     { background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 12, padding: '16px 18px' },
  grid:     { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))', gap: 12 },
  row:      { display: 'flex', alignItems: 'center', gap: 10, padding: '10px 0', borderBottom: '1px solid rgba(255,255,255,0.06)' },
  label:    { fontSize: 13, color: 'rgba(229,231,235,0.7)', flex: 1 },
  mono:     { fontFamily: 'monospace' as const, fontSize: 12 },
  badge:    (c: string) => ({ fontSize: 11, fontWeight: 700 as const, padding: '2px 8px', borderRadius: 4, background: c + '22', color: c }),
  btn:      (variant: 'primary'|'danger'|'ghost') => ({
    display: 'flex', alignItems: 'center', gap: 6, padding: '7px 14px', borderRadius: 8, border: 'none',
    cursor: 'pointer', fontSize: 13, fontWeight: 600,
    background: variant === 'primary' ? '#3b82f6' : variant === 'danger' ? 'rgba(239,68,68,0.15)' : 'rgba(255,255,255,0.06)',
    color: variant === 'danger' ? '#ef4444' : '#fff',
  }),
  input:    { width: '100%', background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 8, padding: '8px 12px', color: '#e5e7eb', fontSize: 13, outline: 'none', boxSizing: 'border-box' as const },
  empty:    { textAlign: 'center' as const, color: 'rgba(229,231,235,0.35)', fontSize: 14, padding: 32 },
  sevColor: (s: string) => s === 'CRITICAL' ? '#ef4444' : s === 'HIGH' ? '#f97316' : s === 'MEDIUM' ? '#f59e0b' : s === 'LOW' ? '#22c55e' : '#6b7280',
  summCard: { background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 10, padding: '12px 16px', textAlign: 'center' as const },
}

const PRIV_TYPE_COLOR: Record<string, string> = {
  'wildcard': '#3b82f6',
  'repository-view': '#22c55e',
  'repository-admin': '#f59e0b',
  'application': '#a78bfa',
  'script': '#f97316',
  'repository-content-selector': '#06b6d4',
}

/* ─── Sub-components ────────────────────────────────────── */

function RoleModal({
  title,
  form,
  onFormChange,
  allPrivs,
  selectedPrivIds,
  onPrivToggle,
  loadingPrivs,
  onSave,
  saving,
  saveDisabled,
  onCancel,
  onDelete,
}: {
  title: string
  form: { name: string; description: string }
  onFormChange: (f: { name: string; description: string }) => void
  allPrivs: Privilege[]
  selectedPrivIds: string[]
  onPrivToggle: (ids: string[]) => void
  loadingPrivs: boolean
  onSave: () => void
  saving: boolean
  saveDisabled: boolean
  onCancel: () => void
  onDelete?: () => void
}) {
  return (
    <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
      <div style={{ background: '#0f172a', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 14, padding: 24, width: 520, maxHeight: '80vh', overflowY: 'auto' as const, display: 'flex', flexDirection: 'column', gap: 12 }}>
        <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: '#dbeafe' }}>{title}</h3>
        <input style={S.input} placeholder="Name *" value={form.name} onChange={e => onFormChange({ ...form, name: e.target.value })} />
        <input style={S.input} placeholder="Description (optional)" value={form.description} onChange={e => onFormChange({ ...form, description: e.target.value })} />

        <div style={{ fontSize: 13, fontWeight: 600, color: 'rgba(229,231,235,0.7)', marginTop: 4 }}>Privileges</div>
        {loadingPrivs ? (
          <div style={S.empty}>Loading privileges…</div>
        ) : (
          <MultiSelect
            options={allPrivs.map(p => ({ value: p.id, label: p.name }))}
            value={selectedPrivIds}
            onChange={onPrivToggle}
            placeholder="Search and select privileges…"
          />
        )}

        <div style={{ display: 'flex', gap: 8, justifyContent: 'space-between', marginTop: 4 }}>
          <div>{onDelete && <button style={S.btn('danger')} onClick={onDelete}>Delete</button>}</div>
          <div style={{ display: 'flex', gap: 8 }}>
            <button style={S.btn('ghost')} onClick={onCancel}>Cancel</button>
            <button style={S.btn('primary')} onClick={onSave} disabled={saving || saveDisabled}>
              {saving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

function RolesTab({ roles, loading, onRefresh, admin }: { roles: Role[]; loading: boolean; onRefresh: () => void; admin: boolean }) {
  const qc = useQueryClient()

  const [editRole, setEditRole] = useState<Role | null>(null)
  const [editForm, setEditForm] = useState({ name: '', description: '' })
  const [editPrivIds, setEditPrivIds] = useState<string[]>([])

  const [showCreate, setShowCreate] = useState(false)
  const [createForm, setCreateForm] = useState({ name: '', description: '' })
  const [createPrivIds, setCreatePrivIds] = useState<string[]>([])

  const [allPrivs, setAllPrivs] = useState<Privilege[]>([])
  const [loadingPrivs, setLoadingPrivs] = useState(false)

  const [roleSearch, setRoleSearch] = useState('')
  const filtered = roles.filter(r =>
    r.name.toLowerCase().includes(roleSearch.toLowerCase())
  )

  async function loadPrivs() {
    setLoadingPrivs(true)
    try {
      setAllPrivs(await nexusApi.listPrivileges().then(r => r.data as Privilege[]))
    } finally { setLoadingPrivs(false) }
  }

  async function openEdit(r: Role) {
    setEditRole(r)
    setEditForm({ name: r.name, description: r.description })
    setLoadingPrivs(true)
    try {
      const [privList, rolePrivs] = await Promise.all([
        nexusApi.listPrivileges().then(res => res.data as Privilege[]),
        nexusApi.listRolePrivileges(r.id).then(res => res.data as Privilege[]),
      ])
      setAllPrivs(privList)
      setEditPrivIds(rolePrivs.map(p => p.id))
    } finally { setLoadingPrivs(false) }
  }

  async function openCreate() {
    setCreateForm({ name: '', description: '' })
    setCreatePrivIds([])
    setShowCreate(true)
    await loadPrivs()
  }

  const saveEdit = useMutation({
    mutationFn: async () => {
      if (!editRole) return
      await nexusApi.updateRole(editRole.id, editForm)
      await nexusApi.setRolePrivileges(editRole.id, editPrivIds)
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['roles'] }); onRefresh(); setEditRole(null) },
  })

  const create = useMutation({
    mutationFn: async () => {
      const res = await apiClient.post<Role>('/service/rest/v1/security/roles', createForm)
      if (createPrivIds.length > 0) await nexusApi.setRolePrivileges(res.data.id, createPrivIds)
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['roles'] }); onRefresh(); setShowCreate(false) },
  })

  const del = useMutation({
    mutationFn: (id: string) => nexusApi.deleteRole(id),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['roles'] }); onRefresh() },
  })

  if (loading) return <div style={S.empty}>Loading…</div>

  return (
    <>
      {admin && (
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 8 }}>
          <button style={S.btn('primary')} onClick={openCreate}><Plus size={14} /> New Role</button>
        </div>
      )}

      <input
        style={{ ...S.input, marginBottom: 12 }}
        placeholder="Search roles…"
        value={roleSearch}
        onChange={e => setRoleSearch(e.target.value)}
      />
      {!filtered.length ? <div style={S.empty}>No roles found</div> : (
        <div style={S.card}>
          {filtered.map((r, idx) => (
            <div key={r.id} style={{
              display: 'flex', alignItems: 'center', gap: 10, padding: '10px 0',
              borderBottom: idx < filtered.length - 1 ? '1px solid rgba(255,255,255,0.06)' : 'none',
            }}>
              <Shield size={15} style={{ color: '#3b82f6', flexShrink: 0 }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' as const }}>
                  <span style={{ fontSize: 14, fontWeight: 600, color: '#dbeafe' }}>{r.name}</span>
                  {r.readOnly && <span style={S.badge('#6b7280')}>built-in</span>}
                </div>
                {r.description && (
                  <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.45)', marginTop: 2 }}>{r.description}</div>
                )}
                <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' as const, marginTop: 4 }}>
                  {(r.privileges ?? []).slice(0, 4).map(p => (
                    <span key={p} style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: 'rgba(99,102,241,0.12)', color: '#a5b4fc', fontFamily: 'monospace' }}>{p}</span>
                  ))}
                  {(r.privileges ?? []).length > 4 && (
                    <span style={{ fontSize: 10, color: 'rgba(229,231,235,0.35)' }}>+{(r.privileges ?? []).length - 4} more</span>
                  )}
                </div>
              </div>
              {!r.readOnly && admin && (
                <button style={{ ...S.btn('ghost'), padding: '4px 10px', fontSize: 12 }} onClick={() => openEdit(r)}>Edit</button>
              )}
            </div>
          ))}
        </div>
      )}

      {editRole && (
        <RoleModal
          title={`Edit Role: ${editRole.name}`}
          form={editForm}
          onFormChange={setEditForm}
          allPrivs={allPrivs}
          selectedPrivIds={editPrivIds}
          onPrivToggle={setEditPrivIds}
          loadingPrivs={loadingPrivs}
          onSave={() => saveEdit.mutate()}
          saving={saveEdit.isPending}
          saveDisabled={!editForm.name.trim()}
          onCancel={() => setEditRole(null)}
          onDelete={() => { if (confirm(`Delete role ${editRole.name}?`)) { del.mutate(editRole.id); setEditRole(null) } }}
        />
      )}

      {showCreate && (
        <RoleModal
          title="New Role"
          form={createForm}
          onFormChange={setCreateForm}
          allPrivs={allPrivs}
          selectedPrivIds={createPrivIds}
          onPrivToggle={setCreatePrivIds}
          loadingPrivs={loadingPrivs}
          onSave={() => create.mutate()}
          saving={create.isPending}
          saveDisabled={!createForm.name.trim()}
          onCancel={() => setShowCreate(false)}
        />
      )}
    </>
  )
}

const SEVERITY_ORDER = ['CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'UNKNOWN']

function fmtElapsedSec(s: number): string {
  const m = Math.floor(s / 60)
  return m > 0 ? `${m}m ${s % 60}s` : `${s}s`
}

function ScanTab() {
  const [componentId, setComponentId] = useState('')
  const [imageRef, setImageRef] = useState('')
  const [result, setResult] = useState<ScanResult | null>(null)
  const [scanning, setScanning] = useState(false)
  const [error, setError] = useState('')
  const [severityFilter, setSeverityFilter] = useState('ALL')
  const [elapsed, setElapsed] = useState(0)

  useEffect(() => {
    if (!scanning) { setElapsed(0); return }
    const t = setInterval(() => setElapsed((n) => n + 1), 1000)
    return () => clearInterval(t)
  }, [scanning])

  async function runScan() {
    if (!componentId.trim()) { setError('Enter a component ID'); return }
    setScanning(true); setError(''); setResult(null)
    try {
      const res = await apiClient.post<ScanResult>(`/api/v1/components/${componentId.trim()}/scan`, { imageRef: imageRef.trim() || undefined })
      setResult(res.data)
    } catch (e: unknown) {
      let msg: string
      if (axios.isAxiosError(e)) {
        const d = e.response?.data
        const fromBody = typeof d === 'object' && d !== null && 'error' in d && typeof (d as { error?: string }).error === 'string'
          ? (d as { error: string }).error
          : undefined
        msg = fromBody ?? e.message
      } else if (e instanceof Error) {
        msg = e.message
      } else {
        msg = String(e)
      }
      setError(msg)
    } finally { setScanning(false) }
  }

  const findings = result?.findings ?? []
  const filtered = severityFilter === 'ALL' ? findings : findings.filter(f => f.severity === severityFilter)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={S.card}>
        <div style={{ fontSize: 14, fontWeight: 600, color: '#dbeafe', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 8 }}>
          <Bug size={15} style={{ color: '#3b82f6' }} /> Trivy Vulnerability Scan
        </div>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' as const }}>
          <input style={{ ...S.input, flex: '1 1 180px' }} placeholder="Component ID (UUID)" value={componentId} onChange={e => setComponentId(e.target.value)} />
          <input style={{ ...S.input, flex: '2 1 260px' }} placeholder="Image ref override (optional, e.g. alpine:3.18)" value={imageRef} onChange={e => setImageRef(e.target.value)} />
          <button style={S.btn('primary')} onClick={runScan} disabled={scanning}>
            {scanning ? <Loader size={14} className="spin" /> : <Shield size={14} />}
            {scanning ? `Scanning… ${fmtElapsedSec(elapsed)}` : 'Scan'}
          </button>
        </div>
        {scanning && (
          <div style={{ marginTop: 8, fontSize: 12, color: 'rgba(229,231,235,0.4)', lineHeight: 1.5 }}>
            Running Trivy vulnerability scan{elapsed >= 20 ? ' — first run downloads the vulnerability DB, this may take 1–3 minutes' : ''}
            {elapsed >= 90 ? '; please wait…' : ''}
          </div>
        )}
        {error && <div style={{ marginTop: 10, padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, color: '#ef4444', fontSize: 13 }}>{error}</div>}
      </div>

      {result && result.status === 'failed' && result.error && (
        <div style={{ padding: '12px 14px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.35)', borderRadius: 10, color: '#fca5a5', fontSize: 13, lineHeight: 1.45 }}>
          {result.error}
        </div>
      )}

      {result && result.status === 'ok' && (
        <>
          {/* Summary cards */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(5, 1fr)', gap: 8 }}>
            {(['CRITICAL','HIGH','MEDIUM','LOW','UNKNOWN'] as const).map(sev => (
              <div key={sev} style={S.summCard}>
                <div style={{ fontSize: 20, fontWeight: 700, color: S.sevColor(sev) }}>{result.summary[sev.toLowerCase() as keyof ScanSummary] as number}</div>
                <div style={{ fontSize: 11, color: 'rgba(229,231,235,0.5)', marginTop: 2 }}>{sev}</div>
              </div>
            ))}
          </div>

          {/* Status / meta */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 12, color: 'rgba(229,231,235,0.5)' }}>
            {result.status === 'ok' ? <CheckCircle size={14} style={{ color: '#22c55e' }} /> : <AlertTriangle size={14} style={{ color: '#f59e0b' }} />}
            <span>Image: <span style={{ color: '#dbeafe' }}>{result.imageRef || '—'}</span></span>
            <span>·</span>
            <span>Scanned {new Date(result.scannedAt).toLocaleString()}</span>
            <span>·</span>
            <span>{result.summary.total} total CVEs</span>
          </div>

          {/* Severity filter */}
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' as const }}>
            {(['ALL', ...SEVERITY_ORDER]).map(s => (
              <button key={s} onClick={() => setSeverityFilter(s)} style={{
                padding: '4px 12px', borderRadius: 6, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 600,
                background: severityFilter === s ? (s === 'ALL' ? '#3b82f6' : S.sevColor(s)) : 'rgba(255,255,255,0.06)',
                color: severityFilter === s ? '#fff' : 'rgba(229,231,235,0.6)',
              }}>{s} {s !== 'ALL' && `(${result.summary[s.toLowerCase() as keyof ScanSummary]})`}</button>
            ))}
          </div>

          {/* Findings table */}
          {filtered.length === 0 ? (
            <div style={S.empty}>{result.summary.total === 0 ? '✓ No vulnerabilities found' : 'No findings for selected severity'}</div>
          ) : (
            <div style={S.card}>
              <table style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
                <thead>
                  <tr style={{ color: 'rgba(229,231,235,0.4)', textAlign: 'left' as const }}>
                    <th style={{ padding: '0 0 10px', fontWeight: 600 }}>CVE ID</th>
                    <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Severity</th>
                    <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Package</th>
                    <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Installed</th>
                    <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Fixed</th>
                    <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Title</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map((f, i) => (
                    <tr key={f.id + f.pkgName + i} style={{ borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                      <td style={{ padding: '8px 0', ...S.mono, color: '#a5b4fc' }}>{f.id}</td>
                      <td style={{ padding: '8px 8px 8px 0' }}><span style={S.badge(S.sevColor(f.severity))}>{f.severity}</span></td>
                      <td style={{ padding: '8px 8px 8px 0', color: '#dbeafe' }}>{f.pkgName}</td>
                      <td style={{ padding: '8px 8px 8px 0', ...S.mono, color: 'rgba(229,231,235,0.6)' }}>{f.installedVersion}</td>
                      <td style={{ padding: '8px 8px 8px 0', ...S.mono, color: '#22c55e' }}>{f.fixedVersion || '—'}</td>
                      <td style={{ padding: '8px 0', color: 'rgba(229,231,235,0.6)', maxWidth: 280, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const }}>{f.title || '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}

      {result && result.status === 'failed' && !result.error && (
        <div style={S.empty}>Scan failed — no details from scanner</div>
      )}
    </div>
  )
}

const WEBHOOK_EVENTS = ['artifact.published', 'artifact.deleted', 'repo.created', 'proxy.error']

function WebhooksTab() {
  const qc = useQueryClient()
  const { data: hooks = [], isLoading, refetch } = useQuery<WebhookDef[]>({
    queryKey: ['webhooks'],
    queryFn: () => apiClient.get<WebhookDef[]>('/api/v1/webhooks').then(r => r.data),
  })
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState({ name: '', url: '', events: WEBHOOK_EVENTS, secret: '', active: true })
  const [saving, setSaving] = useState(false)

  async function save() {
    setSaving(true)
    try {
      await apiClient.post('/api/v1/webhooks', form)
      qc.invalidateQueries({ queryKey: ['webhooks'] })
      setShowForm(false)
      setForm({ name: '', url: '', events: WEBHOOK_EVENTS, secret: '', active: true })
    } finally { setSaving(false) }
  }

  const del = useMutation({
    mutationFn: (id: string) => apiClient.delete(`/api/v1/webhooks/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['webhooks'] }),
  })

  const toggleEvent = (ev: string) => setForm(f => ({
    ...f, events: f.events.includes(ev) ? f.events.filter(e => e !== ev) : [...f.events, ev]
  }))

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div style={{ fontSize: 14, color: 'rgba(229,231,235,0.5)' }}>HTTP callbacks fired on repository events</div>
        <div style={{ display: 'flex', gap: 8 }}>
          <button style={S.btn('ghost')} onClick={() => refetch()}><RefreshCw size={14} /></button>
          <button style={S.btn('primary')} onClick={() => setShowForm(v => !v)}><Plus size={14} />New Webhook</button>
        </div>
      </div>

      {showForm && (
        <div style={S.card}>
          <div style={{ fontSize: 14, fontWeight: 600, color: '#dbeafe', marginBottom: 12 }}>New Webhook</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <input style={S.input} placeholder="Name" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
            <input style={S.input} placeholder="URL (https://...)" value={form.url} onChange={e => setForm(f => ({ ...f, url: e.target.value }))} />
            <input style={S.input} placeholder="HMAC secret (optional)" value={form.secret} onChange={e => setForm(f => ({ ...f, secret: e.target.value }))} />
            <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', marginBottom: 4 }}>Events to subscribe:</div>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' as const }}>
              {WEBHOOK_EVENTS.map(ev => (
                <label key={ev} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: form.events.includes(ev) ? '#dbeafe' : 'rgba(229,231,235,0.4)', cursor: 'pointer' }}>
                  <input type="checkbox" checked={form.events.includes(ev)} onChange={() => toggleEvent(ev)} style={{ accentColor: '#3b82f6' }} />
                  <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{ev}</span>
                </label>
              ))}
            </div>
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <button style={S.btn('ghost')} onClick={() => setShowForm(false)}>Cancel</button>
              <button style={S.btn('primary')} onClick={save} disabled={saving || !form.name || !form.url}>{saving ? 'Saving…' : 'Create'}</button>
            </div>
          </div>
        </div>
      )}

      <div style={S.card}>
        {isLoading ? <div style={S.empty}>Loading…</div> : hooks.length === 0 ? <div style={S.empty}>No webhooks configured</div> : (
          hooks.map(h => (
            <div key={h.id} style={S.row}>
              <Webhook size={14} style={{ color: h.active ? '#22c55e' : '#6b7280', flexShrink: 0 }} />
              <div style={{ flex: 1 }}>
                <div style={{ color: '#dbeafe', fontWeight: 600 }}>{h.name}</div>
                <div style={{ color: 'rgba(229,231,235,0.4)', ...S.mono }}>{h.url}</div>
                <div style={{ display: 'flex', gap: 4, marginTop: 4, flexWrap: 'wrap' as const }}>
                  {h.events.map(ev => <span key={ev} style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: 'rgba(59,130,246,0.12)', color: '#93c5fd', fontFamily: 'monospace' }}>{ev}</span>)}
                </div>
              </div>
              <span style={S.badge(h.active ? '#22c55e' : '#6b7280')}>{h.active ? 'active' : 'inactive'}</span>
              <button style={S.btn('danger')} onClick={() => del.mutate(h.id)}><Trash2 size={13} /></button>
            </div>
          ))
        )}
      </div>
    </div>
  )
}

interface ContentSelector { id: string; name: string; description: string; expression: string }

function PrivilegesTab({ admin }: { admin: boolean }) {
  const qc = useQueryClient()
  const { data: privs = [], isLoading } = useQuery<Privilege[]>({
    queryKey: ['privileges'],
    queryFn: () => nexusApi.listPrivileges().then(r => r.data),
  })
  const { data: selectors = [] } = useQuery<ContentSelector[]>({
    queryKey: ['content-selectors'],
    queryFn: () => nexusApi.listContentSelectors().then(r => r.data),
  })
  const PRIV_ACTIONS = ['read', 'browse', 'write', 'delete'] as const

  const [showModal, setShowModal] = useState(false)
  const [editing, setEditing] = useState<Privilege | null>(null)
  const [form, setForm] = useState({ name: '', description: '', contentSelectorId: '', actions: [] as string[] })
  const [saveError, setSaveError] = useState('')

  function openCreate() {
    setEditing(null)
    setForm({ name: '', description: '', contentSelectorId: '', actions: [] })
    setSaveError('')
    setShowModal(true)
  }

  function openEdit(p: Privilege) {
    setEditing(p)
    setForm({
      name: p.name,
      description: p.description,
      contentSelectorId: p.contentSelectorId ?? '',
      actions: (p.attrs?.actions as string[] | undefined) ?? [],
    })
    setSaveError('')
    setShowModal(true)
  }

  const save = useMutation({
    mutationFn: async () => {
      if (!form.contentSelectorId) throw new Error('Select a content selector')
      const payload = {
        name: form.name,
        description: form.description,
        type: 'repository-content-selector',
        contentSelectorId: form.contentSelectorId,
        attrs: { actions: form.actions },
      }
      if (editing) return nexusApi.updatePrivilege(editing.id, payload)
      return nexusApi.createPrivilege(payload)
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['privileges'] }); setShowModal(false) },
    onError: (e: unknown) => {
      let msg = 'Error'
      if (axios.isAxiosError(e)) {
        const d = e.response?.data
        if (typeof d === 'object' && d !== null && 'error' in d) msg = String((d as { error: unknown }).error)
      } else if (e instanceof Error) { msg = e.message }
      setSaveError(msg)
    },
  })

  const del = useMutation({
    mutationFn: (id: string) => nexusApi.deletePrivilege(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['privileges'] }),
  })

  const selectedSelector = selectors.find(s => s.id === form.contentSelectorId)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {admin && (
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <button style={S.btn('primary')} onClick={openCreate}><Plus size={14} /> New Privilege</button>
        </div>
      )}

      {isLoading ? <div style={S.empty}>Loading…</div> : privs.length === 0 ? <div style={S.empty}>No privileges</div> : (
        <div style={S.card}>
          <table style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
            <thead>
              <tr style={{ color: 'rgba(229,231,235,0.5)', textAlign: 'left' as const }}>
                <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Name</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Type</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Actions</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Description</th>
                {admin && <th style={{ padding: '0 0 10px', fontWeight: 600, width: 80 }}></th>}
              </tr>
            </thead>
            <tbody>
              {privs.map(p => {
                const actions = (p.attrs?.actions as string[] | undefined) ?? []
                return (
                  <tr key={p.id} style={{ borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                    <td style={{ padding: '9px 0', color: '#dbeafe', fontWeight: 600 }}>{p.name}</td>
                    <td style={{ padding: '9px 8px' }}>
                      <span style={S.badge(PRIV_TYPE_COLOR[p.type] ?? '#6b7280')}>{p.type}</span>
                      {p.readOnly && <span style={{ ...S.badge('#6b7280'), marginLeft: 4 }}>built-in</span>}
                    </td>
                    <td style={{ padding: '9px 8px' }}>
                      <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' as const }}>
                        {actions.length > 0 ? actions.map(a => (
                          <span key={a} style={S.badge(a === 'write' || a === 'delete' ? '#f59e0b' : '#22c55e')}>{a}</span>
                        )) : <span style={{ color: 'rgba(229,231,235,0.3)', fontSize: 12 }}>—</span>}
                      </div>
                    </td>
                    <td style={{ padding: '9px 8px', color: 'rgba(229,231,235,0.55)' }}>{p.description || '—'}</td>
                    {admin && (
                      <td style={{ padding: '9px 0', display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                        {!p.readOnly && (
                          <>
                            <button style={{ ...S.btn('ghost'), padding: '4px 8px' }} onClick={() => openEdit(p)}>Edit</button>
                            <button style={{ ...S.btn('danger'), padding: '4px 8px' }} onClick={() => { if (confirm(`Delete ${p.name}?`)) del.mutate(p.id) }}><Trash2 size={13} /></button>
                          </>
                        )}
                      </td>
                    )}
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {showModal && (
        <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
          <div style={{ background: '#0f172a', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 14, padding: 24, width: 480, display: 'flex', flexDirection: 'column', gap: 12 }}>
            <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: '#dbeafe' }}>{editing ? 'Edit Privilege' : 'New Privilege'}</h3>

            <input style={S.input} placeholder="Name *" value={form.name}
              onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
            <input style={S.input} placeholder="Description (optional)" value={form.description}
              onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />

            <div>
              <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', marginBottom: 6 }}>Actions</div>
              <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' as const }}>
                {PRIV_ACTIONS.map(a => (
                  <label key={a} style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer', fontSize: 13, color: form.actions.includes(a) ? '#dbeafe' : 'rgba(229,231,235,0.5)' }}>
                    <input
                      type="checkbox"
                      checked={form.actions.includes(a)}
                      onChange={e => setForm(f => ({ ...f, actions: e.target.checked ? [...f.actions, a] : f.actions.filter(x => x !== a) }))}
                      style={{ accentColor: '#3b82f6' }}
                    />
                    {a}
                  </label>
                ))}
              </div>
            </div>

            <div>
              <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', marginBottom: 4 }}>Content Selector *</div>
              <Select
                options={[
                  { value: '', label: '— select a content selector —' },
                  ...selectors.map(s => ({ value: s.id, label: s.name })),
                ]}
                value={form.contentSelectorId}
                onChange={v => setForm(f => ({ ...f, contentSelectorId: v }))}
              />
              {selectedSelector && (
                <div style={{ marginTop: 6, padding: '6px 10px', background: 'rgba(6,182,212,0.08)', borderRadius: 8, fontSize: 12, color: '#67e8f9', fontFamily: 'monospace' }}>
                  {selectedSelector.expression}
                </div>
              )}
              {selectors.length === 0 && (
                <div style={{ marginTop: 6, fontSize: 12, color: 'rgba(239,68,68,0.7)' }}>
                  No content selectors defined — create one in the Content Selectors tab first.
                </div>
              )}
            </div>

            {saveError && (
              <div style={{ padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, color: '#ef4444', fontSize: 12 }}>{saveError}</div>
            )}

            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 4 }}>
              <button style={S.btn('ghost')} onClick={() => setShowModal(false)}>Cancel</button>
              <button style={S.btn('primary')} onClick={() => save.mutate()} disabled={save.isPending || !form.name.trim() || !form.contentSelectorId}>
                {save.isPending ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function ContentSelectorsTab({ admin }: { admin: boolean }) {
  const qc = useQueryClient()
  const { data: selectors = [], isLoading } = useQuery<{ id: string; name: string; description: string; expression: string }[]>({
    queryKey: ['content-selectors'],
    queryFn: () => nexusApi.listContentSelectors().then(r => r.data),
  })
  const { data: allRepos = [] } = useQuery<{ name: string; format: string; type: string }[]>({
    queryKey: ['repositories'],
    queryFn: () => nexusApi.listRepositories().then(r => r.data),
  })

  const [showModal, setShowModal] = useState(false)
  const [editing, setEditing] = useState<{ id: string; name: string; description: string; expression: string } | null>(null)
  const [form, setForm] = useState({ name: '', description: '', repo: '', path: '' })
  const [repoSearch, setRepoSearch] = useState('')
  const [pathSearch, setPathSearch] = useState('')
  const [saveError, setSaveError] = useState('')
  const [repoOpen, setRepoOpen] = useState(false)
  const [pathOpen, setPathOpen] = useState(false)
  const [legacyExpr, setLegacyExpr] = useState('')

  const { data: pathTree, isLoading: pathsLoading } = useQuery<{ paths: string[] }>({
    queryKey: ['path-tree', form.repo, pathSearch],
    queryFn: () => nexusApi.listPathTree(form.repo, pathSearch || undefined).then(r => r.data),
    enabled: !!form.repo,
  })

  function buildExpression(repo: string, path: string): string {
    if (repo && path) return `repository == "${repo}" && path.startsWith("${path}")`
    if (repo)         return `repository == "${repo}"`
    if (path)         return `path.startsWith("${path}")`
    return ''
  }

  function parseExpression(expr: string): { repo: string; path: string } {
    const full = expr.match(/^repository == "([^"]+)" && path\.startsWith\("([^"]+)"\)$/)
    if (full) return { repo: full[1], path: full[2] }
    const repoOnly = expr.match(/^repository == "([^"]+)"$/)
    if (repoOnly) return { repo: repoOnly[1], path: '' }
    const pathOnly = expr.match(/^path\.startsWith\("([^"]+)"\)$/)
    if (pathOnly) return { repo: '', path: pathOnly[1] }
    return { repo: '', path: '' }
  }

  function selectorSummary(expr: string): string {
    const { repo, path } = parseExpression(expr)
    const displayPath = path.replace(/^\//, '').replace(/\/$/, '') // "/da/bas/" → "da/bas"
    if (repo && path) return `${displayPath}/* in ${repo}`
    if (repo)         return `all paths in ${repo}`
    if (path)         return `${displayPath}/* in all repos`
    return expr
  }

  function openCreate() {
    setEditing(null)
    setForm({ name: '', description: '', repo: '', path: '' })
    setRepoSearch(''); setPathSearch(''); setSaveError('')
    setRepoOpen(false); setPathOpen(false); setLegacyExpr('')
    setShowModal(true)
  }

  function openEdit(s: { id: string; name: string; description: string; expression: string }) {
    setEditing(s)
    const { repo, path } = parseExpression(s.expression)
    const isLegacy = !repo && !path && !!s.expression
    setLegacyExpr(isLegacy ? s.expression : '')
    setForm({ name: s.name, description: s.description, repo, path })
    setRepoSearch(repo); setPathSearch(path); setSaveError('')
    setRepoOpen(false); setPathOpen(false)
    setShowModal(true)
  }

  const save = useMutation({
    mutationFn: async () => {
      const expression = legacyExpr || buildExpression(form.repo, form.path)
      if (!expression) throw new Error('Select a repository or path')
      const payload = { name: form.name, description: form.description, expression }
      if (editing) return nexusApi.updateContentSelector(editing.id, payload)
      return nexusApi.createContentSelector(payload)
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['content-selectors'] }); setShowModal(false) },
    onError: (e: unknown) => {
      let msg = 'Error'
      if (axios.isAxiosError(e)) {
        const d = e.response?.data
        if (typeof d === 'object' && d !== null && 'error' in d) msg = String((d as { error: unknown }).error)
      } else if (e instanceof Error) { msg = e.message }
      setSaveError(msg)
    },
  })

  const del = useMutation({
    mutationFn: (id: string) => nexusApi.deleteContentSelector(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['content-selectors'] }),
  })

  const filteredRepos = allRepos.filter(r =>
    !repoSearch || r.name.toLowerCase().includes(repoSearch.toLowerCase())
  )

  const paths = pathTree?.paths ?? []
  const canSave = !!form.name.trim() && (!!form.repo || !!form.path || !!legacyExpr)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {admin && (
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <button style={S.btn('primary')} onClick={openCreate}><Plus size={14} /> New Selector</button>
        </div>
      )}

      {isLoading ? <div style={S.empty}>Loading…</div> : selectors.length === 0 ? <div style={S.empty}>No content selectors</div> : (
        <div style={S.card}>
          <table style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
            <thead>
              <tr style={{ color: 'rgba(229,231,235,0.5)', textAlign: 'left' as const }}>
                <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Name</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Scope</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Description</th>
                {admin && <th style={{ padding: '0 0 10px', width: 80 }}></th>}
              </tr>
            </thead>
            <tbody>
              {selectors.map(s => (
                <tr key={s.id} style={{ borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                  <td style={{ padding: '9px 0', color: '#dbeafe', fontWeight: 600 }}>{s.name}</td>
                  <td style={{ padding: '9px 8px' }}>
                    <code style={{ ...S.mono, fontSize: 12, color: '#a5b4fc' }}>{selectorSummary(s.expression)}</code>
                  </td>
                  <td style={{ padding: '9px 8px', color: 'rgba(229,231,235,0.55)' }}>{s.description || '—'}</td>
                  {admin && (
                    <td style={{ padding: '9px 0', display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                      <button style={{ ...S.btn('ghost'), padding: '4px 8px' }} onClick={() => openEdit(s)}>Edit</button>
                      <button style={{ ...S.btn('danger'), padding: '4px 8px' }} onClick={() => { if (confirm(`Delete ${s.name}?`)) del.mutate(s.id) }}><Trash2 size={13} /></button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showModal && (
        <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
          <div style={{ background: '#0f172a', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 14, padding: 24, width: 520, display: 'flex', flexDirection: 'column', gap: 12 }}>
            <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: '#dbeafe' }}>
              {editing ? 'Edit Content Selector' : 'New Content Selector'}
            </h3>

            <input style={S.input} placeholder="Name *" value={form.name}
              onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
            <input style={S.input} placeholder="Description (optional)" value={form.description}
              onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />

            {/* Repository dropdown */}
            <div>
              <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', marginBottom: 4 }}>Repository</div>
              <input
                style={S.input}
                placeholder="Search repositories…"
                value={repoSearch}
                onFocus={() => setRepoOpen(true)}
                onChange={e => { setRepoSearch(e.target.value); setForm(f => ({ ...f, repo: '', path: '' })); setRepoOpen(true) }}
              />
              {repoOpen && (
                <div style={{ maxHeight: 160, overflowY: 'auto', background: 'rgba(0,0,0,0.4)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, marginTop: 4 }}>
                  <div
                    style={{ padding: '7px 12px', fontSize: 13, cursor: 'pointer', color: 'rgba(229,231,235,0.5)',
                      background: !form.repo ? 'rgba(59,130,246,0.12)' : 'transparent' }}
                    onClick={() => { setForm(f => ({ ...f, repo: '', path: '' })); setRepoSearch(''); setRepoOpen(false) }}
                  >
                    Any repository
                  </div>
                  {filteredRepos.map(r => (
                    <div
                      key={r.name}
                      style={{ padding: '7px 12px', fontSize: 13, cursor: 'pointer',
                        color: form.repo === r.name ? '#3b82f6' : '#dbeafe',
                        background: form.repo === r.name ? 'rgba(59,130,246,0.12)' : 'transparent' }}
                      onClick={() => { setForm(f => ({ ...f, repo: r.name, path: '' })); setRepoSearch(r.name); setPathSearch(''); setRepoOpen(false) }}
                    >
                      {r.name}
                      <span style={{ fontSize: 11, color: 'rgba(229,231,235,0.4)', marginLeft: 8 }}>{r.format}</span>
                    </div>
                  ))}
                  {filteredRepos.length === 0 && (
                    <div style={{ padding: '7px 12px', fontSize: 13, color: 'rgba(229,231,235,0.35)' }}>No repositories found</div>
                  )}
                </div>
              )}
            </div>

            {/* Path dropdown */}
            <div>
              <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', marginBottom: 4 }}>
                Path prefix {!form.repo && <span style={{ color: 'rgba(229,231,235,0.3)' }}>(select a repository first)</span>}
              </div>
              <input
                style={{ ...S.input, opacity: !form.repo ? 0.4 : 1 }}
                placeholder="Search paths…"
                disabled={!form.repo}
                value={pathSearch}
                onFocus={() => setPathOpen(true)}
                onChange={e => { setPathSearch(e.target.value); setForm(f => ({ ...f, path: '' })); setPathOpen(true) }}
              />
              {form.repo && pathOpen && (
                <div style={{ maxHeight: 180, overflowY: 'auto', background: 'rgba(0,0,0,0.4)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, marginTop: 4 }}>
                  <div
                    style={{ padding: '7px 12px', fontSize: 13, cursor: 'pointer', color: 'rgba(229,231,235,0.5)',
                      background: !form.path ? 'rgba(59,130,246,0.12)' : 'transparent' }}
                    onClick={() => { setForm(f => ({ ...f, path: '' })); setPathSearch(''); setPathOpen(false) }}
                  >
                    Any path
                  </div>
                  {pathsLoading ? (
                    <div style={{ padding: '7px 12px', fontSize: 13, color: 'rgba(229,231,235,0.35)' }}>Loading…</div>
                  ) : paths.length === 0 ? (
                    <div style={{ padding: '7px 12px', fontSize: 13, color: 'rgba(229,231,235,0.35)' }}>No paths found</div>
                  ) : paths.map(p => {
                    // strip leading/trailing slashes for display: "/da/bas/" → "da/bas"
                    const label = p.replace(/^\//, '').replace(/\/$/, '')
                    const depth = (label.match(/\//g) ?? []).length
                    return (
                      <div
                        key={p}
                        style={{ padding: '6px 12px', paddingLeft: 12 + depth * 14, fontSize: 13, cursor: 'pointer',
                          color: form.path === p ? '#3b82f6' : '#dbeafe',
                          background: form.path === p ? 'rgba(59,130,246,0.12)' : 'transparent',
                          fontFamily: 'monospace' }}
                        onClick={() => { setForm(f => ({ ...f, path: p })); setPathSearch(label); setPathOpen(false) }}
                      >
                        {label}
                      </div>
                    )
                  })}
                </div>
              )}
            </div>

            {/* CEL preview */}
            {(form.repo || form.path) && (
              <div style={{ padding: '6px 10px', background: 'rgba(59,130,246,0.08)', borderRadius: 8, fontSize: 12, color: '#93c5fd', fontFamily: 'monospace' }}>
                {buildExpression(form.repo, form.path)}
              </div>
            )}

            {legacyExpr !== undefined && editing && !form.repo && !form.path && editing.expression && (
              <div>
                <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', marginBottom: 4 }}>
                  CEL Expression (legacy — not parseable as simple selector)
                </div>
                <textarea
                  style={{ ...S.input, fontFamily: 'monospace', fontSize: 12, height: 64, resize: 'vertical' as const }}
                  value={legacyExpr}
                  onChange={e => setLegacyExpr(e.target.value)}
                />
              </div>
            )}

            {saveError && (
              <div style={{ padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, color: '#ef4444', fontSize: 12 }}>{saveError}</div>
            )}

            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <button style={S.btn('ghost')} onClick={() => setShowModal(false)}>Cancel</button>
              <button style={S.btn('primary')} onClick={() => save.mutate()} disabled={save.isPending || !canSave}>
                {save.isPending ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

/* ─── Main page ──────────────────────────────────────────── */
type Tab = 'roles' | 'privileges' | 'selectors' | 'users' | 'scan' | 'webhooks'

export default function SecurityPage() {
  const { isAdmin } = useAuthStore()
  const admin = isAdmin()
  const [tab, setTab] = useState<Tab>('roles')
  const { data: roles = [], isLoading, refetch } = useQuery<Role[]>({
    queryKey: ['roles'],
    queryFn: () => nexusApi.listRoles().then(r => r.data),
  })

  const allTabs: [Tab, string][] = [
    ['roles',      'Roles'],
    ['privileges', 'Privileges'],
    ['selectors',  'Content Selectors'],
    ...(admin ? [['users', 'Users'], ['scan', 'CVE Scan'], ['webhooks', 'Webhooks']] as [Tab, string][] : []),
  ]

  return (
    <div style={S.page}>
      <div style={S.header}>
        <div>
          <h1 style={S.title}>Security</h1>
          <p style={S.subtitle}>
            {admin
              ? 'Roles, users, privileges, content selectors and webhooks'
              : 'View roles and privileges. Content Selector → Privilege → Role defines what each user can access.'}
          </p>
        </div>
        {tab === 'roles' && <button style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: 8, color: 'rgba(229,231,235,0.7)', cursor: 'pointer', display: 'flex', alignItems: 'center' }} onClick={() => refetch()}><RefreshCw size={16} /></button>}
      </div>

      <div style={S.tabs}>
        {allTabs.map(([id, label]) => (
          <button key={id} style={S.tab(tab === id)} onClick={() => setTab(id)}>{label}</button>
        ))}
      </div>

      {tab === 'roles'      && <RolesTab roles={roles} loading={isLoading} onRefresh={refetch} admin={admin} />}
      {tab === 'privileges' && <PrivilegesTab admin={admin} />}
      {tab === 'selectors'  && <ContentSelectorsTab admin={admin} />}
      {tab === 'users'      && admin && <UsersTab />}
      {tab === 'scan'       && admin && <ScanTab />}
      {tab === 'webhooks'   && admin && <WebhooksTab />}
    </div>
  )
}
