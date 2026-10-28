import { describe, it, expect, beforeEach } from 'vitest'
import { screen, waitFor, within, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from '@/test/msw/http'
import RepositoriesPage from './RepositoriesPage'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'
import { fixtures } from '@/test/fixtures'

// Pull gate (#641): docker/oci repositories of any type carry
// formatConfig.pull_block_severity; absent or empty is off.

const dockerProxy = fixtures.repository({
  id: 'p1', name: 'docker-proxy', format: 'docker', type: 'proxy', online: true,
  proxyConfig: { remote_url: 'https://registry-1.docker.io' },
  formatConfig: { pull_block_severity: 'critical', keep_me: 'x' },
})
const dockerHosted = fixtures.repository({
  id: 'h1', name: 'docker-hosted', format: 'docker', type: 'hosted', online: true,
  formatConfig: { write_policy: 'allow_once' },
})
const rawHosted = fixtures.repository({
  id: 'r1', name: 'raw-hosted', format: 'raw', type: 'hosted', online: true,
})

function seed() {
  server.use(
    http.get('/service/rest/v1/repositories', () => HttpResponse.json([dockerProxy, dockerHosted, rawHosted])),
    http.get('/service/rest/v1/blobstores', () =>
      HttpResponse.json([{ id: 'bs-1', name: 'default', type: 'file', quotaBytes: null, usedBytes: 0 }])),
    http.get('/service/rest/v1/cleanup-policies', () => HttpResponse.json([])),
    http.get('/service/rest/v1/routing-rules', () => HttpResponse.json([])),
    http.get('/api/v1/repositories/:name/quota', () =>
      HttpResponse.json({ usedBytes: 1, quotaBytes: null, percentUsed: null })),
  )
}

async function openSettingsFor(name: string) {
  const label = await screen.findByText(name)
  const row = label.closest('div[tabindex]') as HTMLElement
  fireEvent.click(within(row).getByTitle('Settings'))
  await screen.findByText('Repository settings')
}

function capture(method: 'post' | 'put') {
  const seen: { body: Record<string, unknown> | null } = { body: null }
  const path = method === 'post'
    ? '/service/rest/v1/repositories/:format/:type'
    : '/service/rest/v1/repositories/:format/:type/:name'
  server.use(
    http[method](path, async ({ request }) => {
      seen.body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json(fixtures.repository(), { status: method === 'post' ? 201 : 200 })
    }),
  )
  return seen
}

const FIELD = 'Refuse pulls at severity'

async function save(seen: { body: Record<string, unknown> | null }) {
  const form = document.querySelector('form') as HTMLFormElement
  fireEvent.click(within(form).getByRole('button', { name: /^Save$/ }))
  await waitFor(() => expect(seen.body).toBeTruthy())
}

describe('RepositoriesPage — pull gate', () => {
  beforeEach(() => {
    seedAuthAsAdmin()
    seed()
  })

  it('creates a docker proxy with the gate on', async () => {
    const user = userEvent.setup()
    const seen = capture('post')
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('docker-proxy')
    await user.click(screen.getByRole('button', { name: /Create Repository/ }))
    await screen.findByText('Step 1 of 3')
    await user.click(screen.getByText('maven2'))
    await user.click(await screen.findByRole('option', { name: 'docker' }))
    await user.click(screen.getByText(/^Hosted — /))
    await user.click(await screen.findByText(/^Proxy — /))
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 2 of 3')
    await user.type(screen.getByPlaceholderText('my-repo'), 'hub')

    expect(screen.getByText(FIELD)).toBeInTheDocument()
    expect(screen.getByText('Images are served whatever their scan found.')).toBeInTheDocument()
    await user.click(screen.getByText('Off'))
    await user.click(await screen.findByText('High or higher'))
    expect(screen.getByText(/the first pull brings it in to be scanned/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 3 of 3')
    await user.click(screen.getByRole('button', { name: /^Create$/ }))

    await waitFor(() => expect(seen.body).toBeTruthy())
    expect(seen.body!.formatConfig).toEqual({ pull_block_severity: 'high' })
  })

  it('leaves formatConfig alone when the gate stays off on create', async () => {
    const user = userEvent.setup()
    const seen = capture('post')
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('docker-proxy')
    await user.click(screen.getByRole('button', { name: /Create Repository/ }))
    await screen.findByText('Step 1 of 3')
    await user.click(screen.getByText('maven2'))
    await user.click(await screen.findByRole('option', { name: 'docker' }))
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 2 of 3')
    await user.type(screen.getByPlaceholderText('my-repo'), 'images')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 3 of 3')
    await user.click(screen.getByRole('button', { name: /^Create$/ }))

    await waitFor(() => expect(seen.body).toBeTruthy())
    expect(seen.body!.formatConfig).toEqual({ write_policy: 'allow' })
  })

  it('does not offer the gate for a non-image format', async () => {
    const user = userEvent.setup()
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('docker-proxy')
    await user.click(screen.getByRole('button', { name: /Create Repository/ }))
    await screen.findByText('Step 1 of 3')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 2 of 3')
    expect(screen.queryByText(FIELD)).not.toBeInTheDocument()
  })

  it('does not show the gate when editing a non-image repository', async () => {
    renderWithProviders(<RepositoriesPage />)
    await openSettingsFor('raw-hosted')
    expect(screen.queryByText(FIELD)).not.toBeInTheDocument()
  })

  it('prefills the stored threshold on a proxy and keeps other formatConfig keys', async () => {
    const user = userEvent.setup()
    const seen = capture('put')
    renderWithProviders(<RepositoriesPage />)
    await openSettingsFor('docker-proxy')

    expect(screen.getByText('Critical or higher')).toBeInTheDocument()
    await user.click(screen.getByText('Critical or higher'))
    await user.click(await screen.findByText('Malicious only'))
    await save(seen)
    expect(seen.body!.formatConfig).toEqual({ pull_block_severity: 'malicious', keep_me: 'x' })
  })

  it('turns the gate off by dropping the key', async () => {
    const user = userEvent.setup()
    const seen = capture('put')
    renderWithProviders(<RepositoriesPage />)
    await openSettingsFor('docker-proxy')
    await user.click(screen.getByText('Critical or higher'))
    await user.click(await screen.findByText('Off'))
    await save(seen)
    expect(seen.body!.formatConfig).toEqual({ keep_me: 'x' })
  })

  it('sends the gate alongside the write policy on a hosted repo', async () => {
    const user = userEvent.setup()
    const seen = capture('put')
    renderWithProviders(<RepositoriesPage />)
    await openSettingsFor('docker-hosted')
    await user.click(screen.getByText('Off'))
    await user.click(await screen.findByText('Medium or higher'))
    expect(screen.getByText(/An image with no finished scan is refused too/)).toBeInTheDocument()
    await save(seen)
    expect(seen.body!.formatConfig).toEqual({ write_policy: 'allow_once', pull_block_severity: 'medium' })
  })
})
