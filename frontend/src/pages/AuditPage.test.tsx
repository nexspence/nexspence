import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import AuditPage from './AuditPage'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'

const event = (overrides?: Record<string, unknown>) => ({
  id: 1,
  eventTime: '2026-06-01T12:00:00Z',
  username: 'admin',
  remoteIp: '10.0.0.1',
  domain: 'REPOSITORY',
  action: 'CREATE',
  entityType: 'repository',
  entityName: 'maven-hosted',
  result: 'success',
  context: { path: '/repository/maven-hosted/foo.jar' },
  ...overrides,
})

describe('AuditPage', () => {
  beforeEach(() => {
    seedAuthAsAdmin()
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows empty state when no events', async () => {
    renderWithProviders(<AuditPage />)
    expect(await screen.findByText('No audit events')).toBeInTheDocument()
    expect(
      screen.getByText(/Audit events are recorded as users and services interact/),
    ).toBeInTheDocument()
  })

  it('renders the event table after load', async () => {
    server.use(
      http.get('/service/rest/v1/audit', () =>
        HttpResponse.json({ items: [event(), event({ id: 2, domain: 'SECURITY', action: 'LOGIN', result: 'failure', entityName: 'bob', context: {} })], total: 2 }),
      ),
    )
    renderWithProviders(<AuditPage />)
    expect(await screen.findByText('REPOSITORY')).toBeInTheDocument()
    expect(screen.getByText('SECURITY')).toBeInTheDocument()
    expect(screen.getByText('maven-hosted')).toBeInTheDocument()
    expect(screen.getByText('/repository/maven-hosted/foo.jar')).toBeInTheDocument()
    expect(screen.getByText('Showing 1–2 of 2')).toBeInTheDocument()
  })

  it('filters by username and resets offset', async () => {
    const user = userEvent.setup()
    let lastUrl = ''
    server.use(
      http.get('/service/rest/v1/audit', ({ request }) => {
        lastUrl = request.url
        return HttpResponse.json({ items: [], total: 0 })
      }),
    )
    renderWithProviders(<AuditPage />)
    await screen.findByText('No audit events')
    const input = screen.getByPlaceholderText('username…')
    await user.type(input, 'bob')
    await waitFor(() => expect(lastUrl).toContain('username=bob'))
    // Empty-state message reflects active filters.
    expect(await screen.findByText('No audit events matching filters')).toBeInTheDocument()
  })

  it('triggers a refetch via the refresh button', async () => {
    let calls = 0
    server.use(
      http.get('/service/rest/v1/audit', () => {
        calls++
        return HttpResponse.json({ items: [], total: 0 })
      }),
    )
    renderWithProviders(<AuditPage />)
    await screen.findByText('No audit events')
    const initial = calls
    fireEvent.click(screen.getByTitle('Refresh'))
    await waitFor(() => expect(calls).toBeGreaterThan(initial))
  })

  it('paginates when total exceeds page size', async () => {
    const many = Array.from({ length: 50 }, (_, i) => event({ id: i + 1 }))
    server.use(
      http.get('/service/rest/v1/audit', ({ request }) => {
        const offset = Number(new URL(request.url).searchParams.get('offset') ?? '0')
        return HttpResponse.json({ items: many, total: 120, offset })
      }),
    )
    renderWithProviders(<AuditPage />)
    expect(await screen.findByText('Showing 1–50 of 120')).toBeInTheDocument()
    // Next button should be enabled; click it to advance the offset.
    const buttons = screen.getAllByRole('button')
    const next = buttons[buttons.length - 1]
    fireEvent.click(next)
    expect(await screen.findByText('Showing 51–100 of 120')).toBeInTheDocument()
  })

  it('exports filtered events as NDJSON', async () => {
    seedAuthAsAdmin()
    localStorage.setItem('nexspence_token', 'tok-123')
    const blob = new Blob(['{}'], { type: 'application/x-ndjson' })
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      blob: () => Promise.resolve(blob),
    })
    vi.stubGlobal('fetch', fetchMock)
    const createUrl = vi.fn().mockReturnValue('blob:audit')
    const revokeUrl = vi.fn()
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createUrl })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeUrl })
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

    renderWithProviders(<AuditPage />)
    await screen.findByText('No audit events')
    fireEvent.click(screen.getByTitle('Export filtered events as NDJSON'))
    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    expect(fetchMock.mock.calls[0][1].headers.Authorization).toBe('Bearer tok-123')
    await waitFor(() => expect(clickSpy).toHaveBeenCalled())
    expect(createUrl).toHaveBeenCalledWith(blob)
    expect(revokeUrl).toHaveBeenCalled()
    localStorage.removeItem('nexspence_token')
    vi.unstubAllGlobals()
  })

  it('alerts when export fails', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: false, status: 500, statusText: 'err' })
    vi.stubGlobal('fetch', fetchMock)
    const alertMock = vi.spyOn(window, 'alert').mockImplementation(() => {})
    renderWithProviders(<AuditPage />)
    await screen.findByText('No audit events')
    fireEvent.click(screen.getByTitle('Export filtered events as NDJSON'))
    await waitFor(() => expect(alertMock).toHaveBeenCalledWith('Export failed: 500 err'))
    vi.unstubAllGlobals()
  })
})
