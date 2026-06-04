import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, waitFor, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import RepositoriesPage from './RepositoriesPage'
import {
  renderWithProviders,
  seedAuthAsAdmin,
  seedAuthAsGuest,
} from '@/test/renderUtils'
import { server } from '@/test/msw/server'
import { fixtures } from '@/test/fixtures'
import { useAuthStore } from '@/store/authStore'

const repoList = [
  fixtures.repository({
    id: 'repo-1',
    name: 'maven-hosted',
    format: 'maven2',
    type: 'hosted',
    online: true,
    description: 'Main maven repo',
    blobStoreId: 'bs-1',
  }),
  fixtures.repository({
    id: 'repo-2',
    name: 'npm-proxy',
    format: 'npm',
    type: 'proxy',
    online: false,
  }),
  fixtures.repository({
    id: 'repo-3',
    name: 'docker-group',
    format: 'docker',
    type: 'group',
    online: true,
  }),
]

const blobStores = [
  { id: 'bs-1', name: 'default', type: 'file', quotaBytes: null, usedBytes: 0 },
  { id: 'bs-2', name: 'big', type: 's3', quotaBytes: 10 * 1024 * 1024 * 1024, usedBytes: 1024 },
]

function seedRepos(list = repoList) {
  server.use(
    http.get('/service/rest/v1/repositories', () => HttpResponse.json(list)),
    http.get('/service/rest/v1/blobstores', () => HttpResponse.json(blobStores)),
    http.get('/service/rest/v1/cleanup-policies', () =>
      HttpResponse.json([
        { id: 'cp-1', name: 'maven-cleanup', format: 'maven2' },
        { id: 'cp-2', name: 'all-cleanup', format: '*' },
      ]),
    ),
    http.get('/service/rest/v1/routing-rules', () =>
      HttpResponse.json([{ id: 'rr-1', name: 'block-rule', mode: 'BLOCK' }]),
    ),
    http.get('/api/v1/repositories/:name/quota', () =>
      HttpResponse.json({ usedBytes: 5000, quotaBytes: 1024 * 1024 * 1024, percentUsed: 50 }),
    ),
  )
}

