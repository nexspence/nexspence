import { Moon, Sun } from 'lucide-react'
import { useThemeStore } from '@/theme/theme'

/**
 * Switches between the dark and light themes. The icon shows the theme the
 * click leads to, the label says it in words.
 */
export function ThemeToggle({ className, size = 13 }: { className?: string; size?: number }) {
  const theme = useThemeStore(s => s.theme)
  const toggleTheme = useThemeStore(s => s.toggleTheme)
  const label = theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'
  return (
    <button
      type="button"
      className={className}
      onClick={toggleTheme}
      title={label}
      aria-label={label}
      data-testid="theme-toggle"
    >
      {theme === 'dark' ? <Sun size={size} /> : <Moon size={size} />}
    </button>
  )
}
