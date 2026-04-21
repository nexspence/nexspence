import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Trash2, RefreshCw, Plus, Play, Pencil, X, Check, Clock, AlertCircle } from 'lucide-react'
import { nexusApi } from '@/api/client'
import { Select } from '../components/Select'

interface CleanupPolicy {
  id: string
  name: string
  description?: string
  format: string
  criteria: Record<string, number | string>
  scheduleCron?: string
  enabled: boolean
  dryRun: boolean
  lastRunAt?: string
  lastRunFreedBytes?: number
  lastRunCount?: number
}

interface PolicyForm {
  name: string
  description: string
  format: string
  enabled: boolean
  dryRun: boolean
  lastDownloadedDays: string
  artifactAgeDays: string
  pathPrefix: string
  nameGlob: string
  scheduleCron: string
}

const FORMATS = ['*', 'maven2', 'npm', 'docker', 'pypi', 'go', 'nuget', 'helm', 'raw', 'apt', 'yum', 'cargo', 'conan']

const FORMAT_COLOR: Record<string, string> = {
  maven2: '#f97316', npm: '#ef4444', docker: '#3b82f6', pypi: '#a78bfa',
  go: '#06b6d4', nuget: '#8b5cf6', helm: '#0ea5e9', raw: '#6b7280',
  apt: '#f59e0b', yum: '#10b981', cargo: '#fb923c', conan: '#94a3b8',
  '*': '#6b7280',
}

const emptyForm = (): PolicyForm => ({
  name: '', description: '', format: '*',
  enabled: true, dryRun: false,
  lastDownloadedDays: '', artifactAgeDays: '',
  pathPrefix: '', nameGlob: '',
  scheduleCron: '',
})

function fmtBytes(b: number) {
  if (b >= 1e9) return (b / 1e9).toFixed(1) + ' GB'
  if (b >= 1e6) return (b / 1e6).toFixed(1) + ' MB'
  if (b >= 1e3) return (b / 1e3).toFixed(1) + ' KB'
  return b + ' B'
}

