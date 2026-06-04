import { describe, it, expect, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { Routes, Route } from 'react-router-dom'
import LoginPage from './LoginPage'
import { renderWithProviders, seedAuthAsGuest } from '@/test/renderUtils'
import { fixtures } from '@/test/fixtures'
import { server } from '@/test/msw/server'

function renderLogin() {
  return renderWithProviders(
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/repositories" element={<div>Repositories page</div>} />
    </Routes>,
    { routerProps: { initialEntries: ['/login'] } }
  )
}

describe('LoginPage', () => {
  beforeEach(() => {
    seedAuthAsGuest()
    // Reset stubbed location each test.
    Object.assign(window.location, { href: 'http://localhost/', search: '' })
  })

  it('renders username and password fields plus the sign-in button', () => {
    renderLogin()
    expect(screen.getByLabelText('Username')).toBeInTheDocument()
    expect(screen.getByLabelText('Password')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument()
  })

  it('logs in successfully and navigates to /repositories', async () => {
    const user = userEvent.setup()
    renderLogin()
    await user.type(screen.getByLabelText('Username'), 'admin')
    await user.type(screen.getByLabelText('Password'), 'admin123')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByText('Repositories page')).toBeInTheDocument()
  })

  it('shows an error alert on invalid credentials', async () => {
    server.use(
      http.post('/api/v1/login', () =>
        HttpResponse.json({ error: 'bad creds' }, { status: 401 })
      )
    )
    const user = userEvent.setup()
    renderLogin()
    await user.type(screen.getByLabelText('Username'), 'admin')
    await user.type(screen.getByLabelText('Password'), 'wrong')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Invalid username or password')
  })

  it('shows the loading state while signing in', async () => {
    let release: () => void = () => {}
    const gate = new Promise<void>((r) => { release = r })
    server.use(
      http.post('/api/v1/login', async () => {
        await gate
        return HttpResponse.json(fixtures.loginResponse())
      })
    )
    const user = userEvent.setup()
    renderLogin()
    await user.type(screen.getByLabelText('Username'), 'admin')
    await user.type(screen.getByLabelText('Password'), 'admin123')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByRole('button', { name: 'Signing in…' })).toBeDisabled()
    release()
    expect(await screen.findByText('Repositories page')).toBeInTheDocument()
  })

  it('renders the OIDC button when oidcEnabled and triggers redirect', async () => {
    server.use(
      http.get('/api/v1/auth/config', () =>
        HttpResponse.json(
          fixtures.authConfig({
            oidcEnabled: true,
            oidcDisplayName: 'Keycloak',
            oidcLoginUrl: '/api/v1/auth/oidc/login',
          })
        )
      )
    )
    const user = userEvent.setup()
    renderLogin()
    const oidcBtn = await screen.findByRole('button', { name: /Sign in with Keycloak/ })
    await user.click(oidcBtn)
    expect(window.location.href).toContain('/api/v1/auth/oidc/login?return_to=')
  })

  it('renders the SAML button when samlEnabled and triggers redirect', async () => {
    server.use(
      http.get('/api/v1/auth/config', () =>
        HttpResponse.json(
          fixtures.authConfig({
            samlEnabled: true,
            samlDisplayName: 'Okta',
            samlLoginUrl: '/api/v1/auth/saml/login',
          })
        )
      )
    )
    const user = userEvent.setup()
    renderLogin()
    const samlBtn = await screen.findByRole('button', { name: /Sign in with Okta/ })
    await user.click(samlBtn)
    expect(window.location.href).toContain('/api/v1/auth/saml/login?return_to=')
  })

  it('surfaces an oidc_error from the URL query string', async () => {
    window.location.search = '?oidc_error=access+denied'
    renderLogin()
    expect(await screen.findByText(/SSO login failed: access denied/)).toBeInTheDocument()
  })

  it('surfaces a saml_error from the URL query string', async () => {
    window.location.search = '?saml_error=assertion+expired'
    renderLogin()
    expect(await screen.findByText(/SSO login failed: assertion expired/)).toBeInTheDocument()
  })

  it('tolerates a failed auth-config fetch', async () => {
    server.use(
      http.get('/api/v1/auth/config', () => HttpResponse.error())
    )
    renderLogin()
    // Form still renders even though auth config could not load.
    expect(await screen.findByLabelText('Username')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Sign in with/ })).not.toBeInTheDocument()
  })

  it('does not redirect when OIDC config has no login url', async () => {
    server.use(
      http.get('/api/v1/auth/config', () =>
        HttpResponse.json(
          fixtures.authConfig({ oidcEnabled: true, oidcLoginUrl: '' })
        )
      )
    )
    const user = userEvent.setup()
    renderLogin()
    const oidcBtn = await screen.findByRole('button', { name: /Sign in with/ })
    await user.click(oidcBtn)
    expect(window.location.href).toBe('http://localhost/')
  })
})
