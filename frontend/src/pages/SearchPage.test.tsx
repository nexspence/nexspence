import { describe, it, expect, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import SearchPage from './SearchPage'
import { renderWithProviders, seedAuthAsAdmin } from '@/test/renderUtils'
import { server } from '@/test/msw/server'

const component = (overrides?: Record<string, unknown>) => ({
  id: 'c1',
  repository: 'maven-hosted',
  format: 'maven2',
  group: 'org.example',
  name: 'spring-core',
  version: '1.2.3',
  tags: ['prod'],
  assets: [
    { id: 'a1', path: 'org/example/spring-core/1.2.3/spring-core-1.2.3.jar', fileSize: 2048, contentType: 'application/java-archive', lastModified: '2026-05-01T00:00:00Z', lastDownloaded: '2026-05-10T00:00:00Z' },
    { id: 'a2', path: 'org/example/spring-core/1.2.3/spring-core-1.2.3.pom', fileSize: 512, contentType: 'application/xml', lastModified: '2026-05-01T00:00:00Z' },
  ],
  ...overrides,
})

describe('SearchPage', () => {
  beforeEach(() => {
    seedAuthAsAdmin()
    sessionStorage.clear()
    // jsdom has no layout; SearchPage scrolls a highlighted row into view.
    Element.prototype.scrollIntoView = () => {}
  })

  it('renders the prompt empty state before searching', async () => {
    renderWithProviders(<SearchPage />)
    expect(screen.getByRole('heading', { name: 'Search' })).toBeInTheDocument()
    expect(screen.getByText('Enter filters and click Search')).toBeInTheDocument()
    expect(screen.getByPlaceholderText('e.g. spring-core')).toBeInTheDocument()
  })

  it('searches and shows grouped results', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/search', () => HttpResponse.json({ items: [component()] })),
    )
    renderWithProviders(<SearchPage />)
    await user.type(screen.getByPlaceholderText('e.g. spring-core'), 'spring')
    await user.click(screen.getByRole('button', { name: /Search/ }))
    expect(await screen.findByText('spring-core')).toBeInTheDocument()
    expect(screen.getByText('maven-hosted')).toBeInTheDocument()
    expect(screen.getByText('1.2.3')).toBeInTheDocument()
    expect(screen.getByText('prod')).toBeInTheDocument()
    // results label
    expect(screen.getByText(/1 result in 1 repo/)).toBeInTheDocument()
  })

  it('shows no-results state when search returns empty', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/search', () => HttpResponse.json({ items: [] })),
    )
    renderWithProviders(<SearchPage />)
    await user.type(screen.getByPlaceholderText('e.g. spring-core'), 'nope')
    await user.click(screen.getByRole('button', { name: /Search/ }))
    expect(await screen.findByText('No results matched your filters')).toBeInTheDocument()
  })

  it('expands a result row to show assets and toggles sort columns', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/search', () => HttpResponse.json({ items: [component()] })),
    )
    renderWithProviders(<SearchPage />)
    await user.type(screen.getByPlaceholderText('e.g. spring-core'), 'spring')
    await user.click(screen.getByRole('button', { name: /Search/ }))
    await screen.findByText('spring-core')

    // Expand via the chevron next to the name (title "Expand assets")
    const expandToggle = screen.getByTitle('Expand assets')
    await user.click(expandToggle)
    expect(await screen.findByText('org/example/spring-core/1.2.3/spring-core-1.2.3.pom')).toBeInTheDocument()

    // Sort headers: clicking toggles direction without crashing.
    // "Modified" only appears as a sort header (not a form label), so it is unique.
    await user.click(screen.getByText(/^Modified/))
    await user.click(screen.getByText(/^Modified/))
    expect(screen.getByText('spring-core')).toBeInTheDocument()
  })

  it('renders docker digest aliases inside the expanded parent tag', async () => {
    const user = userEvent.setup()
    const tag = component({
      id: 'd1', repository: 'docker-hosted', format: 'docker', name: 'myimage', version: 'latest', group: '',
      assets: [{ id: 'da1', path: 'manifests/latest', fileSize: 1000, contentType: 'application/vnd.oci', lastModified: '2026-05-01T00:00:00Z' }],
    })
    const digest = component({
      id: 'd2', repository: 'docker-hosted', format: 'docker', name: 'myimage', version: 'sha256:abcdef0123456789abcdef', group: '',
      assets: [{ id: 'da2', path: 'blobs/sha256:abcdef', fileSize: 999, contentType: 'application/octet-stream', lastModified: '2026-05-01T00:00:00Z' }],
    })
    server.use(
      http.get('/service/rest/v1/search', () => HttpResponse.json({ items: [tag, digest] })),
    )
    renderWithProviders(<SearchPage />)
    await user.type(screen.getByPlaceholderText('e.g. spring-core'), 'myimage')
    await user.click(screen.getByRole('button', { name: /Search/ }))
    await screen.findByText('myimage')
    // Only the tag (latest) is in the main list, not the sha256 alias.
    expect(screen.getByText('latest')).toBeInTheDocument()
    expect(screen.queryByText(/sha256:abcdef0123456789/)).not.toBeInTheDocument()
    // Expand and look for digest aliases section
    await user.click(screen.getByTitle('Expand assets'))
    expect(await screen.findByText('Digest aliases')).toBeInTheDocument()
    expect(screen.getByText('blobs/sha256:abcdef')).toBeInTheDocument()
  })

  it('navigates to Browse via the group Browse button', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/search', () => HttpResponse.json({ items: [component()] })),
    )
    renderWithProviders(<SearchPage />)
    await user.type(screen.getByPlaceholderText('e.g. spring-core'), 'spring')
    await user.click(screen.getByRole('button', { name: /Search/ }))
    await screen.findByText('spring-core')
    const browseBtn = screen.getByRole('button', { name: /Browse/ })
    await user.click(browseBtn)
    // navigate() called — component still mounted (MemoryRouter has no /browse route, but no crash)
    expect(screen.getByRole('heading', { name: 'Search' })).toBeInTheDocument()
  })

  it('clears filters with the Clear button', async () => {
    const user = userEvent.setup()
    server.use(
      http.get('/service/rest/v1/search', () => HttpResponse.json({ items: [component()] })),
    )
    renderWithProviders(<SearchPage />)
    const nameInput = screen.getByPlaceholderText('e.g. spring-core')
    await user.type(nameInput, 'spring')
    await user.click(screen.getByRole('button', { name: /Search/ }))
    await screen.findByText('spring-core')
    await user.click(screen.getByRole('button', { name: 'Clear' }))
    // Back to the prompt empty state
    expect(await screen.findByText('Enter filters and click Search')).toBeInTheDocument()
  })

  it('restores filters from URL search params and runs the query', async () => {
    server.use(
      http.get('/service/rest/v1/search', ({ request }) => {
        const url = new URL(request.url)
        expect(url.searchParams.get('name')).toBe('spring-core')
        return HttpResponse.json({ items: [component()] })
      }),
    )
    renderWithProviders(<SearchPage />, {
      routerProps: { initialEntries: ['/search?q=spring-core'] },
    })
    expect(await screen.findByText('spring-core')).toBeInTheDocument()
  })

  it('caps rendered rows at PAGE_SIZE=50 and shows a show-more button for larger result sets', async () => {
    const user = userEvent.setup()
    // Build 120 unique components across two repos (60 each) — all non-docker so no digest filtering.
    const manyItems = Array.from({ length: 120 }, (_, i) => ({
      id: `bulk-${i}`,
      repository: i < 60 ? 'repo-a' : 'repo-b',
      format: 'maven2',
      group: 'org.bulk',
      name: `artifact-${i}`,
      version: '1.0.0',
      tags: [],
      assets: [
        { id: `bulk-a-${i}`, path: `org/bulk/artifact-${i}/1.0.0/artifact-${i}-1.0.0.jar`, fileSize: 1024, contentType: 'application/java-archive', lastModified: '2026-05-01T00:00:00Z' },
      ],
    }))
    server.use(
      http.get('/service/rest/v1/search', () => HttpResponse.json({ items: manyItems })),
    )
    renderWithProviders(<SearchPage />)
    await user.type(screen.getByPlaceholderText('e.g. spring-core'), '*')
    await user.click(screen.getByRole('button', { name: /Search/ }))

    // Wait for results to appear
    await screen.findByText('120 results in 2 repos')

    // Only 50 rows should be rendered initially
    const rows = screen.getAllByTestId('search-result-row')
    expect(rows).toHaveLength(50)

    // A "show more" control must be present
    expect(screen.getByRole('button', { name: /Show more/i })).toBeInTheDocument()

    // Clicking show-more adds another PAGE_SIZE batch
    await user.click(screen.getByRole('button', { name: /Show more/i }))
    const rowsAfter = screen.getAllByTestId('search-result-row')
    expect(rowsAfter).toHaveLength(100)
  })

  it('repo-count label counts ALL repos even when the cap hides them (non-interleaved)', async () => {
    const user = userEvent.setup()
    // 60 items from repo-a named "aaa-…" and 60 from repo-b named "zzz-…".
    // Default sort is by name ascending, so all 60 repo-a rows sort BEFORE any repo-b row.
    // The PAGE_SIZE cap of 50 therefore renders only repo-a rows — but the label must still say 2 repos.
    const nonInterleavedItems = [
      ...Array.from({ length: 60 }, (_, i) => ({
        id: `ni-a-${i}`,
        repository: 'repo-a',
        format: 'maven2',
        group: 'org.test',
        name: `aaa-artifact-${i}`,
        version: '1.0.0',
        tags: [],
        assets: [{ id: `ni-aa-${i}`, path: `aaa-artifact-${i}.jar`, fileSize: 512, contentType: 'application/java-archive', lastModified: '2026-05-01T00:00:00Z' }],
      })),
      ...Array.from({ length: 60 }, (_, i) => ({
        id: `ni-b-${i}`,
        repository: 'repo-b',
        format: 'maven2',
        group: 'org.test',
        name: `zzz-artifact-${i}`,
        version: '1.0.0',
        tags: [],
        assets: [{ id: `ni-ba-${i}`, path: `zzz-artifact-${i}.jar`, fileSize: 512, contentType: 'application/java-archive', lastModified: '2026-05-01T00:00:00Z' }],
      })),
    ]
    server.use(
      http.get('/service/rest/v1/search', () => HttpResponse.json({ items: nonInterleavedItems })),
    )
    renderWithProviders(<SearchPage />)
    await user.type(screen.getByPlaceholderText('e.g. spring-core'), '*')
    await user.click(screen.getByRole('button', { name: /Search/ }))

    // Label shows correct total and full repo count from the uncapped set
    await screen.findByText('120 results in 2 repos')

    // Only 50 rows rendered (all from repo-a due to sort order)
    const rows = screen.getAllByTestId('search-result-row')
    expect(rows).toHaveLength(50)

    // Repo-b is invisible in the rendered rows, yet the label correctly counts it
    expect(screen.queryByText('repo-b')).not.toBeInTheDocument()
  })

  it('highlights a returning row from sessionStorage', async () => {
    sessionStorage.setItem('search:lastClickedComponentId', 'c1')
    server.use(
      http.get('/service/rest/v1/search', () => HttpResponse.json({ items: [component()] })),
    )
    renderWithProviders(<SearchPage />, {
      routerProps: { initialEntries: ['/search?q=spring'] },
    })
    expect(await screen.findByText('spring-core')).toBeInTheDocument()
    // The return key should have been consumed.
    await waitFor(() =>
      expect(sessionStorage.getItem('search:lastClickedComponentId')).toBeNull(),
    )
  })
})
