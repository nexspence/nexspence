import { useEffect } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import Layout from '@/components/Layout'
import LoginPage from '@/pages/LoginPage'
import RepositoriesPage from '@/pages/RepositoriesPage'
import BrowsePage from '@/pages/BrowsePage'
import SearchPage from '@/pages/SearchPage'
import UsersPage from '@/pages/UsersPage'
import CleanupPage from '@/pages/CleanupPage'
import AdminPage from '@/pages/AdminPage'
import SecurityPage from '@/pages/SecurityPage'
import AuditPage from '@/pages/AuditPage'
import OIDCCallbackPage from '@/pages/OIDCCallbackPage'
import { useAuthStore } from '@/store/authStore'

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
          <Route path="browse" element={<BrowsePage />} />
          <Route path="search" element={<SearchPage />} />
          <Route path="users" element={<UsersPage />} />
          <Route path="cleanup" element={<CleanupPage />} />
          <Route path="admin" element={<AdminPage />} />
          <Route path="migration" element={<Navigate to="/admin?tab=migration" replace />} />
          <Route path="security" element={<SecurityPage />} />
          <Route path="audit" element={<AuditPage />} />
          <Route path="monitoring" element={<Navigate to="/admin?tab=monitoring" replace />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
