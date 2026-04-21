import { useState } from 'react'
import { Outlet, NavLink } from 'react-router-dom'
import {
  Home, Search, FolderOpen, Trash2,
  Settings, Shield, FileText, LogOut,
  ArrowRightLeft, Activity, Key, Plus, X,
} from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import styles from './Layout.module.css'
import { useAuthStore } from '@/store/authStore'
import { apiClient } from '@/api/client'
import logo from '@/assets/logo.png'

const navItems = [
  { to: '/repositories', icon: Home,       label: 'Repositories' },
  { to: '/browse',       icon: FolderOpen,  label: 'Browse' },
  { to: '/search',       icon: Search,      label: 'Search' },
]

const systemItems = [
  { to: '/security',   icon: Shield,         label: 'Security' },
  { to: '/admin',      icon: Settings,       label: 'System Admin' },
  { to: '/monitoring', icon: Activity,       label: 'Monitoring' },
  { to: '/audit',      icon: FileText,       label: 'Audit Log' },
  { to: '/cleanup',    icon: Trash2,         label: 'Cleanup Policies' },
  { to: '/migration',  icon: ArrowRightLeft, label: 'Migration' },
]

interface UserToken { id: string; name: string; createdAt: string; lastUsedAt?: string; expiresAt?: string }
interface NewToken  { id: string; name: string; token: string }

