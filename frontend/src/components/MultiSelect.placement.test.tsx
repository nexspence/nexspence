import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MultiSelect } from './MultiSelect'

afterEach(() => vi.restoreAllMocks())

describe('MultiSelect menu placement', () => {
  it('opens above the trigger near the bottom of the viewport', async () => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      top: 720, bottom: 760, left: 100, right: 460, width: 360, height: 40, x: 100, y: 720, toJSON: () => ({}),
    } as DOMRect)
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
    render(<MultiSelect options={[{ value: 'a', label: 'Alpha' }]} value={[]} onChange={() => {}} />)
    await userEvent.click(screen.getByText('— Select —'))
    const menu = screen.getByPlaceholderText('Filter…').closest('.holo-card') as HTMLElement
    expect(menu.style.top).toBe('')
    expect(menu.style.bottom).toBe('84px')
  })
})
