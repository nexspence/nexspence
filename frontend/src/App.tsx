import { useEffect, Suspense, lazy } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import Layout from '@/components/Layout'
import LoginPage from '@/pages/LoginPage'
import RepositoriesPage from '@/pages/RepositoriesPage'
import OIDCCallbackPage from '@/pages/OIDCCallbackPage'
import SAMLCallbackPage from '@/pages/SAMLCallbackPage'
import { useAuthStore } from '@/store/authStore'

// Lazy-load large page components for code splitting
const BrowsePage = lazy(() => import('@/pages/BrowsePage'))
const SearchPage = lazy(() => import('@/pages/SearchPage'))
const UsersPage = lazy(() => import('@/pages/UsersPage'))
const CleanupPage = lazy(() => import('@/pages/CleanupPage'))
const AdminPage = lazy(() => import('@/pages/AdminPage'))
const SecurityPage = lazy(() => import('@/pages/SecurityPage'))
const AuditPage = lazy(() => import('@/pages/AuditPage'))
const DocsPage = lazy(() => import('@/pages/DocsPage'))

function PageSkeleton() {
  return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100vh', color: '#64748b', fontSize: '14px' }}>
      Loading…
    </div>
  )
}

function PrivateRoute({ children }: { children: React.ReactNode }) {
  const token = useAuthStore(s => s.token)
  if (!token) return <Navigate to="/login" replace />
  return <>{children}</>
}

export default function App() {
  const init = useAuthStore(s => s.init)
  useEffect(() => { init() }, [init])

  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/oidc/callback" element={<OIDCCallbackPage />} />
        <Route path="/saml/callback" element={<SAMLCallbackPage />} />
        <Route
          path="/"
          element={
            <PrivateRoute>
              <Layout />
            </PrivateRoute>
          }
        >
          <Route index element={<Navigate to="/repositories" replace />} />
          <Route path="repositories" element={<RepositoriesPage />} />
          <Route
            path="browse"
            element={
              <Suspense fallback={<PageSkeleton />}>
                <BrowsePage />
              </Suspense>
            }
          />
          <Route
            path="search"
            element={
              <Suspense fallback={<PageSkeleton />}>
                <SearchPage />
              </Suspense>
            }
          />
          <Route
            path="users"
            element={
              <Suspense fallback={<PageSkeleton />}>
                <UsersPage />
              </Suspense>
            }
          />
          <Route
            path="cleanup"
            element={
              <Suspense fallback={<PageSkeleton />}>
                <CleanupPage />
              </Suspense>
            }
          />
          <Route
            path="admin"
            element={
              <Suspense fallback={<PageSkeleton />}>
                <AdminPage />
              </Suspense>
            }
          />
          <Route path="migration" element={<Navigate to="/admin?tab=migration" replace />} />
          <Route
            path="security"
            element={
              <Suspense fallback={<PageSkeleton />}>
                <SecurityPage />
              </Suspense>
            }
          />
          <Route
            path="audit"
            element={
              <Suspense fallback={<PageSkeleton />}>
                <AuditPage />
              </Suspense>
            }
          />
          <Route path="monitoring" element={<Navigate to="/admin?tab=monitoring" replace />} />
          <Route
            path="docs"
            element={
              <Suspense fallback={<PageSkeleton />}>
                <DocsPage />
              </Suspense>
            }
          />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
