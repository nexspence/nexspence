import { describe, it, expect, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MonitoringView } from './MonitoringPage'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'

const snapshot = (overrides?: Record<string, unknown>) => ({
  uptime_seconds: 90061, // 1d 1h 1m
  requests_total: 12345,
  request_errors: 5,
  artifacts_stored: 2048,
  bytes_stored: 5_000_000,
  downloads_total: 9001,
  artifacts_deleted: 7,
  goroutines: 42,
  memory: {
    alloc_bytes: 50_000_000,
    total_alloc_bytes: 200_000_000,
    sys_bytes: 100_000_000,
    gc_cycles: 17,
  },
  ...overrides,
})

const historyPoints = [
  { timestamp: Math.floor(Date.now() / 1000) - 20, requests_total: 100, request_errors: 1, artifacts_stored: 10, bytes_stored: 1000, downloads_total: 5, goroutines: 10 },
  { timestamp: Math.floor(Date.now() / 1000) - 10, requests_total: 200, request_errors: 3, artifacts_stored: 20, bytes_stored: 2000, downloads_total: 8, goroutines: 12 },
]

const repos = [
  { name: 'maven-hosted', format: 'maven2', type: 'hosted', downloads: 5000, size_bytes: 3_000_000 },
  { name: 'npm-proxy', format: 'npm', type: 'proxy', downloads: 1200, size_bytes: 800_000 },
]

describe('MonitoringPage (MonitoringView)', () => {
  beforeEach(() => {
    seedAuthAsAdmin()
    server.use(
      http.get('/api/v1/metrics', () => HttpResponse.json(snapshot())),
    )
  })

  it('renders the Overview tab by default with stat cards', async () => {
    renderWithProviders(<MonitoringView />)
    expect(await screen.findByText('Monitoring')).toBeInTheDocument()
    expect(await screen.findByText('Total Requests')).toBeInTheDocument()
    expect(screen.getByText('Artifacts Stored')).toBeInTheDocument()
    // fmtNum(12345) => 12.3K
    expect(await screen.findByText('12.3K')).toBeInTheDocument()
    expect(screen.getByText('Memory')).toBeInTheDocument()
    expect(screen.getByText('Storage Activity')).toBeInTheDocument()
    // error rate badge present
    expect(screen.getAllByText(/% error rate/).length).toBeGreaterThan(0)
  })

  it('shows the failed-to-load message when metrics return null', async () => {
    server.use(http.get('/api/v1/metrics', () => HttpResponse.json(null)))
    renderWithProviders(<MonitoringView />)
    expect(await screen.findByText('Failed to load metrics')).toBeInTheDocument()
  })

  it('refetches on the refresh button', async () => {
    let calls = 0
    server.use(
      http.get('/api/v1/metrics', () => {
        calls++
        return HttpResponse.json(snapshot())
      }),
    )
    renderWithProviders(<MonitoringView />)
    await screen.findByText('Monitoring')
    const initial = calls
    fireEvent.click(screen.getByTitle('Refresh now'))
    await waitFor(() => expect(calls).toBeGreaterThan(initial))
  })

  it('switches to the Charts tab and shows no-data placeholders when empty', async () => {
    const user = userEvent.setup()
    server.use(http.get('/api/v1/metrics/history', () => HttpResponse.json([])))
    renderWithProviders(<MonitoringView />)
    await screen.findByText('Monitoring')
    await user.click(screen.getByRole('button', { name: 'Charts' }))
    expect(await screen.findByText('Requests / sec')).toBeInTheDocument()
    expect(screen.getByText('Error Rate %')).toBeInTheDocument()
    expect(screen.getByText('Storage (bytes)')).toBeInTheDocument()
    expect(screen.getAllByText(/No data yet — collecting samples/).length).toBe(3)
  })

  it('renders charts when history data is present', async () => {
    const user = userEvent.setup()
    server.use(http.get('/api/v1/metrics/history', () => HttpResponse.json(historyPoints)))
    renderWithProviders(<MonitoringView />)
    await screen.findByText('Monitoring')
    await user.click(screen.getByRole('button', { name: 'Charts' }))
    expect(await screen.findByText('Requests / sec')).toBeInTheDocument()
    // With data, the "no data" placeholder should NOT be shown.
    await waitFor(() =>
      expect(screen.queryByText(/No data yet — collecting samples/)).not.toBeInTheDocument(),
    )
  })

  it('switches to the Repositories tab and renders rows, toggling sort', async () => {
    const user = userEvent.setup()
    server.use(http.get('/api/v1/metrics/repos', () => HttpResponse.json(repos)))
    renderWithProviders(<MonitoringView />)
    await screen.findByText('Monitoring')
    await user.click(screen.getByRole('button', { name: 'Repositories' }))
    expect(await screen.findByText('Top Repositories')).toBeInTheDocument()
    expect(screen.getByText('maven-hosted')).toBeInTheDocument()
    expect(screen.getByText('npm-proxy')).toBeInTheDocument()
    expect(screen.getByText('MAVEN2')).toBeInTheDocument()
    // Toggle sort to Storage
    await user.click(screen.getByRole('button', { name: 'Storage' }))
    await user.click(screen.getByRole('button', { name: 'Downloads' }))
    expect(screen.getByText('maven-hosted')).toBeInTheDocument()
  })

  it('shows no-data message on the Repositories tab when empty', async () => {
    const user = userEvent.setup()
    server.use(http.get('/api/v1/metrics/repos', () => HttpResponse.json([])))
    renderWithProviders(<MonitoringView />)
    await screen.findByText('Monitoring')
    await user.click(screen.getByRole('button', { name: 'Repositories' }))
    expect(await screen.findByText('No data yet')).toBeInTheDocument()
  })
})
