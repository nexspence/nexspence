import { describe, it, expect, beforeEach } from 'vitest'
import { screen, waitFor, within, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import RepositoriesPage from './RepositoriesPage'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'
import { fixtures } from '@/test/fixtures'

// Deployment policy (#539): hosted repositories carry formatConfig.write_policy
// (allow | allow_once | deny); docker/oci under allow_once may also set
// allow_redeploy_latest.

const dockerHosted = fixtures.repository({
  id: 'r1', name: 'docker-hosted', format: 'docker', type: 'hosted', online: true,
  formatConfig: { write_policy: 'allow_once', allow_redeploy_latest: true, keep_me: 'x' },
})
const rawProxy = fixtures.repository({
  id: 'r2', name: 'raw-proxy', format: 'raw', type: 'proxy', online: true,
  proxyConfig: { remote_url: 'https://example.com/' },
})
const rawGroup = fixtures.repository({
  id: 'r3', name: 'raw-group', format: 'raw', type: 'group', online: true,
  formatConfig: { member_names: ['raw-proxy'] },
})

function seed(list = [dockerHosted, rawProxy, rawGroup]) {
  server.use(
    http.get('/service/rest/v1/repositories', () => HttpResponse.json(list)),
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

function captureCreate() {
  const seen: { body: Record<string, unknown> | null } = { body: null }
  server.use(
    http.post('/service/rest/v1/repositories/:format/:type', async ({ request }) => {
      seen.body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json(fixtures.repository(), { status: 201 })
    }),
  )
  return seen
}

function captureUpdate() {
  const seen: { body: Record<string, unknown> | null } = { body: null }
  server.use(
    http.put('/service/rest/v1/repositories/:format/:type/:name', async ({ request }) => {
      seen.body = (await request.json()) as Record<string, unknown>
      return HttpResponse.json(fixtures.repository())
    }),
  )
  return seen
}

const LATEST = /Allow redeploy of 'latest'/

describe('RepositoriesPage — deployment policy', () => {
  beforeEach(() => {
    seedAuthAsAdmin()
    seed()
  })

  it('creates a maven hosted repo with Disable redeploy, without the latest option', async () => {
    const user = userEvent.setup()
    const seen = captureCreate()
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('docker-hosted')
    await user.click(screen.getByRole('button', { name: /Create Repository/ }))
    await screen.findByText('Step 1 of 3')
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 2 of 3')

    expect(screen.getByText('Deployment policy')).toBeInTheDocument()
    await user.type(screen.getByPlaceholderText('my-repo'), 'releases')
    await user.click(screen.getByText('Allow redeploy'))
    await user.click(await screen.findByText('Disable redeploy'))
    expect(screen.queryByText(LATEST)).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 3 of 3')
    await user.click(screen.getByRole('button', { name: /^Create$/ }))

    await waitFor(() => expect(seen.body).toBeTruthy())
    expect(seen.body!.formatConfig).toEqual({ write_policy: 'allow_once' })
  })

  it('offers the latest exemption for docker only under Disable redeploy and sends it', async () => {
    const user = userEvent.setup()
    const seen = captureCreate()
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('docker-hosted')
    await user.click(screen.getByRole('button', { name: /Create Repository/ }))
    await screen.findByText('Step 1 of 3')
    await user.click(screen.getByText('maven2'))
    await user.click(await screen.findByRole('option', { name: 'docker' }))
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 2 of 3')
    await user.type(screen.getByPlaceholderText('my-repo'), 'images')

    expect(screen.queryByText(LATEST)).not.toBeInTheDocument()
    await user.click(screen.getByText('Allow redeploy'))
    await user.click(await screen.findByText('Disable redeploy'))
    await user.click(screen.getByRole('checkbox', { name: LATEST }))
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 3 of 3')
    await user.click(screen.getByRole('button', { name: /^Create$/ }))

    await waitFor(() => expect(seen.body).toBeTruthy())
    expect(seen.body!.formatConfig).toEqual({ write_policy: 'allow_once', allow_redeploy_latest: true })
  })

  it('does not show the policy for a proxy repository in the wizard', async () => {
    const user = userEvent.setup()
    renderWithProviders(<RepositoriesPage />)
    await screen.findByText('docker-hosted')
    await user.click(screen.getByRole('button', { name: /Create Repository/ }))
    await screen.findByText('Step 1 of 3')
    await user.click(screen.getByText(/^Hosted — /))
    await user.click(await screen.findByText(/^Proxy — /))
    await user.click(screen.getByRole('button', { name: /Next/ }))
    await screen.findByText('Step 2 of 3')
    expect(screen.queryByText('Deployment policy')).not.toBeInTheDocument()
  })

  it('prefills the stored policy when editing and keeps other formatConfig keys on save', async () => {
    const user = userEvent.setup()
    const seen = captureUpdate()
    renderWithProviders(<RepositoriesPage />)
    await openSettingsFor('docker-hosted')

    expect(screen.getByText('Disable redeploy')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: LATEST })).toBeChecked()

    await user.click(screen.getByText('Disable redeploy'))
    await user.click(await screen.findByText('Read-only'))
    expect(screen.queryByText(LATEST)).not.toBeInTheDocument()

    const form = document.querySelector('form') as HTMLFormElement
    fireEvent.click(within(form).getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(seen.body).toBeTruthy())
    expect(seen.body!.formatConfig).toEqual({ write_policy: 'deny', keep_me: 'x' })
  })

  it('clears the latest exemption when it is unchecked', async () => {
    const user = userEvent.setup()
    const seen = captureUpdate()
    renderWithProviders(<RepositoriesPage />)
    await openSettingsFor('docker-hosted')
    await user.click(screen.getByRole('checkbox', { name: LATEST }))
    const form = document.querySelector('form') as HTMLFormElement
    fireEvent.click(within(form).getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(seen.body).toBeTruthy())
    expect(seen.body!.formatConfig).toEqual({ write_policy: 'allow_once', keep_me: 'x' })
  })

  it('does not show the policy when editing a proxy or a group', async () => {
    renderWithProviders(<RepositoriesPage />)
    await openSettingsFor('raw-proxy')
    expect(screen.queryByText('Deployment policy')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Cancel/ }))
    await openSettingsFor('raw-group')
    expect(screen.queryByText('Deployment policy')).not.toBeInTheDocument()
  })
})
