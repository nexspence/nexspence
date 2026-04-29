import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { UserPlus, Trash2, RefreshCw, Shield, User, AlertTriangle, Plus, Edit2 } from 'lucide-react'
import { nexusApi, apiClient } from '@/api/client'
import styles from './UsersPage.module.css'
import { Select } from '../components/Select'
import { HoloTabs, HoloPill, HoloButton, HoloInput, HoloModal, HoloCard } from '@/components/holo'

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
  const [leftSearch, setLeftSearch] = useState('')
  const [rightSearch, setRightSearch] = useState('')
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  const add = (id: string) => setSelected(prev => [...prev, id])
  const remove = (id: string) => setSelected(prev => prev.filter(x => x !== id))
  const addAll = () => setSelected(prev => [...new Set([...prev, ...available.map(r => r.id)])])
  const removeAll = () => setSelected([])

  const available = roles.filter(r =>
    !selected.includes(r.id) &&
    (!leftSearch || r.name.toLowerCase().includes(leftSearch.toLowerCase()))
  )
  const selectedRoles = roles.filter(r =>
    selected.includes(r.id) &&
    (!rightSearch || r.name.toLowerCase().includes(rightSearch.toLowerCase()))
  )

  const save = async () => {
    setSaving(true); setErr('')
    try {
      await apiClient.put(`/service/rest/v1/security/users/${user.userId}/roles`, { roleIds: selected })
      onSaved()
    } catch (e: any) {
      setErr(e.response?.data?.error ?? 'Failed to save roles')
    } finally { setSaving(false) }
  }

  const panelStyle: React.CSSProperties = {
    border: '1px solid rgba(124,92,255,0.2)', borderRadius: 10, overflow: 'hidden', flex: 1,
  }
  const headerStyle: React.CSSProperties = {
    padding: '6px 10px', fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)',
    textTransform: 'uppercase' as const, letterSpacing: '0.4px',
    borderBottom: '1px solid rgba(255,255,255,0.06)', background: 'rgba(0,0,0,0.2)',
  }
  const listStyle: React.CSSProperties = { maxHeight: 200, overflowY: 'auto' as const }
  const itemBase: React.CSSProperties = {
    padding: '7px 10px', fontSize: 12, cursor: 'pointer',
    borderBottom: '1px solid rgba(255,255,255,0.03)',
  }
  const arrowBtn: React.CSSProperties = {
    width: 28, height: 28, display: 'flex', alignItems: 'center', justifyContent: 'center',
    borderRadius: 8, border: '1px solid rgba(124,92,255,0.2)',
    background: 'rgba(124,92,255,0.1)', color: 'var(--holo-a)', cursor: 'pointer', fontSize: 14,
  }

  return (
    <HoloModal open={true} onClose={onClose}>
      <h2 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: 'var(--holo-text)' }}>Assign Roles — {user.userId}</h2>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 28px 1fr', gap: 8, alignItems: 'stretch' }}>
        {/* Available */}
        <div style={panelStyle}>
          <div style={headerStyle}>Available ({roles.filter(r => !selected.includes(r.id)).length})</div>
          <div style={{ padding: '4px 6px', borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
            <input
              placeholder="Filter…"
              value={leftSearch}
              onChange={e => setLeftSearch(e.target.value)}
              className="holo-input"
              style={{ width: '100%', boxSizing: 'border-box' as const, fontSize: 11, padding: '4px 8px' }}
            />
          </div>
          <div style={listStyle}>
            {available.map(r => (
              <div key={r.id} style={{ ...itemBase, color: 'var(--holo-text)' }}
                onClick={() => add(r.id)}
                onMouseEnter={e => (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.08)'}
                onMouseLeave={e => (e.currentTarget as HTMLDivElement).style.background = 'transparent'}
              >
                <div style={{ fontWeight: 600 }}>{r.name}</div>
                {r.description && <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 1 }}>{r.description}</div>}
              </div>
            ))}
            {available.length === 0 && (
              <div style={{ ...itemBase, color: 'var(--holo-text-faint)' }}>
                {leftSearch ? 'No matches' : 'All assigned'}
              </div>
            )}
          </div>
        </div>

        {/* Arrow column */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6, justifyContent: 'center', alignItems: 'center' }}>
          <button type="button" style={arrowBtn} onClick={addAll} title="Add all">→</button>
          <button type="button" style={arrowBtn} onClick={removeAll} title="Remove all">←</button>
        </div>

        {/* Selected */}
        <div style={panelStyle}>
          <div style={headerStyle}>Selected ({selected.length})</div>
          <div style={{ padding: '4px 6px', borderBottom: '1px solid rgba(255,255,255,0.05)' }}>
            <input
              placeholder="Filter…"
              value={rightSearch}
              onChange={e => setRightSearch(e.target.value)}
              className="holo-input"
              style={{ width: '100%', boxSizing: 'border-box' as const, fontSize: 11, padding: '4px 8px' }}
            />
          </div>
          <div style={listStyle}>
            {selectedRoles.map(r => (
              <div key={r.id} style={{ ...itemBase, color: '#c4b5fd', background: 'rgba(124,92,255,0.12)', display: 'flex', alignItems: 'flex-start', gap: 6 }}
                onClick={() => remove(r.id)}
                onMouseEnter={e => (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.2)'}
                onMouseLeave={e => (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.12)'}
              >
                <span style={{ width: 6, height: 6, borderRadius: '50%', background: '#7c5cff', flexShrink: 0, display: 'inline-block', marginTop: 4 }} />
                <div>
                  <div style={{ fontWeight: 600 }}>{r.name}</div>
                  {r.description && <div style={{ fontSize: 11, color: 'rgba(196,181,253,0.6)', marginTop: 1 }}>{r.description}</div>}
                </div>
              </div>
            ))}
            {selected.length === 0 && (
              <div style={{ ...itemBase, color: 'var(--holo-text-faint)' }}>None selected</div>
            )}
            {selected.length > 0 && selectedRoles.length === 0 && (
              <div style={{ ...itemBase, color: 'var(--holo-text-faint)' }}>No matches</div>
            )}
          </div>
        </div>
      </div>

      {err && <div role="alert" style={{ marginTop: 12, padding: '8px 12px', background: 'rgba(255,107,107,0.08)', border: '1px solid rgba(255,107,107,0.2)', borderRadius: 8, fontSize: 13, color: 'var(--holo-red)' }}>{err}</div>}

      <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 16 }}>
        <HoloButton type="button" onClick={onClose}>Cancel</HoloButton>
        <HoloButton variant="primary" onClick={save} disabled={saving}>{saving ? 'Saving…' : 'Save'}</HoloButton>
      </div>
    </HoloModal>
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
          <HoloButton onClick={() => refetch()} aria-label="Refresh"><RefreshCw size={16} /></HoloButton>
          <HoloButton variant="primary" icon={<UserPlus size={16} />} onClick={() => setShowCreate(true)}>Add User</HoloButton>
        </div>
      </div>

      <div className={styles.toolbar}>
        <HoloInput placeholder="Filter by username or email…" value={filter} onChange={e => setFilter(e.target.value)} style={{ maxWidth: 360 }} />
      </div>

      {isError && (
        <div role="alert" style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '12px 16px', background: 'rgba(255,107,107,0.08)', border: '1px solid rgba(255,107,107,0.2)', borderRadius: 10, color: 'var(--holo-red)', fontSize: 13 }}>
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
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          {filtered.map(user => {
            const isActive = user.status === 'active'
            const fullName = [user.firstName, user.lastName].filter(Boolean).join(' ')
            return (
              <div key={user.userId} style={{
                display: 'grid', gridTemplateColumns: '8px 1fr auto auto auto',
                alignItems: 'center', gap: 12, padding: '11px 16px',
                background: 'rgba(10,8,28,0.97)', border: '1px solid rgba(124,92,255,0.2)',
                borderRadius: 10, transition: 'border-color 0.15s, background 0.15s',
              }}
              onMouseEnter={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'rgba(124,92,255,0.45)'; (e.currentTarget as HTMLDivElement).style.background = 'rgba(124,92,255,0.04)' }}
              onMouseLeave={e => { (e.currentTarget as HTMLDivElement).style.borderColor = 'rgba(124,92,255,0.2)'; (e.currentTarget as HTMLDivElement).style.background = 'rgba(10,8,28,0.97)' }}
              >
                <span style={{ width: 7, height: 7, borderRadius: '50%', flexShrink: 0, display: 'inline-block', background: isActive ? 'var(--holo-green)' : 'rgba(255,255,255,0.2)', boxShadow: isActive ? '0 0 5px var(--holo-green)' : 'none' }} />
                <div style={{ minWidth: 0 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <User size={13} style={{ color: 'var(--holo-a)', flexShrink: 0 }} />
                    <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)', fontFamily: 'monospace' }}>{user.userId}</span>
                    {fullName && <span style={{ fontSize: 12, color: 'var(--holo-text-faint)' }}>{fullName}</span>}
                  </div>
                  {user.emailAddress && <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 1 }}>{user.emailAddress}</div>}
                </div>
                <div style={{ display: 'flex', flexWrap: 'wrap' as const, gap: 4, alignItems: 'center' }}>
                  {(user.roles ?? []).map(r => (
                    <HoloPill key={r} style={{ fontSize: 11 }}><Shield size={10} style={{ marginRight: 4 }} />{r}</HoloPill>
                  ))}
                  <HoloButton style={{ padding: '3px 8px', fontSize: 11 }} onClick={() => setAssignUser(user)} title="Assign roles">
                    <Edit2 size={11} />
                  </HoloButton>
                </div>
                <HoloPill style={{ fontSize: 11 }}>{user.source}</HoloPill>
                <HoloButton variant="danger" style={{ padding: 5 }} disabled={user.userId === 'admin'} onClick={() => {
                  if (confirm(`Delete user "${user.userId}"?`)) deleteMutation.mutate(user.userId)
                }} title="Delete user"><Trash2 size={14} /></HoloButton>
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
          <HoloButton onClick={() => refetch()} aria-label="Refresh"><RefreshCw size={16} /></HoloButton>
          <HoloButton variant="primary" icon={<Plus size={16} />} onClick={() => setShowForm(v => !v)}>New Role</HoloButton>
        </div>
      </div>

      {isError && (
        <div role="alert" style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '12px 16px', background: 'rgba(255,107,107,0.08)', border: '1px solid rgba(255,107,107,0.2)', borderRadius: 10, color: 'var(--holo-red)', fontSize: 13, marginBottom: 16 }}>
          <AlertTriangle size={16} style={{ flexShrink: 0 }} />
          {(error as any)?.response?.data?.error ?? (error as Error)?.message ?? 'Failed to load roles'}
        </div>
      )}

      {showForm && (
        <HoloCard style={{ marginBottom: 16, padding: 16 }}>
          <div style={{ fontSize: 14, fontWeight: 600, color: 'var(--holo-text)', marginBottom: 12 }}>New Role</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <HoloInput placeholder="Role name (e.g. developer)" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
            <HoloInput placeholder="Description (optional)" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />
            {formErr && <div role="alert" style={{ color: 'var(--holo-red)', fontSize: 13 }}>{formErr}</div>}
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <HoloButton onClick={() => { setShowForm(false); setFormErr('') }}>Cancel</HoloButton>
              <HoloButton variant="primary" onClick={createRole} disabled={saving}>{saving ? 'Creating…' : 'Create'}</HoloButton>
            </div>
          </div>
        </HoloCard>
      )}

      {isLoading ? (
        <div className={styles.empty}>Loading…</div>
      ) : roles.length === 0 && !isError ? (
        <div className={styles.empty}><Shield size={40} className={styles.emptyIcon} /><p>No roles defined</p></div>
      ) : (
        <HoloCard style={{ padding: 0 }}>
          {roles.map((r, i) => (
            <div key={r.id} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 16px', borderBottom: i < roles.length - 1 ? '1px solid rgba(255,255,255,0.06)' : 'none' }}>
              <Shield size={15} style={{ color: 'var(--holo-a)', flexShrink: 0 }} />
              <div style={{ flex: 1 }}>
                <div style={{ color: 'var(--holo-text)', fontWeight: 600 }}>{r.name}</div>
                {r.description && <div style={{ fontSize: 12, color: 'var(--holo-text-faint)' }}>{r.description}</div>}
              </div>
              {r.readOnly
                ? <HoloPill style={{ fontSize: 11 }}>built-in</HoloPill>
                : <HoloButton variant="danger" style={{ padding: '5px 10px' }} onClick={() => {
                    if (confirm(`Delete role "${r.name}"?`)) deleteMutation.mutate(r.id)
                  }}><Trash2 size={13} /></HoloButton>
              }
            </div>
          ))}
        </HoloCard>
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
      <div style={{ marginBottom: 24 }}>
        <div className="holo-section-label" style={{ marginBottom: 4 }}>ADMINISTRATION / USERS</div>
        <h1 style={{ fontSize: 20, fontWeight: 700, margin: '0 0 3px', letterSpacing: '-0.01em', lineHeight: 1.2, background: 'linear-gradient(110deg, #7c5cff, #22d3ee 60%)', WebkitBackgroundClip: 'text', WebkitTextFillColor: 'transparent', backgroundClip: 'text' as const }}>Users</h1>
        <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: 0 }}>Manage users and roles</p>
      </div>
      <HoloTabs
        items={[
          { value: 'users', label: 'Users' },
          { value: 'roles', label: 'Roles' },
        ]}
        value={tab}
        onChange={v => setTab(v as Tab)}
      />
      <div style={{ marginTop: 20 }}>
        {tab === 'users' ? <UsersTab /> : <RolesTab />}
      </div>
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
    <HoloModal open={true} onClose={onClose}>
      <h2 style={{ margin: 0, fontSize: 18, fontWeight: 700, color: 'var(--holo-text)' }}>Add User</h2>
      <form onSubmit={handleSubmit} className={styles.form}>
        <div className={styles.formRow}>
          <label className={styles.label}>Username *</label>
          <HoloInput value={form.username} onChange={e => setForm(f => ({ ...f, username: e.target.value }))} required />
        </div>
        <div className={styles.formRow}>
          <label className={styles.label}>Password *</label>
          <HoloInput type="password" value={form.password} onChange={e => setForm(f => ({ ...f, password: e.target.value }))} required />
        </div>
        <div className={styles.formGrid}>
          <div className={styles.formRow}>
            <label className={styles.label}>First name</label>
            <HoloInput value={form.firstName} onChange={e => setForm(f => ({ ...f, firstName: e.target.value }))} />
          </div>
          <div className={styles.formRow}>
            <label className={styles.label}>Last name</label>
            <HoloInput value={form.lastName} onChange={e => setForm(f => ({ ...f, lastName: e.target.value }))} />
          </div>
        </div>
        <div className={styles.formRow}>
          <label className={styles.label}>Email</label>
          <HoloInput type="email" value={form.email} onChange={e => setForm(f => ({ ...f, email: e.target.value }))} />
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
        {error && <div role="alert" className={styles.error}>{error}</div>}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10, marginTop: 8 }}>
          <HoloButton type="button" onClick={onClose}>Cancel</HoloButton>
          <HoloButton variant="primary" type="submit" disabled={loading}>{loading ? 'Creating…' : 'Create User'}</HoloButton>
        </div>
      </form>
    </HoloModal>
  )
}
