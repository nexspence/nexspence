import { describe, it, expect, vi, beforeAll } from 'vitest'
import { render, fireEvent, screen } from '@testing-library/react'
import { MiniChart } from './MiniChart'

class ROStub {
  observe = vi.fn()
  unobserve = vi.fn()
  disconnect = vi.fn()
}

beforeAll(() => {
  vi.stubGlobal('ResizeObserver', ROStub)
})

const data = [
  { label: '2m ago', value: 1 },
  { label: '1m ago', value: 5 },
  { label: 'now', value: 3 },
]

describe('MiniChart', () => {
  it('renders a line path and tick labels', () => {
    const { container } = render(
      <MiniChart data={data} type="line" color="#3b82f6" ariaLabel="Requests per second" />,
    )
    const path = container.querySelector('svg path')
    expect(path).not.toBeNull()
    expect(path!.getAttribute('d')).toMatch(/^M/)
    expect(path!.getAttribute('d')).not.toContain('Z')
    expect(screen.getByText('2m ago')).toBeInTheDocument()
    expect(screen.getByRole('img', { name: 'Requests per second' })).toBeInTheDocument()
  })

  it('renders a closed area path for type="area"', () => {
    const { container } = render(
      <MiniChart data={data} type="area" color="#22c55e" ariaLabel="Storage" />,
    )
    expect(container.querySelector('svg path')!.getAttribute('d')).toContain('Z')
  })

  it('shows a tooltip on mouse move and hides it on leave', () => {
    const { container } = render(
      <MiniChart data={data} type="line" color="#3b82f6" ariaLabel="req"
        valueFormatter={v => `${v.toFixed(1)} rps`} />,
    )
    const svg = container.querySelector('svg')!
    // Y ticks always use the formatter ('1.0 rps' … '5.0 rps'), so tooltip
    // presence is asserted by the duplicated hovered-point label/value and
    // the hover marker circle, not by raw textContent.
    expect(screen.getAllByText('1m ago').length).toBe(1) // x tick only
    expect(container.querySelector('circle')).toBeNull()
    fireEvent.mouseMove(svg, { clientX: 300, clientY: 50 }) // nearest point: index 1
    expect(screen.getAllByText('1m ago').length).toBe(2) // x tick + tooltip
    expect(screen.getAllByText('5.0 rps').length).toBe(2) // y tick + tooltip
    expect(container.querySelectorAll('circle').length).toBe(1) // hover marker
    fireEvent.mouseLeave(svg)
    expect(screen.getAllByText('1m ago').length).toBe(1)
    expect(container.querySelector('circle')).toBeNull()
  })

  it('handles flat and single-point series without NaN', () => {
    const flat = render(
      <MiniChart data={[{ label: 'a', value: 2 }, { label: 'b', value: 2 }]}
        type="line" color="#fff" ariaLabel="flat" />,
    )
    expect(flat.container.querySelector('svg path')!.getAttribute('d')).not.toContain('NaN')

    const single = render(
      <MiniChart data={[{ label: 'a', value: 2 }]} type="line" color="#fff" ariaLabel="one" />,
    )
    expect(single.container.querySelector('svg circle')).not.toBeNull()
    expect(single.container.innerHTML).not.toContain('NaN')
  })

  it('formats billion-scale values with a G suffix', () => {
    render(
      <MiniChart
        data={[{ label: 'a', value: 50_000_000_000 }, { label: 'b', value: 80_000_000_000 }]}
        type="area" color="#22c55e" ariaLabel="Storage bytes" />,
    )
    expect(screen.getAllByText(/\d(\.\d)?G/).length).toBeGreaterThan(0)
  })

  it('renders nothing for empty data', () => {
    const { container } = render(
      <MiniChart data={[]} type="line" color="#fff" ariaLabel="empty" />,
    )
    expect(container.querySelector('svg')).toBeNull()
  })
})