function ProfileModal({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient()
  const user = useAuthStore(s => s.user)

  const { data: tokens = [], isLoading } = useQuery<UserToken[]>({
    queryKey: ['my-tokens'],
    queryFn: () => apiClient.get<UserToken[]>('/api/v1/tokens').then(r => r.data ?? []),
  })
  const [name, setName] = useState('')
  const [newToken, setNewToken] = useState<NewToken | null>(null)
  const [creating, setCreating] = useState(false)

  async function create() {
    if (!name.trim()) return
    setCreating(true)
    try {
      const res = await apiClient.post<NewToken>('/api/v1/tokens', { name: name.trim() })
      setNewToken(res.data)
      setName('')
      qc.invalidateQueries({ queryKey: ['my-tokens'] })
    } finally { setCreating(false) }
  }

  const del = useMutation({
    mutationFn: (id: string) => apiClient.delete(`/api/v1/tokens/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['my-tokens'] }),
  })

  const S = {
    overlay:  { position: 'fixed' as const, inset: 0, background: 'rgba(0,0,0,0.6)', backdropFilter: 'blur(4px)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 },
    modal:    { background: 'linear-gradient(135deg,rgba(11,20,38,0.98),rgba(7,11,20,0.98))', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 16, padding: 24, width: 480, maxWidth: '95vw', maxHeight: '80vh', display: 'flex', flexDirection: 'column' as const, gap: 16 },
    header:   { display: 'flex', alignItems: 'center', justifyContent: 'space-between' },
    title:    { fontSize: 16, fontWeight: 700, color: '#dbeafe', display: 'flex', alignItems: 'center', gap: 8 },
    card:     { background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 12, padding: 16 },
    input:    { flex: 1, background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 8, padding: '8px 12px', color: '#e5e7eb', fontSize: 13, outline: 'none' },
    btn:      (v: 'primary'|'danger'|'ghost') => ({ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 14px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 13, fontWeight: 600 as const, background: v === 'primary' ? '#3b82f6' : v === 'danger' ? 'rgba(239,68,68,0.15)' : 'rgba(255,255,255,0.06)', color: v === 'danger' ? '#ef4444' : '#fff' }),
    row:      { display: 'flex', alignItems: 'center', gap: 10, padding: '8px 0', borderBottom: '1px solid rgba(255,255,255,0.06)' },
    empty:    { color: 'rgba(229,231,235,0.4)', fontSize: 13, padding: '12px 0' },
    mono:     { fontFamily: 'ui-monospace,monospace' },
    scroll:   { overflowY: 'auto' as const, display: 'flex', flexDirection: 'column' as const, gap: 16 },
    closeBtn: { background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(229,231,235,0.5)', padding: 4, display: 'flex' },
  }

  return (
    <div style={S.overlay} onClick={onClose}>
      <div style={S.modal} onClick={e => e.stopPropagation()}>
        <div style={S.header}>
          <div style={S.title}>
            <Key size={16} style={{ color: '#3b82f6' }} />
            Profile — {user?.username}
          </div>
          <button style={S.closeBtn} onClick={onClose}><X size={18} /></button>
        </div>

        <div style={S.scroll}>
          <div style={S.card}>
            <div style={{ fontSize: 13, fontWeight: 600, color: '#dbeafe', marginBottom: 10 }}>Create API Token</div>
            <div style={{ display: 'flex', gap: 8 }}>
              <input
                style={S.input}
                placeholder="Token name"
                value={name}
                onChange={e => setName(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && create()}
              />
              <button style={S.btn('primary')} onClick={create} disabled={creating || !name.trim()}>
                <Plus size={14} />{creating ? 'Creating…' : 'Create'}
              </button>
            </div>
          </div>

          {newToken && (
            <div style={{ ...S.card, background: 'rgba(34,197,94,0.06)', border: '1px solid rgba(34,197,94,0.3)' }}>
              <div style={{ fontSize: 13, color: '#22c55e', fontWeight: 600, marginBottom: 8 }}>
                Token created — copy it now, it won't be shown again
              </div>
              <code style={{ ...S.mono, fontSize: 12, background: 'rgba(0,0,0,0.3)', padding: '8px 12px', borderRadius: 8, display: 'block', wordBreak: 'break-all' as const, color: '#a5b4fc' }}>
                {newToken.token}
              </code>
              <button style={{ ...S.btn('ghost'), marginTop: 8, fontSize: 12 }} onClick={() => setNewToken(null)}>Dismiss</button>
            </div>
          )}

          <div style={S.card}>
            <div style={{ fontSize: 13, fontWeight: 600, color: '#dbeafe', marginBottom: 10 }}>Your API Tokens</div>
            {isLoading
              ? <div style={S.empty}>Loading…</div>
              : tokens.length === 0
                ? <div style={S.empty}>No tokens yet</div>
                : tokens.map(t => (
                  <div key={t.id} style={S.row}>
                    <Key size={13} style={{ color: '#3b82f6', flexShrink: 0 }} />
                    <div style={{ flex: 1 }}>
                      <div style={{ color: '#dbeafe', fontWeight: 600, fontSize: 13 }}>{t.name}</div>
                      <div style={{ fontSize: 11, color: 'rgba(229,231,235,0.4)' }}>
                        Created {new Date(t.createdAt).toLocaleDateString()}
                        {t.lastUsedAt && ` · Last used ${new Date(t.lastUsedAt).toLocaleDateString()}`}
                        {t.expiresAt && ` · Expires ${new Date(t.expiresAt).toLocaleDateString()}`}
                      </div>
                    </div>
                    <button style={S.btn('danger')} onClick={() => del.mutate(t.id)}>
                      <Trash2 size={13} />
                    </button>
                  </div>
                ))
            }
          </div>
        </div>
      </div>
    </div>
  )
}

export default function Layout() {
  const { user, logout, isAdmin } = useAuthStore()
  const admin = isAdmin()
  const [profileOpen, setProfileOpen] = useState(false)

  return (
    <div className={styles.root}>
      <aside className={styles.sidebar}>
        {/* Brand */}
        <div className={styles.brand}>
          <img src={logo} alt="Nexspence" className={styles.brandLogo} />
        </div>

        {/* Primary nav */}
        <span className={styles.sectionLabel}>Browse</span>
        <nav className={styles.nav}>
          {navItems.map(({ to, icon: Icon, label }) => (
            <NavLink
              key={to}
              to={to}
              className={({ isActive }) =>
                `${styles.navBtn} ${isActive ? styles.active : ''}`
              }
            >
              <Icon size={16} />
              <span>{label}</span>
            </NavLink>
          ))}
        </nav>

        {admin && (
          <>
            <hr className={styles.divider} />
            <span className={styles.sectionLabel}>System</span>
            <nav className={styles.nav}>
              {systemItems.map(({ to, icon: Icon, label }) => (
                <NavLink
                  key={to}
                  to={to}
                  className={({ isActive }) =>
                    `${styles.navBtn} ${isActive ? styles.active : ''}`
                  }
                >
                  <Icon size={16} />
                  <span>{label}</span>
                </NavLink>
              ))}
            </nav>
          </>
        )}

        {/* Footer */}
        <div className={styles.footer}>
          {user && (
            <div className={styles.userInfo}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 2, minWidth: 0 }}>
                <span className={styles.userName}>
                  {user.firstName || user.username}
                </span>
                <span className={styles.userRole}>
                  {user.roles?.includes('nx-admin') ? 'Admin' : user.roles?.length === 0 ? 'No access' : 'User'}
                </span>
                {user.roles?.length === 0 && (
                  <span style={{ fontSize: 11, color: 'rgba(229,231,235,0.4)', marginTop: 2, lineHeight: 1.3 }}>
                    Contact admin to grant permissions
                  </span>
                )}
              </div>
              <button
                title="API Tokens & Profile"
                onClick={() => setProfileOpen(true)}
                style={{ background: 'rgba(59,130,246,0.12)', border: '1px solid rgba(59,130,246,0.25)', borderRadius: 7, padding: '5px 7px', cursor: 'pointer', color: '#3b82f6', display: 'flex', alignItems: 'center', flexShrink: 0 }}
              >
                <Key size={14} />
              </button>
            </div>
          )}
          <button className={`${styles.navBtn} ${styles.danger}`} onClick={logout}>
            <LogOut size={16} />
            <span>Sign Out</span>
          </button>
          <span className={styles.version}>Nexspence v1.0.0 · OSS</span>
        </div>
      </aside>

      <main className={styles.main}>
        <Outlet />
      </main>

      {profileOpen && <ProfileModal onClose={() => setProfileOpen(false)} />}
    </div>
  )
}
