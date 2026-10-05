import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Select, SelectOption } from './Select'

const opts: SelectOption[] = [
  { value: 'a', label: 'Alpha' },
  { value: 'b', label: 'Beta' },
]

// Puts the trigger at a given vertical position in an 800px-high viewport.
function placeTrigger(top: number) {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
    top, bottom: top + 40, left: 100, right: 460, width: 360, height: 40, x: 100, y: top,
    toJSON: () => ({}),
  } as DOMRect)
  Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
}

afterEach(() => vi.restoreAllMocks())

describe('Select menu placement', () => {
  it('opens below the trigger when there is room', async () => {
    placeTrigger(100)
    render(<Select options={opts} value="" onChange={() => {}} />)
    await userEvent.click(screen.getByRole('button'))
    const menu = screen.getByRole('listbox')
    expect(menu.style.top).toBe('144px')
    expect(menu.style.bottom).toBe('')
  })

  // Near the bottom of the screen the menu used to run off the viewport.
  it('opens above the trigger when the space below is too small', async () => {
    placeTrigger(720)
    render(<Select options={opts} value="" onChange={() => {}} />)
    await userEvent.click(screen.getByRole('button'))
    const menu = screen.getByRole('listbox')
    expect(menu.style.top).toBe('')
    expect(menu.style.bottom).toBe(`${800 - 720 + 4}px`)
    expect(parseInt(menu.style.maxHeight, 10)).toBeLessThanOrEqual(720 - 12)
  })

  it('never makes the menu taller than the space it opens into', async () => {
    placeTrigger(560)
    render(<Select options={opts} value="" onChange={() => {}} />)
    await userEvent.click(screen.getByRole('button'))
    const menu = screen.getByRole('listbox')
    const below = 800 - (560 + 40) - 4
    if (menu.style.top) {
      expect(parseInt(menu.style.maxHeight, 10)).toBeLessThanOrEqual(below - 8)
    }
  })
})
