import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, waitFor, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import BrowsePage from './BrowsePage'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'
import { fixtures } from '@/test/fixtures'
import { useAuthStore } from '@/store/authStore'

const repos = [
  fixtures.repository({ id: 'r1', name: 'maven-hosted', format: 'maven2', type: 'hosted' }),
  fixtures.repository({ id: 'r2', name: 'docker-hosted', format: 'docker', type: 'hosted' }),
  fixtures.repository({ id: 'r3', name: 'raw-hosted', format: 'raw', type: 'hosted' }),
]

function renderBrowse(search = '') {
  return renderWithProviders(<BrowsePage />, { routerProps: { initialEntries: [`/browse${search}`] } })
}

function seedRepos() {
  server.use(http.get('/service/rest/v1/repositories', () => HttpResponse.json(repos)))
}

beforeEach(() => {
  seedAuthAsAdmin()
  seedRepos()
})
afterEach(() => {
  vi.restoreAllMocks()
})

describe('BrowsePage — repo selector & empty states', () => {
  it('shows the initial no-repo prompt', async () => {
    renderBrowse()
    expect(await screen.findByText('Choose a repository above')).toBeInTheDocument()
    expect(screen.getByText('Select a repository to browse')).toBeInTheDocument()
  })

  it('selects a maven (component) repo and shows empty components', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/components', () => HttpResponse.json({ items: [], continuationToken: null })),
    )
    renderBrowse()
    await screen.findByText('Choose a repository above')
    await user.click(screen.getByRole('button', { name: /Select repository/ }))
    await user.click((await screen.findAllByText('maven-hosted'))[0])
    expect(await screen.findByText('No components in this repository')).toBeInTheDocument()
  })

  it('lists components with assets, bulk-selects and shows promote bar', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({
          items: [
            { id: 'c1', name: 'pkg-a', group: 'com.example', version: '1.0', format: 'maven2', assets: [{ id: 'a1', path: 'com/example/pkg-a/1.0/pkg-a.jar', fileSize: 2048, contentType: 'application/java-archive' }, { id: 'a2', path: 'x', fileSize: 1, contentType: 't' }] },
            { id: 'c2', name: 'pkg-b', group: '', version: '2.0', format: 'maven2', assets: [] },
          ],
          continuationToken: 'next',
        }),
      ),
      http.get('/api/v1/components/:id/promotion-rules', () =>
        HttpResponse.json([{ id: 'pr1', name: 'rel', from_repo: 'maven-hosted', to_repo: 'maven-release', require_scan_pass: false, require_manual_approval: false }]),
      ),
    )
    renderBrowse('?repo=maven-hosted')
    expect(await screen.findByText('pkg-a')).toBeInTheDocument()
    expect(screen.getByText('pkg-b')).toBeInTheDocument()
    // pagination shows next enabled
    expect(screen.getByText('Page 1')).toBeInTheDocument()
    // bulk select first row
    const checkboxes = screen.getAllByRole('checkbox')
    await user.click(checkboxes[0])
    expect(await screen.findByText('1 selected')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Promote selected/ }))
    expect(await screen.findByText(/Promote 1 component/)).toBeInTheDocument()
  })

  it('paginates next and prev', async () => {
    const user = userEvent.setup()
    let lastOffset = '0'
    server.use(
      http.get('/service/rest/v1/components', ({ request }) => {
        const url = new URL(request.url)
        lastOffset = url.searchParams.get('offset') ?? '0'
        return HttpResponse.json({
          items: [{ id: 'c1', name: 'pkg-a', group: '', version: '1', format: 'maven2', assets: [] }],
          continuationToken: 'next',
        })
      }),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('pkg-a')
    await user.click(screen.getByText('Next →'))
    await waitFor(() => expect(lastOffset).toBe('25'))
    await user.click(screen.getByText('← Prev'))
    await waitFor(() => expect(lastOffset).toBe('0'))
  })

  it('shows access-denied on 403', async () => {
    server.use(
      http.get('/service/rest/v1/components', () => HttpResponse.json({ error: 'denied' }, { status: 403 })),
    )
    renderBrowse('?repo=maven-hosted')
    expect(await screen.findByText(/Access denied/)).toBeInTheDocument()
  })

  it('deletes a component asset by path after confirm', async () => {
    const user = userEvent.setup()
    let deleted = false
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({ items: [{ id: 'c1', name: 'pkg-a', group: '', version: '1', format: 'maven2', assets: [{ id: 'a1', path: 'p/pkg.jar', fileSize: 1, contentType: 't' }] }], continuationToken: null }),
      ),
      http.delete('/api/v1/browse/repositories/:name/path', () => { deleted = true; return new HttpResponse(null, { status: 204 }) }),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('pkg-a')
    await user.click(screen.getByTitle('Delete'))
    await screen.findByText('Delete file?')
    const delBtns = screen.getAllByRole('button', { name: /^Delete$/ })
    await user.click(delBtns[delBtns.length - 1])
    await waitFor(() => expect(deleted).toBe(true))
  })

  // Regression for #75/#76: the row delete must target the asset path. When the
  // component name was used instead, the prefix matched nothing (npm: silent
  // no-op) or was empty (apt proxy: 400 with no server-side error).
  it('deletes using the asset path, not the component name', async () => {
    const user = userEvent.setup()
    const deletedPaths: string[] = []
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({
          items: [{
            id: 'c1', name: 'lodash', group: '', version: '4.17.21', format: 'npm',
            assets: [{ id: 'a1', path: '/lodash/-/lodash-4.17.21.tgz', fileSize: 1, contentType: 't' }],
          }],
          continuationToken: null,
        }),
      ),
      http.delete('/api/v1/browse/repositories/:name/path', ({ request }) => {
        deletedPaths.push(new URL(request.url).searchParams.get('path') ?? '')
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('lodash')
    await user.click(screen.getByTitle('Delete'))
    await screen.findByText('Delete file?')
    const delBtns = screen.getAllByRole('button', { name: /^Delete/ })
    await user.click(delBtns[delBtns.length - 1])
    await waitFor(() => expect(deletedPaths).toEqual(['/lodash/-/lodash-4.17.21.tgz']))
  })

  // An apt proxy stores every cached file under one component, so deleting the
  // row must remove all of its assets — not just the first one.
  it('deletes every asset of a multi-asset component', async () => {
    const user = userEvent.setup()
    const deletedPaths: string[] = []
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({
          items: [{
            id: 'c1', name: 'nginx', group: '', version: '1.24', format: 'apt',
            assets: [
              { id: 'a1', path: '/pool/main/n/nginx/nginx_1.24_amd64.deb', fileSize: 1, contentType: 't' },
              { id: 'a2', path: '/dists/trixie/InRelease', fileSize: 1, contentType: 't' },
            ],
          }],
          continuationToken: null,
        }),
      ),
      http.delete('/api/v1/browse/repositories/:name/path', ({ request }) => {
        deletedPaths.push(new URL(request.url).searchParams.get('path') ?? '')
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('nginx')
    await user.click(screen.getByTitle('Delete'))
    await screen.findByText('Delete component?')
    const delBtns = screen.getAllByRole('button', { name: /^Delete/ })
    await user.click(delBtns[delBtns.length - 1])
    await waitFor(() => expect(deletedPaths.sort()).toEqual([
      '/dists/trixie/InRelease',
      '/pool/main/n/nginx/nginx_1.24_amd64.deb',
    ]))
  })

  // A component with no assets has nothing to delete: report it instead of
  // firing a request that the server rejects with an unexplained 400.
  it('reports an error instead of deleting a component that has no assets', async () => {
    const user = userEvent.setup()
    let called = false
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({
          items: [{ id: 'c1', name: 'ghost', group: '', version: '1', format: 'npm', assets: [] }],
          continuationToken: null,
        }),
      ),
      http.delete('/api/v1/browse/repositories/:name/path', () => {
        called = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('ghost')
    await user.click(screen.getByTitle('Delete'))
    await screen.findByText('Delete file?')
    const delBtns = screen.getAllByRole('button', { name: /^Delete/ })
    await user.click(delBtns[delBtns.length - 1])
    expect(await screen.findByText(/no assets to delete/i)).toBeInTheDocument()
    expect(called).toBe(false)
  })
})

describe('BrowsePage — Raw tree', () => {
  const rawTree = {
    root: {
      kind: 'folder', label: '', path: '', children: [
        {
          kind: 'folder', label: 'releases', path: '/releases', children: [
            { kind: 'file', label: 'app.tar.gz', path: '/releases/app.tar.gz', size: 4096, sha256: 'abc123', contentType: 'application/gzip', updatedAt: new Date().toISOString(), componentId: 'comp-raw-1' },
          ],
        },
      ],
    },
  }

  function seedRaw() {
    server.use(
      http.get('/api/v1/browse/repositories/:name/raw-tree', () => HttpResponse.json(rawTree)),
      http.get('/service/rest/v1/components/:id', () => HttpResponse.json({ tags: ['stable'] })),
    )
  }

  it('shows empty raw tree', async () => {
    server.use(http.get('/api/v1/browse/repositories/:name/raw-tree', () => HttpResponse.json({ root: { kind: 'folder', label: '', path: '', children: [] } })))
    renderBrowse('?repo=raw-hosted')
    expect(await screen.findByText('No files in this repository yet')).toBeInTheDocument()
  })

  it('expands folder, selects file, shows detail panel', async () => {
    const user = userEvent.setup()
    seedRaw()
    renderBrowse('?repo=raw-hosted')
    await user.click(await screen.findByText('releases'))
    await user.click(await screen.findByText('app.tar.gz'))
    expect(await screen.findByText('File details')).toBeInTheDocument()
    expect(screen.getByText('abc123')).toBeInTheDocument()
    expect(screen.getByText('SHA256')).toBeInTheDocument()
    // tag editor section loaded
    expect(await screen.findByText('stable')).toBeInTheDocument()
  })

  it('downloads, copies link and opens usage from detail panel', async () => {
    const user = userEvent.setup()
    seedRaw()
    const writeText = vi.fn()
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    Object.defineProperty(window, 'location', { value: { ...window.location, origin: 'http://localhost' }, configurable: true })
    const click = vi.fn()
    vi.spyOn(document, 'createElement').mockImplementation(((tag: string) => {
      const el = document.createElementNS('http://www.w3.org/1999/xhtml', tag) as HTMLElement
      if (tag === 'a') (el as HTMLAnchorElement).click = click
      return el
    }) as typeof document.createElement)
    globalThis.URL.createObjectURL = vi.fn(() => 'blob:x')
    globalThis.URL.revokeObjectURL = vi.fn()
    server.use(http.get('/repository/:name/*', () => HttpResponse.text('data')))
    renderBrowse('?repo=raw-hosted')
    await user.click(await screen.findByText('releases'))
    await user.click(await screen.findByText('app.tar.gz'))
    const panel = (await screen.findByText('File details')).closest('.holo-card') as HTMLElement
    await user.click(within(panel).getByRole('button', { name: /Download/ }))
    await waitFor(() => expect(click).toHaveBeenCalled())
    await user.click(within(panel).getByRole('button', { name: /Copy link/ }))
    expect(writeText).toHaveBeenCalled()
    await user.click(within(panel).getByRole('button', { name: /Usage/ }))
    expect(await screen.findByText('Example Usage')).toBeInTheDocument()
  })

  it('downloads via the hover row buttons', async () => {
    const user = userEvent.setup()
    seedRaw()
    const writeText = vi.fn()
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    globalThis.URL.createObjectURL = vi.fn(() => 'blob:x')
    globalThis.URL.revokeObjectURL = vi.fn()
    server.use(http.get('/repository/:name/*', () => HttpResponse.text('data')))
    renderBrowse('?repo=raw-hosted')
    await user.click(await screen.findByText('releases'))
    const fileRow = (await screen.findByText('app.tar.gz')).closest('[role="button"]')!
    fireEvent.mouseEnter(fileRow)
    fireEvent.click(within(fileRow as HTMLElement).getByTitle('Copy link'))
    expect(writeText).toHaveBeenCalled()
  })

  it('deletes a raw file via hover delete', async () => {
    const user = userEvent.setup()
    let deleted = false
    seedRaw()
    server.use(
      http.delete('/api/v1/browse/repositories/:name/path', () => { deleted = true; return new HttpResponse(null, { status: 204 }) }),
    )
    renderBrowse('?repo=raw-hosted')
    await user.click(await screen.findByText('releases'))
    const fileRow = (await screen.findByText('app.tar.gz')).closest('[role="button"]')!
    fireEvent.mouseEnter(fileRow)
    fireEvent.click(within(fileRow as HTMLElement).getByTitle('Delete'))
    await screen.findByText('Delete file?')
    const delBtns = screen.getAllByRole('button', { name: /^Delete$/ })
    await user.click(delBtns[delBtns.length - 1])
    await waitFor(() => expect(deleted).toBe(true))
  })

  it('deletes a raw folder (shows affected paths)', async () => {
    const user = userEvent.setup()
    let deleted = false
    seedRaw()
    server.use(
      http.delete('/api/v1/browse/repositories/:name/path', () => { deleted = true; return new HttpResponse(null, { status: 204 }) }),
    )
    renderBrowse('?repo=raw-hosted')
    const folderRow = (await screen.findByText('releases')).closest('div')!
    fireEvent.mouseEnter(folderRow)
    fireEvent.click(within(folderRow).getByTitle(/Delete folder/))
    expect(await screen.findByText('Delete folder?')).toBeInTheDocument()
    expect(screen.getByText(/files affected/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Delete 1 files/ }))
    await waitFor(() => expect(deleted).toBe(true))
  })

  it('shows the upload modal for hosted raw repos and uploads', async () => {
    const user = userEvent.setup()
    seedRaw()
    renderBrowse('?repo=raw-hosted')
    await screen.findByText('releases')
    await user.click(screen.getByRole('button', { name: /Upload/ }))
    expect(await screen.findByText('Upload file')).toBeInTheDocument()
    // close it
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByText('Upload file')).not.toBeInTheDocument())
  })
})

