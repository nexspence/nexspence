import { describe, it, expect, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Truncated } from './Truncated'

function stubWidths(scrollWidth: number, clientWidth: number) {
  Object.defineProperty(HTMLElement.prototype, 'scrollWidth', { configurable: true, value: scrollWidth })
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', { configurable: true, value: clientWidth })
}

afterEach(() => {
  // @ts-expect-error — removing the stub restores jsdom's zero-width getters
  delete HTMLElement.prototype.scrollWidth
  // @ts-expect-error — same
  delete HTMLElement.prototype.clientWidth
})

describe('Truncated', () => {
  // A hook cannot be called per row inside a .map(), which is where nearly
  // every clipped value in this UI lives. That is what the component is for.
  it('reveals the value on overflow, row by row', () => {
    stubWidths(300, 100)
    const rows = ['first overlong value', 'second overlong value']
    render(<div>{rows.map(r => <Truncated key={r} text={r} />)}</div>)

    expect(screen.getByText('first overlong value')).toHaveAttribute('title', 'first overlong value')
    expect(screen.getByText('second overlong value')).toHaveAttribute('title', 'second overlong value')
  })

  it('stays quiet when the value fits', () => {
    stubWidths(100, 100)
    render(<Truncated text="fits" />)
    expect(screen.getByText('fits')).not.toHaveAttribute('title')
  })

  // Owning the clipping styles is the point: the three properties were copied
  // by hand at every site, and a site that forgets one does not clip at all.
  it('applies the clipping styles itself', () => {
    stubWidths(100, 100)
    render(<Truncated text="v" data-testid="t" />)

    const el = screen.getByTestId('t')
    expect(el).toHaveStyle({ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })
  })

  it('lets the call site add its own styles', () => {
    stubWidths(100, 100)
    render(<Truncated text="v" data-testid="t" style={{ maxWidth: 200, color: 'rgb(239, 68, 68)' }} />)

    const el = screen.getByTestId('t')
    expect(el).toHaveStyle({ maxWidth: '200px', color: 'rgb(239, 68, 68)', overflow: 'hidden' })
  })

  it('renders as the requested element, for table cells', () => {
    stubWidths(100, 100)
    render(<table><tbody><tr><Truncated as="td" text="cell" /></tr></tbody></table>)

    expect(screen.getByText('cell').tagName).toBe('TD')
  })

  // Some sites decorate the value — an arrow, a badge — while the title should
  // still be the value alone.
  it('titles with text even when children render something else', () => {
    stubWidths(300, 100)
    render(
      <Truncated text="https://registry.npmjs.org" data-testid="t">
        <span aria-hidden="true">↗ </span>
        <span>https://registry.npmjs.org</span>
      </Truncated>,
    )

    expect(screen.getByTestId('t')).toHaveAttribute('title', 'https://registry.npmjs.org')
  })
})
