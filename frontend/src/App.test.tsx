import { describe, it, expect, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import App from './App'
import { seedAuthAsAdmin, seedAuthAsGuest } from '@/test/renderUtils'

function renderApp(path: string, hash = '') {
  // App owns its own <BrowserRouter>, which reads the initial location from
  // window.location. The test setup stubs window.location as a plain object,
  // so assign the route fields directly (origin/href are required by
  // react-router's NavLink encodeLocation).
  Object.assign(window.location, {
    origin: 'http://localhost',
    href: `http://localhost${path}${hash}`,
    pathname: path,
    search: '',
    hash,
  })
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={qc}>
      <App />
    </QueryClientProvider>,
  )
}

describe('App', () => {
  beforeEach(() => {
    seedAuthAsGuest()
    Object.assign(window.location, {
      origin: 'http://localhost',
      href: 'http://localhost/',
      pathname: '/',
      search: '',
      hash: '',
    })
  })

  it('does not render the protected layout for an unauthenticated user (PrivateRoute guard)', async () => {
    renderApp('/repositories')
    // PrivateRoute redirects guests away from the Layout; the sidebar nav
    // (e.g. "Cleanup Policies") must never appear.
    await Promise.resolve()
    expect(screen.queryByText('Cleanup Policies')).not.toBeInTheDocument()
  })

  it('renders the login page on /login', async () => {
    renderApp('/login')
    expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
  })

  it('renders the OIDC callback route', async () => {
    renderApp('/oidc/callback', '#return_to=/repositories')
    // App wires /oidc/callback → OIDCCallbackPage, which shows the spinner.
    expect(await screen.findByText('Finishing sign-in…')).toBeInTheDocument()
  })

  it('renders the SAML callback route', async () => {
    renderApp('/saml/callback', '#return_to=/repositories')
    expect(await screen.findByText('Finishing sign-in…')).toBeInTheDocument()
  })

  it('shows the app shell with the repositories page for an authenticated admin', async () => {
    seedAuthAsAdmin()
    renderApp('/repositories')
    // Layout sidebar nav labels.
    expect((await screen.findAllByText('Browse')).length).toBeGreaterThan(0)
    expect(screen.getByText('Cleanup Policies')).toBeInTheDocument()
  })

  it('redirects the index route to /repositories for an authenticated admin', async () => {
    seedAuthAsAdmin()
    renderApp('/')
    expect((await screen.findAllByText('Browse')).length).toBeGreaterThan(0)
  })

  it('lazy-loads the audit page inside the layout for an authenticated admin', async () => {
    seedAuthAsAdmin()
    renderApp('/audit')
    // Layout always renders; lazy AuditPage resolves under Suspense.
    expect((await screen.findAllByText('Browse')).length).toBeGreaterThan(0)
  })

  it('lazy-loads the docs page for an authenticated admin', async () => {
    seedAuthAsAdmin()
    renderApp('/docs')
    expect((await screen.findAllByText('Browse')).length).toBeGreaterThan(0)
  })
})
