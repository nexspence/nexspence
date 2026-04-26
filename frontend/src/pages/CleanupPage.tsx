import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Trash2, RefreshCw, Plus, Play, Pencil, X, Check, AlertCircle } from 'lucide-react'
import { nexusApi } from '@/api/client'
import { Select } from '../components/Select'
import { HoloButton, HoloInput, HoloModal, HoloPill, Wizard } from '@/components/holo'

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
  const [wizardError, setWizardError] = useState('')
  const [wizardLoading, setWizardLoading] = useState(false)

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

  // ── Create mode: stepped wizard ───────────────────────────────────────
  if (!initial) {
    const LABEL = { fontSize: 12, fontWeight: 600 as const, color: 'var(--holo-text-dim)', textTransform: 'uppercase' as const, letterSpacing: '0.04em' }

    const validateStep = (stepIdx: number): boolean => {
      setWizardError('')
      if (stepIdx === 0 && !form.name.trim()) {
        setWizardError('Name is required')
        return false
      }
      return true
    }

    const handleFinish = async () => {
      setWizardError('')
      if (!form.name.trim()) { setWizardError('Name is required'); return }
      setWizardLoading(true)
      try {
        await nexusApi.createCleanupPolicy(payload())
        onSaved()
        onClose()
      } catch (e: any) {
        setWizardError(e?.response?.data?.error ?? 'Save failed')
      } finally {
        setWizardLoading(false)
      }
    }

    const wizStep1 = (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Name *</label>
          <HoloInput value={form.name} onChange={set('name')} placeholder="e.g. delete-old-snapshots" autoFocus />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Description</label>
          <HoloInput value={form.description} onChange={set('description')} placeholder="Optional description" />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Format</label>
          <Select
            options={FORMATS.map(f => ({ value: f, label: f === '*' ? 'All formats' : f }))}
            value={form.format}
            onChange={v => setForm(f => ({ ...f, format: v }))}
          />
        </div>
      </div>
    )

    const wizStep2 = (
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Not downloaded for (days)</label>
          <HoloInput type="number" min="1" value={form.lastDownloadedDays} onChange={set('lastDownloadedDays')} placeholder="e.g. 30" />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Artifact age (days)</label>
          <HoloInput type="number" min="1" value={form.artifactAgeDays} onChange={set('artifactAgeDays')} placeholder="e.g. 90" />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Path prefix</label>
          <HoloInput value={form.pathPrefix} onChange={set('pathPrefix')} placeholder="e.g. /releases/" />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Name glob</label>
          <HoloInput value={form.nameGlob} onChange={set('nameGlob')} placeholder="e.g. *-SNAPSHOT*" />
        </div>
      </div>
    )

    const wizStep3 = (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Schedule (cron)</label>
          <HoloInput value={form.scheduleCron} onChange={set('scheduleCron')} placeholder="e.g. 0 2 * * * (default: every 6 hours)" />
          <span style={{ fontSize: 11, color: 'rgba(229,231,235,0.35)' }}>Leave blank to use the global default. Format: minute hour day month weekday</span>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <label style={LABEL}>Options</label>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text-dim)', cursor: 'pointer' }}>
            <input type="checkbox" checked={form.enabled} onChange={e => setForm(f => ({ ...f, enabled: e.target.checked }))} />
            Enabled
          </label>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text-dim)', cursor: 'pointer' }}>
            <input type="checkbox" checked={form.dryRun} onChange={e => setForm(f => ({ ...f, dryRun: e.target.checked }))} />
            Dry run (no deletes)
          </label>
        </div>
      </div>
    )

    return (
      <Wizard
        steps={[
          { label: 'Идентификация', content: wizStep1 },
          { label: 'Критерии', content: wizStep2 },
          { label: 'Расписание', content: wizStep3 },
        ]}
        onFinish={handleFinish}
        finishLabel="Create Policy"
        onValidateStep={validateStep}
        onClose={onClose}
        loading={wizardLoading}
        error={wizardError}
      />
    )
  }

  return (
    <HoloModal open={true} onClose={onClose}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h2 style={{ fontSize: 17, fontWeight: 700, color: 'var(--holo-text)', margin: 0 }}>
          {initial ? 'Edit Policy' : 'New Cleanup Policy'}
        </h2>
        <HoloButton onClick={onClose} style={{ padding: 4 }}><X size={15} /></HoloButton>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Name *</label>
        <HoloInput value={form.name} onChange={set('name')} placeholder="e.g. delete-old-snapshots" />
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Description</label>
        <HoloInput value={form.description} onChange={set('description')} placeholder="Optional description" />
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Format</label>
          <Select
            options={FORMATS.map(f => ({ value: f, label: f === '*' ? 'All formats' : f }))}
            value={form.format}
            onChange={v => setForm(f => ({ ...f, format: v }))}
          />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Options</label>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, paddingTop: 4 }}>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text-dim)', cursor: 'pointer' }}>
              <input type="checkbox" checked={form.enabled}
                onChange={e => setForm(f => ({ ...f, enabled: e.target.checked }))} />
              Enabled
            </label>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text-dim)', cursor: 'pointer' }}>
              <input type="checkbox" checked={form.dryRun}
                onChange={e => setForm(f => ({ ...f, dryRun: e.target.checked }))} />
              Dry run (no deletes)
            </label>
          </div>
        </div>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Schedule (cron)</label>
        <HoloInput value={form.scheduleCron} onChange={set('scheduleCron')}
          placeholder="e.g. 0 2 * * * (default: every 6 hours)" />
        <span style={{ fontSize: 11, color: 'rgba(229,231,235,0.35)' }}>
          Leave blank to use the global default schedule. Format: minute hour day month weekday
        </span>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Not downloaded for (days)</label>
          <HoloInput type="number" min="1" value={form.lastDownloadedDays}
            onChange={set('lastDownloadedDays')} placeholder="e.g. 30" />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Artifact age (days)</label>
          <HoloInput type="number" min="1" value={form.artifactAgeDays}
            onChange={set('artifactAgeDays')} placeholder="e.g. 90" />
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Path prefix</label>
          <HoloInput value={form.pathPrefix}
            onChange={set('pathPrefix')} placeholder="e.g. com/example/" />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Name glob</label>
          <HoloInput value={form.nameGlob}
            onChange={set('nameGlob')} placeholder="e.g. *.jar or *-SNAPSHOT*" />
        </div>
      </div>

      {err && <div style={{ fontSize: 12, color: 'var(--holo-red)', display: 'flex', gap: 6, alignItems: 'center' }}><AlertCircle size={13} />{err}</div>}

      <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end' }}>
        <HoloButton onClick={onClose}>Cancel</HoloButton>
        <HoloButton variant="primary" onClick={handleSave} icon={<Check size={14} />}>{initial ? 'Save changes' : 'Create policy'}</HoloButton>
      </div>
    </HoloModal>
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
    <div style={{ padding: 24, display: 'flex', flexDirection: 'column', gap: 20 }}>
      {(modal !== null) && (
        <PolicyModal
          initial={modal === 'create' ? null : modal}
          onClose={() => setModal(null)}
          onSaved={() => qc.invalidateQueries({ queryKey: ['cleanupPolicies'] })}
        />
      )}

      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', flexWrap: 'wrap', gap: 12 }}>
        <div>
          <div className="holo-section-label" style={{ marginBottom: 4 }}>ADMINISTRATION / CLEANUP</div>
          <h1 style={{ fontSize: 20, fontWeight: 700, margin: '0 0 3px', letterSpacing: '-0.01em', lineHeight: 1.2, background: 'linear-gradient(110deg, #7c5cff, #22d3ee 60%)', WebkitBackgroundClip: 'text', WebkitTextFillColor: 'transparent', backgroundClip: 'text' as const }}>Cleanup Policies</h1>
          <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: 0, maxWidth: 560 }}>
            Automate deletion of old, unused artifacts by criteria. Attach each policy to one or more
            repositories under Repositories → repository settings — unattached policies do not delete anything.
          </p>
        </div>
        <div style={{ display: 'flex', gap: 10 }}>
          <HoloButton onClick={() => refetch()} title="Refresh"><RefreshCw size={16} /></HoloButton>
          <HoloButton variant="primary" icon={<Plus size={15} />} onClick={() => setModal('create')}>New Policy</HoloButton>
        </div>
      </div>

      {isLoading ? (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12, color: 'var(--holo-text-faint)', fontSize: 14, paddingTop: 48 }}>Loading…</div>
      ) : policies.length === 0 ? (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12, color: 'var(--holo-text-faint)', fontSize: 14, paddingTop: 48 }}>
          <Trash2 size={40} style={{ opacity: 0.3 }} />
          <p>No cleanup policies configured</p>
          <HoloButton variant="primary" icon={<Plus size={14} />} onClick={() => setModal('create')}>Create first policy</HoloButton>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          {policies.map(p => {
            const color = FORMAT_COLOR[p.format] ?? '#6b7280'
            const criteria = [
              p.criteria?.lastDownloadedDays && `≥${p.criteria.lastDownloadedDays}d not downloaded`,
              p.criteria?.artifactAgeDays && `age >${p.criteria.artifactAgeDays}d`,
              p.criteria?.pathPrefix && `path: ${p.criteria.pathPrefix}`,
              p.criteria?.nameGlob && `glob: ${p.criteria.nameGlob}`,
            ].filter(Boolean) as string[]

            return (
              <div
                key={p.id}
                style={{
                  display: 'grid',
                  gridTemplateColumns: '8px 88px 1fr auto auto',
                  alignItems: 'center',
                  gap: 14,
                  padding: '11px 16px',
                  background: 'rgba(10,8,28,0.97)',
                  border: '1px solid rgba(124,92,255,0.2)',
                  borderRadius: 10,
                  opacity: p.enabled ? 1 : 0.55,
                  transition: 'border-color 0.15s, background 0.15s',
                }}
                onMouseEnter={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'rgba(124,92,255,0.45)'; (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.04)' }}
                onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'rgba(124,92,255,0.2)'; (e.currentTarget as HTMLDivElement).style.background = 'rgba(10,8,28,0.97)' }}
              >
                {/* Status dot */}
                <span style={{
                  width: 7, height: 7, borderRadius: '50%', flexShrink: 0,
                  background: p.enabled ? 'var(--holo-green)' : 'rgba(255,255,255,0.2)',
                  boxShadow: p.enabled ? '0 0 5px var(--holo-green)' : 'none',
                  display: 'inline-block',
                }} />

                {/* Format badge */}
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                  <span style={{
                    fontSize: 10, fontWeight: 600, padding: '2px 8px', borderRadius: 4,
                    textTransform: 'uppercase', letterSpacing: '0.3px', whiteSpace: 'nowrap',
                    background: color + '22', color,
                  }}>
                    {p.format === '*' ? 'all' : p.format}
                  </span>
                  {p.dryRun && <HoloPill tone="warn" style={{ fontSize: 10 }}>dry</HoloPill>}
                </div>

                {/* Name + meta */}
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontWeight: 600, fontSize: 13, color: 'var(--holo-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {p.name}
                  </div>
                  <div style={{ display: 'flex', gap: 6, marginTop: 3, flexWrap: 'wrap', alignItems: 'center' }}>
                    {criteria.length > 0 ? criteria.map(c => (
                      <span key={c} style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: 'rgba(124,92,255,0.1)', color: 'var(--holo-a)', fontFamily: 'monospace', whiteSpace: 'nowrap' }}>{c}</span>
                    )) : (
                      <span style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>{p.description || 'No criteria'}</span>
                    )}
                    {p.description && criteria.length > 0 && (
                      <span style={{ fontSize: 11, color: 'var(--holo-text-faint)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.description}</span>
                    )}
                  </div>
                </div>

                {/* Last run + schedule */}
                <div style={{ textAlign: 'right', fontSize: 11, color: 'var(--holo-text-faint)', display: 'flex', flexDirection: 'column', gap: 2, minWidth: 120 }}>
                  {p.lastRunAt && (
                    <span>{new Date(p.lastRunAt).toLocaleDateString()}{p.lastRunCount != null ? ` · ${p.lastRunCount} del` : ''}{p.lastRunFreedBytes != null ? ` · ${fmtBytes(p.lastRunFreedBytes)}` : ''}</span>
                  )}
                  <span style={{ color: p.scheduleCron ? 'var(--holo-a)' : 'var(--holo-text-faint)', fontFamily: 'monospace', fontSize: 10 }}>
                    {p.scheduleCron || 'default schedule'}
                  </span>
                </div>

                {/* Actions */}
                <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                  <HoloButton
                    style={{ background: 'rgba(94,255,184,0.1)', border: '1px solid rgba(94,255,184,0.25)', color: 'var(--holo-green)', padding: '4px 8px' }}
                    icon={<Play size={12} />}
                    onClick={() => handleRun(p.id)}
                    disabled={running === p.id}
                    title="Run now"
                  >{running === p.id ? '…' : 'Run'}</HoloButton>
                  <HoloButton icon={<Pencil size={13} />} onClick={() => setModal(p)} title="Edit" style={{ padding: '4px 8px' }} />
                  <HoloButton variant="danger" icon={<Trash2 size={13} />} onClick={() => window.confirm(`Delete policy "${p.name}"?`) && deleteMut.mutate(p.id)} title="Delete" style={{ padding: '4px 8px' }} />
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
