import { Outlet, NavLink } from 'react-router-dom'
import {
  Home, Search, FolderOpen, Users, Trash2,
  Settings, Shield, FileText, LogOut,
  ArrowRightLeft,
} from 'lucide-react'
import styles from './Layout.module.css'
import { useAuthStore } from '@/store/authStore'
import logo from '@/assets/logo.png'

const navItems = [
  { to: '/repositories', icon: Home,       label: 'Repositories' },
  { to: '/browse',       icon: FolderOpen,  label: 'Browse' },
  { to: '/search',       icon: Search,      label: 'Search' },
]

const manageItems = [
  { to: '/users',     icon: Users,  label: 'Users & Roles' },
  { to: '/cleanup',   icon: Trash2, label: 'Cleanup Policies' },
  { to: '/migration', icon: ArrowRightLeft, label: 'Migration' },
]

const systemItems = [
  { to: '/admin',    icon: Settings,  label: 'System Admin' },
  { to: '/security', icon: Shield,    label: 'Security & SSL' },
  { to: '/audit',    icon: FileText,  label: 'Audit Log' },
]

export default function Layout() {
  const { user, logout } = useAuthStore()
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

        <hr className={styles.divider} />
        <span className={styles.sectionLabel}>Manage</span>
        <nav className={styles.nav}>
          {manageItems.map(({ to, icon: Icon, label }) => (
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

        {/* Footer */}
        <div className={styles.footer}>
          {user && (
            <div className={styles.userInfo}>
              <span className={styles.userName}>
                {user.firstName || user.username}
              </span>
              <span className={styles.userRole}>
                {user.roles?.includes('nx-admin') ? 'Admin' : 'User'}
              </span>
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
    </div>
  )
}
