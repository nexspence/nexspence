import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { useOverflowTitle } from './useOverflowTitle'

/**
 * jsdom performs no layout, so scrollWidth and clientWidth are always 0. The
 * hook exists precisely to compare them, so the test has to supply them —
 * stubbing the prototype is the only place they can come from here.
 */
function stubWidths(scrollWidth: number, clientWidth: number) {
  Object.defineProperty(HTMLElement.prototype, 'scrollWidth', { configurable: true, value: scrollWidth })
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', { configurable: true, value: clientWidth })
}

function restoreWidths() {
  // @ts-expect-error — removing the stub restores jsdom's own zero-width getters
  delete HTMLElement.prototype.scrollWidth
  // @ts-expect-error — same
  delete HTMLElement.prototype.clientWidth
}

function Probe({ text }: { text?: string | null }) {
  const { ref, title } = useOverflowTitle<HTMLDivElement>(text)
  return <div ref={ref} title={title} data-testid="cell">{text}</div>
}

afterEach(() => {
  restoreWidths()
  vi.unstubAllGlobals()
})

describe('useOverflowTitle', () => {
  // The whole point: a tooltip only where something is actually unreadable.
  it('exposes the full text when it is clipped', () => {
    stubWidths(300, 120)
    render(<Probe text="a replication error far too long to fit in the cell" />)

    expect(screen.getByTestId('cell')).toHaveAttribute(
      'title',
      'a replication error far too long to fit in the cell',
    )
  })

  // An unconditional title turns every row into a popup on the way past.
  it('sets no title when the text fits', () => {
    stubWidths(120, 120)
    render(<Probe text="short" />)

    expect(screen.getByTestId('cell')).not.toHaveAttribute('title')
  })

  it('sets no title when there is no text to reveal', () => {
    stubWidths(300, 120)
    render(<Probe text="" />)

    expect(screen.getByTestId('cell')).not.toHaveAttribute('title')
  })

  // A column that was wide enough a moment ago may not be after a resize.
  it('re-measures when the element resizes', () => {
    let trigger: (() => void) | undefined
    vi.stubGlobal('ResizeObserver', class {
      constructor(cb: () => void) { trigger = cb }
      observe() {}
      disconnect() {}
    })

    stubWidths(120, 120)
    render(<Probe text="a value that only overflows once the column narrows" />)
    expect(screen.getByTestId('cell')).not.toHaveAttribute('title')

    stubWidths(300, 120)
    act(() => { trigger?.() })

    expect(screen.getByTestId('cell')).toHaveAttribute(
      'title',
      'a value that only overflows once the column narrows',
    )
  })

  // MiniChart already guards for this: jsdom, and any environment without the
  // API, must not throw.
  it('works where ResizeObserver does not exist', () => {
    vi.stubGlobal('ResizeObserver', undefined)
    stubWidths(300, 120)

    expect(() => render(<Probe text="still measured once on mount" />)).not.toThrow()
    expect(screen.getByTestId('cell')).toHaveAttribute('title', 'still measured once on mount')
  })
})
