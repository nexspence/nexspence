import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ArrowRightLeft, Play, Pause, RefreshCw, Plus } from 'lucide-react'
import { nexspenceApi } from '@/api/client'

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

const S = {
  page: { padding: 24, display: 'flex', flexDirection: 'column' as const, gap: 24 },
  header: { display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 16 },
  title: { fontSize: 20, fontWeight: 700, color: '#dbeafe', margin: '0 0 4px' },
  subtitle: { fontSize: 13, color: 'rgba(229,231,235,0.5)', margin: 0 },
  actions: { display: 'flex', gap: 10, alignItems: 'center' },
  createBtn: { background: '#3b82f6', border: 'none', borderRadius: 8, padding: '8px 16px', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6 },
  iconBtn: { background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: 8, color: 'rgba(229,231,235,0.7)', cursor: 'pointer', display: 'flex', alignItems: 'center' },
  card: { background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 14, padding: 20 },
  infoBox: { background: 'rgba(59,130,246,0.08)', border: '1px solid rgba(59,130,246,0.2)', borderRadius: 10, padding: '12px 16px', fontSize: 13, color: 'rgba(147,197,253,0.9)', lineHeight: 1.6 },
  empty: { display: 'flex', flexDirection: 'column' as const, alignItems: 'center', justifyContent: 'center', gap: 12, color: 'rgba(229,231,235,0.4)', fontSize: 14, padding: '48px 0' },
  jobCard: { background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 12, padding: '16px 20px', display: 'flex', flexDirection: 'column' as const, gap: 12 },
  jobTop: { display: 'flex', alignItems: 'center', gap: 10 },
  jobUrl: { flex: 1, fontSize: 14, fontWeight: 600, color: '#dbeafe', fontFamily: 'monospace' as const, wordBreak: 'break-all' as const },
  statusBadge: (s: string) => ({ ...(STATUS_STYLE[s] ?? STATUS_STYLE.pending), fontSize: 11, fontWeight: 600 as const, padding: '3px 9px', borderRadius: 6 }),
  jobStats: { display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 12 },
  stat: { background: 'rgba(255,255,255,0.02)', borderRadius: 8, padding: '10px 12px' },
  statLabel: { fontSize: 11, color: 'rgba(229,231,235,0.4)', marginBottom: 4, textTransform: 'uppercase' as const, letterSpacing: '0.04em' },
  statVal: { fontSize: 18, fontWeight: 700, color: '#dbeafe' },
  progress: { height: 4, borderRadius: 2, background: 'rgba(255,255,255,0.08)', overflow: 'hidden' as const },
  jobBtns: { display: 'flex', gap: 8, justifyContent: 'flex-end' as const },
  smallBtn: (danger?: boolean) => ({
    background: danger ? 'rgba(239,68,68,0.1)' : 'rgba(255,255,255,0.06)',
    border: `1px solid ${danger ? 'rgba(239,68,68,0.25)' : 'rgba(255,255,255,0.1)'}`,
    borderRadius: 7, padding: '6px 12px',
    color: danger ? '#fca5a5' : 'rgba(229,231,235,0.7)',
    fontSize: 12, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 5,
  }),
  muted: { fontSize: 12, color: 'rgba(229,231,235,0.4)' },
  modal: { position: 'fixed' as const, inset: 0, background: 'rgba(0,0,0,0.6)', backdropFilter: 'blur(4px)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 100 },
  modalBox: { background: '#0f1420', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 16, padding: 28, width: 460, maxWidth: '90vw' },
  modalTitle: { fontSize: 18, fontWeight: 700, color: '#dbeafe', margin: '0 0 20px' },
  form: { display: 'flex', flexDirection: 'column' as const, gap: 14 },
  field: { display: 'flex', flexDirection: 'column' as const, gap: 5 },
  label: { fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.5)', textTransform: 'uppercase' as const, letterSpacing: '0.04em' },
  input: { background: 'rgba(255,255,255,0.05)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: '9px 12px', color: '#e5e7eb', fontSize: 14, outline: 'none' },
  error: { background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.25)', borderRadius: 8, padding: '10px 12px', color: '#fca5a5', fontSize: 13 },
  modalFooter: { display: 'flex', justifyContent: 'flex-end' as const, gap: 10, marginTop: 8 },
  cancelBtn: { background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: '8px 18px', color: 'rgba(229,231,235,0.7)', fontSize: 13, cursor: 'pointer' },
  submitBtn: (disabled: boolean) => ({ background: '#3b82f6', border: 'none', borderRadius: 8, padding: '8px 18px', color: '#fff', fontSize: 13, fontWeight: 600 as const, cursor: disabled ? 'not-allowed' : 'pointer', opacity: disabled ? 0.6 : 1 }),
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
    <div style={S.page}>
      <div style={S.header}>
        <div>
          <h1 style={S.title}>Migration from Nexus</h1>
          <p style={S.subtitle}>Import repositories, users, and artifacts from a live Nexus OSS instance</p>
        </div>
        <div style={S.actions}>
          <button style={S.iconBtn} onClick={() => refetch()} title="Refresh">
            <RefreshCw size={16} />
          </button>
          <button style={S.createBtn} onClick={() => setShowCreate(true)}>
            <Plus size={16} /> New Migration
          </button>
        </div>
      </div>

      <div style={S.infoBox}>
        <strong>How it works:</strong> Nexspence connects to your Nexus instance via its REST API and
        streams repositories, users, roles and all artifacts directly — no downtime required.
        Jobs are pausable and resumable. Requires Nexus admin credentials.
      </div>

      {isLoading ? (
        <div style={S.empty}>Loading…</div>
      ) : jobs.length === 0 ? (
        <div style={S.empty}>
          <ArrowRightLeft size={40} style={{ opacity: 0.3 }} />
          <p>No migration jobs yet</p>
          <button style={S.createBtn} onClick={() => setShowCreate(true)}>
            <Plus size={14} /> Start Migration
          </button>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {jobs.map(job => {
            const reposPct = job.repositoriesTotal ? Math.round((job.repositoriesDone / job.repositoriesTotal) * 100) : 0
            const assetsPct = job.assetsTotal ? Math.round((job.assetsDone / job.assetsTotal) * 100) : 0
            return (
              <div key={job.id} style={S.jobCard}>
                <div style={S.jobTop}>
                  <ArrowRightLeft size={15} style={{ color: 'rgba(229,231,235,0.4)', flexShrink: 0 }} />
                  <span style={S.jobUrl}>{job.sourceUrl}</span>
                  <span style={S.statusBadge(job.status)}>{job.status}</span>
                </div>

                <div style={S.jobStats}>
                  <div style={S.stat}>
                    <div style={S.statLabel}>Repositories</div>
                    <div style={S.statVal}>{job.repositoriesDone}<span style={{ fontSize: 13, color: 'rgba(229,231,235,0.4)', fontWeight: 400 }}>/{job.repositoriesTotal || '?'}</span></div>
                    <div style={{ ...S.progress, marginTop: 6 }}>
                      <div style={{ height: '100%', width: reposPct + '%', background: '#3b82f6', transition: 'width 0.4s' }} />
                    </div>
                  </div>
                  <div style={S.stat}>
                    <div style={S.statLabel}>Assets</div>
                    <div style={S.statVal}>{job.assetsDone}<span style={{ fontSize: 13, color: 'rgba(229,231,235,0.4)', fontWeight: 400 }}>/{job.assetsTotal || '?'}</span></div>
                    <div style={{ ...S.progress, marginTop: 6 }}>
                      <div style={{ height: '100%', width: assetsPct + '%', background: '#22c55e', transition: 'width 0.4s' }} />
                    </div>
                  </div>
                  <div style={S.stat}>
                    <div style={S.statLabel}>Errors</div>
                    <div style={{ ...S.statVal, color: job.errorCount > 0 ? '#ef4444' : '#22c55e' }}>{job.errorCount}</div>
                  </div>
                </div>

                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                  <span style={S.muted}>Started {new Date(job.createdAt).toLocaleString()}</span>
                  <div style={S.jobBtns}>
                    {job.status === 'running' && (
                      <button style={S.smallBtn()} onClick={() => pauseMut.mutate(job.id)}>
                        <Pause size={12} /> Pause
                      </button>
                    )}
                    {job.status === 'paused' && (
                      <button style={S.smallBtn()} onClick={() => resumeMut.mutate(job.id)}>
                        <Play size={12} /> Resume
                      </button>
                    )}
                  </div>
                </div>
              </div>
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
    <div style={S.modal} onClick={onClose}>
      <div style={S.modalBox} onClick={e => e.stopPropagation()}>
        <h2 style={S.modalTitle}>New Migration Job</h2>
        <form style={S.form} onSubmit={handleSubmit}>
          <div style={S.field}>
            <label style={S.label}>Nexus URL *</label>
            <input style={S.input} placeholder="https://nexus.example.com" value={form.sourceUrl} onChange={set('sourceUrl')} required />
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
            <div style={S.field}>
              <label style={S.label}>Username</label>
              <input style={S.input} value={form.username} onChange={set('username')} />
            </div>
            <div style={S.field}>
              <label style={S.label}>Password *</label>
              <input style={S.input} type="password" value={form.password} onChange={set('password')} required />
            </div>
          </div>
          <div style={S.field}>
            <label style={S.label}>Concurrency</label>
            <input style={S.input} type="number" min={1} max={16} value={form.concurrency} onChange={set('concurrency')} />
          </div>
          {error && <div style={S.error}>{error}</div>}
          <div style={S.modalFooter}>
            <button type="button" style={S.cancelBtn} onClick={onClose}>Cancel</button>
            <button type="submit" style={S.submitBtn(loading)} disabled={loading}>
              {loading ? 'Starting…' : 'Start Migration'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
