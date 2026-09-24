import { describe, it, expect, beforeEach } from 'vitest'
import { screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import BrowsePage from './BrowsePage'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'
import { fixtures } from '@/test/fixtures'

// The "Example Usage" stub in Browse became the real Set Me Up dialog (#534).

const repos = [
  fixtures.repository({ id: 'r1', name: 'maven-group', format: 'maven2', type: 'group' }),
  fixtures.repository({ id: 'r2', name: 'pypi-hosted', format: 'pypi', type: 'hosted' }),
]

describe('BrowsePage — Set me up', () => {
  beforeEach(() => {
    seedAuthAsAdmin()
    Object.assign(window.location, { origin: 'http://localhost' })
    server.use(http.get('/service/rest/v1/repositories', () => HttpResponse.json(repos)))
  })

  it('opens the dialog for the selected repository from the toolbar', async () => {
    const user = userEvent.setup()
    renderWithProviders(<BrowsePage />, { routerProps: { initialEntries: ['/browse?repo=maven-group'] } })
    await user.click(await screen.findByRole('button', { name: /Set me up/ }))
    const dialog = await screen.findByRole('dialog', { name: 'Set me up: maven-group' })
    expect(within(dialog).getByText('maven2 · group · maven-group')).toBeInTheDocument()
    expect(within(dialog).getByRole('note')).toHaveTextContent(/group repository/)
    // A group gets the mirror setup, never deploy instructions.
    expect(within(dialog).getByText(/<mirrorOf>\*<\/mirrorOf>/)).toBeInTheDocument()
    expect(within(dialog).queryByText(/distributionManagement/)).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Close dialog' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('offers no Set me up before a repository is chosen', async () => {
    renderWithProviders(<BrowsePage />, { routerProps: { initialEntries: ['/browse'] } })
    expect(await screen.findByText('Choose a repository above')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Set me up/ })).not.toBeInTheDocument()
  })

  it('switches client tabs with the keyboard and fills in the username', async () => {
    const user = userEvent.setup()
    renderWithProviders(<BrowsePage />, { routerProps: { initialEntries: ['/browse?repo=pypi-hosted'] } })
    await user.click(await screen.findByRole('button', { name: /Set me up/ }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('Username'), 'alice')

    const pip = within(dialog).getByRole('tab', { name: 'pip' })
    expect(pip).toHaveAttribute('aria-selected', 'true')
    expect(within(dialog).getByText(/--username alice/)).toBeInTheDocument()
    pip.focus()
    await user.keyboard('{ArrowRight}')
    const uv = within(dialog).getByRole('tab', { name: 'uv' })
    expect(uv).toHaveAttribute('aria-selected', 'true')
    expect(uv).toHaveFocus()
    expect(within(dialog).getByRole('tabpanel')).toHaveTextContent(/tool\.uv\.index/)
    await user.keyboard('{End}')
    expect(within(dialog).getByRole('tab', { name: 'poetry' })).toHaveAttribute('aria-selected', 'true')
    await user.keyboard('{ArrowRight}')
    expect(pip).toHaveAttribute('aria-selected', 'true')
    await user.keyboard('{ArrowLeft}')
    expect(within(dialog).getByRole('tab', { name: 'poetry' })).toHaveAttribute('aria-selected', 'true')
    await user.keyboard('{Home}')
    expect(pip).toHaveAttribute('aria-selected', 'true')
    await user.click(within(dialog).getByRole('tab', { name: 'poetry' }))
    expect(within(dialog).getByRole('tabpanel')).toHaveTextContent(/poetry publish/)
    // The password is never filled in.
    expect(dialog).not.toHaveTextContent(/alice:[^<]/)
  })
})
