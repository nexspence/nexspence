import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { UserPlus, Trash2, RefreshCw, Shield, User } from 'lucide-react'
import { nexusApi } from '@/api/client'
import styles from './UsersPage.module.css'

interface UserItem {
  id: string
  username: string
  email: string
  firstName: string
  lastName: string
  status: string
  source: string
  roles: string[]
}

const STATUS_COLORS: Record<string, { bg: string; text: string }> = {
  active:   { bg: 'rgba(16,185,129,0.15)', text: '#10b981' },
  disabled: { bg: 'rgba(107,114,128,0.15)', text: '#6b7280' },
}

export default function UsersPage() {
  const qc = useQueryClient()
  const [filter, setFilter] = useState('')
  const [showCreate, setShowCreate] = useState(false)

  const { data: users = [], isLoading, refetch } = useQuery<UserItem[]>({
    queryKey: ['users'],
    queryFn: () => nexusApi.listUsers().then(r => r.data),
  })

  const deleteMutation = useMutation({
    mutationFn: (username: string) => nexusApi.deleteUser(username),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })

  const filtered = users.filter(u =>
    u.username.toLowerCase().includes(filter.toLowerCase()) ||
    u.email.toLowerCase().includes(filter.toLowerCase())
  )

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <div>
          <h1 className={styles.title}>Users &amp; Roles</h1>
          <p className={styles.subtitle}>{users.length} users</p>
        </div>
        <div className={styles.actions}>
          <button className={styles.iconBtn} onClick={() => refetch()} title="Refresh">
            <RefreshCw size={16} />
          </button>
          <button className={styles.createBtn} onClick={() => setShowCreate(true)}>
            <UserPlus size={16} />
            Add User
          </button>
        </div>
      </div>

      <div className={styles.toolbar}>
        <input
          className={styles.search}
          placeholder="Filter by username or email…"
          value={filter}
          onChange={e => setFilter(e.target.value)}
        />
      </div>

      {isLoading ? (
        <div className={styles.empty}>Loading…</div>
      ) : filtered.length === 0 ? (
        <div className={styles.empty}>
          <User size={40} className={styles.emptyIcon} />
          <p>No users found</p>
        </div>
      ) : (
        <div className={styles.table}>
          <div className={styles.tableHead}>
            <div>Username</div>
            <div>Name</div>
            <div>Email</div>
            <div>Roles</div>
            <div>Status</div>
            <div>Source</div>
            <div></div>
          </div>
          {filtered.map(user => {
            const sc = STATUS_COLORS[user.status] ?? STATUS_COLORS.disabled
            return (
              <div key={user.id} className={styles.tableRow}>
                <div className={styles.username}>
                  <User size={14} className={styles.userIcon} />
                  {user.username}
                </div>
                <div className={styles.cell}>
                  {[user.firstName, user.lastName].filter(Boolean).join(' ') || '—'}
                </div>
                <div className={styles.cell}>{user.email || '—'}</div>
                <div className={styles.roles}>
                  {(user.roles ?? []).map(r => (
                    <span key={r} className={styles.roleBadge}>
                      <Shield size={10} /> {r}
                    </span>
                  ))}
                </div>
                <div>
                  <span
                    className={styles.statusBadge}
                    style={{ background: sc.bg, color: sc.text }}
                  >
                    {user.status}
                  </span>
                </div>
                <div className={styles.cell}>{user.source}</div>
                <div>
                  <button
                    className={styles.deleteBtn}
                    disabled={user.username === 'admin'}
                    onClick={() => {
                      if (confirm(`Delete user "${user.username}"?`)) {
                        deleteMutation.mutate(user.username)
                      }
                    }}
                    title="Delete user"
                  >
                    <Trash2 size={14} />
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      )}

      {showCreate && (
        <CreateUserModal
          onClose={() => setShowCreate(false)}
          onCreated={() => {
            setShowCreate(false)
            qc.invalidateQueries({ queryKey: ['users'] })
          }}
        />
      )}
    </div>
  )
}

function CreateUserModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [form, setForm] = useState({
    username: '', email: '', firstName: '', lastName: '',
    password: '', status: 'active',
  })
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await nexusApi.createUser({ ...form })
      onCreated()
    } catch (err: any) {
      setError(err.response?.data?.error ?? 'Failed to create user')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className={styles.modalOverlay} onClick={onClose}>
      <div className={styles.modal} onClick={e => e.stopPropagation()}>
        <h2 className={styles.modalTitle}>Add User</h2>
        <form onSubmit={handleSubmit} className={styles.form}>
          <div className={styles.formRow}>
            <label className={styles.label}>Username *</label>
            <input
              className={styles.input}
              value={form.username}
              onChange={e => setForm(f => ({ ...f, username: e.target.value }))}
              required
            />
          </div>
          <div className={styles.formRow}>
            <label className={styles.label}>Password *</label>
            <input
              type="password"
              className={styles.input}
              value={form.password}
              onChange={e => setForm(f => ({ ...f, password: e.target.value }))}
              required
            />
          </div>
          <div className={styles.formGrid}>
            <div className={styles.formRow}>
              <label className={styles.label}>First name</label>
              <input
                className={styles.input}
                value={form.firstName}
                onChange={e => setForm(f => ({ ...f, firstName: e.target.value }))}
              />
            </div>
            <div className={styles.formRow}>
              <label className={styles.label}>Last name</label>
              <input
                className={styles.input}
                value={form.lastName}
                onChange={e => setForm(f => ({ ...f, lastName: e.target.value }))}
              />
            </div>
          </div>
          <div className={styles.formRow}>
            <label className={styles.label}>Email</label>
            <input
              type="email"
              className={styles.input}
              value={form.email}
              onChange={e => setForm(f => ({ ...f, email: e.target.value }))}
            />
          </div>
          <div className={styles.formRow}>
            <label className={styles.label}>Status</label>
            <select
              className={styles.input}
              value={form.status}
              onChange={e => setForm(f => ({ ...f, status: e.target.value }))}
            >
              <option value="active">Active</option>
              <option value="disabled">Disabled</option>
            </select>
          </div>
          {error && <div className={styles.error}>{error}</div>}
          <div className={styles.modalFooter}>
            <button type="button" className={styles.cancelBtn} onClick={onClose}>Cancel</button>
            <button type="submit" className={styles.submitBtn} disabled={loading}>
              {loading ? 'Creating…' : 'Create User'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
