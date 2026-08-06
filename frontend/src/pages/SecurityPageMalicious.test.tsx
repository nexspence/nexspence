import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import SecurityPage from './SecurityPage'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'
import { fixtures } from '@/test/fixtures'

/**
 * A malicious-package report (OSV.dev `MAL-…`) carries no CVSS severity, so it
 * used to be counted as "Unknown" — a column the org-wide dashboard did not even
 * render. These cover the visibility side of that gap.
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
  )
}

function seedDashboard(opts: { malicious: number }) {
  server.use(
    http.get('/api/v1/security/summary', () =>
      HttpResponse.json({
        malicious: opts.malicious, critical: 3, high: 5, medium: 1, low: 0, unknown: 0, scanned_total: 9,
      }),
    ),
    http.get('/api/v1/security/vulnerabilities', () =>
      HttpResponse.json({
        total: 1,
        items: [
          {
            repoName: 'npm-hosted', format: 'npm', componentId: 'c1', name: 'debug', version: '4.4.2',
            malicious: opts.malicious, critical: 0, high: 0, medium: 0, low: 0, unknown: 0,
            scannedAt: '2026-06-01T00:00:00Z',
          },
        ],
      }),
    ),
  )
}

async function openDashboard() {
  const user = userEvent.setup()
  renderWithProviders(<SecurityPage />)
  await screen.findByText('nx-admin')
  await user.click(screen.getByRole('button', { name: 'Vulnerability Dashboard' }))
  return user
}

describe('SecurityPage — malicious packages', () => {
  beforeEach(() => {
    seedAuthAsAdmin()
    seedBaseline()
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows a MALICIOUS summary tile with its own count', async () => {
    seedDashboard({ malicious: 2 })
    await openDashboard()

    // `selector: 'div'` skips the same-named <option> in the severity filter.
    const tile = (await screen.findByText('MALICIOUS', { selector: 'div' })).parentElement
    expect(tile).toBeTruthy()
    expect(within(tile as HTMLElement).getByText('2')).toBeInTheDocument()
  })

  it('renders a malicious column in the dashboard grid', async () => {
    seedDashboard({ malicious: 1 })
    await openDashboard()

    await screen.findByText('debug')
    expect(screen.getByRole('columnheader', { name: 'MAL' })).toBeInTheDocument()
  })

  it('offers MALICIOUS as its own filter and sends it to the API', async () => {
    seedDashboard({ malicious: 1 })
    let requestedSeverity: string | null = null
    server.use(
      http.get('/api/v1/security/vulnerabilities', ({ request }) => {
        requestedSeverity = new URL(request.url).searchParams.get('severity')
        return HttpResponse.json({ total: 0, items: [] })
      }),
    )
    const user = await openDashboard()

    const select = await screen.findByRole('combobox')
    await user.selectOptions(select, 'MALICIOUS')

    expect(requestedSeverity).toBe('MALICIOUS')
  })
})
