import { useState } from 'react'
import { Outlet, NavLink } from 'react-router-dom'
import {
  Home, Search, FolderOpen, Trash2,
  Settings, Shield, FileText, LogOut,
  ArrowRightLeft, Key, Plus, X,
} from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import styles from './Layout.module.css'
import { useAuthStore } from '@/store/authStore'
import { apiClient } from '@/api/client'
import logo from '@/assets/logo.png'
import { HoloApp, HoloModal, HoloButton, HoloInput } from '@/components/holo'

const navItems = [
  { to: '/repositories', icon: Home,       label: 'Repositories' },
  { to: '/browse',       icon: FolderOpen,  label: 'Browse' },
  { to: '/search',       icon: Search,      label: 'Search' },
]

const systemItems = [
  { to: '/security',   icon: Shield,         label: 'Security' },
  { to: '/admin',      icon: Settings,       label: 'System Admin' },
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
    header:    { display: 'flex', alignItems: 'center', justifyContent: 'space-between' },
    title:     { fontSize: 16, fontWeight: 700, color: 'var(--holo-text)', display: 'flex', alignItems: 'center', gap: 8 },
    tokenList: { maxHeight: 240, overflowY: 'auto' as const, display: 'flex', flexDirection: 'column' as const },
    row:       { display: 'flex', alignItems: 'flex-start', gap: 10, padding: '10px 0', borderBottom: '1px solid rgba(255,255,255,0.06)', minWidth: 0 },
    rowMeta:   { flex: 1, minWidth: 0 },
    rowName:   { fontWeight: 600, fontSize: 13, color: 'var(--holo-text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const },
    rowDates:  { fontSize: 11, color: 'var(--holo-text-dim)', marginTop: 2, lineHeight: 1.4 },
    empty:     { color: 'var(--holo-text-dim)', fontSize: 13, padding: '12px 0' },
    mono:      { fontFamily: 'ui-monospace,monospace' },
  }

  return (
    <HoloModal open={true} onClose={onClose}>
      <div style={S.header}>
        <div style={S.title}>
          <Key size={16} style={{ color: 'var(--holo-a)' }} />
          Profile — {user?.username}
        </div>
        <button style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--holo-text-dim)', padding: 4, display: 'flex' }} onClick={onClose}><X size={18} /></button>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <div className="holo-card" style={{ padding: 16 }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)', marginBottom: 10 }}>Create API Token</div>
          <div style={{ display: 'flex', gap: 8 }}>
            <HoloInput
              style={{ flex: 1 }}
              placeholder="Token name"
              value={name}
              onChange={e => setName(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && create()}
            />
            <HoloButton variant="primary" icon={<Plus size={14} />} onClick={create} disabled={creating || !name.trim()}>
              {creating ? 'Creating…' : 'Create'}
            </HoloButton>
          </div>
        </div>

        {newToken && (
          <div className="holo-card" style={{ padding: 16, background: 'rgba(94,255,184,0.08)', border: '1px solid rgba(94,255,184,0.25)' }}>
            <div style={{ fontSize: 13, color: 'var(--holo-green)', fontWeight: 600, marginBottom: 8 }}>
              Token created — copy it now, it won't be shown again
            </div>
            <code style={{ ...S.mono, fontSize: 12, background: 'rgba(0,0,0,0.3)', padding: '8px 12px', borderRadius: 8, display: 'block', wordBreak: 'break-all' as const, color: 'var(--holo-a)' }}>
              {newToken.token}
            </code>
            <HoloButton style={{ marginTop: 8 }} onClick={() => setNewToken(null)}>Dismiss</HoloButton>
          </div>
        )}

        <div className="holo-card" style={{ padding: 16 }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)', marginBottom: 10 }}>
            Your API Tokens {tokens.length > 0 && <span style={{ fontWeight: 400, color: 'var(--holo-text-faint)', fontSize: 12 }}>({tokens.length})</span>}
          </div>
          {isLoading
            ? <div style={S.empty}>Loading…</div>
            : tokens.length === 0
              ? <div style={S.empty}>No tokens yet</div>
              : (
                <div style={S.tokenList}>
                  {tokens.map(t => (
                    <div key={t.id} style={S.row}>
                      <Key size={13} style={{ color: 'var(--holo-a)', flexShrink: 0, marginTop: 2 }} />
                      <div style={S.rowMeta}>
                        <div style={S.rowName}>{t.name}</div>
                        <div style={S.rowDates}>
                          Created {new Date(t.createdAt).toLocaleDateString()}
                          {t.lastUsedAt && ` · Last used ${new Date(t.lastUsedAt).toLocaleDateString()}`}
                          {t.expiresAt && ` · Expires ${new Date(t.expiresAt).toLocaleDateString()}`}
                        </div>
                      </div>
                      <HoloButton variant="danger" icon={<Trash2 size={13} />} onClick={() => del.mutate(t.id)} style={{ flexShrink: 0 }} />
                    </div>
                  ))}
                </div>
              )
          }
        </div>
      </div>
    </HoloModal>
  )
}

export default function Layout() {
  const { user, logout, isAdmin, isOIDC } = useAuthStore()
  const admin = isAdmin()
  const [profileOpen, setProfileOpen] = useState(false)

  return (
    <HoloApp>
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
                style={{ background: 'rgba(124,92,255,0.12)', border: '1px solid rgba(124,92,255,0.25)', borderRadius: 7, padding: '5px 7px', cursor: 'pointer', color: 'var(--holo-a)', display: 'flex', alignItems: 'center', flexShrink: 0 }}
              >
                <Key size={14} />
              </button>
            </div>
          )}
          <button
            className={`${styles.navBtn} ${styles.danger}`}
            onClick={async () => {
              if (isOIDC()) {
                try {
                  const res = await apiClient.get('/api/v1/auth/oidc/logout')
                  logout()
                  window.location.href = res.data.logout_url
                } catch {
                  logout()
                }
              } else {
                logout()
              }
            }}
          >
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
    </HoloApp>
  )
}
