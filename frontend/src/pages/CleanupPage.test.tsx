import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { screen, waitFor, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import CleanupPage from './CleanupPage'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'

const policy = (overrides?: Record<string, unknown>) => ({
  id: 'cp-1',
  name: 'delete-old-snapshots',
  description: 'cleanup snapshots',
  format: 'maven2',
  criteria: { lastDownloadedDays: 30, artifactAgeDays: 90, pathPrefix: '/snapshots/', nameGlob: '*-SNAPSHOT*' },
  scheduleCron: '0 2 * * *',
  enabled: true,
  dryRun: true,
  retainNVersions: 3,
  scope: { repositoryName: 'maven-hosted', pathPrefix: '/releases/' },
  lastRunAt: '2026-06-01T10:00:00Z',
  lastRunFreedBytes: 1500000,
  lastRunCount: 12,
  ...overrides,
})

describe('CleanupPage', () => {
  beforeEach(() => {
    seedAuthAsAdmin()
    server.use(
      http.get('/service/rest/v1/cleanup-policies', () =>
        HttpResponse.json([
          policy(),
          policy({ id: 'cp-2', name: 'wildcard-policy', format: '*', criteria: {}, scheduleCron: '', enabled: false, dryRun: false, retainNVersions: 0, scope: {}, lastRunAt: undefined }),
        ]),
      ),
    )
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders the policy list with cards', async () => {
    renderWithProviders(<CleanupPage />)
    expect(await screen.findByText('delete-old-snapshots')).toBeInTheDocument()
    expect(screen.getByText('wildcard-policy')).toBeInTheDocument()
    // criteria chips
    expect(screen.getByText('≥30d not downloaded')).toBeInTheDocument()
    expect(screen.getByText('retain ≥3')).toBeInTheDocument()
    expect(screen.getByText('0 2 * * *')).toBeInTheDocument()
    expect(screen.getByText('default schedule')).toBeInTheDocument()
  })

  it('shows the empty state', async () => {
    server.use(
      http.get('/service/rest/v1/cleanup-policies', () => HttpResponse.json([])),
    )
    renderWithProviders(<CleanupPage />)
    expect(await screen.findByText('No cleanup policies configured')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Create first policy/ })).toBeInTheDocument()
  })

  it('refreshes the list via the refresh button', async () => {
    let calls = 0
    server.use(
      http.get('/service/rest/v1/cleanup-policies', () => {
        calls++
        return HttpResponse.json([])
      }),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('No cleanup policies configured')
    const initial = calls
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(calls).toBeGreaterThan(initial))
  })

  it('runs a policy via Run now', async () => {
    let ran = false
    server.use(
      http.post('/service/rest/v1/cleanup-policies/:id/run', () => {
        ran = true
        return HttpResponse.json({ ok: true })
      }),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    const runBtns = screen.getAllByTitle('Run now')
    fireEvent.click(runBtns[0])
    await waitFor(() => expect(ran).toBe(true))
  })

  it('deletes a policy after confirm', async () => {
    let deleted = false
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    server.use(
      http.delete('/service/rest/v1/cleanup-policies/:id', () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    const delBtns = screen.getAllByTitle('Delete')
    fireEvent.click(delBtns[0])
    await waitFor(() => expect(deleted).toBe(true))
  })

  it('does not delete when confirm is cancelled', async () => {
    let deleted = false
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    server.use(
      http.delete('/service/rest/v1/cleanup-policies/:id', () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    fireEvent.click(screen.getAllByTitle('Delete')[0])
    await new Promise(r => setTimeout(r, 50))
    expect(deleted).toBe(false)
  })

  /* ── Preview modal ── */
  it('opens the preview modal and shows matching assets', async () => {
    server.use(
      http.post('/api/v1/cleanup-policies/:id/preview', () =>
        HttpResponse.json({
          totalCount: 2,
          totalBytes: 2500000,
          assets: [
            { path: '/a/old.jar', repository: 'maven-hosted', sizeBytes: 1500000, lastDownloaded: '2026-01-01T00:00:00Z', createdAt: '2025-12-01T00:00:00Z', reason: 'age' },
            { path: '/b/older.jar', repository: 'maven-hosted', sizeBytes: 1000000, lastDownloaded: null, createdAt: '2025-11-01T00:00:00Z', reason: 'never downloaded' },
          ],
        }),
      ),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    fireEvent.click(screen.getAllByTitle('Dry run preview')[0])
    expect(await screen.findByText('Dry Run Preview')).toBeInTheDocument()
    expect(await screen.findByText('2 assets to delete')).toBeInTheDocument()
    expect(screen.getByText('/a/old.jar')).toBeInTheDocument()
    expect(screen.getByText('never')).toBeInTheDocument()
  })

  it('shows empty preview message', async () => {
    server.use(
      http.post('/api/v1/cleanup-policies/:id/preview', () =>
        HttpResponse.json({ totalCount: 0, totalBytes: 0, assets: [] }),
      ),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    fireEvent.click(screen.getAllByTitle('Dry run preview')[0])
    expect(await screen.findByText('Nothing matches the policy criteria.')).toBeInTheDocument()
  })

  it('shows preview error', async () => {
    server.use(
      http.post('/api/v1/cleanup-policies/:id/preview', () =>
        HttpResponse.json({ error: 'preview broke' }, { status: 500 }),
      ),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    fireEvent.click(screen.getAllByTitle('Dry run preview')[0])
    expect(await screen.findByText('preview broke')).toBeInTheDocument()
  })

  it('runs for real from the preview modal', async () => {
    let ran = false
    server.use(
      http.post('/api/v1/cleanup-policies/:id/preview', () =>
        HttpResponse.json({ totalCount: 0, totalBytes: 0, assets: [] }),
      ),
      http.post('/service/rest/v1/cleanup-policies/:id/run', () => {
        ran = true
        return HttpResponse.json({ ok: true })
      }),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    fireEvent.click(screen.getAllByTitle('Dry run preview')[0])
    await screen.findByText('Nothing matches the policy criteria.')
    fireEvent.click(screen.getByRole('button', { name: /Run for real/ }))
    await waitFor(() => expect(ran).toBe(true))
  })

  it('closes the preview modal', async () => {
    server.use(
      http.post('/api/v1/cleanup-policies/:id/preview', () =>
        HttpResponse.json({ totalCount: 0, totalBytes: 0, assets: [] }),
      ),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    fireEvent.click(screen.getAllByTitle('Dry run preview')[0])
    await screen.findByText('Dry Run Preview')
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    await waitFor(() => expect(screen.queryByText('Dry Run Preview')).not.toBeInTheDocument())
  })

  /* ── Create wizard ── */
  it('steps through the create wizard and submits', async () => {
    const user = userEvent.setup()
    let posted: Record<string, unknown> | null = null
    server.use(
      http.post('/service/rest/v1/cleanup-policies', async ({ request }) => {
        posted = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ id: 'cp-new' }, { status: 201 })
      }),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    await user.click(screen.getByRole('button', { name: /New Policy/ }))

    // Step 1 — Identity
    expect(await screen.findByText('Step 1 of 3')).toBeInTheDocument()
    await user.type(screen.getByPlaceholderText('e.g. delete-old-snapshots'), 'my-policy')
    await user.type(screen.getByPlaceholderText('Optional description'), 'desc')
    await user.click(screen.getByRole('button', { name: /Next/ }))

    // Step 2 — Criteria
    await screen.findByText('Step 2 of 3')
    await user.type(screen.getByPlaceholderText('e.g. 30'), '15')
    await user.type(screen.getByPlaceholderText('e.g. 90'), '45')
    await user.type(screen.getByPlaceholderText('e.g. 3 (0 = disabled)'), '2')
    await user.click(screen.getByRole('button', { name: /Next/ }))

    // Step 3 — Schedule
    await screen.findByText('Step 3 of 3')
    await user.type(screen.getByPlaceholderText(/0 2 \* \* \*/), '0 4 * * *')
    await user.click(screen.getByRole('button', { name: /Create Policy/ }))

    await waitFor(() => expect(posted).toBeTruthy())
    expect((posted as { name: string }).name).toBe('my-policy')
    expect((posted as { retainNVersions: number }).retainNVersions).toBe(2)
  })

  it('validates the required name in the wizard', async () => {
    const user = userEvent.setup()
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    await user.click(screen.getByRole('button', { name: /New Policy/ }))
    await screen.findByText('Step 1 of 3')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    expect(await screen.findByText('Name is required')).toBeInTheDocument()
  })

  it('shows wizard error when create fails', async () => {
    const user = userEvent.setup()
    server.use(
      http.post('/service/rest/v1/cleanup-policies', () =>
        HttpResponse.json({ error: 'create broke' }, { status: 400 }),
      ),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    await user.click(screen.getByRole('button', { name: /New Policy/ }))
    await screen.findByText('Step 1 of 3')
    await user.type(screen.getByPlaceholderText('e.g. delete-old-snapshots'), 'p')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 2 of 3')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 3 of 3')
    await user.click(screen.getByRole('button', { name: /Create Policy/ }))
    expect(await screen.findByText('create broke')).toBeInTheDocument()
  })

  it('navigates back in the wizard', async () => {
    const user = userEvent.setup()
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    await user.click(screen.getByRole('button', { name: /New Policy/ }))
    await screen.findByText('Step 1 of 3')
    await user.type(screen.getByPlaceholderText('e.g. delete-old-snapshots'), 'p')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 2 of 3')
    await user.click(screen.getByRole('button', { name: /Back/ }))
    await screen.findByText('Step 1 of 3')
  })

  it('shows the scope section when a format is selected in the wizard', async () => {
    const user = userEvent.setup()
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    await user.click(screen.getByRole('button', { name: /New Policy/ }))
    await screen.findByText('Step 1 of 3')
    await user.type(screen.getByPlaceholderText('e.g. delete-old-snapshots'), 'p')
    // open format Select (shows "All formats")
    await user.click(screen.getByRole('button', { name: /All formats/ }))
    // pick the maven2 option inside the portal dropdown (last match)
    const mavenOptions = await screen.findAllByText('maven2')
    await user.click(mavenOptions[mavenOptions.length - 1])
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 2 of 3')
    // Scope section appears
    expect(await screen.findByText('Target repository')).toBeInTheDocument()
  })

  /* ── Edit modal ── */
  it('opens the edit modal pre-filled and saves', async () => {
    const user = userEvent.setup()
    let put: Record<string, unknown> | null = null
    server.use(
      http.put('/service/rest/v1/cleanup-policies/:id', async ({ request }) => {
        put = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ id: 'cp-1' })
      }),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    fireEvent.click(screen.getAllByTitle('Edit')[0])
    expect(await screen.findByText('Edit Policy')).toBeInTheDocument()
    const nameInput = screen.getByDisplayValue('delete-old-snapshots')
    await user.clear(nameInput)
    await user.type(nameInput, 'renamed-policy')
    await user.click(screen.getByRole('button', { name: /Save changes/ }))
    await waitFor(() => expect(put).toBeTruthy())
    expect((put as { name: string }).name).toBe('renamed-policy')
  })

  it('validates required name in the edit modal', async () => {
    const user = userEvent.setup()
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    fireEvent.click(screen.getAllByTitle('Edit')[0])
    await screen.findByText('Edit Policy')
    const nameInput = screen.getByDisplayValue('delete-old-snapshots')
    await user.clear(nameInput)
    await user.click(screen.getByRole('button', { name: /Save changes/ }))
    expect(await screen.findByText('Name is required')).toBeInTheDocument()
  })

  it('shows error when edit save fails', async () => {
    const user = userEvent.setup()
    server.use(
      http.put('/service/rest/v1/cleanup-policies/:id', () =>
        HttpResponse.json({ error: 'save broke' }, { status: 400 }),
      ),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    fireEvent.click(screen.getAllByTitle('Edit')[0])
    await screen.findByText('Edit Policy')
    await user.click(screen.getByRole('button', { name: /Save changes/ }))
    expect(await screen.findByText('save broke')).toBeInTheDocument()
  })

  it('closes the edit modal via Cancel', async () => {
    const user = userEvent.setup()
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    fireEvent.click(screen.getAllByTitle('Edit')[0])
    await screen.findByText('Edit Policy')
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByText('Edit Policy')).not.toBeInTheDocument())
  })

  it('opens the path browser from the edit modal scope section', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/api/v1/browse/repositories/:name/path-tree', () =>
        HttpResponse.json({ paths: ['/releases/v1/', '/releases/v2/'] }),
      ),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    fireEvent.click(screen.getAllByTitle('Edit')[0])
    await screen.findByText('Edit Policy')
    // scope already populated (maven-hosted + /releases/), Browse… enabled
    await user.click(screen.getByRole('button', { name: 'Browse…' }))
    expect(await screen.findByText(/Browse — maven-hosted/)).toBeInTheDocument()
    expect(await screen.findByText('/releases/v1/')).toBeInTheDocument()
    // select a path
    await user.click(screen.getByText('/releases/v2/'))
    await user.click(screen.getByRole('button', { name: /Select/ }))
    await waitFor(() => expect(screen.queryByText(/Browse — maven-hosted/)).not.toBeInTheDocument())
  })

  it('toggles the enabled and dry-run checkboxes in the wizard step 3', async () => {
    const user = userEvent.setup()
    let posted: Record<string, unknown> | null = null
    server.use(
      http.post('/service/rest/v1/cleanup-policies', async ({ request }) => {
        posted = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ id: 'cp-new' }, { status: 201 })
      }),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    await user.click(screen.getByRole('button', { name: /New Policy/ }))
    await screen.findByText('Step 1 of 3')
    await user.type(screen.getByPlaceholderText('e.g. delete-old-snapshots'), 'toggle-policy')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 2 of 3')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 3 of 3')
    // toggle both checkboxes (Enabled, Dry run)
    const checks = screen.getAllByRole('checkbox')
    fireEvent.click(checks[0])
    fireEvent.click(checks[1])
    await user.click(screen.getByRole('button', { name: /Create Policy/ }))
    await waitFor(() => expect(posted).toBeTruthy())
  })

  it('changes format, scope repository and toggles checkboxes in the edit modal', async () => {
    const user = userEvent.setup()
    let put: Record<string, unknown> | null = null
    server.use(
      http.get('/service/rest/v1/repositories', () =>
        HttpResponse.json([
          { id: 'r1', name: 'maven-hosted', format: 'maven2', type: 'hosted', online: true },
          { id: 'r2', name: 'maven-two', format: 'maven2', type: 'hosted', online: true },
        ]),
      ),
      http.put('/service/rest/v1/cleanup-policies/:id', async ({ request }) => {
        put = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ id: 'cp-1' })
      }),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    fireEvent.click(screen.getAllByTitle('Edit')[0])
    await screen.findByText('Edit Policy')
    // toggle the two option checkboxes (Enabled, Dry run)
    const checks = screen.getAllByRole('checkbox')
    fireEvent.click(checks[0])
    fireEvent.click(checks[1])
    // change the scope repository Select (maven-hosted → maven-two), scoped to the modal
    const modal = document.querySelector('.holo-modal') as HTMLElement
    const repoTrigger = within(modal).getAllByText('maven-hosted')[0]
    await user.click(repoTrigger)
    await user.click(await screen.findByText('maven-two'))
    await user.click(screen.getByRole('button', { name: /Save changes/ }))
    await waitFor(() => expect(put).toBeTruthy())
  })

  it('filters and hovers paths in the path browser', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/api/v1/browse/repositories/:name/path-tree', () =>
        HttpResponse.json({ paths: ['/releases/v1/', '/releases/v2/', '/snapshots/'] }),
      ),
    )
    renderWithProviders(<CleanupPage />)
    await screen.findByText('delete-old-snapshots')
    fireEvent.click(screen.getAllByTitle('Edit')[0])
    await screen.findByText('Edit Policy')
    await user.click(screen.getByRole('button', { name: 'Browse…' }))
    await screen.findByText(/Browse — maven-hosted/)
    // hover a path row (onMouseEnter/onMouseLeave)
    const row = await screen.findByText('/releases/v1/')
    fireEvent.mouseEnter(row)
    fireEvent.mouseLeave(row)
    // filter paths (onChange)
    const filter = screen.getByPlaceholderText('Filter paths…')
    fireEvent.change(filter, { target: { value: 'snapshots' } })
    await waitFor(() => expect(screen.queryByText('/releases/v2/')).not.toBeInTheDocument())
    expect(screen.getByText('/snapshots/')).toBeInTheDocument()
  })
})