const S = {
  page:    { padding: 24, display: 'flex', flexDirection: 'column' as const, gap: 20 },
  header:  { display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', flexWrap: 'wrap' as const, gap: 12 },
  title:   { fontSize: 20, fontWeight: 700, color: '#dbeafe', margin: '0 0 4px' },
  subtitle:{ fontSize: 13, color: 'rgba(229,231,235,0.5)', margin: 0 },
  actions: { display: 'flex', gap: 10 },
  iconBtn: { background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: 8, color: 'rgba(229,231,235,0.7)', cursor: 'pointer', display: 'flex', alignItems: 'center' },
  primaryBtn: { display: 'flex', alignItems: 'center', gap: 6, background: '#3b82f6', border: 'none', borderRadius: 8, padding: '8px 14px', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' },
  grid:    { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(320px, 1fr))', gap: 16 },
  card:    { background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 12, padding: '18px 20px', display: 'flex', flexDirection: 'column' as const, gap: 12 },
  cardTop: { display: 'flex', alignItems: 'center', gap: 8 },
  cardName:{ flex: 1, fontSize: 14, fontWeight: 600, color: '#dbeafe', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const },
  badge:   (color: string) => ({ fontSize: 11, fontWeight: 600 as const, padding: '2px 8px', borderRadius: 4, background: color + '22', color }),
  criteria:{ display: 'flex', flexDirection: 'column' as const, gap: 5 },
  criterion:{ display: 'flex', gap: 8, fontSize: 12, color: 'rgba(229,231,235,0.7)' },
  criterionKey:{ color: 'rgba(229,231,235,0.4)', minWidth: 130 },
  criterionVal:{ color: '#93c5fd', fontFamily: 'monospace' as const },
  lastRun: { fontSize: 11, color: 'rgba(229,231,235,0.35)', display: 'flex', gap: 12 },
  cardActions:{ display: 'flex', gap: 8, marginTop: 4 },
  smBtn:   (danger?: boolean) => ({
    display: 'flex', alignItems: 'center', gap: 4, fontSize: 12,
    padding: '5px 10px', borderRadius: 6, cursor: 'pointer', fontWeight: 500,
    background: danger ? 'rgba(239,68,68,0.12)' : 'rgba(255,255,255,0.06)',
    border: `1px solid ${danger ? 'rgba(239,68,68,0.25)' : 'rgba(255,255,255,0.1)'}`,
    color: danger ? '#ef4444' : 'rgba(229,231,235,0.7)',
  }),
  runBtn: {
    display: 'flex', alignItems: 'center', gap: 4, fontSize: 12,
    padding: '5px 10px', borderRadius: 6, cursor: 'pointer', fontWeight: 500,
    background: 'rgba(34,197,94,0.12)', border: '1px solid rgba(34,197,94,0.25)', color: '#22c55e',
  },
  empty:   { display: 'flex', flexDirection: 'column' as const, alignItems: 'center', justifyContent: 'center', gap: 12, color: 'rgba(229,231,235,0.35)', fontSize: 14, paddingTop: 48 },
  // Modal
  overlay: { position: 'fixed' as const, inset: 0, background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 },
  modal:   { background: '#0d1526', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 16, padding: 28, width: 480, maxWidth: '95vw', display: 'flex', flexDirection: 'column' as const, gap: 20 },
  modalTitle:{ fontSize: 17, fontWeight: 700, color: '#dbeafe' },
  field:   { display: 'flex', flexDirection: 'column' as const, gap: 6 },
  label:   { fontSize: 12, fontWeight: 600, color: 'rgba(229,231,235,0.55)', textTransform: 'uppercase' as const, letterSpacing: '0.04em' },
  input:   { background: 'rgba(255,255,255,0.05)', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 8, padding: '8px 12px', color: '#e5e7eb', fontSize: 13, outline: 'none', width: '100%', boxSizing: 'border-box' as const },
  row2:    { display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 },
  toggle:  { display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'rgba(229,231,235,0.7)', cursor: 'pointer' },
  modalFooter:{ display: 'flex', gap: 10, justifyContent: 'flex-end' },
  cancelBtn:{ background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: '8px 16px', color: 'rgba(229,231,235,0.7)', fontSize: 13, cursor: 'pointer' },
  saveBtn: { background: '#3b82f6', border: 'none', borderRadius: 8, padding: '8px 20px', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' },
  error:   { fontSize: 12, color: '#ef4444', display: 'flex', gap: 6, alignItems: 'center' },
}

function PolicyModal({
  initial, onClose, onSaved,
}: { initial?: CleanupPolicy | null; onClose: () => void; onSaved: () => void }) {
  const [form, setForm] = useState<PolicyForm>(() => {
    if (!initial) return emptyForm()
    return {
      name: initial.name,
      description: initial.description ?? '',
      format: initial.format,
      enabled: initial.enabled,
      dryRun: initial.dryRun,
      lastDownloadedDays: String(initial.criteria?.lastDownloadedDays ?? ''),
      artifactAgeDays: String(initial.criteria?.artifactAgeDays ?? ''),
      pathPrefix: String(initial.criteria?.pathPrefix ?? ''),
      nameGlob: String(initial.criteria?.nameGlob ?? ''),
      scheduleCron: initial.scheduleCron ?? '',
    }
  })
  const [err, setErr] = useState('')

  const payload = () => ({
    name: form.name.trim(),
    description: form.description.trim(),
    format: form.format,
    enabled: form.enabled,
    dryRun: form.dryRun,
    scheduleCron: form.scheduleCron.trim(),
    criteria: {
      ...(form.lastDownloadedDays ? { lastDownloadedDays: Number(form.lastDownloadedDays) } : {}),
      ...(form.artifactAgeDays ? { artifactAgeDays: Number(form.artifactAgeDays) } : {}),
      ...(form.pathPrefix.trim() ? { pathPrefix: form.pathPrefix.trim() } : {}),
      ...(form.nameGlob.trim() ? { nameGlob: form.nameGlob.trim() } : {}),
    },
  })

  const handleSave = async () => {
    if (!form.name.trim()) { setErr('Name is required'); return }
    try {
      if (initial) {
        await nexusApi.updateCleanupPolicy(initial.id, payload())
      } else {
        await nexusApi.createCleanupPolicy(payload())
      }
      onSaved()
      onClose()
    } catch (e: any) {
      setErr(e?.response?.data?.error ?? 'Save failed')
    }
  }

  const set = (k: keyof PolicyForm) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setForm(f => ({ ...f, [k]: e.target.value }))

  return (
    <div style={S.overlay} onClick={e => e.target === e.currentTarget && onClose()}>
      <div style={S.modal}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span style={S.modalTitle}>{initial ? 'Edit Policy' : 'New Cleanup Policy'}</span>
          <button style={{ ...S.iconBtn, padding: 6 }} onClick={onClose}><X size={15} /></button>
        </div>

        <div style={S.field}>
          <label style={S.label}>Name *</label>
          <input style={S.input} value={form.name} onChange={set('name')} placeholder="e.g. delete-old-snapshots" />
        </div>

        <div style={S.field}>
          <label style={S.label}>Description</label>
          <input style={S.input} value={form.description} onChange={set('description')} placeholder="Optional description" />
        </div>

        <div style={S.row2}>
          <div style={S.field}>
            <label style={S.label}>Format</label>
            <Select
              options={FORMATS.map(f => ({ value: f, label: f === '*' ? 'All formats' : f }))}
              value={form.format}
              onChange={v => setForm(f => ({ ...f, format: v }))}
            />
          </div>
          <div style={S.field}>
            <label style={S.label}>Options</label>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, paddingTop: 4 }}>
              <label style={S.toggle}>
                <input type="checkbox" checked={form.enabled}
                  onChange={e => setForm(f => ({ ...f, enabled: e.target.checked }))} />
                Enabled
              </label>
              <label style={S.toggle}>
                <input type="checkbox" checked={form.dryRun}
                  onChange={e => setForm(f => ({ ...f, dryRun: e.target.checked }))} />
                Dry run (no deletes)
              </label>
            </div>
          </div>
        </div>

        <div style={S.field}>
          <label style={S.label}>Schedule (cron)</label>
          <input style={S.input} value={form.scheduleCron} onChange={set('scheduleCron')}
            placeholder="e.g. 0 2 * * * (default: every 6 hours)" />
          <span style={{ fontSize: 11, color: 'rgba(229,231,235,0.35)' }}>
            Leave blank to use the global default schedule. Format: minute hour day month weekday
          </span>
        </div>

        <div style={S.row2}>
          <div style={S.field}>
            <label style={S.label}>Not downloaded for (days)</label>
            <input style={S.input} type="number" min="1" value={form.lastDownloadedDays}
              onChange={set('lastDownloadedDays')} placeholder="e.g. 30" />
          </div>
          <div style={S.field}>
            <label style={S.label}>Artifact age (days)</label>
            <input style={S.input} type="number" min="1" value={form.artifactAgeDays}
              onChange={set('artifactAgeDays')} placeholder="e.g. 90" />
          </div>
        </div>

        <div style={S.row2}>
          <div style={S.field}>
            <label style={S.label}>Path prefix</label>
            <input style={S.input} value={form.pathPrefix}
              onChange={set('pathPrefix')} placeholder="e.g. com/example/" />
          </div>
          <div style={S.field}>
            <label style={S.label}>Name glob</label>
            <input style={S.input} value={form.nameGlob}
              onChange={set('nameGlob')} placeholder="e.g. *.jar or *-SNAPSHOT*" />
          </div>
        </div>

        {err && <div style={S.error}><AlertCircle size={13} />{err}</div>}

        <div style={S.modalFooter}>
          <button style={S.cancelBtn} onClick={onClose}>Cancel</button>
          <button style={S.saveBtn} onClick={handleSave}>
            <Check size={14} style={{ marginRight: 4 }} />
            {initial ? 'Save changes' : 'Create policy'}
          </button>
        </div>
      </div>
    </div>
  )
}

export default function CleanupPage() {
  const qc = useQueryClient()
  const [modal, setModal] = useState<'create' | CleanupPolicy | null>(null)
  const [running, setRunning] = useState<string | null>(null)

  const { data: policies = [], isLoading, refetch } = useQuery<CleanupPolicy[]>({
    queryKey: ['cleanupPolicies'],
    queryFn: () => nexusApi.listCleanupPolicies().then(r => r.data),
  })

  const deleteMut = useMutation({
    mutationFn: (id: string) => nexusApi.deleteCleanupPolicy(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['cleanupPolicies'] }),
  })

  const handleRun = async (id: string) => {
    setRunning(id)
    try {
      await nexusApi.runCleanupPolicy(id)
      setTimeout(() => { refetch(); setRunning(null) }, 1500)
    } catch { setRunning(null) }
  }

  return (
    <div style={S.page}>
      {(modal !== null) && (
        <PolicyModal
          initial={modal === 'create' ? null : modal}
          onClose={() => setModal(null)}
          onSaved={() => qc.invalidateQueries({ queryKey: ['cleanupPolicies'] })}
        />
      )}

      <div style={S.header}>
        <div>
          <h1 style={S.title}>Cleanup Policies</h1>
          <p style={S.subtitle}>
            Automate deletion of old, unused artifacts by criteria. Attach each policy to one or more
            repositories under Repositories → repository settings — unattached policies do not delete anything.
          </p>
        </div>
        <div style={S.actions}>
          <button style={S.iconBtn} onClick={() => refetch()} title="Refresh"><RefreshCw size={16} /></button>
          <button style={S.primaryBtn} onClick={() => setModal('create')}><Plus size={15} /> New Policy</button>
        </div>
      </div>

      {isLoading ? (
        <div style={S.empty}>Loading…</div>
      ) : policies.length === 0 ? (
        <div style={S.empty}>
          <Trash2 size={40} style={{ opacity: 0.3 }} />
          <p>No cleanup policies configured</p>
          <button style={S.primaryBtn} onClick={() => setModal('create')}><Plus size={14} /> Create first policy</button>
        </div>
      ) : (
        <div style={S.grid}>
          {policies.map(p => {
            const color = FORMAT_COLOR[p.format] ?? '#6b7280'
            const criteria = [
              p.criteria?.lastDownloadedDays && { k: 'Not downloaded for', v: `${p.criteria.lastDownloadedDays} days` },
              p.criteria?.artifactAgeDays && { k: 'Artifact age >', v: `${p.criteria.artifactAgeDays} days` },
            ].filter(Boolean) as { k: string; v: string }[]

            return (
              <div key={p.id} style={{ ...S.card, opacity: p.enabled ? 1 : 0.6 }}>
                <div style={S.cardTop}>
                  <Clock size={14} style={{ color: 'rgba(229,231,235,0.4)', flexShrink: 0 }} />
                  <span style={S.cardName} title={p.name}>{p.name}</span>
                  <span style={S.badge(color)}>{p.format === '*' ? 'all' : p.format}</span>
                  {p.dryRun && <span style={S.badge('#f59e0b')}>dry-run</span>}
                  {!p.enabled && <span style={S.badge('#6b7280')}>disabled</span>}
                </div>

                {p.description && (
                  <p style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', margin: 0 }}>{p.description}</p>
                )}

                {criteria.length > 0 && (
                  <div style={S.criteria}>
                    {criteria.map(c => (
                      <div key={c.k} style={S.criterion}>
                        <span style={S.criterionKey}>{c.k}</span>
                        <span style={S.criterionVal}>{c.v}</span>
                      </div>
                    ))}
                  </div>
                )}

                <div style={S.lastRun}>
                  {p.scheduleCron
                    ? <span style={{ color: '#93c5fd' }}>Schedule: {p.scheduleCron}</span>
                    : <span>Schedule: global default</span>}
                </div>

                {(p.lastRunAt || p.lastRunCount != null) && (
                  <div style={S.lastRun}>
                    {p.lastRunAt && <span>Last run: {new Date(p.lastRunAt).toLocaleString()}</span>}
                    {p.lastRunCount != null && <span>Deleted: {p.lastRunCount}</span>}
                    {p.lastRunFreedBytes != null && <span>Freed: {fmtBytes(p.lastRunFreedBytes)}</span>}
                  </div>
                )}

                <div style={S.cardActions}>
                  <button style={S.runBtn}
                    onClick={() => handleRun(p.id)}
                    disabled={running === p.id}>
                    <Play size={11} />
                    {running === p.id ? 'Running…' : 'Run now'}
                  </button>
                  <button style={S.smBtn()} onClick={() => setModal(p)}>
                    <Pencil size={11} /> Edit
                  </button>
                  <button style={S.smBtn(true)}
                    onClick={() => window.confirm(`Delete policy "${p.name}"?`) && deleteMut.mutate(p.id)}>
                    <Trash2 size={11} /> Delete
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
