import { describe, it, expect, beforeEach, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { Routes, Route } from 'react-router-dom'
import Layout from './Layout'
import { renderWithProviders, seedAuthAsAdmin, seedAuthAsGuest } from '@/test/renderUtils'
import { useAuthStore } from '@/store/authStore'
import { fixtures } from '@/test/fixtures'
import { server } from '@/test/msw/server'

function renderLayout() {
  return renderWithProviders(
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<div>child content</div>} />
      </Route>
    </Routes>,
    { routerProps: { initialEntries: ['/'] } }
  )
}

describe('Layout', () => {
  beforeEach(() => {
    server.use(http.get('/api/v1/tokens', () => HttpResponse.json([])))
    seedAuthAsAdmin()
  })

  it('renders child content and the primary nav', () => {
    renderLayout()
    expect(screen.getByText('child content')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Repositories/ })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Browse/ })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Search/ })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Documentation/ })).toBeInTheDocument()
  })

  it('shows the admin-only System nav for admins', () => {
    renderLayout()
    expect(screen.getByRole('link', { name: /Security/ })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /System Admin/ })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Audit Log/ })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Cleanup Policies/ })).toBeInTheDocument()
  })

  it('hides the System nav for non-admin users', () => {
    useAuthStore.setState({
      token: 'tok',
      user: fixtures.user({ roles: ['viewer'] }) as never,
    })
    renderLayout()
    expect(screen.queryByRole('link', { name: /System Admin/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /Audit Log/ })).not.toBeInTheDocument()
  })

  it('shows the username and the system version', async () => {
    renderLayout()
    expect(screen.getByText('Admin')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText(/Nexspence v1\.9\.0/)).toBeInTheDocument())
  })

  it('toggles sidebar collapse and persists to localStorage', async () => {
    renderLayout()
    const collapseBtn = screen.getByTitle('Collapse sidebar')
    await userEvent.click(collapseBtn)
    expect(localStorage.getItem('sidebar-collapsed')).toBe('true')
    expect(screen.getByTitle('Expand sidebar')).toBeInTheDocument()
  })

  it('opens the Profile modal and can dismiss it', async () => {
    renderLayout()
    await userEvent.click(screen.getByTitle('API Tokens & Profile'))
    expect(await screen.findByText(/Profile — admin/)).toBeInTheDocument()
    expect(screen.getByText('Create API Token')).toBeInTheDocument()
    expect(await screen.findByText('No tokens yet')).toBeInTheDocument()
    // Close via the X button in the modal header.
    const dialog = screen.getByText(/Profile — admin/).closest('.holo-modal') as HTMLElement
    const closeBtn = within(dialog).getAllByRole('button')[0]
    await userEvent.click(closeBtn)
    await waitFor(() => expect(screen.queryByText(/Profile — admin/)).not.toBeInTheDocument())
  })

  it('logs out when clicking Sign Out (local auth)', async () => {
    // Replace the real logout (which navigates via window.location) with a stub
    // so it does not corrupt window.location.href for later tests.
    const logoutStub = vi.fn()
    const realLogout = useAuthStore.getState().logout
    useAuthStore.setState({ logout: logoutStub })
    renderLayout()
    await userEvent.click(screen.getByTitle('Sign Out'))
    expect(logoutStub).toHaveBeenCalled()
    useAuthStore.setState({ logout: realLogout })
  })

  it('uses the OIDC logout flow for OIDC sessions', async () => {
    // Build a JWT whose payload has auth_method=oidc so isOIDC() returns true.
    const payload = btoa(JSON.stringify({ sub: 'user-1', auth_method: 'oidc' }))
    const oidcToken = `eyJhbGciOiJIUzI1NiJ9.${payload}.sig`
    const logoutStub = vi.fn()
    const realLogout = useAuthStore.getState().logout
    useAuthStore.setState({
      token: oidcToken,
      user: fixtures.user() as never,
      logout: logoutStub,
    })
    server.use(
      http.get('/api/v1/auth/oidc/logout', () =>
        HttpResponse.json({ logout_url: 'https://idp.example.com/logout' })
      )
    )
    renderLayout()
    await userEvent.click(screen.getByTitle('Sign Out'))
    await waitFor(() => expect(logoutStub).toHaveBeenCalled())
    useAuthStore.setState({ logout: realLogout })
  })

  it('does not render the command bar when there is no user', () => {
    seedAuthAsGuest()
    renderLayout()
    expect(screen.queryByTitle('Sign Out')).not.toBeInTheDocument()
    expect(screen.queryByTitle('Profile')).not.toBeInTheDocument()
  })
})

describe('Layout ProfileModal token management', () => {
  beforeEach(() => seedAuthAsAdmin())

  it('lists existing tokens', async () => {
    server.use(
      http.get('/api/v1/tokens', () =>
        HttpResponse.json([
          { id: 't1', name: 'ci-deploy', createdAt: '2026-01-01T00:00:00Z', lastUsedAt: '2026-02-01T00:00:00Z', expiresAt: '2027-01-01T00:00:00Z' },
        ])
      )
    )
    renderLayout()
    await userEvent.click(screen.getByTitle('API Tokens & Profile'))
    expect(await screen.findByText('ci-deploy')).toBeInTheDocument()
    expect(screen.getByText(/Created/)).toBeInTheDocument()
  })

  it('validates expiry over the maximum', async () => {
    server.use(http.get('/api/v1/tokens', () => HttpResponse.json([])))
    renderLayout()
    await userEvent.click(screen.getByTitle('API Tokens & Profile'))
    await screen.findByText('Create API Token')
    await userEvent.type(screen.getByLabelText('Token name'), 'mytoken')
    await userEvent.type(screen.getByLabelText('Expiry (days, optional)'), '9999')
    expect(await screen.findByText(/Expiry exceeds the maximum of 365 days/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Create token/ })).toBeDisabled()
  })

  it('creates a token and shows it once with a copy control', async () => {
    server.use(
      http.get('/api/v1/tokens', () => HttpResponse.json([])),
      http.post('/api/v1/tokens', () =>
        HttpResponse.json({ id: 'new1', name: 'fresh', token: 'nxs_secretvalue' }, { status: 201 })
      )
    )
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
    renderLayout()
    await userEvent.click(screen.getByTitle('API Tokens & Profile'))
    await screen.findByText('Create API Token')
    await userEvent.type(screen.getByLabelText('Token name'), 'fresh')
    await userEvent.click(screen.getByRole('button', { name: /Create token/ }))
    expect(await screen.findByText('nxs_secretvalue')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /Copy/ }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledWith('nxs_secretvalue'))
    // Dismiss the new-token card.
    await userEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
    await waitFor(() => expect(screen.queryByText('nxs_secretvalue')).not.toBeInTheDocument())
  })

  it('deletes a token', async () => {
    const delHandler = vi.fn(() => new HttpResponse(null, { status: 204 }))
    server.use(
      http.get('/api/v1/tokens', () =>
        HttpResponse.json([{ id: 't9', name: 'old-token', createdAt: '2026-01-01T00:00:00Z' }])
      ),
      http.delete('/api/v1/tokens/:id', delHandler)
    )
    renderLayout()
    await userEvent.click(screen.getByTitle('API Tokens & Profile'))
    await screen.findByText('old-token')
    await userEvent.click(screen.getByRole('button', { name: 'Delete token' }))
    await waitFor(() => expect(delHandler).toHaveBeenCalled())
  })
})
