import { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuthStore } from '@/store/authStore'

/**
 * Finishes the SAML authentication flow.
 * Backend redirects here with fragment: #token=<JWT>&return_to=<path>.
 * We read the token from the URL fragment (never sent to server, not in
 * Referer, not in access logs), store it in localStorage, scrub the URL
 * via history.replaceState, then re-hydrate authStore via /api/v1/me.
 */
export default function SAMLCallbackPage() {
  const navigate = useNavigate()
  const init = useAuthStore(s => s.init)

  useEffect(() => {
    const hash = new URLSearchParams(window.location.hash.slice(1))
    const token = hash.get('token')
    const returnTo = hash.get('return_to') || '/'

    if (!token) {
      navigate('/login?saml_error=missing+token', { replace: true })
      return
    }

    localStorage.setItem('nexspence_token', token)
    // Remove fragment from URL so browser history / back-button don't expose it.
    window.history.replaceState(null, '', returnTo)

    // authStore.init() early-returns if user is already set; force a re-hydrate
    // by setting token in state first so init() fetches /api/v1/me.
    useAuthStore.setState({ token, user: null })
    init()
      .then(() => navigate(returnTo, { replace: true }))
      .catch(() =>
        navigate('/login?saml_error=session+init+failed', { replace: true })
      )
  }, [init, navigate])

  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        minHeight: '100vh',
        color: '#dbe4f2',
        background: '#070b14',
      }}
    >
      Finishing sign-in…
    </div>
  )
}
