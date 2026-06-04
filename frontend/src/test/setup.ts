import '@testing-library/jest-dom'
import { beforeAll, afterEach, afterAll, vi } from 'vitest'
import { server } from './msw/server'

beforeAll(() => server.listen({ onUnhandledRequest: 'warn' }))
afterEach(() => {
  server.resetHandlers()
  localStorage.clear()
})
afterAll(() => server.close())

// Stub window.location — jsdom does not support navigation assignment.
// Use a valid URL as `href` base so MSW's XHR interceptor can resolve
// relative API paths (it calls `new URL(path, window.location.href)`).
Object.defineProperty(window, 'location', {
  writable: true,
  value: {
    href: 'http://localhost/',
    pathname: '/',
    search: '',
    hash: '',
    assign: vi.fn(),
    replace: vi.fn(),
  },
})

const originalError = console.error.bind(console)
console.error = (...args: unknown[]) => {
  if (typeof args[0] === 'string' && args[0].includes('Warning:')) return
  originalError(...args)
}
