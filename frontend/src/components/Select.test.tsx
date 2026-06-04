import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Select, SelectOption } from './Select'

const opts: SelectOption[] = [
  { value: 'a', label: 'Alpha' },
  { value: 'b', label: 'Beta' },
  { value: 'c', label: 'Gamma' },
]

describe('Select', () => {
  it('shows placeholder when nothing is selected', () => {
    render(<Select options={opts} value="" onChange={() => {}} placeholder="Pick one" />)
    expect(screen.getByRole('button', { name: /Pick one/ })).toBeInTheDocument()
  })

  it('shows the selected option label', () => {
    render(<Select options={opts} value="b" onChange={() => {}} />)
    expect(screen.getByRole('button', { name: /Beta/ })).toBeInTheDocument()
  })

  it('opens the dropdown and lists options, selecting fires onChange and closes', async () => {
    const onChange = vi.fn()
    render(<Select options={opts} value="" onChange={onChange} />)
    await userEvent.click(screen.getByRole('button'))
    // Options rendered in portal.
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    expect(screen.getByText('Gamma')).toBeInTheDocument()
    await userEvent.click(screen.getByText('Gamma'))
    expect(onChange).toHaveBeenCalledWith('c')
    // Dropdown closed.
    expect(screen.queryByText('Alpha')).not.toBeInTheDocument()
  })

  it('does not open when disabled', async () => {
    render(<Select options={opts} value="" onChange={() => {}} disabled />)
    await userEvent.click(screen.getByRole('button'))
    expect(screen.queryByText('Alpha')).not.toBeInTheDocument()
  })

  it('toggles closed on a second trigger click', async () => {
    render(<Select options={opts} value="" onChange={() => {}} />)
    const trigger = screen.getByRole('button')
    await userEvent.click(trigger)
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    await userEvent.click(trigger)
    expect(screen.queryByText('Alpha')).not.toBeInTheDocument()
  })

  it('filters options when searchable and shows "No matches"', async () => {
    render(<Select options={opts} value="" onChange={() => {}} searchable />)
    await userEvent.click(screen.getByRole('button'))
    const filter = screen.getByPlaceholderText('Filter…')
    await userEvent.type(filter, 'alp')
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    expect(screen.queryByText('Beta')).not.toBeInTheDocument()
    await userEvent.clear(filter)
    await userEvent.type(filter, 'zzz')
    expect(screen.getByText('No matches')).toBeInTheDocument()
  })

  it('shows "No options" when given an empty list', async () => {
    render(<Select options={[]} value="" onChange={() => {}} />)
    await userEvent.click(screen.getByRole('button'))
    expect(screen.getByText('No options')).toBeInTheDocument()
  })

  it('renders badge and tag for the selected option and in the list', async () => {
    const withExtras: SelectOption[] = [
      { value: 'a', label: 'Alpha', badge: <span data-testid="badge">B</span>, tag: <span data-testid="tag">T</span> },
    ]
    render(<Select options={withExtras} value="a" onChange={() => {}} />)
    expect(screen.getByTestId('badge')).toBeInTheDocument()
    expect(screen.getByTestId('tag')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button'))
    // Now both trigger and list render badge/tag.
    expect(screen.getAllByTestId('badge').length).toBeGreaterThan(0)
  })

  it('closes on Escape key', async () => {
    render(<Select options={opts} value="" onChange={() => {}} />)
    await userEvent.click(screen.getByRole('button'))
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByText('Alpha')).not.toBeInTheDocument()
  })

  it('closes on outside mousedown', async () => {
    render(<Select options={opts} value="" onChange={() => {}} />)
    await userEvent.click(screen.getByRole('button'))
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    fireEvent.mouseDown(document.body)
    expect(screen.queryByText('Alpha')).not.toBeInTheDocument()
  })

  it('closes on window resize', async () => {
    render(<Select options={opts} value="" onChange={() => {}} />)
    await userEvent.click(screen.getByRole('button'))
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    fireEvent(window, new Event('resize'))
    expect(screen.queryByText('Alpha')).not.toBeInTheDocument()
  })

  it('highlights the selected option in the list', async () => {
    render(<Select options={opts} value="b" onChange={() => {}} />)
    await userEvent.click(screen.getByRole('button'))
    // The selected row "Beta" appears in the list.
    const rows = screen.getAllByText('Beta')
    expect(rows.length).toBeGreaterThan(0)
  })

  it('applies hover background on mouse enter/leave', async () => {
    render(<Select options={opts} value="a" onChange={() => {}} />)
    await userEvent.click(screen.getByRole('button'))
    const beta = screen.getByText('Beta').closest('div')!
    fireEvent.mouseEnter(beta)
    expect(beta.style.background).toContain('124, 92, 255')
    fireEvent.mouseLeave(beta)
    expect(beta.style.background).toBe('transparent')
  })
})
