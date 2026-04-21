import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { UserPlus, Trash2, RefreshCw, Shield, User, AlertTriangle, Plus, Edit2 } from 'lucide-react'
import { nexusApi, apiClient } from '@/api/client'
import styles from './UsersPage.module.css'
import { Select } from '../components/Select'

/* ─── Types ─────────────────────────────────────────────────── */
interface UserItem {
  id: string
  userId: string
  emailAddress: string
  firstName: string
  lastName: string
  status: string
  source: string
  roles: string[]
}

interface RoleItem {
  id: string
  name: string
  description: string
  readOnly: boolean
  privileges: string[]
  roles: string[]
}

const STATUS_COLORS: Record<string, { bg: string; text: string }> = {
  active:   { bg: 'rgba(16,185,129,0.15)', text: '#10b981' },
  disabled: { bg: 'rgba(107,114,128,0.15)', text: '#6b7280' },
}

const S = {
  tabs:  { display: 'flex', gap: 4, borderBottom: '1px solid rgba(255,255,255,0.08)', marginBottom: 20 },
  tab:   (active: boolean) => ({
    padding: '8px 16px', fontSize: 13, fontWeight: 600 as const, cursor: 'pointer', border: 'none',
    background: 'none', color: active ? '#3b82f6' : 'rgba(229,231,235,0.5)',
    borderBottom: active ? '2px solid #3b82f6' : '2px solid transparent',
  }),
  error: { display: 'flex', alignItems: 'center', gap: 10, padding: '12px 16px', background: 'rgba(239,68,68,0.08)', border: '1px solid rgba(239,68,68,0.25)', borderRadius: 10, color: '#fca5a5', fontSize: 13 },
  card:  { background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 12, padding: '16px 18px', marginBottom: 12 },
  row:   { display: 'flex', alignItems: 'center', gap: 10, padding: '10px 0', borderBottom: '1px solid rgba(255,255,255,0.06)' },
  btn:   (v: 'primary'|'danger'|'ghost') => ({
    display: 'flex', alignItems: 'center', gap: 6, padding: '7px 14px', borderRadius: 8, border: 'none',
    cursor: 'pointer', fontSize: 13, fontWeight: 600 as const,
    background: v === 'primary' ? '#3b82f6' : v === 'danger' ? 'rgba(239,68,68,0.15)' : 'rgba(255,255,255,0.06)',
    color: v === 'danger' ? '#ef4444' : '#fff',
  }),
  input: { width: '100%', background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 8, padding: '8px 12px', color: '#e5e7eb', fontSize: 13, outline: 'none', boxSizing: 'border-box' as const },
}

/* ─── Role assign modal ─────────────────────────────────────── */
export function AssignRolesModal({ user, roles, onClose, onSaved }: {
  user: UserItem
  roles: RoleItem[]
  onClose: () => void
  onSaved: () => void
}) {
  const userRoles = user.roles ?? []
  const userRoleIds = roles.filter(r => userRoles.includes(r.name)).map(r => r.id)
  const [selected, setSelected] = useState<string[]>(userRoleIds)
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  const toggle = (id: string) =>
    setSelected(prev => prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id])

  const save = async () => {
    setSaving(true); setErr('')
    try {
      await apiClient.put(`/service/rest/v1/security/users/${user.userId}/roles`, { roleIds: selected })
      onSaved()
    } catch (e: any) {
      setErr(e.response?.data?.error ?? 'Failed to save roles')
    } finally { setSaving(false) }
  }

  return (
    <div
      style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.65)', backdropFilter: 'blur(4px)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}
      onClick={onClose}
    >
      <div
        style={{ background: '#0f172a', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 14, padding: 28, width: 460, maxWidth: '90vw', maxHeight: '80vh', overflowY: 'auto' as const, display: 'flex', flexDirection: 'column', gap: 0 }}
        onClick={e => e.stopPropagation()}
      >
        <h2 style={{ margin: '0 0 20px', fontSize: 16, fontWeight: 700, color: '#dbeafe' }}>Assign Roles — {user.userId}</h2>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 0, marginBottom: 16 }}>
          {roles.map(r => (
            <label key={r.id} style={{ display: 'flex', alignItems: 'center', gap: 10, cursor: 'pointer', padding: '10px 0', borderBottom: '1px solid rgba(255,255,255,0.06)' }}>
              <input type="checkbox" checked={selected.includes(r.id)} onChange={() => toggle(r.id)} style={{ accentColor: '#3b82f6', width: 16, height: 16, flexShrink: 0 }} />
              <div style={{ flex: 1 }}>
                <div style={{ color: '#dbeafe', fontWeight: 600, fontSize: 14 }}>{r.name}</div>
                {r.description && <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.4)', marginTop: 2 }}>{r.description}</div>}
              </div>
              {r.readOnly && <span style={{ fontSize: 11, padding: '2px 6px', borderRadius: 4, background: 'rgba(107,114,128,0.2)', color: '#9ca3af', flexShrink: 0 }}>built-in</span>}
            </label>
          ))}
          {roles.length === 0 && <div style={{ color: 'rgba(229,231,235,0.4)', fontSize: 13, padding: '12px 0' }}>No roles available</div>}
        </div>

        {err && <div style={{ marginBottom: 12, padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, fontSize: 13, color: '#fca5a5' }}>{err}</div>}

        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <button
            type="button"
            onClick={onClose}
            style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 16px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 13, fontWeight: 600, background: 'rgba(255,255,255,0.06)', color: '#e5e7eb' }}
          >
            Cancel
          </button>
          <button
            onClick={save}
            disabled={saving}
            style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 16px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 13, fontWeight: 600, background: '#3b82f6', color: '#fff', opacity: saving ? 0.7 : 1 }}
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>
    </div>
  )
}

