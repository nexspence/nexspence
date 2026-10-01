import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, waitFor, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import SecurityPage from './SecurityPage'
import {
  renderWithProviders,
  seedAuthAsAdmin,
} from '@/test/renderUtils'
import { server } from '@/test/msw/server'
import { fixtures } from '@/test/fixtures'
import { useAuthStore } from '@/store/authStore'

const roles = [
  { id: 'role-1', name: 'nx-admin', description: 'Admin role', privileges: ['p1'], roles: [], readOnly: true, source: 'default' },
  { id: 'role-2', name: 'developer', description: 'Dev role', privileges: ['p1', 'p2'], roles: [], readOnly: false },
]

const privileges = [
  { id: 'p1', name: 'read-all', description: 'read everything', type: 'repository-content-selector', attrs: { actions: ['read', 'browse'] }, contentSelectorId: 'cs-1', readOnly: false },
  { id: 'p2', name: 'admin-builtin', description: 'built in', type: 'wildcard', attrs: {}, readOnly: true },
]

const selectors = [
  { id: 'cs-1', name: 'all-maven', description: 'maven sel', expression: 'repository == "maven-hosted"' },
]

function seedSecurity() {
  server.use(
    http.get('/service/rest/v1/security/roles', () => HttpResponse.json(roles)),
    http.get('/service/rest/v1/security/privileges', () => HttpResponse.json(privileges)),
    http.get('/service/rest/v1/security/content-selectors', () => HttpResponse.json(selectors)),
    http.get('/service/rest/v1/security/roles/:roleId/privileges', () => HttpResponse.json([privileges[0]])),
    http.get('/api/v1/security/privilege-role-map', () => HttpResponse.json({ p1: ['developer'] })),
    http.get('/service/rest/v1/repositories', () => HttpResponse.json([fixtures.repository()])),
    http.get('/service/rest/v1/security/users', () => HttpResponse.json([fixtures.user()])),
    http.get('/api/v1/webhooks', () => HttpResponse.json([
      { id: 'wh-1', name: 'slack', url: 'https://hooks.slack.com/x', events: ['artifact.published'], active: true },
    ])),
  )
}

function seedAdmin() {
  seedAuthAsAdmin()
}

function seedNonAdmin() {
  useAuthStore.setState({
    token: 'tok',
    user: fixtures.user({ roles: ['viewer'] }) as ReturnType<typeof fixtures.user>,
  })
}

