import { describe, it, expect } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { TagEditor } from './TagEditor'
import { renderWithProviders } from '@/test/renderUtils'
import { server } from '@/test/msw/server'

const TAGS_URL = '/service/rest/v1/components/:id/tags'

describe('TagEditor', () => {
  it('renders initial tags and the hint', () => {
    renderWithProviders(
      <TagEditor componentId="c1" initialTags={['prod', 'team:backend']} queryKey={['k']} />
    )
    // "prod" appears in both a chip and the hint code sample.
    expect(screen.getAllByText('prod').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('team:backend').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText(/Press Enter then Save/)).toBeInTheDocument()
  })

  it('shows "No tags" placeholder when there are no tags', () => {
    renderWithProviders(<TagEditor componentId="c1" initialTags={[]} queryKey={['k']} />)
    expect(screen.getByText('No tags')).toBeInTheDocument()
  })

  it('adds a tag via the Add button and shows the Save button', async () => {
    renderWithProviders(<TagEditor componentId="c1" initialTags={[]} queryKey={['k']} />)
    await userEvent.type(screen.getByPlaceholderText('Add tag (Enter)'), 'newtag')
    await userEvent.click(screen.getByRole('button', { name: '+ Add' }))
    expect(screen.getByText('newtag')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save tags' })).toBeInTheDocument()
  })

  it('adds a tag on Enter keydown', async () => {
    renderWithProviders(<TagEditor componentId="c1" initialTags={[]} queryKey={['k']} />)
    const input = screen.getByPlaceholderText('Add tag (Enter)')
    await userEvent.type(input, 'viaenter{Enter}')
    expect(screen.getByText('viaenter')).toBeInTheDocument()
  })

  it('ignores blank and duplicate tags', async () => {
    renderWithProviders(<TagEditor componentId="c1" initialTags={['dup']} queryKey={['k']} />)
    const input = screen.getByPlaceholderText('Add tag (Enter)')
    // Blank
    await userEvent.click(screen.getByRole('button', { name: '+ Add' }))
    // Duplicate
    await userEvent.type(input, 'dup{Enter}')
    expect(screen.getAllByText('dup')).toHaveLength(1)
    // No Save button because nothing became dirty.
    expect(screen.queryByRole('button', { name: 'Save tags' })).not.toBeInTheDocument()
  })

  it('removes a tag and marks dirty', async () => {
    renderWithProviders(<TagEditor componentId="c1" initialTags={['gone', 'stay']} queryKey={['k']} />)
    await userEvent.click(screen.getAllByTitle('Remove tag')[0])
    expect(screen.queryByText('gone')).not.toBeInTheDocument()
    expect(screen.getByText('stay')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save tags' })).toBeInTheDocument()
  })

  it('saves tags successfully and hides the Save button', async () => {
    server.use(http.put(TAGS_URL, () => HttpResponse.json({ tags: ['saved'] })))
    renderWithProviders(<TagEditor componentId="c1" initialTags={[]} queryKey={['k']} />)
    await userEvent.type(screen.getByPlaceholderText('Add tag (Enter)'), 'saved{Enter}')
    await userEvent.click(screen.getByRole('button', { name: 'Save tags' }))
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Save tags' })).not.toBeInTheDocument()
    )
  })

  it('shows an error message when save fails', async () => {
    server.use(http.put(TAGS_URL, () => new HttpResponse(null, { status: 500 })))
    renderWithProviders(<TagEditor componentId="c1" initialTags={[]} queryKey={['k']} />)
    await userEvent.type(screen.getByPlaceholderText('Add tag (Enter)'), 'boom{Enter}')
    await userEvent.click(screen.getByRole('button', { name: 'Save tags' }))
    await waitFor(() => expect(screen.getByText('Save failed')).toBeInTheDocument())
  })

  it('hides editing controls in readOnly mode', () => {
    renderWithProviders(<TagEditor componentId="c1" initialTags={['ro']} queryKey={['k']} readOnly />)
    expect(screen.getByText('ro')).toBeInTheDocument()
    expect(screen.queryByPlaceholderText('Add tag (Enter)')).not.toBeInTheDocument()
    expect(screen.queryByTitle('Remove tag')).not.toBeInTheDocument()
  })
})
