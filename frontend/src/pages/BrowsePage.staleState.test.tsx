import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, waitFor, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import BrowsePage from './BrowsePage'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'
import { fixtures } from '@/test/fixtures'

/**
 * Stale-state guards (#337): component state crossing an async boundary — a
 * fetch, a race, an unmount — must not act on the value from before that
 * boundary.
 */

const repos = [
  fixtures.repository({ id: 'r1', name: 'maven-hosted', format: 'maven2', type: 'hosted' }),
  fixtures.repository({ id: 'r3', name: 'raw-hosted', format: 'raw', type: 'hosted' }),
]

function renderBrowse(search = '') {
  return renderWithProviders(<BrowsePage />, {
    routerProps: { initialEntries: [`/browse${search}`] },
  })
}

beforeEach(() => {
  seedAuthAsAdmin()
  server.use(http.get('/service/rest/v1/repositories', () => HttpResponse.json(repos)))
})
afterEach(() => {
  vi.restoreAllMocks()
})

describe('BrowsePage — single-item Promote seeds the rule from the fresh fetch', () => {
  const twoFileTree = {
    root: {
      kind: 'folder', label: '', path: '', children: [
        {
          kind: 'folder', label: 'releases', path: '/releases', children: [
            { kind: 'file', label: 'app.tar.gz', path: '/releases/app.tar.gz', size: 4096, sha256: 'abc123', contentType: 'application/gzip', updatedAt: new Date().toISOString(), componentId: 'comp-raw-1' },
            { kind: 'file', label: 'lib.tar.gz', path: '/releases/lib.tar.gz', size: 2048, sha256: 'def456', contentType: 'application/gzip', updatedAt: new Date().toISOString(), componentId: 'comp-raw-2' },
          ],
        },
      ],
    },
  }

  // The security-relevant scenario from #337: rules already fetched for one
  // component must never leak into the Promote dialog of another — the operator
  // sees an apparently empty selection while a rule from an unrelated pair is
  // silently armed for submission.
  it('promoting a second component submits its own rule, not the previous component\'s', async () => {
    const user = userEvent.setup()
    const promoted: string[] = []
    server.use(
      http.get('/api/v1/browse/repositories/:name/raw-tree', () => HttpResponse.json(twoFileTree)),
      http.get('/service/rest/v1/components/:id', () => HttpResponse.json({ tags: [] })),
      http.get('/api/v1/components/:id/promotion-rules', ({ params }) =>
        HttpResponse.json(params.id === 'comp-raw-1'
          ? [{ id: 'rule-a', name: 'ruleA', from_repo: 'raw-hosted', to_repo: 'staging', require_scan_pass: false, require_manual_approval: false }]
          : [{ id: 'rule-b', name: 'ruleB', from_repo: 'raw-hosted', to_repo: 'prod', require_scan_pass: false, require_manual_approval: false }]),
      ),
      http.post('/api/v1/promotion/promote', async ({ request }) => {
        const body = await request.json() as { rule_id: string }
        promoted.push(body.rule_id)
        return HttpResponse.json({ requests: [{ status: 'completed' }] })
      }),
    )

    renderBrowse('?repo=raw-hosted')
    await user.click(await screen.findByText('releases'))

    // First component: open Promote (its rule becomes the live selection),
    // then cancel without submitting.
    await user.click(await screen.findByText('app.tar.gz'))
    await screen.findByText('File details')
    await user.click(screen.getByRole('button', { name: 'Promote' }))
    await screen.findByText(/Promote 1 component/)
    expect(await screen.findByText('ruleA (raw-hosted → staging)')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByText(/Promote 1 component/)).not.toBeInTheDocument())

    // Second component: the dialog must be seeded from ITS freshly-fetched
    // rule list, not the closure's previous one.
    await user.click(await screen.findByText('lib.tar.gz'))
    await screen.findByText('File details')
    await user.click(screen.getByRole('button', { name: 'Promote' }))
    const dialog = (await screen.findByText(/Promote 1 component/)).closest('.holo-modal') as HTMLElement
    expect(await within(dialog).findByText('ruleB (raw-hosted → prod)')).toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: 'Promote' }))
    await waitFor(() => expect(promoted).toHaveLength(1))
    expect(promoted[0]).toBe('rule-b')
  })
})

