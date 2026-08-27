import { describe, it, expect, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import MigrationPage from './MigrationPage'
import { shouldPollJobs } from './migrationJobs'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'

const job = (overrides?: Record<string, unknown>) => ({
  id: 'job-1',
  status: 'running',
  sourceUrl: 'https://nexus.example.com',
  repositoriesTotal: 10,
  repositoriesDone: 4,
  assetsTotal: 100,
  assetsDone: 25,
  errorCount: 2,
  createdAt: '2026-06-01T10:00:00Z',
  updatedAt: '2026-06-01T11:00:00Z',
  ...overrides,
})

describe('MigrationPage', () => {
  beforeEach(() => {
    seedAuthAsAdmin()
  })

  it('renders the empty state with start button', async () => {
    renderWithProviders(<MigrationPage />)
    expect(await screen.findByText('No migration jobs yet')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Start Migration/ })).toBeInTheDocument()
    expect(screen.getByText(/How it works:/)).toBeInTheDocument()
  })

  it('renders a job card with progress and status', async () => {
    server.use(
      http.get('/api/v1/migration/jobs', () =>
        HttpResponse.json([job(), job({ id: 'job-2', status: 'completed', errorCount: 0, repositoriesDone: 8, assetsDone: 90, sourceUrl: 'https://other.example.com' })]),
      ),
    )
    renderWithProviders(<MigrationPage />)
    expect(await screen.findByText('https://nexus.example.com')).toBeInTheDocument()
    expect(screen.getByText('running')).toBeInTheDocument()
    expect(screen.getByText('completed')).toBeInTheDocument()
    // repositoriesDone 4 of 10 for first job
    expect(screen.getByText('4')).toBeInTheDocument()
    // Pause button for running job
    expect(screen.getByRole('button', { name: /Pause/ })).toBeInTheDocument()
  })

  it('shows Resume for a paused job and calls the resume endpoint', async () => {
    let resumed = false
    server.use(
      http.get('/api/v1/migration/jobs', () => HttpResponse.json([job({ status: 'paused' })])),
      http.post('/api/v1/migration/jobs/:id/resume', () => {
        resumed = true
        return HttpResponse.json({ ok: true })
      }),
    )
    renderWithProviders(<MigrationPage />)
    const resume = await screen.findByRole('button', { name: /Resume/ })
    fireEvent.click(resume)
    await waitFor(() => expect(resumed).toBe(true))
  })

  it('calls pause endpoint for a running job', async () => {
    let paused = false
    server.use(
      http.get('/api/v1/migration/jobs', () => HttpResponse.json([job()])),
      http.post('/api/v1/migration/jobs/:id/pause', () => {
        paused = true
        return HttpResponse.json({ ok: true })
      }),
    )
    renderWithProviders(<MigrationPage />)
    const pause = await screen.findByRole('button', { name: /Pause/ })
    fireEvent.click(pause)
    await waitFor(() => expect(paused).toBe(true))
  })

  it('opens the create modal and submits a new migration job', async () => {
    const user = userEvent.setup()
    let posted: unknown = null
    server.use(
      http.post('/api/v1/migration/jobs', async ({ request }) => {
        posted = await request.json()
        return HttpResponse.json({ id: 'new-job' }, { status: 201 })
      }),
    )
    renderWithProviders(<MigrationPage />)
    await screen.findByText('No migration jobs yet')
    await user.click(screen.getByRole('button', { name: /New Migration/ }))
    expect(await screen.findByRole('heading', { name: 'New Migration Job' })).toBeInTheDocument()

    await user.type(screen.getByPlaceholderText('https://nexus.example.com'), 'https://src.example.com')
    // Fill password field (the only type=password input)
    const pwd = document.querySelector('input[type="password"]') as HTMLInputElement
    await user.type(pwd, 'secret')

    // Submit the form (modal submit button is type=submit inside the form)
    const submit = pwd.closest('form')!.querySelector('button[type="submit"]') as HTMLButtonElement
    await user.click(submit)
    await waitFor(() => expect(posted).toBeTruthy())
    expect((posted as { sourceUrl: string }).sourceUrl).toBe('https://src.example.com')
  })

  it('shows an error when create fails', async () => {
    const user = userEvent.setup()
    server.use(
      http.post('/api/v1/migration/jobs', () =>
        HttpResponse.json({ error: 'bad creds' }, { status: 400 }),
      ),
    )
    renderWithProviders(<MigrationPage />)
    await screen.findByText('No migration jobs yet')
    await user.click(screen.getByRole('button', { name: /New Migration/ }))
    await screen.findByRole('heading', { name: 'New Migration Job' })
    await user.type(screen.getByPlaceholderText('https://nexus.example.com'), 'https://src.example.com')
    const pwd = document.querySelector('input[type="password"]') as HTMLInputElement
    await user.type(pwd, 'secret')
    const submit = pwd.closest('form')!.querySelector('button[type="submit"]') as HTMLButtonElement
    await user.click(submit)
    expect(await screen.findByText(/bad creds|Failed to create migration job/)).toBeInTheDocument()
  })

  it('closes the create modal via Cancel', async () => {
    const user = userEvent.setup()
    renderWithProviders(<MigrationPage />)
    await screen.findByText('No migration jobs yet')
    await user.click(screen.getByRole('button', { name: /New Migration/ }))
    await screen.findByRole('heading', { name: 'New Migration Job' })
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() =>
      expect(screen.queryByRole('heading', { name: 'New Migration Job' })).not.toBeInTheDocument(),
    )
  })

  it('refreshes the job list via the refresh button', async () => {
    let calls = 0
    server.use(
      http.get('/api/v1/migration/jobs', () => {
        calls++
        return HttpResponse.json([])
      }),
    )
    renderWithProviders(<MigrationPage />)
    await screen.findByText('No migration jobs yet')
    const initial = calls
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(calls).toBeGreaterThan(initial))
  })
})

