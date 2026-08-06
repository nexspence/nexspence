import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import SecurityPage from './SecurityPage'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'
import { fixtures } from '@/test/fixtures'

/**
 * Exporting the scan results (#162). The value is a file someone can hand on,
 * so what these pin is: the request carries the filters that are on screen, and
 * a download actually happens.
 */

function seedBaseline() {
  server.use(
    http.get('/service/rest/v1/security/roles', () => HttpResponse.json([
      { id: 'role-1', name: 'nx-admin', description: 'Admin role', privileges: [], roles: [], readOnly: true },
    ])),
    http.get('/service/rest/v1/security/privileges', () => HttpResponse.json([])),
    http.get('/service/rest/v1/security/content-selectors', () => HttpResponse.json([])),
    http.get('/api/v1/security/privilege-role-map', () => HttpResponse.json({})),
    http.get('/service/rest/v1/repositories', () => HttpResponse.json([fixtures.repository()])),
    http.get('/service/rest/v1/security/users', () => HttpResponse.json([fixtures.user()])),
    http.get('/api/v1/webhooks', () => HttpResponse.json([])),
    http.get('/api/v1/security/summary', () =>
      HttpResponse.json({ malicious: 1, critical: 0, high: 0, medium: 0, low: 0, unknown: 0, scanned_total: 1 }),
    ),
  )
}

/** Records every /security/vulnerabilities request and returns one row. */
function captureVulnRequests(): string[] {
  const urls: string[] = []
  server.use(
    http.get('/api/v1/security/vulnerabilities', ({ request }) => {
      urls.push(request.url)
      if (new URL(request.url).searchParams.get('export')) {
        return HttpResponse.text('repository,format\nnpm-hosted,npm\n', {
          headers: { 'Content-Type': 'text/csv' },
        })
      }
      return HttpResponse.json({
        total: 1,
        items: [{
          repoName: 'npm-hosted', format: 'npm', componentId: 'c1', name: 'debug', version: '4.4.2',
          malicious: 1, critical: 0, high: 0, medium: 0, low: 0, unknown: 0,
          scannedAt: '2026-08-06T00:00:00Z',
        }],
      })
    }),
  )
  return urls
}

async function openDashboard() {
  const user = userEvent.setup()
  renderWithProviders(<SecurityPage />)
  await screen.findByText('nx-admin')
  await user.click(screen.getByRole('button', { name: 'Vulnerability Dashboard' }))
  await screen.findByText('debug')
  return user
}

describe('SecurityPage — exporting scan results', () => {
  let clickSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    seedAuthAsAdmin()
    seedBaseline()
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: vi.fn(() => 'blob:x') })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() })
    clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('downloads the dashboard as CSV', async () => {
    const urls = captureVulnRequests()
    const user = await openDashboard()

    await user.click(screen.getByRole('button', { name: /Export CSV/i }))

    await waitFor(() => {
      expect(urls.some((u) => new URL(u).searchParams.get('export') === 'csv')).toBe(true)
    })
    await waitFor(() => expect(clickSpy).toHaveBeenCalled())
  })

  it('downloads the dashboard as JSON', async () => {
    const urls = captureVulnRequests()
    const user = await openDashboard()

    await user.click(screen.getByRole('button', { name: /Export JSON/i }))

    await waitFor(() => {
      expect(urls.some((u) => new URL(u).searchParams.get('export') === 'json')).toBe(true)
    })
  })

  // The other view that shows findings: a single component's CVE list.
  it('downloads a single component scan as CSV', async () => {
    let exportedAs: string | null = null
    server.use(
      http.post('/api/v1/components/:id/scan', () =>
        HttpResponse.json({
          scannedAt: '2026-08-06T00:00:00Z', imageRef: 'debug:4.4.2', status: 'ok',
          summary: { malicious: 1, critical: 0, high: 0, medium: 0, low: 0, unknown: 0, total: 1 },
          findings: [{ id: 'MAL-2025-46974', severity: 'MALICIOUS', pkgName: 'debug', installedVersion: '4.4.2', title: 'Malicious code' }],
        }),
      ),
      http.get('/api/v1/components/:id/scan', ({ request }) => {
        exportedAs = new URL(request.url).searchParams.get('export')
        return HttpResponse.text('id,severity\nMAL-2025-46974,MALICIOUS\n', {
          headers: { 'Content-Type': 'text/csv' },
        })
      }),
    )

    const user = userEvent.setup()
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'CVE Scan' }))
    await screen.findByText('Trivy Vulnerability Scan')

    await user.type(screen.getByPlaceholderText(/component id/i), 'comp-1')
    await user.click(screen.getByRole('button', { name: 'Scan' }))
    await screen.findByText('MAL-2025-46974')

    await user.click(screen.getByRole('button', { name: /Export CSV/i }))
    await waitFor(() => expect(exportedAs).toBe('csv'))
    await waitFor(() => expect(clickSpy).toHaveBeenCalled())
  })

  // An export that ignores the filters on screen is a different report from the
  // one the user is looking at.
  it('exports what is on screen, filters included', async () => {
    const urls = captureVulnRequests()
    const user = await openDashboard()

    await user.selectOptions(screen.getByRole('combobox'), 'MALICIOUS')
    await screen.findByText('debug')
    await user.click(screen.getByRole('button', { name: /Export CSV/i }))

    await waitFor(() => {
      const exportURL = urls.map((u) => new URL(u)).find((u) => u.searchParams.get('export') === 'csv')
      expect(exportURL?.searchParams.get('severity')).toBe('MALICIOUS')
    })
  })
})