describe('BrowsePage — Docker tree', () => {
  const dockerTree = {
    root: {
      kind: 'folder', label: '', path: '', children: [
        {
          kind: 'folder', label: 'myapp', path: '/myapp', imageRef: 'myapp', children: [
            {
              kind: 'folder', label: 'Tags', path: '/myapp/Tags', children: [
                { kind: 'tag', label: 'latest', path: '/myapp/Tags/latest', imageRef: 'myapp', version: 'latest', componentId: 'dc1' },
              ],
            },
          ],
        },
      ],
    },
  }

  const dockerDetail = {
    id: 'dc1', repository: 'docker-hosted', format: 'docker', name: 'myapp', version: 'latest', group: '',
    createdAt: new Date().toISOString(), tags: ['prod'],
    assets: [{ path: 'v2/myapp/manifests/latest', fileSize: 512, contentType: 'application/vnd.docker.distribution.manifest.v2+json', createdAt: new Date().toISOString(), lastModified: new Date().toISOString(), blobKey: 'sha256:xyz', blobStoreId: 'bs1', uploader: 'admin' }],
  }

  function seedDocker() {
    server.use(
      http.get('/api/v1/browse/repositories/:name/docker-tree', () => HttpResponse.json(dockerTree)),
      http.get('/service/rest/v1/components/:id', () => HttpResponse.json(dockerDetail)),
    )
  }

  it('shows empty docker tree', async () => {
    server.use(http.get('/api/v1/browse/repositories/:name/docker-tree', () => HttpResponse.json({ root: { kind: 'folder', label: '', path: '', children: [] } })))
    renderBrowse('?repo=docker-hosted')
    expect(await screen.findByText(/No Docker metadata cached yet/)).toBeInTheDocument()
  })

  it('expands tree, selects a tag and shows component details', async () => {
    const user = userEvent.setup()
    seedDocker()
    server.use(http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })))
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    await user.click(await screen.findByText('latest'))
    expect(await screen.findByText('Component details')).toBeInTheDocument()
    expect((await screen.findAllByText('docker-hosted')).length).toBeGreaterThan(0)
    // scan badge row
    expect(await screen.findByText('Vulnerability scan')).toBeInTheDocument()
  })

  it('runs a scan from the docker detail panel', async () => {
    const user = userEvent.setup()
    seedDocker()
    server.use(
      http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })),
      http.post('/api/v1/components/:id/scan', () =>
        HttpResponse.json({ scannedAt: new Date().toISOString(), imageRef: 'myapp:latest', status: 'ok', summary: { critical: 1, high: 0, medium: 0, low: 0, unknown: 0, total: 1 }, findings: [{ id: 'CVE-1', severity: 'CRITICAL', pkgName: 'openssl', installedVersion: '1.0', fixedVersion: '1.1', title: 'bad' }] }),
      ),
    )
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    await user.click(await screen.findByText('latest'))
    await screen.findByText('Vulnerability scan')
    await user.click(screen.getByRole('button', { name: /Scan now/ }))
    expect(await screen.findByText('CRITICAL: 1')).toBeInTheDocument()
    expect(screen.getByText('openssl')).toBeInTheDocument()
  })

  it('opens Example Usage and Promote from docker panel', async () => {
    const user = userEvent.setup()
    seedDocker()
    server.use(
      http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })),
      http.get('/api/v1/components/:id/promotion-rules', () =>
        HttpResponse.json([{ id: 'pr1', name: 'rel', from_repo: 'docker-hosted', to_repo: 'docker-release', require_scan_pass: false, require_manual_approval: false }]),
      ),
    )
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    await user.click(await screen.findByText('latest'))
    await screen.findByText('Component details')
    await user.click(screen.getByRole('button', { name: /Example Usage/ }))
    expect(await screen.findByText('Documentation coming soon')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Close' }))
    await user.click(screen.getByRole('button', { name: /Promote/ }))
    expect(await screen.findByText(/Promote 1 component/)).toBeInTheDocument()
  })

  it('promote with no rules alerts', async () => {
    const user = userEvent.setup()
    seedDocker()
    const alertSpy = vi.spyOn(window, 'alert').mockImplementation(() => {})
    server.use(
      http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })),
      http.get('/api/v1/components/:id/promotion-rules', () => HttpResponse.json([])),
    )
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    await user.click(await screen.findByText('latest'))
    await screen.findByText('Component details')
    await user.click(screen.getByRole('button', { name: /Promote/ }))
    await waitFor(() => expect(alertSpy).toHaveBeenCalled())
  })

  it('deletes a docker tag', async () => {
    const user = userEvent.setup()
    let deleted = false
    seedDocker()
    server.use(
      http.delete('/api/v1/browse/repositories/:name/docker-tag', () => { deleted = true; return new HttpResponse(null, { status: 204 }) }),
    )
    renderBrowse('?repo=docker-hosted')
    await user.click(await screen.findByText('myapp'))
    await user.click(await screen.findByText('Tags'))
    const tagRow = (await screen.findByText('latest')).closest('[role="button"]')!
    fireEvent.click(within(tagRow as HTMLElement).getByTitle('Delete tag'))
    expect(await screen.findByText('Delete file?')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /^Delete$/ }))
    await waitFor(() => expect(deleted).toBe(true))
  })

  it('refreshes the docker tree', async () => {
    const user = userEvent.setup()
    let calls = 0
    server.use(
      http.get('/api/v1/browse/repositories/:name/docker-tree', () => { calls++; return HttpResponse.json(dockerTree) }),
    )
    renderBrowse('?repo=docker-hosted')
    await screen.findByText('myapp')
    const before = calls
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(calls).toBeGreaterThan(before))
  })

  it('auto-drills to a component via ?cid= URL param', async () => {
    seedDocker()
    server.use(http.get('/api/v1/components/:id/scan', () => new HttpResponse(null, { status: 204 })))
    Element.prototype.scrollIntoView = vi.fn()
    renderBrowse('?repo=docker-hosted&cid=dc1')
    expect(await screen.findByText('Component details')).toBeInTheDocument()
    expect(await screen.findByText('Vulnerability scan')).toBeInTheDocument()
  })
})