describe('MigrationPage — job status from the engine', () => {
  beforeEach(() => {
    seedAuthAsAdmin()
  })

  it('renders the terminal statuses the API actually returns', async () => {
    server.use(
      http.get('/api/v1/migration/jobs', () =>
        HttpResponse.json([
          job({ id: 'j-done', status: 'done' }),
          job({ id: 'j-error', status: 'error', lastError: 'connection refused' }),
        ]),
      ),
    )
    renderWithProviders(<MigrationPage />)
    expect(await screen.findByText('done')).toBeInTheDocument()
    expect(screen.getByText('error')).toBeInTheDocument()
    // A finished job offers neither Pause nor Resume.
    expect(screen.queryByRole('button', { name: /Pause/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Resume/ })).not.toBeInTheDocument()
  })

  it('shows the last error reported by a job', async () => {
    server.use(
      http.get('/api/v1/migration/jobs', () =>
        HttpResponse.json([job({ status: 'error', lastError: 'nexus returned 401' })]),
      ),
    )
    renderWithProviders(<MigrationPage />)
    expect(await screen.findByText(/nexus returned 401/)).toBeInTheDocument()
  })

  it('keeps polling while a job is pending or running, and stops once it settles', () => {
    expect(shouldPollJobs([{ status: 'pending' }])).toBe(3000)
    expect(shouldPollJobs([{ status: 'running' }])).toBe(3000)
    expect(shouldPollJobs([{ status: 'done' }, { status: 'paused' }])).toBe(false)
    expect(shouldPollJobs([])).toBe(false)
    expect(shouldPollJobs(undefined)).toBe(false)
  })
})