describe('SecurityPage', () => {
  beforeEach(() => {
    seedAdmin()
    seedSecurity()
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  /* ── Default tab + tab structure ── */
  it('renders the Roles tab by default with roles', async () => {
    renderWithProviders(<SecurityPage />)
    expect(await screen.findByText('nx-admin')).toBeInTheDocument()
    expect(screen.getByText('developer')).toBeInTheDocument()
    expect(screen.getByText('built-in')).toBeInTheDocument()
  })

  it('shows all admin tabs for an admin', async () => {
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    expect(screen.getByRole('button', { name: 'Users' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'CVE Scan' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Vulnerability Dashboard' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Webhooks' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Access Map' })).toBeInTheDocument()
  })

  it('hides admin-only tabs for a non-admin', async () => {
    seedNonAdmin()
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    expect(screen.queryByRole('button', { name: 'CVE Scan' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Webhooks' })).not.toBeInTheDocument()
    // non-admin subtitle
    expect(screen.getByText(/Content Selector → Privilege → Role/)).toBeInTheDocument()
    // no New Role button
    expect(screen.queryByRole('button', { name: /New Role/ })).not.toBeInTheDocument()
  })

  it('expands a role to show its privileges', async () => {
    const user = userEvent.setup()
    renderWithProviders(<SecurityPage />)
    await screen.findByText('developer')
    // developer has 2 privileges → expand button "2 privileges"
    await user.click(screen.getByText(/2 privileges/))
    expect(await screen.findByText('read-all')).toBeInTheDocument()
  })

  it('filters roles via search', async () => {
    const user = userEvent.setup()
    renderWithProviders(<SecurityPage />)
    await screen.findByText('developer')
    await user.type(screen.getByPlaceholderText('Search roles…'), 'admin')
    await waitFor(() => expect(screen.queryByText('developer')).not.toBeInTheDocument())
    expect(screen.getByText('nx-admin')).toBeInTheDocument()
  })

  it('refreshes roles via the refresh button', async () => {
    let calls = 0
    server.use(
      http.get('/service/rest/v1/security/roles', () => {
        calls++
        return HttpResponse.json(roles)
      }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    const before = calls
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(calls).toBeGreaterThan(before))
  })

  /* ── Roles create ── */
  it('creates a new role', async () => {
    const user = userEvent.setup()
    let posted: { name: string } | null = null
    server.use(
      http.post('/service/rest/v1/security/roles', async ({ request }) => {
        posted = (await request.json()) as { name: string }
        return HttpResponse.json({ id: 'role-new', name: 'qa' }, { status: 201 })
      }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: /New Role/ }))
    const heading = await screen.findByRole('heading', { name: 'New Role' })
    const dialog = heading.closest('.holo-modal') as HTMLElement
    await user.type(within(dialog).getByPlaceholderText('Name *'), 'qa')
    await user.click(within(dialog).getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(posted).toBeTruthy())
    expect(posted!.name).toBe('qa')
  })

  it('shows an error when role creation fails', async () => {
    const user = userEvent.setup()
    server.use(
      http.post('/service/rest/v1/security/roles', () =>
        HttpResponse.json({ error: 'role exists' }, { status: 400 }),
      ),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: /New Role/ }))
    const heading = await screen.findByRole('heading', { name: 'New Role' })
    const dialog = heading.closest('.holo-modal') as HTMLElement
    await user.type(within(dialog).getByPlaceholderText('Name *'), 'dup')
    await user.click(within(dialog).getByRole('button', { name: /^Save$/ }))
    expect(await screen.findByText('role exists')).toBeInTheDocument()
  })

  it('opens the edit role modal and saves', async () => {
    const user = userEvent.setup()
    let saved = false
    server.use(
      http.put('/service/rest/v1/security/roles/:id', () => {
        saved = true
        return HttpResponse.json({ id: 'role-2' })
      }),
      http.put('/service/rest/v1/security/roles/:id/privileges', () =>
        new HttpResponse(null, { status: 204 }),
      ),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('developer')
    await user.click(screen.getByRole('button', { name: 'Edit' }))
    expect(await screen.findByText(/Edit Role: developer/)).toBeInTheDocument()
    const dialog = screen.getByText(/Edit Role: developer/).closest('.holo-modal') as HTMLElement
    const save = within(dialog).getByRole('button', { name: /^Save$/ })
    await waitFor(() => expect(save).toBeEnabled())
    await user.click(save)
    await waitFor(() => expect(saved).toBe(true))
  })

  // #571: Save must stay disabled until the role's privileges have loaded,
  // and the role and its privileges go out in one PUT /roles/:id — the
  // backend replaces the privilege list on that call, so a separate
  // name-only PUT would briefly (or, on failure, permanently) empty the role.
  it('keeps Save disabled while the role privileges load, then saves them in one request', async () => {
    const user = userEvent.setup()
    let release!: () => void
    const held = new Promise<void>(r => { release = r })
    const puts: { url: string; body: unknown }[] = []
    server.use(
      http.get('/service/rest/v1/security/roles/:roleId/privileges', async () => {
        await held
        return HttpResponse.json([privileges[0]])
      }),
      http.put('/service/rest/v1/security/roles/:id', async ({ request }) => {
        puts.push({ url: new URL(request.url).pathname, body: await request.json() })
        return HttpResponse.json({ id: 'role-2' })
      }),
      http.put('/service/rest/v1/security/roles/:id/privileges', async ({ request }) => {
        puts.push({ url: new URL(request.url).pathname, body: await request.json() })
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('developer')
    await user.click(screen.getByRole('button', { name: 'Edit' }))
    const dialog = (await screen.findByText(/Edit Role: developer/)).closest('.holo-modal') as HTMLElement
    expect(within(dialog).getByText('Loading privileges…')).toBeInTheDocument()
    const save = within(dialog).getByRole('button', { name: /^Save$/ })
    expect(save).toBeDisabled()

    release()
    await waitFor(() => expect(save).toBeEnabled())
    await user.click(save)
    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0].url).toBe('/service/rest/v1/security/roles/role-2')
    expect(puts[0].body).toMatchObject({ name: 'developer', privileges: ['p1'] })
  })

  it('does not carry over the previous role privileges when loading this role fails', async () => {
    const user = userEvent.setup()
    let puts = 0
    server.use(
      http.get('/service/rest/v1/security/roles', () => HttpResponse.json([
        ...roles,
        { id: 'role-3', name: 'qa', description: 'QA role', privileges: ['p1'], roles: [], readOnly: false },
      ])),
      http.get('/service/rest/v1/security/roles/:roleId/privileges', ({ params }) =>
        params.roleId === 'role-2'
          ? HttpResponse.json({ error: 'privileges unavailable' }, { status: 500 })
          : HttpResponse.json([privileges[0]]),
      ),
      http.put('/service/rest/v1/security/roles/:id', () => { puts++; return HttpResponse.json({}) }),
      http.put('/service/rest/v1/security/roles/:id/privileges', () => { puts++; return new HttpResponse(null, { status: 204 }) }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('qa')
    const editButtons = screen.getAllByRole('button', { name: 'Edit' })

    // qa first: its privilege p1 lands in the checklist.
    await user.click(editButtons[1])
    let dialog = (await screen.findByText(/Edit Role: qa/)).closest('.holo-modal') as HTMLElement
    await waitFor(() => expect(within(dialog).getByRole('button', { name: /^Save$/ })).toBeEnabled())
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))

    // developer's privileges fail to load: the checklist must not keep qa's.
    await user.click(editButtons[0])
    dialog = (await screen.findByText(/Edit Role: developer/)).closest('.holo-modal') as HTMLElement
    expect(await within(dialog).findByText('privileges unavailable')).toBeInTheDocument()
    expect(within(dialog).getByText('None selected')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: /^Save$/ })).toBeDisabled()
    expect(puts).toBe(0)
  })

  it('deletes a role from the edit modal after confirm', async () => {
    const user = userEvent.setup()
    let deleted = false
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    server.use(
      http.delete('/service/rest/v1/security/roles/:id', () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('developer')
    await user.click(screen.getByRole('button', { name: 'Edit' }))
    await screen.findByText(/Edit Role: developer/)
    const dialog = screen.getByText(/Edit Role: developer/).closest('.holo-modal') as HTMLElement
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(deleted).toBe(true))
  })

  /* ── Privileges tab ── */
  it('switches to the Privileges tab and lists privileges', async () => {
    const user = userEvent.setup()
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Privileges' }))
    expect(await screen.findByText('read-all')).toBeInTheDocument()
    expect(screen.getByText('admin-builtin')).toBeInTheDocument()
    // role chip from privilege-role-map
    expect(screen.getByText('developer')).toBeInTheDocument()
  })

  it('creates a privilege with a content selector', async () => {
    const user = userEvent.setup()
    let posted: Record<string, unknown> | null = null
    server.use(
      http.post('/service/rest/v1/security/privileges', async ({ request }) => {
        posted = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ id: 'priv-new' }, { status: 201 })
      }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Privileges' }))
    await screen.findByText('read-all')
    await user.click(screen.getByRole('button', { name: /New Privilege/ }))
    const heading = await screen.findByRole('heading', { name: 'New Privilege' })
    const dialog = heading.closest('.holo-modal') as HTMLElement
    await user.type(within(dialog).getByPlaceholderText('Name *'), 'my-priv')
    // pick content selector via portal Select
    await user.click(within(dialog).getByRole('button', { name: /select a content selector/ }))
    await user.click(await screen.findByText('all-maven'))
    // No action ticked yet: RBAC reads an empty list as "every action" (#565),
    // so Save stays disabled until at least one action is chosen.
    const saveBtn = within(dialog).getByRole('button', { name: /^Save$/ })
    expect(saveBtn).toBeDisabled()
    await user.click(within(dialog).getByRole('checkbox', { name: 'read' }))
    expect(saveBtn).toBeEnabled()
    await user.click(saveBtn)
    await waitFor(() => expect(posted).toBeTruthy())
    expect((posted! as { name: string }).name).toBe('my-priv')
    expect((posted! as { contentSelectorId: string }).contentSelectorId).toBe('cs-1')
    expect((posted! as { attrs: { actions: string[] } }).attrs.actions).toEqual(['read'])
  })

  it('disables Save on an edited privilege once every action is cleared', async () => {
    const user = userEvent.setup()
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Privileges' }))
    await screen.findByText('read-all')
    await user.click(screen.getAllByRole('button', { name: 'Edit' })[0])
    const heading = await screen.findByRole('heading', { name: 'Edit Privilege' })
    const dialog = heading.closest('.holo-modal') as HTMLElement
    const saveBtn = within(dialog).getByRole('button', { name: /^Save$/ })
    expect(saveBtn).toBeEnabled()
    await user.click(within(dialog).getByRole('checkbox', { name: 'read' }))
    await user.click(within(dialog).getByRole('checkbox', { name: 'browse' }))
    expect(saveBtn).toBeDisabled()
  })

  it('filters privileges via search', async () => {
    const user = userEvent.setup()
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Privileges' }))
    await screen.findByText('read-all')
    await user.type(screen.getByPlaceholderText('Search privileges…'), 'admin-builtin')
    await waitFor(() => expect(screen.queryByText('read-all')).not.toBeInTheDocument())
  })

  it('deletes a privilege after confirm', async () => {
    const user = userEvent.setup()
    let deleted = false
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    server.use(
      http.delete('/service/rest/v1/security/privileges/:id', () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Privileges' }))
    await screen.findByText('read-all')
    // p1 is not readOnly → has Edit + delete; click the delete (Trash) button in its row
    const editBtns = screen.getAllByRole('button', { name: 'Edit' })
    // its sibling delete button
    const row = editBtns[0].closest('div')!.parentElement as HTMLElement
    const buttons = within(row).getAllByRole('button')
    fireEvent.click(buttons[buttons.length - 1])
    await waitFor(() => expect(deleted).toBe(true))
  })

  /* ── Content selectors tab ── */
  it('switches to the Content Selectors tab', async () => {
    const user = userEvent.setup()
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Content Selectors' }))
    expect(await screen.findByText('all-maven')).toBeInTheDocument()
    // linked priv chip
    expect(screen.getByText('read-all')).toBeInTheDocument()
  })

  it('creates a content selector via repo dropdown', async () => {
    const user = userEvent.setup()
    let posted: Record<string, unknown> | null = null
    server.use(
      http.post('/service/rest/v1/security/content-selectors', async ({ request }) => {
        posted = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ id: 'cs-new' }, { status: 201 })
      }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Content Selectors' }))
    await screen.findByText('all-maven')
    await user.click(screen.getByRole('button', { name: /New Selector/ }))
    expect(await screen.findByText('New Content Selector')).toBeInTheDocument()
    const dialog = screen.getByText('New Content Selector').closest('.holo-modal') as HTMLElement
    await user.type(within(dialog).getByPlaceholderText('Name *'), 'my-cs')
    // open repo dropdown then pick the repo
    await user.click(within(dialog).getByPlaceholderText('Search repositories…'))
    await user.click(await within(dialog).findByText('maven-hosted'))
    // CEL preview shows
    expect(await within(dialog).findByText(/repository == "maven-hosted"/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(posted).toBeTruthy())
    expect((posted! as { name: string }).name).toBe('my-cs')
  })

  // #572: once the picker has a value the legacy textarea is hidden and the
  // preview shows the picked expression — that is what must be saved, not
  // the stale legacy text.
  it('saves the picked repository when editing a legacy selector', async () => {
    const user = userEvent.setup()
    let putBody: { expression: string } | null = null
    server.use(
      http.get('/service/rest/v1/security/content-selectors', () => HttpResponse.json([
        { id: 'cs-legacy', name: 'legacy-sel', description: '', expression: 'repository != "maven-hosted"' },
      ])),
      http.put('/service/rest/v1/security/content-selectors/:id', async ({ request }) => {
        putBody = (await request.json()) as { expression: string }
        return HttpResponse.json({ id: 'cs-legacy' })
      }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Content Selectors' }))
    await screen.findByText('legacy-sel')
    await user.click(screen.getByRole('button', { name: 'Edit' }))
    const dialog = (await screen.findByText('Edit Content Selector')).closest('.holo-modal') as HTMLElement
    expect(within(dialog).getByText(/CEL Expression \(legacy/)).toBeInTheDocument()

    await user.click(within(dialog).getByPlaceholderText('Search repositories…'))
    await user.click(await within(dialog).findByText('maven-hosted'))
    expect(await within(dialog).findByText('repository == "maven-hosted"')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(putBody).toBeTruthy())
    expect(putBody!.expression).toBe('repository == "maven-hosted"')
  })

  it('deletes a content selector after confirm', async () => {
    const user = userEvent.setup()
    let deleted = false
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    server.use(
      http.delete('/service/rest/v1/security/content-selectors/:id', () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Content Selectors' }))
    await screen.findByText('all-maven')
    const row = screen.getByText('all-maven').closest('div')!.parentElement!.parentElement as HTMLElement
    const buttons = within(row).getAllByRole('button')
    fireEvent.click(buttons[buttons.length - 1])
    await waitFor(() => expect(deleted).toBe(true))
  })

  /* ── CVE Scan tab ── */
  it('switches to CVE Scan tab and validates component id', async () => {
    const user = userEvent.setup()
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'CVE Scan' }))
    expect(await screen.findByText('Trivy Vulnerability Scan')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Scan' }))
    expect(await screen.findByText('Enter a component ID')).toBeInTheDocument()
  })

  it('runs a CVE scan and shows findings', async () => {
    const user = userEvent.setup()
    server.use(
      http.post('/api/v1/components/:id/scan', () =>
        HttpResponse.json({
          scannedAt: '2026-06-01T00:00:00Z',
          imageRef: 'alpine:3.18',
          status: 'ok',
          summary: { critical: 1, high: 2, medium: 0, low: 0, unknown: 0, total: 3 },
          findings: [
            { id: 'CVE-2024-1', severity: 'CRITICAL', pkgName: 'openssl', installedVersion: '1.0', fixedVersion: '1.1', title: 'bad bug' },
            { id: 'CVE-2024-2', severity: 'HIGH', pkgName: 'zlib', installedVersion: '2.0', title: 'another' },
          ],
        }),
      ),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'CVE Scan' }))
    await screen.findByText('Trivy Vulnerability Scan')
    await user.type(screen.getByPlaceholderText('Component ID (UUID)'), 'comp-1')
    await user.click(screen.getByRole('button', { name: 'Scan' }))
    expect(await screen.findByText('CVE-2024-1')).toBeInTheDocument()
    expect(screen.getByText('CVE-2024-2')).toBeInTheDocument()
    // filter by severity HIGH
    await user.click(screen.getByRole('button', { name: /HIGH \(2\)/ }))
    await waitFor(() => expect(screen.queryByText('CVE-2024-1')).not.toBeInTheDocument())
  })

  it('shows a CVE scan failure', async () => {
    const user = userEvent.setup()
    server.use(
      http.post('/api/v1/components/:id/scan', () =>
        HttpResponse.json({ error: 'trivy unavailable' }, { status: 500 }),
      ),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'CVE Scan' }))
    await screen.findByText('Trivy Vulnerability Scan')
    await user.type(screen.getByPlaceholderText('Component ID (UUID)'), 'comp-1')
    await user.click(screen.getByRole('button', { name: 'Scan' }))
    expect(await screen.findByText('trivy unavailable')).toBeInTheDocument()
  })

  it('disables the scan button and explains why when scanning is off', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/api/v1/security/scanner', () =>
        HttpResponse.json({ state: 'disabled', message: 'Image scanning is disabled by the administrator' }),
      ),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'CVE Scan' }))
    await screen.findByText('Trivy Vulnerability Scan')

    expect(await screen.findByText('Image scanning is disabled by the administrator')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Scan' })).toBeDisabled()
    expect(screen.queryByRole('link', { name: /how to enable image scanning/i })).toBeNull()
  })

  it('names the path it looked for when the binary is missing', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/api/v1/security/scanner', () =>
        HttpResponse.json({ state: 'missing', message: 'Trivy not found: looked for /opt/trivy/trivy' }),
      ),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'CVE Scan' }))
    await screen.findByText('Trivy Vulnerability Scan')

    expect(await screen.findByText(/Trivy not found: looked for \/opt\/trivy\/trivy/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Scan' })).toBeDisabled()
    const doc = screen.getByRole('link', { name: /how to enable image scanning/i })
    expect(doc).toHaveAttribute('href', expect.stringContaining('docs/scanning.md'))
  })

  it('shows the version and path when the scanner is ready', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/api/v1/security/scanner', () =>
        HttpResponse.json({ state: 'ready', version: '0.70.0', path: '/opt/trivy/trivy', message: 'Trivy 0.70.0 \u2014 /opt/trivy/trivy' }),
      ),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'CVE Scan' }))
    await screen.findByText('Trivy Vulnerability Scan')

    expect(await screen.findByText(/Trivy 0\.70\.0 \u2014 \/opt\/trivy\/trivy/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled()
  })

  it('leaves the scan button usable when the scanner status cannot be fetched', async () => {
    const user = userEvent.setup()
    let asked = false
    server.use(
      http.get('/api/v1/security/scanner', () => {
        asked = true
        return HttpResponse.json({ error: 'boom' }, { status: 500 })
      }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'CVE Scan' }))
    await screen.findByText('Trivy Vulnerability Scan')

    await waitFor(() => expect(asked).toBe(true))
    expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled()
  })

  /* ── Vulnerability Dashboard tab ── */
  it('switches to Vulnerability Dashboard and shows rows', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/api/v1/security/summary', () =>
        HttpResponse.json({ critical: 3, high: 5, medium: 1, low: 0, unknown: 0, scanned_total: 9 }),
      ),
      http.get('/api/v1/security/vulnerabilities', () =>
        HttpResponse.json({
          total: 1,
          items: [
            { repoName: 'maven-hosted', format: 'maven2', componentId: 'c1', name: 'lib', version: '1.0', critical: 1, high: 0, medium: 0, low: 0, unknown: 0, scannedAt: '2026-06-01T00:00:00Z' },
          ],
        }),
      ),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Vulnerability Dashboard' }))
    expect(await screen.findByText('lib')).toBeInTheDocument()
    expect(screen.getByText('Showing 1 of 1')).toBeInTheDocument()
  })

  it('shows empty vulnerability dashboard and runs Rescan All', async () => {
    const user = userEvent.setup()
    let scanned = false
    server.use(
      http.get('/api/v1/security/summary', () =>
        HttpResponse.json({ critical: 0, high: 0, medium: 0, low: 0, unknown: 0, scanned_total: 0 }),
      ),
      http.get('/api/v1/security/vulnerabilities', () =>
        HttpResponse.json({ total: 0, items: [] }),
      ),
      http.post('/api/v1/security/scan/bulk', () => {
        scanned = true
        return HttpResponse.json({ ok: true })
      }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Vulnerability Dashboard' }))
    expect(await screen.findByText(/No vulnerabilities found/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Rescan All/ }))
    await waitFor(() => expect(scanned).toBe(true))
  })

  /* ── Webhooks tab ── */
  it('switches to Webhooks tab and lists webhooks', async () => {
    const user = userEvent.setup()
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Webhooks' }))
    expect(await screen.findByText('slack')).toBeInTheDocument()
    expect(screen.getByText('https://hooks.slack.com/x')).toBeInTheDocument()
  })

  it('creates a webhook', async () => {
    const user = userEvent.setup()
    let posted: Record<string, unknown> | null = null
    server.use(
      http.post('/api/v1/webhooks', async ({ request }) => {
        posted = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ id: 'wh-new' }, { status: 201 })
      }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Webhooks' }))
    await screen.findByText('slack')
    await user.click(screen.getByRole('button', { name: /New Webhook/ }))
    await user.type(screen.getByPlaceholderText('Name'), 'my-hook')
    await user.type(screen.getByPlaceholderText('URL (https://...)'), 'https://example.com/hook')
    await user.click(screen.getByRole('button', { name: /^Create$/ }))
    await waitFor(() => expect(posted).toBeTruthy())
    expect((posted! as { name: string }).name).toBe('my-hook')
  })

  it('tests a webhook', async () => {
    const user = userEvent.setup()
    server.use(
      http.post('/api/v1/webhooks/:id/test', () =>
        HttpResponse.json({ status: 200, latency_ms: 42 }),
      ),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Webhooks' }))
    await screen.findByText('slack')
    fireEvent.click(screen.getByTitle('Send test event'))
    expect(await screen.findByText(/200 \(42ms\)/)).toBeInTheDocument()
  })

  it('edits a webhook', async () => {
    const user = userEvent.setup()
    let put = false
    server.use(
      http.put('/api/v1/webhooks/:id', () => {
        put = true
        return HttpResponse.json({ id: 'wh-1' })
      }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Webhooks' }))
    await screen.findByText('slack')
    fireEvent.click(screen.getByTitle('Edit'))
    expect(await screen.findByText('Edit Webhook')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /^Save$/ }))
    await waitFor(() => expect(put).toBe(true))
  })

  it('deletes a webhook', async () => {
    const user = userEvent.setup()
    let deleted = false
    server.use(
      http.delete('/api/v1/webhooks/:id', () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Webhooks' }))
    await screen.findByText('slack')
    // the danger delete button is the last button in the row
    const row = screen.getByText('slack').closest('.holo-card') as HTMLElement
    const buttons = within(row).getAllByRole('button')
    fireEvent.click(buttons[buttons.length - 1])
    await waitFor(() => expect(deleted).toBe(true))
  })

  it('shows empty webhooks state', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/api/v1/webhooks', () => HttpResponse.json([])),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Webhooks' }))
    expect(await screen.findByText('No webhooks configured')).toBeInTheDocument()
  })

  /* ── Access Map tab ── */
  it('switches to the Access Map tab and renders the default graph', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/api/v1/security/access-graph', () =>
        HttpResponse.json({
          users: [{ id: 'u1', username: 'admin', email: 'a@b.c', status: 'active', source: 'local', roleIds: ['r1'] }],
          roles: [{ id: 'r1', name: 'nx-admin', description: 'admin', privilegeIds: ['pr1'], roleIds: [] }],
          privileges: [{ id: 'pr1', name: 'all', type: 'wildcard', contentSelectorId: 'sc1' }],
          selectors: [{ id: 'sc1', name: 'sel', expression: 'true' }],
        }),
      ),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Access Map' }))
    // default view places admin chain → admin username node appears in SVG
    expect(await screen.findByText('Type:')).toBeInTheDocument()
    // pick a type pill then a node
    await user.click(screen.getByText('User'))
    const search = await screen.findByPlaceholderText('Search users…')
    fireEvent.focus(search)
    fireEvent.change(search, { target: { value: 'admin' } })
    // the dropdown option is a div whose hint text "a@b.c" is unique to the option
    const optionHint = await screen.findByText('a@b.c')
    fireEvent.click(optionHint.parentElement as HTMLElement)
    // sidebar detail panel shows the email again
    await waitFor(() => expect(screen.getAllByText('a@b.c').length).toBeGreaterThan(0))
  })

  it('explores role, privilege and selector nodes with reset in the Access Map', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/api/v1/security/access-graph', () =>
        HttpResponse.json({
          users: [{ id: 'u1', username: 'alice', email: 'alice@x.io', status: 'active', source: 'local', roleIds: ['r1'] }],
          roles: [
            { id: 'r1', name: 'team-lead', description: 'leads', privilegeIds: ['pr1'], roleIds: ['r2'] },
            { id: 'r2', name: 'developer', description: 'dev', privilegeIds: ['pr1'], roleIds: [] },
          ],
          privileges: [{ id: 'pr1', name: 'read-maven', type: 'repository-content-selector', contentSelectorId: 'sc1' }],
          selectors: [{ id: 'sc1', name: 'maven-sel', expression: 'format == "maven2"' }],
        }),
      ),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Access Map' }))
    await screen.findByText('Type:')

    // Select a Role node — exercises roleUp/roleDown + role sidebar detail.
    await user.click(screen.getByText('Role'))
    const roleSearch = await screen.findByPlaceholderText('Search roles…')
    fireEvent.focus(roleSearch)
    fireEvent.change(roleSearch, { target: { value: 'team' } })
    const roleOpt = await screen.findByText('team-lead')
    fireEvent.mouseEnter(roleOpt)
    fireEvent.mouseLeave(roleOpt)
    fireEvent.click(roleOpt.closest('div')!)
    await waitFor(() => expect(screen.getByText('× Reset')).toBeInTheDocument())

    // Select a Privilege node — privilege sidebar detail branch.
    await user.click(screen.getByText('Privilege'))
    const privSearch = await screen.findByPlaceholderText('Search privileges…')
    fireEvent.change(privSearch, { target: { value: 'read' } })
    fireEvent.click((await screen.findByText('read-maven')).closest('div')!)
    await waitFor(() => expect(screen.getByText('× Reset')).toBeInTheDocument())

    // Select a Content Selector node — selector sidebar detail branch + Escape/blur.
    // "Content Selector" also appears as a tab label, so target the pill (first match).
    fireEvent.click(screen.getAllByText('Content Selector')[0])
    const selSearch = await screen.findByPlaceholderText('Search selectors…')
    fireEvent.change(selSearch, { target: { value: 'maven' } })
    fireEvent.keyDown(selSearch, { key: 'Escape' })
    fireEvent.focus(selSearch)
    fireEvent.click((await screen.findByText('maven-sel')).closest('div')!)
    fireEvent.blur(selSearch)

    // Reset clears the current selection.
    await user.click(screen.getByText('× Reset'))
  })

  it('opens the edit role modal and edits the privilege transfer list', async () => {
    const user = userEvent.setup()
    server.use(
      http.put('/service/rest/v1/security/roles/:id', () => HttpResponse.json({ id: 'role-2' })),
      http.put('/service/rest/v1/security/roles/:id/privileges', () => new HttpResponse(null, { status: 204 })),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('developer')
    await user.click(screen.getByRole('button', { name: 'Edit' }))
    const dialog = (await screen.findByText(/Edit Role: developer/)).closest('.holo-modal') as HTMLElement
    // The transfer list has an "available" panel and a "Selected" panel.
    // Move an available privilege into the selected set (add), then back (remove),
    // exercising hover handlers on both panels.
    const available = within(dialog).getAllByText(/read-all|admin-builtin/)
    if (available.length) {
      fireEvent.mouseEnter(available[0])
      fireEvent.mouseLeave(available[0])
      fireEvent.click(available[0])
    }
    // Use the "Add all" / "Remove all" arrow buttons too.
    const addAll = within(dialog).queryByTitle('Add all')
    if (addAll) fireEvent.click(addAll)
    const removeAll = within(dialog).queryByTitle('Remove all')
    if (removeAll) fireEvent.click(removeAll)
    await user.click(within(dialog).getByRole('button', { name: /^Save$/ }))
  })

  it('shows access map empty state when graph has no data', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/api/v1/security/access-graph', () =>
        HttpResponse.json({ users: [], roles: [], privileges: [], selectors: [] }),
      ),
    )
    renderWithProviders(<SecurityPage />)
    await screen.findByText('nx-admin')
    await user.click(screen.getByRole('button', { name: 'Access Map' }))
    expect(await screen.findByText(/No data — system has no users/)).toBeInTheDocument()
  })
})