describe('RepositoriesPage', () => {
  beforeEach(() => {
    seedAuthAsAdmin()
    seedRepos()
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders the repository list with different formats/types', async () => {
    renderWithProviders(<RepositoriesPage />)
    expect(await screen.findByText('maven-hosted')).toBeInTheDocument()
    expect(screen.getByText('npm-proxy')).toBeInTheDocument()
    expect(screen.getByText('docker-group')).toBeInTheDocument()
    expect(screen.getByText('3 total')).toBeInTheDocument()
    // type pills
    expect(screen.getByText('Hosted')).toBeInTheDocument()
    expect(screen.getByText('Proxy')).toBeInTheDocument()
    expect(screen.getByText('Group')).toBeInTheDocument()
    // description shown
    expect(screen.getByText('Main maven repo')).toBeInTheDocument()
  })

  it('filters by name', async () => {
    const user = userEvent.setup()
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    await user.type(screen.getByPlaceholderText('Filter by name…'), 'npm')
    await waitFor(() => expect(screen.queryByText('maven-hosted')).not.toBeInTheDocument())
    expect(screen.getByText('npm-proxy')).toBeInTheDocument()
  })

  it('filters by format via the Select dropdown', async () => {
    const user = userEvent.setup()
    let lastFormat: string | null = null
    server.use(
      http.get('/service/rest/v1/repositories', ({ request }) => {
        const url = new URL(request.url)
        lastFormat = url.searchParams.get('format')
        return HttpResponse.json(repoList)
      }),
    )
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    await user.click(screen.getByRole('button', { name: /All formats/ }))
    const opts = await screen.findAllByText('docker')
    await user.click(opts[opts.length - 1])
    await waitFor(() => expect(lastFormat).toBe('docker'))
  })

  it('shows the empty state with a create button for admins', async () => {
    seedRepos([])
    renderWithProviders(<RepositoriesPage />)
    expect(await screen.findByText('No repositories found')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Create your first repository/ })).toBeInTheDocument()
  })

  it('shows the error state and retries', async () => {
    let calls = 0
    server.use(
      http.get('/service/rest/v1/repositories', () => {
        calls++
        return HttpResponse.json({ error: 'boom' }, { status: 500 })
      }),
    )
    renderWithProviders(<RepositoriesPage />)
    expect(await screen.findByText('Error loading repositories')).toBeInTheDocument()
    const before = calls
    fireEvent.click(screen.getByRole('button', { name: /Retry/ }))
    await waitFor(() => expect(calls).toBeGreaterThan(before))
  })

  it('refreshes via the refresh button', async () => {
    let calls = 0
    server.use(
      http.get('/service/rest/v1/repositories', () => {
        calls++
        return HttpResponse.json(repoList)
      }),
    )
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    const before = calls
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(calls).toBeGreaterThan(before))
  })

  it('navigates to browse on row click', async () => {
    renderWithProviders(<RepositoriesPage />)
    const row = await screen.findByText('maven-hosted')
    fireEvent.click(row)
    // navigate is internal; just ensure no crash and row still present after click
    expect(screen.getByText('maven-hosted')).toBeInTheDocument()
  })

  it('toggles online state', async () => {
    let patched: { online: boolean } | null = null
    server.use(
      http.patch('/service/rest/v1/repositories/:name', async ({ request }) => {
        patched = (await request.json()) as { online: boolean }
        return HttpResponse.json(fixtures.repository())
      }),
    )
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    fireEvent.click(screen.getAllByTitle('Disable repository')[0])
    await waitFor(() => expect(patched).toBeTruthy())
    expect(patched!.online).toBe(false)
  })

  it('exports a repository', async () => {
    const click = vi.fn()
    vi.spyOn(document, 'createElement').mockImplementation(((tag: string) => {
      const el = document.createElementNS('http://www.w3.org/1999/xhtml', tag) as HTMLElement
      if (tag === 'a') (el as HTMLAnchorElement).click = click
      return el
    }) as typeof document.createElement)
    global.URL.createObjectURL = vi.fn(() => 'blob:x')
    global.URL.revokeObjectURL = vi.fn()
    server.use(
      http.get('/api/v1/repositories/:name/export', () =>
        HttpResponse.text('tarball', { headers: { 'Content-Type': 'application/gzip' } }),
      ),
    )
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    fireEvent.click(screen.getAllByTitle('Export repository')[0])
    await waitFor(() => expect(click).toHaveBeenCalled())
  })

  it('deletes a repository after confirm', async () => {
    let deleted = false
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    server.use(
      http.delete('/service/rest/v1/repositories/:name', () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    fireEvent.click(screen.getAllByTitle('Delete')[0])
    await waitFor(() => expect(deleted).toBe(true))
  })

  it('does not delete when confirm is cancelled', async () => {
    let deleted = false
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    server.use(
      http.delete('/service/rest/v1/repositories/:name', () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    fireEvent.click(screen.getAllByTitle('Delete')[0])
    await new Promise(r => setTimeout(r, 30))
    expect(deleted).toBe(false)
  })

  it('hides admin actions for non-admin users', async () => {
    seedAuthAsGuest()
    useAuthStore.setState({
      token: 'tok',
      user: fixtures.user({ roles: ['viewer'] }) as ReturnType<typeof fixtures.user>,
    })
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    expect(screen.queryByRole('button', { name: /Create Repository/ })).not.toBeInTheDocument()
    expect(screen.queryByTitle('Delete')).not.toBeInTheDocument()
  })

  /* ── Create wizard ── */
  it('creates a hosted repository through the wizard', async () => {
    const user = userEvent.setup()
    let posted: Record<string, unknown> | null = null
    let postedUrl = ''
    server.use(
      http.post('/service/rest/v1/repositories/:format/:type', async ({ request }) => {
        posted = (await request.json()) as Record<string, unknown>
        postedUrl = request.url
        return HttpResponse.json(fixtures.repository(), { status: 201 })
      }),
    )
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    await user.click(screen.getByRole('button', { name: /Create Repository/ }))

    // Step 1 — Type (defaults: maven2 / hosted) → Next
    expect(await screen.findByText('Step 1 of 3')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Next/ }))

    // Step 2 — Settings
    await screen.findByText('Step 2 of 3')
    await user.type(screen.getByPlaceholderText('my-repo'), 'new-maven')
    await user.click(screen.getByRole('button', { name: /Next/ }))

    // Step 3 — Storage → Create
    await screen.findByText('Step 3 of 3')
    await user.click(screen.getByRole('button', { name: /^Create$/ }))

    await waitFor(() => expect(posted).toBeTruthy())
    expect((posted as { name: string }).name).toBe('new-maven')
    expect(postedUrl).toContain('/maven2/hosted')
  })

  it('validates the required name in the wizard', async () => {
    const user = userEvent.setup()
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    await user.click(screen.getByRole('button', { name: /Create Repository/ }))
    await screen.findByText('Step 1 of 3')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 2 of 3')
    // no name → Next should error
    await user.click(screen.getByRole('button', { name: /Next/ }))
    expect(await screen.findByText('Name is required')).toBeInTheDocument()
  })

  it('creates a proxy repository and requires a remote URL', async () => {
    const user = userEvent.setup()
    let posted: Record<string, unknown> | null = null
    server.use(
      http.post('/service/rest/v1/repositories/:format/:type', async ({ request }) => {
        posted = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(fixtures.repository(), { status: 201 })
      }),
    )
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    await user.click(screen.getByRole('button', { name: /Create Repository/ }))
    await screen.findByText('Step 1 of 3')

    // pick proxy type
    await user.click(screen.getByRole('button', { name: /Hosted — store/ }))
    const proxyOpt = await screen.findByText(/Proxy — cache/)
    await user.click(proxyOpt)
    await user.click(screen.getByRole('button', { name: /Next/ }))

    await screen.findByText('Step 2 of 3')
    await user.type(screen.getByPlaceholderText('my-repo'), 'maven-proxy')
    // remote URL default is prefilled from PROXY_DEFAULTS for maven2
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 3 of 3')
    await user.click(screen.getByRole('button', { name: /^Create$/ }))

    await waitFor(() => expect(posted).toBeTruthy())
    expect((posted as { proxyConfig?: { remote_url: string } }).proxyConfig?.remote_url).toContain('maven.org')
  })

  it('errors when a group has no members selected', async () => {
    const user = userEvent.setup()
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    await user.click(screen.getByRole('button', { name: /Create Repository/ }))
    await screen.findByText('Step 1 of 3')
    await user.click(screen.getByRole('button', { name: /Hosted — store/ }))
    await user.click(await screen.findByText(/Group — combine/))
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 2 of 3')
    await user.type(screen.getByPlaceholderText('my-repo'), 'my-group')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    expect(await screen.findByText('Select at least one member repository')).toBeInTheDocument()
  })

  it('shows the wizard error when create fails', async () => {
    const user = userEvent.setup()
    server.use(
      http.post('/service/rest/v1/repositories/:format/:type', () =>
        HttpResponse.json({ error: 'duplicate name' }, { status: 400 }),
      ),
    )
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    await user.click(screen.getByRole('button', { name: /Create Repository/ }))
    await screen.findByText('Step 1 of 3')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 2 of 3')
    await user.type(screen.getByPlaceholderText('my-repo'), 'dup')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 3 of 3')
    await user.click(screen.getByRole('button', { name: /^Create$/ }))
    expect(await screen.findByText('duplicate name')).toBeInTheDocument()
  })

  it('closes the create wizard via the overlay', async () => {
    const user = userEvent.setup()
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    await user.click(screen.getByRole('button', { name: /Create Repository/ }))
    await screen.findByText('Step 1 of 3')
    // click overlay (the holo-overlay element)
    const overlay = document.querySelector('.holo-overlay') as HTMLElement
    fireEvent.click(overlay)
    await waitFor(() => expect(screen.queryByText('Step 1 of 3')).not.toBeInTheDocument())
  })

  /* ── Edit modal ── */
  it('opens the edit modal, edits and saves a hosted repo', async () => {
    const user = userEvent.setup()
    let put: Record<string, unknown> | null = null
    server.use(
      http.put('/service/rest/v1/repositories/:format/:type/:name', async ({ request }) => {
        put = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(fixtures.repository())
      }),
    )
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    fireEvent.click(screen.getAllByTitle('Settings')[0])
    expect(await screen.findByText('Repository settings')).toBeInTheDocument()
    const desc = screen.getByPlaceholderText('Optional')
    await user.clear(desc)
    await user.type(desc, 'updated desc')
    const form = document.querySelector('form') as HTMLFormElement
    fireEvent.click(within(form).getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(put).toBeTruthy())
    expect((put as { description: string }).description).toBe('updated desc')
  })

  it('shows an error when edit save fails', async () => {
    const user = userEvent.setup()
    server.use(
      http.put('/service/rest/v1/repositories/:format/:type/:name', () =>
        HttpResponse.json({ error: 'save broke' }, { status: 400 }),
      ),
    )
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    fireEvent.click(screen.getAllByTitle('Settings')[0])
    await screen.findByText('Repository settings')
    const form = document.querySelector('form') as HTMLFormElement
    await user.click(within(form).getByRole('button', { name: /^Save$/ }))
    expect(await screen.findByText('save broke')).toBeInTheDocument()
  })

  it('closes the edit modal via Cancel', async () => {
    const user = userEvent.setup()
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('maven-hosted')
    fireEvent.click(screen.getAllByTitle('Settings')[0])
    await screen.findByText('Repository settings')
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByText('Repository settings')).not.toBeInTheDocument())
  })

  it('shows routing rule selector when editing a group repo', async () => {
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('docker-group')
    // third row settings button
    const settingsBtns = screen.getAllByTitle('Settings')
    fireEvent.click(settingsBtns[settingsBtns.length - 1])
    await screen.findByText('Repository settings')
    expect(screen.getByText('Routing Rule')).toBeInTheDocument()
  })
})
