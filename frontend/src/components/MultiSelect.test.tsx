import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MultiSelect, MultiSelectOption } from './MultiSelect'

const opts: MultiSelectOption[] = [
  { value: 'a', label: 'Alpha' },
  { value: 'b', label: 'Beta' },
  { value: 'c', label: 'Gamma' },
]

describe('MultiSelect', () => {
  it('shows placeholder when nothing selected', () => {
    render(<MultiSelect options={opts} value={[]} onChange={() => {}} placeholder="Choose" />)
    expect(screen.getByText('Choose')).toBeInTheDocument()
  })

  it('shows chips for selected values', () => {
    render(<MultiSelect options={opts} value={['a', 'c']} onChange={() => {}} />)
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    expect(screen.getByText('Gamma')).toBeInTheDocument()
  })

  it('falls back to value as label when option missing', () => {
    render(<MultiSelect options={opts} value={['unknown']} onChange={() => {}} />)
    expect(screen.getByText('unknown')).toBeInTheDocument()
  })

  it('opens dropdown and toggles an option on click', async () => {
    const onChange = vi.fn()
    render(<MultiSelect options={opts} value={[]} onChange={onChange} />)
    await userEvent.click(screen.getByText('— Select —'))
    await userEvent.click(screen.getByText('Beta'))
    expect(onChange).toHaveBeenCalledWith(['b'])
  })

  it('removes a value when toggling an already-selected option', async () => {
    const onChange = vi.fn()
    render(<MultiSelect options={opts} value={['a', 'b']} onChange={onChange} />)
    await userEvent.click(screen.getByText('Alpha'))
    // Open dropdown via chevron region — click the trigger container.
    // Click the option row "Alpha" within the open list.
    const alphaRows = screen.getAllByText('Alpha')
    await userEvent.click(alphaRows[alphaRows.length - 1])
    expect(onChange).toHaveBeenCalledWith(['b'])
  })

  it('removes a chip via its X icon', async () => {
    const onChange = vi.fn()
    const { container } = render(<MultiSelect options={opts} value={['a']} onChange={onChange} />)
    // The X icon (lucide svg) inside the chip.
    const chip = screen.getByText('Alpha').closest('span')!
    const x = chip.querySelector('svg')!
    fireEvent.click(x)
    expect(onChange).toHaveBeenCalledWith([])
  })

  it('filters options and shows "No options" on no match', async () => {
    render(<MultiSelect options={opts} value={[]} onChange={() => {}} />)
    await userEvent.click(screen.getByText('— Select —'))
    const filter = screen.getByPlaceholderText('Filter…')
    await userEvent.type(filter, 'zzz')
    expect(screen.getByText('No options')).toBeInTheDocument()
  })

  it('select all then deselect all toggles all filtered values', async () => {
    const onChange = vi.fn()
    const { rerender } = render(<MultiSelect options={opts} value={[]} onChange={onChange} />)
    await userEvent.click(screen.getByText('— Select —'))
    await userEvent.click(screen.getByText('Select all'))
    expect(onChange).toHaveBeenCalledWith(['a', 'b', 'c'])

    // Re-render as fully selected → label becomes "Deselect all".
    onChange.mockClear()
    rerender(<MultiSelect options={opts} value={['a', 'b', 'c']} onChange={onChange} />)
    expect(screen.getByText('Deselect all')).toBeInTheDocument()
    await userEvent.click(screen.getByText('Deselect all'))
    expect(onChange).toHaveBeenCalledWith([])
  })

  it('closes on Escape', async () => {
    render(<MultiSelect options={opts} value={[]} onChange={() => {}} />)
    await userEvent.click(screen.getByText('— Select —'))
    expect(screen.getByPlaceholderText('Filter…')).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByPlaceholderText('Filter…')).not.toBeInTheDocument()
  })

  it('closes on outside mousedown', async () => {
    render(<MultiSelect options={opts} value={[]} onChange={() => {}} />)
    await userEvent.click(screen.getByText('— Select —'))
    expect(screen.getByPlaceholderText('Filter…')).toBeInTheDocument()
    fireEvent.mouseDown(document.body)
    expect(screen.queryByPlaceholderText('Filter…')).not.toBeInTheDocument()
  })

  it('applies hover styles on option rows', async () => {
    render(<MultiSelect options={opts} value={[]} onChange={() => {}} />)
    await userEvent.click(screen.getByText('— Select —'))
    const row = screen.getByText('Beta').closest('div')!
    fireEvent.mouseEnter(row)
    expect(row.style.background).toContain('124, 92, 255')
    fireEvent.mouseLeave(row)
    expect(row.style.background).toBe('transparent')
  })
})
