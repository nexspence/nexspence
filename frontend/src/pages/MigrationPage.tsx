import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ArrowRightLeft, Play, Pause, RefreshCw, Plus } from 'lucide-react'
import { nexspenceApi } from '@/api/client'
import { HoloCard, HoloButton, HoloPill, HoloInput, HoloModal, HoloText } from '@/components/holo'

interface MigrationJob {
  id: string
  status: 'pending' | 'running' | 'paused' | 'completed' | 'failed'
  sourceUrl: string
  repositoriesTotal: number
  repositoriesDone: number
  assetsTotal: number
  assetsDone: number
  errorCount: number
  createdAt: string
  updatedAt: string
}

const STATUS_STYLE: Record<string, { bg: string; color: string }> = {
  pending:   { bg: 'rgba(245,158,11,0.15)',  color: '#f59e0b' },
  running:   { bg: 'rgba(59,130,246,0.15)',  color: '#3b82f6' },
  paused:    { bg: 'rgba(107,114,128,0.15)', color: '#9ca3af' },
  completed: { bg: 'rgba(34,197,94,0.15)',   color: '#22c55e' },
  failed:    { bg: 'rgba(239,68,68,0.15)',   color: '#ef4444' },
}

export default function MigrationPage() {
  const qc = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)

  const { data: jobs = [], isLoading, refetch } = useQuery<MigrationJob[]>({
    queryKey: ['migrationJobs'],
    queryFn: () => nexspenceApi.listMigrationJobs().then(r => r.data),
    refetchInterval: (q) => {
      const list = q.state.data as MigrationJob[] | undefined
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

  return (
    <div style={{ padding: 24, display: 'flex', flexDirection: 'column', gap: 24 }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 16 }}>
        <div>
          <div className="holo-section-label" style={{ marginBottom: 6 }}>ADMINISTRATION / MIGRATION</div>
          <h1 style={{ fontSize: 40, fontWeight: 700, margin: '0 0 4px', letterSpacing: '-0.04em', lineHeight: 1 }}>
            <HoloText>Migration from Nexus</HoloText>
          </h1>
          <p style={{ fontSize: 13, color: 'var(--holo-text-dim)', margin: 0 }}>
            Import repositories, users, and artifacts from a live Nexus OSS instance
          </p>
        </div>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
          <HoloButton onClick={() => refetch()} title="Refresh"><RefreshCw size={16} /></HoloButton>
          <HoloButton variant="primary" onClick={() => setShowCreate(true)}><Plus size={16} /> New Migration</HoloButton>
        </div>
      </div>

      <div style={{ background: 'rgba(124,92,255,0.08)', border: '1px solid rgba(124,92,255,0.2)', borderRadius: 10, padding: '12px 16px', fontSize: 13, color: 'rgba(180,160,255,0.9)', lineHeight: 1.6 }}>
        <strong>How it works:</strong> Nexspence connects to your Nexus instance via its REST API and
        streams repositories, users, roles and all artifacts directly — no downtime required.
        Jobs are pausable and resumable. Requires Nexus admin credentials.
      </div>

      {isLoading ? (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12, color: 'var(--holo-text-faint)', fontSize: 14, padding: '48px 0' }}>Loading…</div>
      ) : jobs.length === 0 ? (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12, color: 'var(--holo-text-faint)', fontSize: 14, padding: '48px 0' }}>
          <ArrowRightLeft size={40} style={{ opacity: 0.3 }} />
          <p>No migration jobs yet</p>
          <HoloButton variant="primary" onClick={() => setShowCreate(true)}><Plus size={14} /> Start Migration</HoloButton>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {jobs.map(job => {
            const reposPct = job.repositoriesTotal ? Math.round((job.repositoriesDone / job.repositoriesTotal) * 100) : 0
            const assetsPct = job.assetsTotal ? Math.round((job.assetsDone / job.assetsTotal) * 100) : 0
            return (
              <HoloCard key={job.id} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <ArrowRightLeft size={15} style={{ color: 'var(--holo-text-faint)', flexShrink: 0 }} />
                  <span style={{ flex: 1, fontSize: 14, fontWeight: 600, color: 'var(--holo-text)', fontFamily: 'monospace', wordBreak: 'break-all' }}>{job.sourceUrl}</span>
                  <HoloPill style={{ background: STATUS_STYLE[job.status]?.bg ?? STATUS_STYLE.pending.bg, color: STATUS_STYLE[job.status]?.color ?? STATUS_STYLE.pending.color, fontSize: 11, fontWeight: 600 }}>{job.status}</HoloPill>
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
                  <span style={{ fontSize: 12, color: 'var(--holo-text-faint)' }}>Started {new Date(job.createdAt).toLocaleString()}</span>
                  <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                    {job.status === 'running' && (
                      <HoloButton onClick={() => pauseMut.mutate(job.id)}><Pause size={12} /> Pause</HoloButton>
                    )}
                    {job.status === 'paused' && (
                      <HoloButton onClick={() => resumeMut.mutate(job.id)}><Play size={12} /> Resume</HoloButton>
                    )}
                  </div>
                </div>
              </HoloCard>
            )
          })}
        </div>
      )}

      {showCreate && (
        <CreateMigrationModal
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

function CreateMigrationModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [form, setForm] = useState({ sourceUrl: '', username: 'admin', password: '', concurrency: '4' })
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm(f => ({ ...f, [k]: e.target.value }))

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await nexspenceApi.createMigrationJob({
        sourceUrl: form.sourceUrl,
        credentials: { username: form.username, password: form.password },
        options: { concurrency: parseInt(form.concurrency) || 4 },
      })
      onCreated()
    } catch (err: any) {
      setError(err.response?.data?.error ?? 'Failed to create migration job')
    } finally {
      setLoading(false)
    }
  }

  return (
    <HoloModal open={true} onClose={onClose}>
      <h2 style={{ fontSize: 18, fontWeight: 700, color: 'var(--holo-text)', margin: '0 0 20px' }}>New Migration Job</h2>
      <form style={{ display: 'flex', flexDirection: 'column', gap: 14 }} onSubmit={handleSubmit}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Nexus URL *</label>
          <HoloInput placeholder="https://nexus.example.com" value={form.sourceUrl} onChange={set('sourceUrl')} required />
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
            <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Username</label>
            <HoloInput value={form.username} onChange={set('username')} />
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
            <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Password *</label>
            <HoloInput type="password" value={form.password} onChange={set('password')} required />
          </div>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Concurrency</label>
          <HoloInput type="number" min={1} max={16} value={form.concurrency} onChange={set('concurrency')} />
        </div>
        {error && <div style={{ background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.25)', borderRadius: 8, padding: '10px 12px', color: '#fca5a5', fontSize: 13 }}>{error}</div>}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10, marginTop: 8 }}>
          <HoloButton type="button" onClick={onClose}>Cancel</HoloButton>
          <HoloButton type="submit" variant="primary" disabled={loading}>{loading ? 'Starting…' : 'Start Migration'}</HoloButton>
        </div>
      </form>
    </HoloModal>
  )
}
