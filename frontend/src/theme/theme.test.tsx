import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { THEME_STORAGE_KEY } from './theme'
import { tint } from './color'

// The store reads the persisted theme when its module is evaluated, so every
// test loads a fresh copy of it (and of the toggle bound to it).
async function freshModules() {
  vi.resetModules()
  const theme = await import('./theme')
  const toggle = await import('@/components/ThemeToggle')
  return { ...theme, ThemeToggle: toggle.ThemeToggle }
}

beforeEach(() => {
  delete document.documentElement.dataset.theme
  localStorage.clear()
})

afterEach(() => {
  vi.restoreAllMocks()
  delete document.documentElement.dataset.theme
})

describe('theme store', () => {
  it('defaults to dark when nothing is stored', async () => {
    const { useThemeStore } = await freshModules()
    expect(useThemeStore.getState().theme).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('restores a stored light choice', async () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'light')
    const { useThemeStore } = await freshModules()
    expect(useThemeStore.getState().theme).toBe('light')
    expect(document.documentElement.dataset.theme).toBe('light')
  })

  it('ignores an unknown stored value', async () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'sepia')
    const { useThemeStore } = await freshModules()
    expect(useThemeStore.getState().theme).toBe('dark')
  })

  it('keeps the theme theme-init.js already put on <html>', async () => {
    document.documentElement.dataset.theme = 'light'
    const { useThemeStore } = await freshModules()
    expect(useThemeStore.getState().theme).toBe('light')
  })

  it('falls back to dark when storage throws', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('denied') })
    const { readStoredTheme } = await freshModules()
    expect(readStoredTheme()).toBe('dark')
  })

  it('still switches when storage cannot be written', async () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('quota') })
    const { useThemeStore } = await freshModules()
    useThemeStore.getState().setTheme('light')
    expect(useThemeStore.getState().theme).toBe('light')
    expect(document.documentElement.dataset.theme).toBe('light')
  })
})

describe('ThemeToggle', () => {
  it('switches to light, then back to dark, persisting each choice', async () => {
    const { ThemeToggle } = await freshModules()
    render(<ThemeToggle />)

    const btn = screen.getByRole('button', { name: 'Switch to light theme' })
    await userEvent.click(btn)
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')

    await userEvent.click(screen.getByRole('button', { name: 'Switch to dark theme' }))
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark')
  })

  it('offers the way back when the restored theme is light', async () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'light')
    const { ThemeToggle } = await freshModules()
    render(<ThemeToggle />)
    expect(screen.getByRole('button', { name: 'Switch to dark theme' })).toBeInTheDocument()
  })
})

describe('tint', () => {
  it('makes a translucent color that works on a var() reference', () => {
    expect(tint('var(--holo-c-red)', 0.133)).toBe('color-mix(in srgb, var(--holo-c-red) 13.3%, transparent)')
  })

  it('clamps the amount into 0..1', () => {
    expect(tint('#fff', 2)).toBe('color-mix(in srgb, #fff 100%, transparent)')
    expect(tint('#fff', -1)).toBe('color-mix(in srgb, #fff 0%, transparent)')
  })
})
