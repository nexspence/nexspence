import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import axios from 'axios'
import { Shield, RefreshCw, Key, Webhook, AlertTriangle, CheckCircle, Loader, Trash2, Plus, Bug } from 'lucide-react'
import { nexusApi, apiClient } from '@/api/client'

/* ─── Types ─────────────────────────────────────────────── */
interface Role { id: string; name: string; description: string; privileges: string[]; roles: string[]; readOnly: boolean; source?: string }
interface CVEFinding { id: string; severity: string; pkgName: string; installedVersion: string; fixedVersion?: string; title?: string }
interface ScanSummary { critical: number; high: number; medium: number; low: number; unknown: number; total: number }
interface ScanResult { scannedAt: string; imageRef: string; status: string; error?: string; summary: ScanSummary; findings: CVEFinding[] }
interface UserToken { id: string; name: string; scopes: string[]; createdAt: string; lastUsedAt?: string; expiresAt?: string }
interface NewToken extends UserToken { token: string }
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

/* ─── Sub-components ────────────────────────────────────── */

function RolesTab({ roles, loading }: { roles: Role[]; loading: boolean }) {
  if (loading) return <div style={S.empty}>Loading…</div>
  if (!roles.length) return <div style={S.empty}>No roles found</div>
  return (
    <div style={S.grid}>
      {roles.map(r => (
        <div key={r.id} style={S.card}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
            <Shield size={15} style={{ color: '#3b82f6' }} />
            <span style={{ fontSize: 14, fontWeight: 600, color: '#dbeafe', flex: 1 }}>{r.name}</span>
            {r.readOnly && <span style={S.badge('#6b7280')}>built-in</span>}
          </div>
          {r.description && <p style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', margin: '0 0 8px' }}>{r.description}</p>}
          <div style={{ display: 'flex', flexWrap: 'wrap' as const, gap: 4 }}>
            {(r.privileges ?? []).slice(0, 6).map(p => (
              <span key={p} style={{ fontSize: 10, padding: '2px 6px', borderRadius: 4, background: 'rgba(99,102,241,0.12)', color: '#a5b4fc', fontFamily: 'monospace' }}>{p}</span>
            ))}
            {(r.privileges ?? []).length > 6 && <span style={{ fontSize: 10, color: 'rgba(229,231,235,0.4)' }}>+{(r.privileges ?? []).length - 6}</span>}
          </div>
        </div>
      ))}
    </div>
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

function TokensTab() {
  const qc = useQueryClient()
  const { data: tokens = [], isLoading } = useQuery<UserToken[]>({
    queryKey: ['my-tokens'],
    queryFn: () => apiClient.get<UserToken[]>('/api/v1/tokens').then(r => r.data),
  })
  const [name, setName] = useState('')
  const [newToken, setNewToken] = useState<NewToken | null>(null)
  const [creating, setCreating] = useState(false)

  async function create() {
    if (!name.trim()) return
    setCreating(true)
    try {
      const res = await apiClient.post<NewToken>('/api/v1/tokens', { name: name.trim() })
      setNewToken(res.data); setName('')
      qc.invalidateQueries({ queryKey: ['my-tokens'] })
    } finally { setCreating(false) }
  }

  const del = useMutation({
    mutationFn: (id: string) => apiClient.delete(`/api/v1/tokens/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['my-tokens'] }),
  })

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={S.card}>
        <div style={{ fontSize: 14, fontWeight: 600, color: '#dbeafe', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 8 }}>
          <Key size={15} style={{ color: '#3b82f6' }} /> Create API Token
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <input style={S.input} placeholder="Token name" value={name} onChange={e => setName(e.target.value)} onKeyDown={e => e.key === 'Enter' && create()} />
          <button style={S.btn('primary')} onClick={create} disabled={creating || !name.trim()}>
            <Plus size={14} />{creating ? 'Creating…' : 'Create'}
          </button>
        </div>
      </div>

      {newToken && (
        <div style={{ ...S.card, background: 'rgba(34,197,94,0.06)', border: '1px solid rgba(34,197,94,0.3)' }}>
          <div style={{ fontSize: 13, color: '#22c55e', fontWeight: 600, marginBottom: 8 }}>Token created — copy it now, it will not be shown again</div>
          <code style={{ ...S.mono, fontSize: 13, background: 'rgba(0,0,0,0.3)', padding: '8px 12px', borderRadius: 8, display: 'block', wordBreak: 'break-all' as const, color: '#a5b4fc' }}>{newToken.token}</code>
          <button style={{ ...S.btn('ghost'), marginTop: 8, fontSize: 12 }} onClick={() => setNewToken(null)}>Dismiss</button>
        </div>
      )}

      <div style={S.card}>
        <div style={{ fontSize: 14, fontWeight: 600, color: '#dbeafe', marginBottom: 12 }}>Your API Tokens</div>
        {isLoading ? <div style={S.empty}>Loading…</div> : tokens.length === 0 ? <div style={S.empty}>No tokens yet</div> : (
          tokens.map(t => (
            <div key={t.id} style={S.row}>
              <Key size={14} style={{ color: '#3b82f6', flexShrink: 0 }} />
              <div style={{ flex: 1 }}>
                <div style={{ color: '#dbeafe', fontWeight: 600 }}>{t.name}</div>
                <div style={{ fontSize: 11, color: 'rgba(229,231,235,0.4)' }}>
                  Created {new Date(t.createdAt).toLocaleDateString()}
                  {t.lastUsedAt && ` · Last used ${new Date(t.lastUsedAt).toLocaleDateString()}`}
                  {t.expiresAt && ` · Expires ${new Date(t.expiresAt).toLocaleDateString()}`}
                </div>
              </div>
              <button style={S.btn('danger')} onClick={() => del.mutate(t.id)}><Trash2 size={13} /></button>
            </div>
          ))
        )}
      </div>
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

const PRIV_TYPES = ['wildcard', 'repository-view', 'repository-admin', 'application', 'script'] as const
type PrivType = typeof PRIV_TYPES[number]

const PRIV_TYPE_COLOR: Record<PrivType, string> = {
  'wildcard': '#3b82f6',
  'repository-view': '#22c55e',
  'repository-admin': '#f59e0b',
  'application': '#a78bfa',
  'script': '#f97316',
}

function PrivilegeAttrFields({ type, attrs, onChange }: {
  type: PrivType
  attrs: Record<string, unknown>
  onChange: (key: string, value: unknown) => void
}) {
  const inp = (key: string, placeholder: string) => (
    <input
      key={key}
      style={{ ...S.input, flex: 1 }}
      placeholder={placeholder}
      value={(attrs[key] as string) ?? ''}
      onChange={e => onChange(key, e.target.value)}
    />
  )
  if (type === 'wildcard') return inp('pattern', 'Pattern (e.g. nexus:*:read)')
  if (type === 'repository-view' || type === 'repository-admin') return (
    <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' as const }}>
      {inp('format', 'Format (e.g. maven2 or *)')}
      {inp('repository', 'Repository name or *')}
    </div>
  )
  if (type === 'application') return inp('domain', 'Domain (e.g. users)')
  if (type === 'script') return inp('name', 'Script name')
  return null
}

function PrivilegesTab() {
  const qc = useQueryClient()
  const { data: privs = [], isLoading } = useQuery<Privilege[]>({
    queryKey: ['privileges'],
    queryFn: () => nexusApi.listPrivileges().then(r => r.data),
  })
  const [showModal, setShowModal] = useState(false)
  const [editing, setEditing] = useState<Privilege | null>(null)
  const [form, setForm] = useState<{ name: string; description: string; type: PrivType; attrs: Record<string, unknown> }>({
    name: '', description: '', type: 'wildcard', attrs: {},
  })

  function openCreate() {
    setEditing(null)
    setForm({ name: '', description: '', type: 'wildcard', attrs: {} })
    setShowModal(true)
  }

  function openEdit(p: Privilege) {
    setEditing(p)
    setForm({ name: p.name, description: p.description, type: p.type, attrs: { ...p.attrs } })
    setShowModal(true)
  }

  const save = useMutation({
    mutationFn: async () => {
      const payload = { name: form.name, description: form.description, type: form.type, attrs: form.attrs }
      if (editing) return nexusApi.updatePrivilege(editing.id, payload)
      return nexusApi.createPrivilege(payload)
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['privileges'] }); setShowModal(false) },
  })

  const del = useMutation({
    mutationFn: (id: string) => nexusApi.deletePrivilege(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['privileges'] }),
  })

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <button style={S.btn('primary')} onClick={openCreate}><Plus size={14} /> New Privilege</button>
      </div>

      {isLoading ? <div style={S.empty}>Loading…</div> : privs.length === 0 ? <div style={S.empty}>No privileges</div> : (
        <div style={S.card}>
          <table style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
            <thead>
              <tr style={{ color: 'rgba(229,231,235,0.5)', textAlign: 'left' as const }}>
                <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Name</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Type</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Description</th>
                <th style={{ padding: '0 0 10px', fontWeight: 600, width: 80 }}></th>
              </tr>
            </thead>
            <tbody>
              {privs.map(p => (
                <tr key={p.id} style={{ borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                  <td style={{ padding: '9px 0', color: '#dbeafe', fontWeight: 600 }}>{p.name}</td>
                  <td style={{ padding: '9px 8px' }}>
                    <span style={S.badge(PRIV_TYPE_COLOR[p.type] ?? '#6b7280')}>{p.type}</span>
                    {p.readOnly && <span style={{ ...S.badge('#6b7280'), marginLeft: 4 }}>built-in</span>}
                  </td>
                  <td style={{ padding: '9px 8px', color: 'rgba(229,231,235,0.55)' }}>{p.description || '—'}</td>
                  <td style={{ padding: '9px 0', display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                    {!p.readOnly && (
                      <>
                        <button style={{ ...S.btn('ghost'), padding: '4px 8px' }} onClick={() => openEdit(p)}>Edit</button>
                        <button style={{ ...S.btn('danger'), padding: '4px 8px' }} onClick={() => { if (confirm(`Delete ${p.name}?`)) del.mutate(p.id) }}><Trash2 size={13} /></button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showModal && (
        <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
          <div style={{ background: '#0f172a', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 14, padding: 24, width: 480, display: 'flex', flexDirection: 'column', gap: 12 }}>
            <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: '#dbeafe' }}>{editing ? 'Edit Privilege' : 'New Privilege'}</h3>
            <input style={S.input} placeholder="Name" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
            <input style={S.input} placeholder="Description" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />
            <select
              style={{ ...S.input }}
              value={form.type}
              onChange={e => setForm(f => ({ ...f, type: e.target.value as PrivType, attrs: {} }))}
            >
              {PRIV_TYPES.map(t => <option key={t} value={t}>{t}</option>)}
            </select>
            <PrivilegeAttrFields
              type={form.type}
              attrs={form.attrs}
              onChange={(k, v) => setForm(f => ({ ...f, attrs: { ...f.attrs, [k]: v } }))}
            />
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 4 }}>
              <button style={S.btn('ghost')} onClick={() => setShowModal(false)}>Cancel</button>
              <button style={S.btn('primary')} onClick={() => save.mutate()} disabled={save.isPending || !form.name.trim()}>
                {save.isPending ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function ContentSelectorsTab() {
  const qc = useQueryClient()
  const { data: selectors = [], isLoading } = useQuery<{ id: string; name: string; description: string; expression: string }[]>({
    queryKey: ['content-selectors'],
    queryFn: () => nexusApi.listContentSelectors().then(r => r.data),
  })
  const [showModal, setShowModal] = useState(false)
  const [editing, setEditing] = useState<{ id: string; name: string; description: string; expression: string } | null>(null)
  const [form, setForm] = useState({ name: '', description: '', expression: '' })
  const [saveError, setSaveError] = useState('')

  function openCreate() {
    setEditing(null)
    setForm({ name: '', description: '', expression: 'format == "maven2"' })
    setSaveError('')
    setShowModal(true)
  }

  function openEdit(s: { id: string; name: string; description: string; expression: string }) {
    setEditing(s)
    setForm({ name: s.name, description: s.description, expression: s.expression })
    setSaveError('')
    setShowModal(true)
  }

  const save = useMutation({
    mutationFn: async () => {
      if (editing) return nexusApi.updateContentSelector(editing.id, form)
      return nexusApi.createContentSelector(form)
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

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <button style={S.btn('primary')} onClick={openCreate}><Plus size={14} /> New Selector</button>
      </div>

      {isLoading ? <div style={S.empty}>Loading…</div> : selectors.length === 0 ? <div style={S.empty}>No content selectors</div> : (
        <div style={S.card}>
          <table style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
            <thead>
              <tr style={{ color: 'rgba(229,231,235,0.5)', textAlign: 'left' as const }}>
                <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Name</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Expression</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Description</th>
                <th style={{ padding: '0 0 10px', width: 80 }}></th>
              </tr>
            </thead>
            <tbody>
              {selectors.map(s => (
                <tr key={s.id} style={{ borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                  <td style={{ padding: '9px 0', color: '#dbeafe', fontWeight: 600 }}>{s.name}</td>
                  <td style={{ padding: '9px 8px' }}>
                    <code style={{ ...S.mono, fontSize: 12, color: '#a5b4fc' }}>{s.expression}</code>
                  </td>
                  <td style={{ padding: '9px 8px', color: 'rgba(229,231,235,0.55)' }}>{s.description || '—'}</td>
                  <td style={{ padding: '9px 0', display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                    <button style={{ ...S.btn('ghost'), padding: '4px 8px' }} onClick={() => openEdit(s)}>Edit</button>
                    <button style={{ ...S.btn('danger'), padding: '4px 8px' }} onClick={() => { if (confirm(`Delete ${s.name}?`)) del.mutate(s.id) }}><Trash2 size={13} /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showModal && (
        <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
          <div style={{ background: '#0f172a', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 14, padding: 24, width: 520, display: 'flex', flexDirection: 'column', gap: 12 }}>
            <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: '#dbeafe' }}>{editing ? 'Edit Content Selector' : 'New Content Selector'}</h3>
            <input style={S.input} placeholder="Name" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
            <input style={S.input} placeholder="Description (optional)" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />
            <div>
              <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', marginBottom: 4 }}>CEL Expression — variables: <code>format</code>, <code>path</code>, <code>repository</code></div>
              <textarea
                style={{ ...S.input, fontFamily: 'monospace', fontSize: 12, height: 80, resize: 'vertical' as const }}
                placeholder='format == "maven2" && path.startsWith("/com/acme")'
                value={form.expression}
                onChange={e => setForm(f => ({ ...f, expression: e.target.value }))}
              />
            </div>
            {saveError && (
              <div style={{ padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, color: '#ef4444', fontSize: 12 }}>{saveError}</div>
            )}
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <button style={S.btn('ghost')} onClick={() => setShowModal(false)}>Cancel</button>
              <button style={S.btn('primary')} onClick={() => save.mutate()} disabled={save.isPending || !form.name.trim() || !form.expression.trim()}>
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
type Tab = 'roles' | 'privileges' | 'selectors' | 'scan' | 'tokens' | 'webhooks'

export default function SecurityPage() {
  const [tab, setTab] = useState<Tab>('roles')
  const { data: roles = [], isLoading, refetch } = useQuery<Role[]>({
    queryKey: ['roles'],
    queryFn: () => nexusApi.listRoles().then(r => r.data),
  })

  return (
    <div style={S.page}>
      <div style={S.header}>
        <div>
          <h1 style={S.title}>Security</h1>
          <p style={S.subtitle}>Roles, vulnerability scanning, API tokens and webhooks</p>
        </div>
        {tab === 'roles' && <button style={{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: 8, color: 'rgba(229,231,235,0.7)', cursor: 'pointer', display: 'flex', alignItems: 'center' }} onClick={() => refetch()}><RefreshCw size={16} /></button>}
      </div>

      <div style={S.tabs}>
        {([['roles', 'Roles'], ['privileges', 'Privileges'], ['selectors', 'Content Selectors'], ['scan', 'CVE Scan'], ['tokens', 'API Tokens'], ['webhooks', 'Webhooks']] as [Tab, string][]).map(([id, label]) => (
          <button key={id} style={S.tab(tab === id)} onClick={() => setTab(id)}>{label}</button>
        ))}
      </div>

      {tab === 'roles'      && <RolesTab roles={roles} loading={isLoading} />}
      {tab === 'privileges' && <PrivilegesTab />}
      {tab === 'selectors'  && <ContentSelectorsTab />}
      {tab === 'scan'       && <ScanTab />}
      {tab === 'tokens'     && <TokensTab />}
      {tab === 'webhooks'   && <WebhooksTab />}
    </div>
  )
}