/* ─── Users tab ──────────────────────────────────────────────── */
export function UsersTab() {
  const qc = useQueryClient()
  const [filter, setFilter] = useState('')
  const [showCreate, setShowCreate] = useState(false)
  const [assignUser, setAssignUser] = useState<UserItem | null>(null)

  const { data: users = [], isLoading, isError, error, refetch } = useQuery<UserItem[]>({
    queryKey: ['users'],
    queryFn: () => nexusApi.listUsers().then(r => r.data),
  })

  const { data: roles = [] } = useQuery<RoleItem[]>({
    queryKey: ['roles'],
    queryFn: () => nexusApi.listRoles().then(r => r.data),
  })

  const deleteMutation = useMutation({
    mutationFn: (username: string) => nexusApi.deleteUser(username),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })

  const filtered = users.filter(u =>
    (u.userId ?? '').toLowerCase().includes(filter.toLowerCase()) ||
    (u.emailAddress ?? '').toLowerCase().includes(filter.toLowerCase())
  )

  return (
    <>
      <div className={styles.header}>
        <div>
          <h1 className={styles.title}>Users</h1>
          <p className={styles.subtitle}>{users.length} users</p>
        </div>
        <div className={styles.actions}>
          <button className={styles.iconBtn} onClick={() => refetch()} title="Refresh"><RefreshCw size={16} /></button>
          <button className={styles.createBtn} onClick={() => setShowCreate(true)}><UserPlus size={16} />Add User</button>
        </div>
      </div>

      <div className={styles.toolbar}>
        <input className={styles.search} placeholder="Filter by username or email…" value={filter} onChange={e => setFilter(e.target.value)} />
      </div>

      {isError && (
        <div style={S.error}>
          <AlertTriangle size={16} style={{ flexShrink: 0 }} />
          {(error as any)?.response?.data?.error ?? (error as Error)?.message ?? 'Failed to load users'}
        </div>
      )}

      {isLoading ? (
        <div className={styles.empty}>Loading…</div>
      ) : filtered.length === 0 && !isError ? (
        <div className={styles.empty}>
          <User size={40} className={styles.emptyIcon} />
          <p>No users found</p>
        </div>
      ) : (
        <div className={styles.table}>
          <div className={styles.tableHead}>
            <div>Username</div><div>Name</div><div>Email</div>
            <div>Roles</div><div>Status</div><div>Source</div><div></div>
          </div>
          {filtered.map(user => {
            const sc = STATUS_COLORS[user.status] ?? STATUS_COLORS.disabled
            return (
              <div key={user.userId} className={styles.tableRow}>
                <div className={styles.username}><User size={14} className={styles.userIcon} />{user.userId}</div>
                <div className={styles.cell}>{[user.firstName, user.lastName].filter(Boolean).join(' ') || '—'}</div>
                <div className={styles.cell}>{user.emailAddress || '—'}</div>
                <div className={styles.roles}>
                  {(user.roles ?? []).map(r => (
                    <span key={r} className={styles.roleBadge}><Shield size={10} /> {r}</span>
                  ))}
                  <button style={{ ...S.btn('ghost'), padding: '3px 8px', fontSize: 11 }} onClick={() => setAssignUser(user)} title="Assign roles">
                    <Edit2 size={11} />
                  </button>
                </div>
                <div><span className={styles.statusBadge} style={{ background: sc.bg, color: sc.text }}>{user.status}</span></div>
                <div className={styles.cell}>{user.source}</div>
                <div>
                  <button className={styles.deleteBtn} disabled={user.userId === 'admin'} onClick={() => {
                    if (confirm(`Delete user "${user.userId}"?`)) deleteMutation.mutate(user.userId)
                  }} title="Delete user"><Trash2 size={14} /></button>
                </div>
              </div>
            )
          })}
        </div>
      )}

      {showCreate && (
        <CreateUserModal onClose={() => setShowCreate(false)} onCreated={() => {
          setShowCreate(false)
          qc.invalidateQueries({ queryKey: ['users'] })
        }} />
      )}

      {assignUser && (
        <AssignRolesModal user={assignUser} roles={roles} onClose={() => setAssignUser(null)} onSaved={() => {
          setAssignUser(null)
          qc.invalidateQueries({ queryKey: ['users'] })
        }} />
      )}
    </>
  )
}

