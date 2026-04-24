import { useState, useEffect, FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { KeyRound } from 'lucide-react'
import { useAuthStore } from '@/store/authStore'
import { nexusApi, type AuthConfig } from '@/api/client'
import styles from './LoginPage.module.css'
import logo from '@/assets/logo.png'

export default function LoginPage() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [authConfig, setAuthConfig] = useState<AuthConfig | null>(null)
  const [oidcError, setOidcError] = useState<string | null>(null)
  const { login } = useAuthStore()
  const navigate = useNavigate()

  useEffect(() => {
    // Read OIDC redirect-error from URL.
    const params = new URLSearchParams(window.location.search)
    const err = params.get('oidc_error')
    if (err) setOidcError(err)
    nexusApi.getAuthConfig().then(setAuthConfig).catch(() => setAuthConfig(null))
  }, [])

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await login(username, password)
      navigate('/repositories', { replace: true })
    } catch {
      setError('Invalid username or password')
    } finally {
      setLoading(false)
    }
  }

  const handleOIDC = () => {
    if (!authConfig?.oidcLoginUrl) return
    const returnTo = encodeURIComponent('/repositories')
    window.location.href = `${authConfig.oidcLoginUrl}?return_to=${returnTo}`
  }

  return (
    <div className={styles.container}>
      <div className={styles.card}>
        <div className={styles.logo}>
          <img src={logo} alt="Nexspence" className={styles.logoImg} />
        </div>

        <form onSubmit={handleSubmit} className={styles.form}>
          <div className={styles.field}>
            <label className={styles.label}>Username</label>
            <input
              className={styles.input}
              type="text"
              value={username}
              onChange={e => setUsername(e.target.value)}
              autoComplete="username"
              autoFocus
              required
            />
          </div>
          <div className={styles.field}>
            <label className={styles.label}>Password</label>
            <input
              className={styles.input}
              type="password"
              value={password}
              onChange={e => setPassword(e.target.value)}
              autoComplete="current-password"
              required
            />
          </div>

          {error && <div className={styles.error}>{error}</div>}
          {oidcError && (
            <div className={styles.error} role="alert">
              SSO login failed: {oidcError}
            </div>
          )}

          <button className={styles.button} type="submit" disabled={loading}>
            {loading ? 'Signing in…' : 'Sign in'}
          </button>

          {authConfig?.oidcEnabled && (
            <>
              <div className={styles.divider}>or</div>
              <button
                type="button"
                className={styles.oidcButton}
                onClick={handleOIDC}
              >
                <KeyRound size={16} /> Sign in with {authConfig.oidcDisplayName}
              </button>
            </>
          )}
        </form>
      </div>
    </div>
  )
}
