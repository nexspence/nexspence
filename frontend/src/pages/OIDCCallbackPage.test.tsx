import { describe, it, expect, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { Routes, Route } from 'react-router-dom'
import OIDCCallbackPage from './OIDCCallbackPage'
import { renderWithProviders, seedAuthAsGuest } from '@/test/renderUtils'
import { useAuthStore } from '@/store/authStore'
import { fixtures } from '@/test/fixtures'
import { server } from '@/test/msw/server'

function renderCallback() {
  return renderWithProviders(
    <Routes>
      <Route path="/oidc/callback" element={<OIDCCallbackPage />} />
      <Route path="/repositories" element={<div>Repositories page</div>} />
      <Route path="/" element={<div>Home page</div>} />
      <Route path="/login" element={<div>Login page</div>} />
    </Routes>,
    { routerProps: { initialEntries: ['/oidc/callback'] } }
  )
}

describe('OIDCCallbackPage', () => {
  beforeEach(() => {
    seedAuthAsGuest()
    Object.assign(window.location, { hash: '', href: 'http://localhost/' })
  })

  it('shows the finishing-sign-in spinner while init is pending', async () => {
    let release: () => void = () => {}
    const gate = new Promise<void>((r) => { release = r })
    server.use(
      http.get('/api/v1/me', async () => {
        await gate
        return HttpResponse.json(fixtures.user())
      })
    )
    window.location.hash = '#token=jwt-abc&return_to=/repositories'
    renderCallback()
    expect(screen.getByText('Finishing sign-in…')).toBeInTheDocument()
    release()
    await screen.findByText('Repositories page')
  })

  it('stores the token, hydrates the user, and navigates to return_to', async () => {
    server.use(http.get('/api/v1/me', () => HttpResponse.json(fixtures.user())))
    window.location.hash = '#token=jwt-abc&return_to=/repositories'
    renderCallback()
    expect(await screen.findByText('Repositories page')).toBeInTheDocument()
    expect(localStorage.getItem('nexspence_token')).toBe('jwt-abc')
    expect(useAuthStore.getState().user?.username).toBe('admin')
  })

  it('defaults to / when no return_to is provided', async () => {
    server.use(http.get('/api/v1/me', () => HttpResponse.json(fixtures.user())))
    window.location.hash = '#token=jwt-xyz'
    renderCallback()
    expect(await screen.findByText('Home page')).toBeInTheDocument()
  })

  it('redirects to /login when the token is missing', async () => {
    window.location.hash = '#return_to=/repositories'
    renderCallback()
    expect(await screen.findByText('Login page')).toBeInTheDocument()
    expect(localStorage.getItem('nexspence_token')).toBeNull()
  })

  it('redirects to /login when session init rejects', async () => {
    // init() swallows /api/v1/me errors internally, so to exercise the
    // .catch() branch we make the store's init reject directly.
    const realInit = useAuthStore.getState().init
    useAuthStore.setState({ init: () => Promise.reject(new Error('boom')) })
    window.location.hash = '#token=bad-token&return_to=/repositories'
    renderCallback()
    expect(await screen.findByText('Login page')).toBeInTheDocument()
    useAuthStore.setState({ init: realInit })
  })
})
