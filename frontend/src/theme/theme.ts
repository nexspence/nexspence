import { create } from 'zustand'

export type Theme = 'dark' | 'light'

/** localStorage key. public/theme-init.js reads the same key before first paint. */
export const THEME_STORAGE_KEY = 'nexspence-theme'

export const DEFAULT_THEME: Theme = 'dark'

function isTheme(v: unknown): v is Theme {
  return v === 'dark' || v === 'light'
}

/**
 * The persisted choice, or the default. Storage can throw (disabled cookies,
 * some private modes), and an unreadable preference must not break the UI.
 */
export function readStoredTheme(): Theme {
  try {
    const v = localStorage.getItem(THEME_STORAGE_KEY)
    return isTheme(v) ? v : DEFAULT_THEME
  } catch {
    return DEFAULT_THEME
  }
}

function persistTheme(theme: Theme) {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, theme)
  } catch {
    // The theme still applies for this page load; it just won't survive a reload.
  }
}

/** Applies the theme to <html>: the CSS token blocks key off data-theme. */
export function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme
}

/**
 * The theme currently on <html>. theme-init.js normally set it before React
 * loaded; when it didn't run (tests, a blocked script) fall back to storage.
 */
function initialTheme(): Theme {
  const current = document.documentElement.dataset.theme
  return isTheme(current) ? current : readStoredTheme()
}

interface ThemeState {
  theme: Theme
  setTheme: (theme: Theme) => void
  toggleTheme: () => void
}

export const useThemeStore = create<ThemeState>((set, get) => {
  const theme = initialTheme()
  applyTheme(theme)
  return {
    theme,
    setTheme: (next) => {
      applyTheme(next)
      persistTheme(next)
      set({ theme: next })
    },
    toggleTheme: () => get().setTheme(get().theme === 'dark' ? 'light' : 'dark'),
  }
})