describe('BrowsePage — bulk delete invalidates on partial failure', () => {
  it('refreshes the component list when a later delete in the batch fails', async () => {
    const user = userEvent.setup()
    let componentsFetches = 0
    server.use(
      http.get('/service/rest/v1/components', () => {
        componentsFetches++
        return HttpResponse.json({
          items: [{
            id: 'c1', name: 'pkg-a', group: '', version: '1', format: 'maven2',
            assets: [
              { id: 'a1', path: 'p/pkg.jar', fileSize: 1, contentType: 't' },
              { id: 'a2', path: 'p/pkg.pom', fileSize: 1, contentType: 't' },
            ],
          }],
          continuationToken: null,
        })
      }),
      http.delete('/api/v1/browse/repositories/:name/path', ({ request }) => {
        const path = new URL(request.url).searchParams.get('path') ?? ''
        if (path.endsWith('.jar')) return new HttpResponse(null, { status: 204 })
        // The second asset was already removed elsewhere: the first delete
        // genuinely succeeded server-side, the view must not pretend otherwise.
        return HttpResponse.json({ error: 'not found' }, { status: 404 })
      }),
    )

    renderBrowse('?repo=maven-hosted')
    await screen.findByText('pkg-a')
    const fetchesBefore = componentsFetches
    await user.click(screen.getByTitle('Delete'))
    await screen.findByText('Delete component?')
    const delBtns = screen.getAllByRole('button', { name: /^Delete/ })
    await user.click(delBtns[delBtns.length - 1])

    // The failure is reported…
    expect(await screen.findByText(/not found/)).toBeInTheDocument()
    // …and the list is refreshed anyway: the jar IS gone server-side, and a
    // view that still shows the untouched component would mislead a retry.
    await waitFor(() => expect(componentsFetches).toBeGreaterThan(fetchesBefore))
  })
})

describe('BrowsePage — upload aborts when the modal goes away', () => {
  const rawTree = {
    root: {
      kind: 'folder', label: '', path: '', children: [
        { kind: 'file', label: 'existing.bin', path: '/existing.bin', size: 1, sha256: 'aa', contentType: 't', updatedAt: new Date().toISOString(), componentId: 'comp-raw-1' },
      ],
    },
  }

  it('aborts the in-flight XHR when the modal is dismissed via the backdrop', async () => {
    const user = userEvent.setup()
    const abortSpy = vi.spyOn(XMLHttpRequest.prototype, 'abort')
    server.use(
      http.get('/api/v1/browse/repositories/:name/raw-tree', () => HttpResponse.json(rawTree)),
      // The PUT never resolves within the test: the upload stays in flight.
      http.put('/repository/:name/*', () => new Promise<never>(() => {})),
    )

    renderBrowse('?repo=raw-hosted')
    await screen.findByText('existing.bin')
    await user.click(screen.getByRole('button', { name: /Upload/ }))
    await screen.findByText('Upload file')

    const dropZone = screen.getByText('Click or drag a file here')
    fireEvent.drop(dropZone, {
      dataTransfer: { files: [new File(['payload'], 'new-file.bin', { type: 'application/octet-stream' })] },
    })
    await screen.findByText('new-file.bin')
    const dialog = screen.getByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Upload' }))
    await screen.findAllByText(/Uploading/)

    // Dismissing via the backdrop closes the modal exactly like switching
    // repositories does — neither path is the Cancel button.
    fireEvent.click(document.querySelector('.holo-overlay')!)
    await waitFor(() => expect(screen.queryByText('Upload file')).not.toBeInTheDocument())

    await waitFor(() => expect(abortSpy).toHaveBeenCalled(),
      { timeout: 2000 })
  })
})
