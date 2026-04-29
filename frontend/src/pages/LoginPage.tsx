import { useState, useEffect, FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { KeyRound } from 'lucide-react'
import { useAuthStore } from '@/store/authStore'
import { nexusApi, type AuthConfig } from '@/api/client'
import { HoloApp, HoloButton, HoloInput } from '@/components/holo'
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
    <HoloApp>
    <div className={styles.container}>
      <div className={styles.card}>
        <div className={styles.logo}>
          <img src={logo} alt="Nexspence" className={styles.logoImg} />
        </div>

        <form onSubmit={handleSubmit} className={styles.form}>
          <div className={styles.field}>
            <label className={styles.label} htmlFor="username">Username</label>
            <HoloInput
              id="username"
              type="text"
              value={username}
              onChange={e => setUsername(e.target.value)}
              autoComplete="username"
              autoFocus
              required
            />
          </div>
          <div className={styles.field}>
            <label className={styles.label} htmlFor="password">Password</label>
            <HoloInput
              id="password"
              type="password"
              value={password}
              onChange={e => setPassword(e.target.value)}
              autoComplete="current-password"
              required
            />
          </div>

          {error && <div className={styles.error} role="alert">{error}</div>}
          {oidcError && (
            <div className={styles.error} role="alert">
              SSO login failed: {oidcError}
            </div>
          )}

          <HoloButton variant="primary" type="submit" disabled={loading} style={{ width: '100%', justifyContent: 'center' }}>
            {loading ? 'Signing in…' : 'Sign in'}
          </HoloButton>

          {authConfig?.oidcEnabled && (
            <>
              <div className={styles.divider}>or</div>
              <HoloButton
                type="button"
                icon={<KeyRound size={16} />}
                onClick={handleOIDC}
                style={{ width: '100%', justifyContent: 'center' }}
              >
                Sign in with {authConfig.oidcDisplayName}
              </HoloButton>
            </>
          )}
        </form>
      </div>
    </div>
    </HoloApp>
  )
}
