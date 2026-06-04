import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { screen, waitFor, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import UsersPage from './UsersPage'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'

const userItem = (overrides?: Record<string, unknown>) => ({
  id: 'u-1',
  userId: 'alice',
  emailAddress: 'alice@test.com',
  firstName: 'Alice',
  lastName: 'Smith',
  status: 'active',
  source: 'local',
  roles: ['nx-admin'],
  ...overrides,
})

const roleItem = (overrides?: Record<string, unknown>) => ({
  id: 'role-admin',
  name: 'nx-admin',
  description: 'Administrator role',
  readOnly: true,
  privileges: [],
  roles: [],
  ...overrides,
})

describe('UsersPage', () => {
  beforeEach(() => {
    seedAuthAsAdmin()
    server.use(
      http.get('/service/rest/v1/security/users', () =>
        HttpResponse.json([userItem(), userItem({ id: 'u-2', userId: 'bob', emailAddress: 'bob@test.com', firstName: 'Bob', lastName: '', status: 'disabled', roles: [] })]),
      ),
      http.get('/service/rest/v1/security/roles', () =>
        HttpResponse.json([
          roleItem(),
          roleItem({ id: 'role-dev', name: 'developer', description: 'Dev role', readOnly: false }),
        ]),
      ),
    )
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders the users list', async () => {
    renderWithProviders(<UsersPage />)
    expect(await screen.findByText('alice')).toBeInTheDocument()
    expect(screen.getByText('bob')).toBeInTheDocument()
    expect(screen.getByText('2 users')).toBeInTheDocument()
    expect(screen.getByText('alice@test.com')).toBeInTheDocument()
  })

  it('filters users by the filter input', async () => {
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    const filter = screen.getByPlaceholderText('Filter by username or email…')
    await userEvent.type(filter, 'bob')
    await waitFor(() => expect(screen.queryByText('alice')).not.toBeInTheDocument())
    expect(screen.getByText('bob')).toBeInTheDocument()
  })

  it('shows empty state when no users match', async () => {
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    const filter = screen.getByPlaceholderText('Filter by username or email…')
    await userEvent.type(filter, 'zzznotfound')
    expect(await screen.findByText('No users found')).toBeInTheDocument()
  })

  it('shows an error state when the users query fails', async () => {
    server.use(
      http.get('/service/rest/v1/security/users', () =>
        HttpResponse.json({ error: 'boom users' }, { status: 500 }),
      ),
    )
    renderWithProviders(<UsersPage />)
    expect(await screen.findByText('boom users')).toBeInTheDocument()
  })

  it('opens the create-user modal, fills it and submits', async () => {
    const user = userEvent.setup()
    let posted: Record<string, unknown> | null = null
    server.use(
      http.post('/service/rest/v1/security/users', async ({ request }) => {
        posted = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(userItem(), { status: 201 })
      }),
    )
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    await user.click(screen.getByRole('button', { name: /Add User/ }))
    const heading = await screen.findByRole('heading', { name: 'Add User' })
    const form = heading.parentElement!.querySelector('form')!

    const usernameInput = within(form).getByText('Username *').parentElement!.querySelector('input') as HTMLInputElement
    await user.type(usernameInput, 'charlie')
    const pwd = form.querySelector('input[type="password"]') as HTMLInputElement
    await user.type(pwd, 's3cret')

    const submit = form.querySelector('button[type="submit"]') as HTMLButtonElement
    await user.click(submit)
    await waitFor(() => expect(posted).toBeTruthy())
    expect((posted! as { userId: string }).userId).toBe('charlie')
  })

  it('selects a status via the custom Select in the create modal', async () => {
    const user = userEvent.setup()
    let posted: Record<string, unknown> | null = null
    server.use(
      http.post('/service/rest/v1/security/users', async ({ request }) => {
        posted = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(userItem(), { status: 201 })
      }),
    )
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    await user.click(screen.getByRole('button', { name: /Add User/ }))
    await screen.findByRole('heading', { name: 'Add User' })

    // Open the status Select (its trigger shows the current "Active" label)
    await user.click(screen.getByRole('button', { name: /Active/ }))
    await user.click(await screen.findByText('Disabled'))

    const heading = screen.getByRole('heading', { name: 'Add User' })
    const form = heading.parentElement!.querySelector('form')!
    const usernameInput = within(form).getByText('Username *').parentElement!.querySelector('input') as HTMLInputElement
    await user.type(usernameInput, 'dave')
    const pwd = form.querySelector('input[type="password"]') as HTMLInputElement
    await user.type(pwd, 'pw')
    await user.click(form.querySelector('button[type="submit"]') as HTMLButtonElement)
    await waitFor(() => expect(posted).toBeTruthy())
    expect((posted! as { status: string }).status).toBe('disabled')
  })

  it('shows an error when create user fails', async () => {
    const user = userEvent.setup()
    server.use(
      http.post('/service/rest/v1/security/users', () =>
        HttpResponse.json({ error: 'username taken' }, { status: 400 }),
      ),
    )
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    await user.click(screen.getByRole('button', { name: /Add User/ }))
    const heading = await screen.findByRole('heading', { name: 'Add User' })
    const form = heading.parentElement!.querySelector('form')!
    const usernameInput = within(form).getByText('Username *').parentElement!.querySelector('input') as HTMLInputElement
    await user.type(usernameInput, 'charlie')
    const pwd = form.querySelector('input[type="password"]') as HTMLInputElement
    await user.type(pwd, 's3cret')
    await user.click(form.querySelector('button[type="submit"]') as HTMLButtonElement)
    expect(await screen.findByText('username taken')).toBeInTheDocument()
  })

  it('closes the create modal via Cancel', async () => {
    const user = userEvent.setup()
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    await user.click(screen.getByRole('button', { name: /Add User/ }))
    await screen.findByRole('heading', { name: 'Add User' })
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() =>
      expect(screen.queryByRole('heading', { name: 'Add User' })).not.toBeInTheDocument(),
    )
  })

  it('deletes a user after confirm', async () => {
    let deleted = false
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    server.use(
      http.delete('/service/rest/v1/security/users/:userId', () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderWithProviders(<UsersPage />)
    await screen.findByText('bob')
    // bob is not admin -> delete button enabled
    const bobRow = screen.getByText('bob').closest('div[style]')!.parentElement!.parentElement as HTMLElement
    const delBtn = within(bobRow).getByTitle('Delete user')
    fireEvent.click(delBtn)
    await waitFor(() => expect(deleted).toBe(true))
  })

  it('does not delete when confirm is cancelled', async () => {
    let deleted = false
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    server.use(
      http.delete('/service/rest/v1/security/users/:userId', () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderWithProviders(<UsersPage />)
    await screen.findByText('bob')
    const delBtns = screen.getAllByTitle('Delete user')
    // Click bob's (second) delete button
    fireEvent.click(delBtns[1])
    await new Promise(r => setTimeout(r, 50))
    expect(deleted).toBe(false)
  })

  it('opens the assign-roles modal, filters, toggles and saves', async () => {
    const user = userEvent.setup()
    let putBody: Record<string, unknown> | null = null
    server.use(
      http.put('/service/rest/v1/security/users/:userId/roles', async ({ request }) => {
        putBody = (await request.json()) as Record<string, unknown>
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    // open alice's assign-roles modal (first Assign roles button)
    const assignBtns = screen.getAllByTitle('Assign roles')
    await user.click(assignBtns[0])
    expect(await screen.findByText(/Assign Roles — alice/)).toBeInTheDocument()

    // alice already has nx-admin (selected). developer is available.
    expect(screen.getByText('developer')).toBeInTheDocument()
    // Filter the Available list
    const filters = screen.getAllByPlaceholderText('Filter…')
    await user.type(filters[0], 'dev')
    expect(screen.getByText('developer')).toBeInTheDocument()

    // Add developer (click the available item)
    await user.click(screen.getByText('developer'))

    // Save
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(putBody).toBeTruthy())
    expect((putBody! as { roleIds: string[] }).roleIds).toContain('role-dev')
  })

  it('add-all and remove-all arrows in the assign-roles modal', async () => {
    const user = userEvent.setup()
    let putBody: Record<string, unknown> | null = null
    server.use(
      http.put('/service/rest/v1/security/users/:userId/roles', async ({ request }) => {
        putBody = (await request.json()) as Record<string, unknown>
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    await user.click(screen.getAllByTitle('Assign roles')[0])
    await screen.findByText(/Assign Roles — alice/)

    await user.click(screen.getByTitle('Add all'))
    await user.click(screen.getByTitle('Remove all'))
    expect(screen.getByText('None selected')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(putBody).toBeTruthy())
    expect((putBody! as { roleIds: string[] }).roleIds).toEqual([])
  })

  it('shows an error when saving roles fails', async () => {
    const user = userEvent.setup()
    server.use(
      http.put('/service/rest/v1/security/users/:userId/roles', () =>
        HttpResponse.json({ error: 'cannot save roles' }, { status: 500 }),
      ),
    )
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    await user.click(screen.getAllByTitle('Assign roles')[0])
    await screen.findByText(/Assign Roles — alice/)
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('cannot save roles')).toBeInTheDocument()
  })

  it('closes the assign-roles modal via Cancel', async () => {
    const user = userEvent.setup()
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    await user.click(screen.getAllByTitle('Assign roles')[0])
    await screen.findByText(/Assign Roles — alice/)
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() =>
      expect(screen.queryByText(/Assign Roles — alice/)).not.toBeInTheDocument(),
    )
  })

  it('refreshes the user list via the refresh button', async () => {
    let calls = 0
    server.use(
      http.get('/service/rest/v1/security/users', () => {
        calls++
        return HttpResponse.json([userItem()])
      }),
    )
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    const initial = calls
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(calls).toBeGreaterThan(initial))
  })

  /* ── Roles tab ── */
  it('switches to Roles tab and lists roles', async () => {
    const user = userEvent.setup()
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    await user.click(screen.getByRole('button', { name: 'Roles' }))
    expect(await screen.findByText('developer')).toBeInTheDocument()
    expect(screen.getByText('2 roles')).toBeInTheDocument()
    expect(screen.getByText('built-in')).toBeInTheDocument()
  })

  it('creates a role from the Roles tab', async () => {
    const user = userEvent.setup()
    let posted: Record<string, unknown> | null = null
    server.use(
      http.post('/service/rest/v1/security/roles', async ({ request }) => {
        posted = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ id: 'role-new', name: 'qa' }, { status: 201 })
      }),
    )
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    await user.click(screen.getByRole('button', { name: 'Roles' }))
    await screen.findByText('developer')
    await user.click(screen.getByRole('button', { name: /New Role/ }))
    await user.type(screen.getByPlaceholderText('Role name (e.g. developer)'), 'qa')
    await user.type(screen.getByPlaceholderText('Description (optional)'), 'QA team')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(posted).toBeTruthy())
    expect((posted! as { name: string }).name).toBe('qa')
  })

  it('validates required role name', async () => {
    const user = userEvent.setup()
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    await user.click(screen.getByRole('button', { name: 'Roles' }))
    await screen.findByText('developer')
    await user.click(screen.getByRole('button', { name: /New Role/ }))
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText('Name is required')).toBeInTheDocument()
  })

  it('deletes a non-built-in role after confirm', async () => {
    const user = userEvent.setup()
    let deleted = false
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    server.use(
      http.delete('/service/rest/v1/security/roles/:id', () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    await user.click(screen.getByRole('button', { name: 'Roles' }))
    const devRow = (await screen.findByText('developer')).closest('div[style]')!.parentElement!.parentElement as HTMLElement
    const delBtn = within(devRow).getByRole('button')
    fireEvent.click(delBtn)
    await waitFor(() => expect(deleted).toBe(true))
  })

  it('shows an error when roles query fails', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/security/roles', () =>
        HttpResponse.json({ error: 'roles failed' }, { status: 500 }),
      ),
    )
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    await user.click(screen.getByRole('button', { name: 'Roles' }))
    expect(await screen.findByText('roles failed')).toBeInTheDocument()
  })

  it('shows empty state on the Roles tab', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/security/roles', () => HttpResponse.json([])),
    )
    renderWithProviders(<UsersPage />)
    await screen.findByText('alice')
    await user.click(screen.getByRole('button', { name: 'Roles' }))
    expect(await screen.findByText('No roles defined')).toBeInTheDocument()
  })
})
