import { useQuery } from '@tanstack/react-query'
import { Shield, RefreshCw, Lock, Key, Users } from 'lucide-react'
import { nexusApi } from '@/api/client'

interface Role {
  id: string
  name: string
  description: string
  privileges: string[]
  roles: string[]
  readOnly: boolean
  source?: string
}

const S = {
  page:    { padding: 24, display: 'flex', flexDirection: 'column' as const, gap: 20 },
  header:  { display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', flexWrap: 'wrap' as const, gap: 12 },
  title:   { fontSize: 20, fontWeight: 700, color: '#dbeafe', margin: '0 0 4px' },
  subtitle:{ fontSize: 13, color: 'rgba(229,231,235,0.5)', margin: 0 },
  iconBtn: { background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: 8, color: 'rgba(229,231,235,0.7)', cursor: 'pointer', display: 'flex', alignItems: 'center' },
  section: { display: 'flex', flexDirection: 'column' as const, gap: 12 },
  sectionTitle:{ fontSize: 15, fontWeight: 700, color: '#dbeafe', display: 'flex', alignItems: 'center', gap: 8 },
  grid:    { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))', gap: 12 },
  card:    { background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 12, padding: '16px 18px', display: 'flex', flexDirection: 'column' as const, gap: 10 },
  cardTop: { display: 'flex', alignItems: 'center', gap: 8 },
  roleIcon:{ width: 32, height: 32, borderRadius: 8, background: 'rgba(59,130,246,0.12)', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 },
  roleName:{ fontSize: 14, fontWeight: 600, color: '#dbeafe', flex: 1 },
  badge:   (color: string) => ({ fontSize: 11, fontWeight: 600 as const, padding: '2px 7px', borderRadius: 4, background: color + '20', color }),
  desc:    { fontSize: 12, color: 'rgba(229,231,235,0.5)', lineHeight: 1.5 },
  privs:   { display: 'flex', flexWrap: 'wrap' as const, gap: 5 },
  privBadge:{ fontSize: 10, padding: '2px 6px', borderRadius: 4, background: 'rgba(99,102,241,0.12)', color: '#a5b4fc', fontFamily: 'monospace' as const },
  infoCard:{ background: 'rgba(255,255,255,0.02)', border: '1px solid rgba(255,255,255,0.07)', borderRadius: 12, padding: '14px 18px', display: 'flex', gap: 12, alignItems: 'flex-start' },
  infoIcon:{ width: 36, height: 36, borderRadius: 8, background: 'rgba(59,130,246,0.1)', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 },
  infoTitle:{ fontSize: 13, fontWeight: 600, color: '#dbeafe', marginBottom: 4 },
  infoDesc:{ fontSize: 12, color: 'rgba(229,231,235,0.5)', lineHeight: 1.5, margin: 0 },
  infoStub:{ fontSize: 11, fontWeight: 600, padding: '2px 7px', borderRadius: 4, background: 'rgba(245,158,11,0.12)', color: '#f59e0b', marginTop: 8, display: 'inline-block' },
  empty:   { display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'rgba(229,231,235,0.35)', fontSize: 14, padding: 24 },
}

const STUB_ITEMS = [
  {
    icon: Lock,
    title: 'SSL/TLS Certificates',
    desc: 'Manage trusted CA certificates for outbound proxy connections. Import PEM certificates for custom certificate chains.',
  },
  {
    icon: Key,
    title: 'Authentication Realms',
    desc: 'Configure authentication realms: Local (active), LDAP (planned), NuGet API Key, npm Bearer Token.',
  },
  {
    icon: Users,
    title: 'Anonymous Access',
    desc: 'Allow unauthenticated read access to public repositories. Configure via config.yaml (auth.anonymous_enabled).',
  },
]

export default function SecurityPage() {
  const { data: roles = [], isLoading, refetch } = useQuery<Role[]>({
    queryKey: ['roles'],
    queryFn: () => nexusApi.listRoles().then(r => r.data),
  })

  return (
    <div style={S.page}>
      <div style={S.header}>
        <div>
          <h1 style={S.title}>Security &amp; Roles</h1>
          <p style={S.subtitle}>User roles, privileges and authentication configuration</p>
        </div>
        <button style={S.iconBtn} onClick={() => refetch()} title="Refresh"><RefreshCw size={16} /></button>
      </div>

      {/* Roles section */}
      <div style={S.section}>
        <div style={S.sectionTitle}>
          <Shield size={16} style={{ color: '#3b82f6' }} />
          Roles
        </div>

        {isLoading ? (
          <div style={S.empty}>Loading…</div>
        ) : roles.length === 0 ? (
          <div style={S.empty}>No roles found</div>
        ) : (
          <div style={S.grid}>
            {roles.map(role => (
              <div key={role.id} style={S.card}>
                <div style={S.cardTop}>
                  <div style={S.roleIcon}>
                    <Shield size={16} style={{ color: '#3b82f6' }} />
                  </div>
                  <span style={S.roleName}>{role.name}</span>
                  {role.readOnly && <span style={S.badge('#6b7280')}>built-in</span>}
                  {role.source && role.source !== 'default' && (
                    <span style={S.badge('#a78bfa')}>{role.source}</span>
                  )}
                </div>

                {role.description && (
                  <p style={S.desc}>{role.description}</p>
                )}

                {role.privileges && role.privileges.length > 0 && (
                  <div style={S.privs}>
                    {role.privileges.slice(0, 8).map(p => (
                      <span key={p} style={S.privBadge}>{p}</span>
                    ))}
                    {role.privileges.length > 8 && (
                      <span style={{ ...S.privBadge, opacity: 0.6 }}>+{role.privileges.length - 8} more</span>
                    )}
                  </div>
                )}

                {role.roles && role.roles.length > 0 && (
                  <div style={{ fontSize: 11, color: 'rgba(229,231,235,0.4)' }}>
                    Inherits: {role.roles.join(', ')}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Configuration stubs */}
      <div style={S.section}>
        <div style={S.sectionTitle}>
          <Lock size={16} style={{ color: '#3b82f6' }} />
          Configuration
        </div>
        <div style={S.grid}>
          {STUB_ITEMS.map(({ icon: Icon, title, desc }) => (
            <div key={title} style={S.infoCard}>
              <div style={S.infoIcon}>
                <Icon size={17} style={{ color: '#3b82f6' }} />
              </div>
              <div>
                <div style={S.infoTitle}>{title}</div>
                <p style={S.infoDesc}>{desc}</p>
                <span style={S.infoStub}>config / roadmap</span>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