/* ─── Roles tab ──────────────────────────────────────────────── */
function RolesTab() {
  const qc = useQueryClient()
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState({ name: '', description: '' })
  const [saving, setSaving] = useState(false)
  const [formErr, setFormErr] = useState('')

  const { data: roles = [], isLoading, isError, error, refetch } = useQuery<RoleItem[]>({
    queryKey: ['roles'],
    queryFn: () => nexusApi.listRoles().then(r => r.data),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => apiClient.delete(`/service/rest/v1/security/roles/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['roles'] }),
  })

  const createRole = async () => {
    if (!form.name.trim()) { setFormErr('Name is required'); return }
    setSaving(true); setFormErr('')
    try {
      await apiClient.post('/service/rest/v1/security/roles', form)
      qc.invalidateQueries({ queryKey: ['roles'] })
      setShowForm(false); setForm({ name: '', description: '' })
    } catch (e: any) {
      setFormErr(e.response?.data?.error ?? 'Failed to create role')
    } finally { setSaving(false) }
  }

  return (
    <>
      <div className={styles.header}>
        <div>
          <h1 className={styles.title}>Roles</h1>
          <p className={styles.subtitle}>{roles.length} roles</p>
        </div>
        <div className={styles.actions}>
          <button className={styles.iconBtn} onClick={() => refetch()} title="Refresh"><RefreshCw size={16} /></button>
          <button className={styles.createBtn} onClick={() => setShowForm(v => !v)}><Plus size={16} />New Role</button>
        </div>
      </div>

      {isError && (
        <div style={{ ...S.error, marginBottom: 16 }}>
          <AlertTriangle size={16} style={{ flexShrink: 0 }} />
          {(error as any)?.response?.data?.error ?? (error as Error)?.message ?? 'Failed to load roles'}
        </div>
      )}

      {showForm && (
        <div style={{ ...S.card, marginBottom: 16 }}>
          <div style={{ fontSize: 14, fontWeight: 600, color: '#dbeafe', marginBottom: 12 }}>New Role</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <input style={S.input} placeholder="Role name (e.g. developer)" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
            <input style={S.input} placeholder="Description (optional)" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />
            {formErr && <div style={{ color: '#ef4444', fontSize: 13 }}>{formErr}</div>}
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <button style={S.btn('ghost')} onClick={() => { setShowForm(false); setFormErr('') }}>Cancel</button>
              <button style={S.btn('primary')} onClick={createRole} disabled={saving}>{saving ? 'Creating…' : 'Create'}</button>
            </div>
          </div>
        </div>
      )}

      {isLoading ? (
        <div className={styles.empty}>Loading…</div>
      ) : roles.length === 0 && !isError ? (
        <div className={styles.empty}><Shield size={40} className={styles.emptyIcon} /><p>No roles defined</p></div>
      ) : (
        <div style={S.card}>
          {roles.map((r, i) => (
            <div key={r.id} style={{ ...S.row, borderBottom: i < roles.length - 1 ? '1px solid rgba(255,255,255,0.06)' : 'none' }}>
              <Shield size={15} style={{ color: '#3b82f6', flexShrink: 0 }} />
              <div style={{ flex: 1 }}>
                <div style={{ color: '#dbeafe', fontWeight: 600 }}>{r.name}</div>
                {r.description && <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.4)' }}>{r.description}</div>}
              </div>
              {r.readOnly
                ? <span style={{ fontSize: 11, padding: '2px 8px', borderRadius: 4, background: 'rgba(107,114,128,0.2)', color: '#9ca3af' }}>built-in</span>
                : <button style={S.btn('danger')} onClick={() => {
                    if (confirm(`Delete role "${r.name}"?`)) deleteMutation.mutate(r.id)
                  }}><Trash2 size={13} /></button>
              }
            </div>
          ))}
        </div>
      )}
    </>
  )
}

/* ─── Main page ──────────────────────────────────────────────── */
type Tab = 'users' | 'roles'

export default function UsersPage() {
  const [tab, setTab] = useState<Tab>('users')

  return (
    <div className={styles.page}>
      <div style={S.tabs}>
        {([['users', 'Users'], ['roles', 'Roles']] as [Tab, string][]).map(([id, label]) => (
          <button key={id} style={S.tab(tab === id)} onClick={() => setTab(id)}>{label}</button>
        ))}
      </div>
      {tab === 'users' ? <UsersTab /> : <RolesTab />}
    </div>
  )
}

/* ─── Create user modal ──────────────────────────────────────── */
export function CreateUserModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [form, setForm] = useState({ username: '', email: '', firstName: '', lastName: '', password: '', status: 'active' })
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault(); setError(''); setLoading(true)
    try {
      await nexusApi.createUser({ ...form })
      onCreated()
    } catch (err: any) {
      setError(err.response?.data?.error ?? 'Failed to create user')
    } finally { setLoading(false) }
  }

  return (
    <div className={styles.modalOverlay} onClick={onClose}>
      <div className={styles.modal} onClick={e => e.stopPropagation()}>
        <h2 className={styles.modalTitle}>Add User</h2>
        <form onSubmit={handleSubmit} className={styles.form}>
          <div className={styles.formRow}>
            <label className={styles.label}>Username *</label>
            <input className={styles.input} value={form.username} onChange={e => setForm(f => ({ ...f, username: e.target.value }))} required />
          </div>
          <div className={styles.formRow}>
            <label className={styles.label}>Password *</label>
            <input type="password" className={styles.input} value={form.password} onChange={e => setForm(f => ({ ...f, password: e.target.value }))} required />
          </div>
          <div className={styles.formGrid}>
            <div className={styles.formRow}>
              <label className={styles.label}>First name</label>
              <input className={styles.input} value={form.firstName} onChange={e => setForm(f => ({ ...f, firstName: e.target.value }))} />
            </div>
            <div className={styles.formRow}>
              <label className={styles.label}>Last name</label>
              <input className={styles.input} value={form.lastName} onChange={e => setForm(f => ({ ...f, lastName: e.target.value }))} />
            </div>
          </div>
          <div className={styles.formRow}>
            <label className={styles.label}>Email</label>
            <input type="email" className={styles.input} value={form.email} onChange={e => setForm(f => ({ ...f, email: e.target.value }))} />
          </div>
          <div className={styles.formRow}>
            <label className={styles.label}>Status</label>
            <Select
              options={[
                { value: 'active',   label: 'Active' },
                { value: 'disabled', label: 'Disabled' },
              ]}
              value={form.status}
              onChange={v => setForm(f => ({ ...f, status: v }))}
            />
          </div>
          {error && <div className={styles.error}>{error}</div>}
          <div className={styles.modalFooter}>
            <button type="button" className={styles.cancelBtn} onClick={onClose}>Cancel</button>
            <button type="submit" className={styles.submitBtn} disabled={loading}>{loading ? 'Creating…' : 'Create User'}</button>
          </div>
        </form>
      </div>
    </div>
  )
}
