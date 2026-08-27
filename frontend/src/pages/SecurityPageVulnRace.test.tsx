import { it, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import SecurityPage from './SecurityPage'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'
import { fixtures } from '@/test/fixtures'

/**
 * #337 gap 2: the vulnerability table applies responses in resolution order,
 * not request order. A broader, earlier query that resolves late must not
 * overwrite the narrower, current one — the table would show rows for a filter
 * the input no longer contains.
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
      HttpResponse.json({ malicious: 0, critical: 0, high: 0, medium: 0, low: 0, unknown: 0, scanned_total: 1 }),
    ),
  )
}

beforeEach(() => {
  seedAuthAsAdmin()
  seedBaseline()
})
afterEach(() => {
  vi.restoreAllMocks()
})

function vulnRow(repoName: string) {
  return {
    repoName, format: 'npm', componentId: 'c-' + repoName, name: 'pkg-for-' + repoName, version: '1.0.0',
    malicious: 0, critical: 1, high: 0, medium: 0, low: 0, unknown: 0,
    scannedAt: '2026-08-26T00:00:00Z',
  }
}

it('a superseded filter response resolving late does not overwrite the current one', async () => {
  const user = userEvent.setup()
  // Each request parks until the test releases it, keyed by its repo filter.
  const release = new Map<string, () => void>()
  server.use(
    http.get('/api/v1/security/vulnerabilities', async ({ request }) => {
      const repo = new URL(request.url).searchParams.get('repo') ?? ''
      await new Promise<void>(res => { release.set(repo, res) })
      return HttpResponse.json({ total: 1, items: [vulnRow(repo || 'all')] })
    }),
  )

  renderWithProviders(<SecurityPage />)
  await user.click(await screen.findByRole('button', { name: 'Vulnerability Dashboard' }))

  // Initial unfiltered load.
  await waitFor(() => expect(release.has('')).toBe(true))
  release.get('')!()
  expect(await screen.findByText('pkg-for-all')).toBeInTheDocument()

  const filter = screen.getByPlaceholderText('Filter by repo')
  fireEvent.change(filter, { target: { value: 'n' } })
  await waitFor(() => expect(release.has('n')).toBe(true))
  fireEvent.change(filter, { target: { value: 'npm' } })
  await waitFor(() => expect(release.has('npm')).toBe(true))

  // The narrower, CURRENT request resolves first…
  release.get('npm')!()
  expect(await screen.findByText('pkg-for-npm')).toBeInTheDocument()

  // …then the superseded broader one trickles in late. It must be discarded.
  release.get('n')!()
  await new Promise(r => setTimeout(r, 100))
  expect(screen.queryByText('pkg-for-n')).not.toBeInTheDocument()
  expect(screen.getByText('pkg-for-npm')).toBeInTheDocument()
})