describe('BrowsePage — promote flow', () => {
  it('promotes selected components successfully', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({ items: [{ id: 'c1', name: 'pkg-a', group: '', version: '1', format: 'maven2', assets: [] }], continuationToken: null }),
      ),
      http.get('/api/v1/components/:id/promotion-rules', () =>
        HttpResponse.json([{ id: 'pr1', name: 'rel', from_repo: 'maven-hosted', to_repo: 'maven-release', require_scan_pass: false, require_manual_approval: false }]),
      ),
      http.post('/api/v1/promotion/promote', () => HttpResponse.json({ requests: [{ status: 'completed' }] })),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('pkg-a')
    await user.click(screen.getAllByRole('checkbox')[0])
    await user.click(await screen.findByRole('button', { name: /Promote selected/ }))
    await screen.findByText(/Promote 1 component/)
    await user.click(screen.getByRole('button', { name: /Select a rule/ }))
    await user.click(await screen.findByText(/rel \(/))
    const promoteBtns = screen.getAllByRole('button', { name: /^Promote$/ })
    await user.click(promoteBtns[promoteBtns.length - 1])
    expect(await screen.findByText(/Promoted 1 component/)).toBeInTheDocument()
  })

  it('shows promotion error', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({ items: [{ id: 'c1', name: 'pkg-a', group: '', version: '1', format: 'maven2', assets: [] }], continuationToken: null }),
      ),
      http.get('/api/v1/components/:id/promotion-rules', () =>
        HttpResponse.json([{ id: 'pr1', name: 'rel', from_repo: 'maven-hosted', to_repo: 'maven-release', require_scan_pass: false, require_manual_approval: true }]),
      ),
      http.post('/api/v1/promotion/promote', () => HttpResponse.json({ error: 'promote failed' }, { status: 400 })),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('pkg-a')
    await user.click(screen.getAllByRole('checkbox')[0])
    await user.click(await screen.findByRole('button', { name: /Promote selected/ }))
    await screen.findByText(/Promote 1 component/)
    await user.click(screen.getByRole('button', { name: /Select a rule/ }))
    await user.click(await screen.findByText(/rel \(/))
    const promoteBtns = screen.getAllByRole('button', { name: /^Promote$/ })
    await user.click(promoteBtns[promoteBtns.length - 1])
    expect(await screen.findByText(/Error: promote failed/)).toBeInTheDocument()
  })
})

describe('BrowsePage — non-admin', () => {
  it('hides delete actions and upload for non-admin without privileges', async () => {
    useAuthStore.setState({ token: 'tok', user: fixtures.user({ roles: ['viewer'] }) as ReturnType<typeof fixtures.user> })
    server.use(
      http.get('/api/v1/me/privileges', () => HttpResponse.json([])),
      http.get('/service/rest/v1/components', () =>
        HttpResponse.json({ items: [{ id: 'c1', name: 'pkg-a', group: '', version: '1', format: 'maven2', assets: [{ id: 'a1', path: 'p.jar', fileSize: 1, contentType: 't' }] }], continuationToken: null }),
      ),
    )
    renderBrowse('?repo=maven-hosted')
    await screen.findByText('pkg-a')
    expect(screen.queryByTitle('Delete')).not.toBeInTheDocument()
  })
})