describe('MigrationPage — test connection', () => {
  beforeEach(() => {
    seedAuthAsAdmin()
  })

  const openModal = async (user: ReturnType<typeof userEvent.setup>) => {
    renderWithProviders(<MigrationPage />)
    await screen.findByText('No migration jobs yet')
    await user.click(screen.getByRole('button', { name: /New Migration/ }))
    await screen.findByRole('heading', { name: 'New Migration Job' })
  }

  it('reports a reachable Nexus with the repositories it found', async () => {
    const user = userEvent.setup()
    let sent: unknown = null
    server.use(
      http.post('/api/v1/migration/preview', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json({
          reachable: true,
          repoCount: 2,
          repos: [
            { name: 'raw-hosted', format: 'raw', type: 'hosted' },
            { name: 'maven-central', format: 'maven2', type: 'proxy' },
          ],
        })
      }),
    )
    await openModal(user)

    await user.type(screen.getByPlaceholderText('https://nexus.example.com'), 'https://src.example.com')
    const pwd = document.querySelector('input[type="password"]') as HTMLInputElement
    await user.type(pwd, 'secret')
    await user.click(screen.getByRole('button', { name: /Test connection/ }))

    expect(await screen.findByText(/Connected — 2 repositories/)).toBeInTheDocument()
    expect(screen.getByText(/raw-hosted/)).toBeInTheDocument()
    expect(sent).toEqual({
      sourceUrl: 'https://src.example.com',
      username: 'admin',
      password: 'secret',
    })
  })

  it('shows the underlying reason when the Nexus cannot be reached', async () => {
    const user = userEvent.setup()
    server.use(
      http.post('/api/v1/migration/preview', () =>
        HttpResponse.json({ error: 'dial tcp: connection refused', reachable: false }, { status: 502 }),
      ),
    )
    await openModal(user)

    await user.type(screen.getByPlaceholderText('https://nexus.example.com'), 'https://bad.invalid')
    await user.click(screen.getByRole('button', { name: /Test connection/ }))

    expect(await screen.findByText(/connection refused/)).toBeInTheDocument()
  })

  it('drops a stale result as soon as the connection details change', async () => {
    const user = userEvent.setup()
    server.use(
      http.post('/api/v1/migration/preview', () =>
        HttpResponse.json({ reachable: true, repoCount: 1, repos: [{ name: 'raw-hosted', format: 'raw', type: 'hosted' }] }),
      ),
    )
    await openModal(user)

    const url = screen.getByPlaceholderText('https://nexus.example.com')
    await user.type(url, 'https://src.example.com')
    await user.click(screen.getByRole('button', { name: /Test connection/ }))
    expect(await screen.findByText(/Connected — 1 repositor/)).toBeInTheDocument()

    await user.type(url, '/extra')
    await waitFor(() =>
      expect(screen.queryByText(/Connected — 1 repositor/)).not.toBeInTheDocument(),
    )
  })

  it('sends the scopes the operator selected', async () => {
    const user = userEvent.setup()
    let posted: { scope?: Record<string, boolean> } | null = null
    server.use(
      http.post('/api/v1/migration/jobs', async ({ request }) => {
        posted = (await request.json()) as { scope?: Record<string, boolean> }
        return HttpResponse.json({ id: 'new-job' }, { status: 201 })
      }),
    )
    await openModal(user)

    await user.type(screen.getByPlaceholderText('https://nexus.example.com'), 'https://src.example.com')
    const pwd = document.querySelector('input[type="password"]') as HTMLInputElement
    await user.type(pwd, 'secret')
    await user.click(screen.getByRole('checkbox', { name: /Users/ }))
    await user.click(screen.getByRole('checkbox', { name: /Routing rules/ }))

    const submit = pwd.closest('form')!.querySelector('button[type="submit"]') as HTMLButtonElement
    await user.click(submit)

    await waitFor(() => expect(posted).toBeTruthy())
    expect(posted!.scope).toEqual({
      migrateRepos: true,
      migrateBlobs: true,
      migrateUsers: false,
      migratePrivileges: true,
      migrateRoles: true,
      migrateRoutingRules: false,
      userRealms: ['default'],
    })
  })

  // #342/10: user migration is scoped to explicit realms, local by default;
  // an opted-in external realm travels with the scope.
  it('sends the user realms the operator opted into', async () => {
    const user = userEvent.setup()
    let posted: { scope?: { userRealms?: string[] } } | null = null
    server.use(
      http.post('/api/v1/migration/jobs', async ({ request }) => {
        posted = (await request.json()) as { scope?: { userRealms?: string[] } }
        return HttpResponse.json({ id: 'new-job' }, { status: 201 })
      }),
    )
    await openModal(user)

    await user.type(screen.getByPlaceholderText('https://nexus.example.com'), 'https://src.example.com')
    const pwd = document.querySelector('input[type="password"]') as HTMLInputElement
    await user.type(pwd, 'secret')

    expect(screen.getByRole('checkbox', { name: 'Local' })).toBeChecked()
    await user.click(screen.getByRole('checkbox', { name: 'LDAP' }))

    const submit = pwd.closest('form')!.querySelector('button[type="submit"]') as HTMLButtonElement
    await user.click(submit)

    await waitFor(() => expect(posted).toBeTruthy())
    expect(posted!.scope!.userRealms).toEqual(['default', 'LDAP'])
  })

  it('hides the realm picker when user migration is off', async () => {
    const user = userEvent.setup()
    await openModal(user)
    expect(screen.getByText('User realms')).toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: /Users/ }))
    expect(screen.queryByText('User realms')).not.toBeInTheDocument()
  })
})
