import { describe, it, expect, beforeEach, vi } from 'vitest'
import { screen, waitFor, within, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { useLocation } from 'react-router-dom'
import RepositoriesPage from './RepositoriesPage'
import { renderWithProviders, seedAuthAsAdmin, seedAuthAsGuest } from '@/test/renderUtils'
import { server } from '@/test/msw/server'
import { fixtures } from '@/test/fixtures'

// Set Me Up (#534): an explicit action per row. The row click itself keeps
// navigating to Browse, so the action must not leak its click to the row.

const repoList = [
  fixtures.repository({ id: 'r1', name: 'maven-hosted', format: 'maven2', type: 'hosted', online: true }),
  fixtures.repository({ id: 'r2', name: 'npm-proxy', format: 'npm', type: 'proxy', online: true }),
]

function LocationProbe() {
  const loc = useLocation()
  return <div data-testid="location">{loc.pathname + loc.search}</div>
}

function renderPage() {
  return renderWithProviders(
    <>
      <RepositoriesPage />
      <LocationProbe />
    </>,
    { routerProps: { initialEntries: ['/repositories'] } },
  )
}

describe('RepositoriesPage — Set me up', () => {
  beforeEach(() => {
    seedAuthAsAdmin()
    Object.assign(window.location, { origin: 'http://localhost' })
    server.use(
      http.get('/service/rest/v1/repositories', () => HttpResponse.json(repoList)),
      http.get('/service/rest/v1/blobstores', () => HttpResponse.json([])),
      http.get('/service/rest/v1/cleanup-policies', () => HttpResponse.json([])),
      http.get('/service/rest/v1/routing-rules', () => HttpResponse.json([])),
      http.get('/api/v1/repositories/:name/quota', () =>
        HttpResponse.json({ usedBytes: 1, quotaBytes: null, percentUsed: null }),
      ),
    )
  })

  it('row click still navigates to Browse', async () => {
    renderPage()
    fireEvent.click(await screen.findByText('maven-hosted'))
    expect(screen.getByTestId('location')).toHaveTextContent('/browse?repo=maven-hosted')
  })

  it('opens the dialog for that repository without navigating', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('npm-proxy')
    await user.click(screen.getByRole('button', { name: 'Set me up: npm-proxy' }))

    const dialog = await screen.findByRole('dialog', { name: 'Set me up: npm-proxy' })
    expect(screen.getByTestId('location')).toHaveTextContent('/repositories')
    expect(within(dialog).getByText('npm · proxy · npm-proxy')).toBeInTheDocument()
    expect(within(dialog).getByRole('tab', { name: 'npm' })).toHaveAttribute('aria-selected', 'true')
    expect(within(dialog).getAllByText(/http:\/\/localhost\/repository\/npm-proxy\//).length).toBeGreaterThan(0)
    // A proxy gets no publish instructions, only a pointer to a hosted repository.
    expect(within(dialog).queryByText(/npm publish/)).not.toBeInTheDocument()
    expect(within(dialog).getByRole('note')).toHaveTextContent(/hosted/)
  })

  it('pressing Enter on the action does not trigger the row', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('maven-hosted')
    screen.getByRole('button', { name: 'Set me up: maven-hosted' }).focus()
    await user.keyboard('{Enter}')
    expect(await screen.findByRole('dialog', { name: 'Set me up: maven-hosted' })).toBeInTheDocument()
    expect(screen.getByTestId('location')).toHaveTextContent('/repositories')
  })

  it('is available to users who are not admins', async () => {
    seedAuthAsGuest()
    renderPage()
    await screen.findByText('maven-hosted')
    expect(screen.getByRole('button', { name: 'Set me up: maven-hosted' })).toBeInTheDocument()
    expect(screen.queryByTitle('Settings')).not.toBeInTheDocument()
  })

  it('closes on Escape and returns focus to the action', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('maven-hosted')
    const action = screen.getByRole('button', { name: 'Set me up: maven-hosted' })
    await user.click(action)
    const dialog = await screen.findByRole('dialog')
    // Focus starts inside the dialog, on the username field.
    expect(within(dialog).getByLabelText('Username')).toHaveFocus()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(action).toHaveFocus()
  })

  it('opens from the repository settings dialog', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('maven-hosted')
    const row = screen.getByText('maven-hosted').closest('[class*="row"]') as HTMLElement
    await user.click(within(row).getByTitle('Settings'))
    await screen.findByText('Repository settings')
    await user.click(screen.getByRole('button', { name: 'Set me up' }))
    expect(screen.queryByText('Repository settings')).not.toBeInTheDocument()
    const dialog = await screen.findByRole('dialog', { name: 'Set me up: maven-hosted' })
    // A hosted Maven repository gets deploy instructions.
    expect(within(dialog).getAllByText(/distributionManagement/).length).toBeGreaterThan(0)
  })

  it('copies a snippet with its copy button', async () => {
    // After userEvent.setup(): it installs its own clipboard shim.
    const user = userEvent.setup()
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    renderPage()
    await screen.findByText('maven-hosted')
    await user.click(screen.getByRole('button', { name: 'Set me up: maven-hosted' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Copy repository URL' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('http://localhost/repository/maven-hosted/'))
    expect(await within(dialog).findByText('Copied!')).toBeInTheDocument()
  })
})
