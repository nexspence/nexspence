import { describe, it, expect, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import MigrationPage from './MigrationPage'
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
