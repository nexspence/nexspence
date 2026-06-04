// frontend/src/store/authStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useAuthStore } from './authStore'
import { server } from '@/test/msw/server'
import { http, HttpResponse } from 'msw'
import { fixtures } from '@/test/fixtures'

beforeEach(() => {
  localStorage.clear()
  useAuthStore.setState({ token: null, user: null })
})

describe('useAuthStore.login', () => {
  it('stores token and user on success', async () => {
    await useAuthStore.getState().login('admin', 'admin123')
    const state = useAuthStore.getState()
    expect(state.token).toBeTruthy()
    expect(state.user?.username).toBe('admin')
    expect(localStorage.getItem('nexspence_token')).toBeTruthy()
  })

  it('throws on bad credentials', async () => {
    server.use(
      http.post('/api/v1/login', () =>
        HttpResponse.json({ error: 'invalid credentials' }, { status: 401 })
      )
    )
    await expect(
      useAuthStore.getState().login('bad', 'pass')
    ).rejects.toBeDefined()
  })
})

describe('useAuthStore.init', () => {
  it('no-ops when no token', async () => {
    await useAuthStore.getState().init()
    expect(useAuthStore.getState().user).toBeNull()
  })

  it('no-ops when user is already loaded', async () => {
    // init() returns early when user is already set
    const user = fixtures.user() as never
    useAuthStore.setState({ token: 'tok', user })
    await useAuthStore.getState().init()
    // user should still be the same object — no network call made
    expect(useAuthStore.getState().user?.username).toBe('admin')
  })

  it('loads user when token is valid', async () => {
    localStorage.setItem('nexspence_token', 'valid-token')
    useAuthStore.setState({ token: 'valid-token', user: null })
    await useAuthStore.getState().init()
    expect(useAuthStore.getState().user?.username).toBe('admin')
  })

  it('clears token when /me returns 401', async () => {
    localStorage.setItem('nexspence_token', 'expired')
    useAuthStore.setState({ token: 'expired', user: null })
    server.use(
      http.get('/api/v1/me', () =>
        HttpResponse.json({ error: 'unauthorized' }, { status: 401 })
      )
    )
    await useAuthStore.getState().init()
    expect(useAuthStore.getState().token).toBeNull()
    expect(localStorage.getItem('nexspence_token')).toBeNull()
  })
})

describe('useAuthStore.logout', () => {
  it('clears token and user from state and localStorage', () => {
    useAuthStore.setState({ token: 'tok', user: fixtures.user() as never })
    localStorage.setItem('nexspence_token', 'tok')
    useAuthStore.getState().logout()
    expect(useAuthStore.getState().token).toBeNull()
    expect(useAuthStore.getState().user).toBeNull()
    expect(localStorage.getItem('nexspence_token')).toBeNull()
  })

  it('sets window.location.href to /login', () => {
    useAuthStore.setState({ token: 'tok', user: fixtures.user() as never })
    useAuthStore.getState().logout()
    expect(window.location.href).toBe('/login')
  })
})

describe('useAuthStore.isAdmin', () => {
  it('returns true when user has nx-admin role', () => {
    useAuthStore.setState({ token: 'tok', user: fixtures.user() as never })
    expect(useAuthStore.getState().isAdmin()).toBe(true)
  })

  it('returns false when user has no roles', () => {
    useAuthStore.setState({
      token: 'tok',
      user: fixtures.user({ roles: [] }) as never,
    })
    expect(useAuthStore.getState().isAdmin()).toBe(false)
  })

  it('returns false when user is null', () => {
    useAuthStore.setState({ token: null, user: null })
    expect(useAuthStore.getState().isAdmin()).toBe(false)
  })

  it('returns false when user has other roles but not nx-admin', () => {
    useAuthStore.setState({
      token: 'tok',
      user: fixtures.user({ roles: ['developer', 'viewer'] }) as never,
    })
    expect(useAuthStore.getState().isAdmin()).toBe(false)
  })
})

describe('useAuthStore.isOIDC', () => {
  it('returns false when no token', () => {
    useAuthStore.setState({ token: null, user: null })
    expect(useAuthStore.getState().isOIDC()).toBe(false)
  })

  it('returns false for local-auth token (no auth_method=oidc in payload)', () => {
    // The fixture token payload is: {"sub":"user-1","username":"admin","roles":["nx-admin"]}
    // No auth_method field — so isOIDC returns false
    useAuthStore.setState({ token: fixtures.loginResponse().token, user: null })
    expect(useAuthStore.getState().isOIDC()).toBe(false)
  })

  it('returns true for token with auth_method=oidc', () => {
    // Build a JWT-shaped token with auth_method=oidc in the payload
    const payload = btoa(JSON.stringify({ sub: 'user-1', auth_method: 'oidc' }))
    const oidcToken = `header.${payload}.sig`
    useAuthStore.setState({ token: oidcToken, user: null })
    expect(useAuthStore.getState().isOIDC()).toBe(true)
  })

  it('returns false for a malformed token that cannot be decoded', () => {
    useAuthStore.setState({ token: 'not-a-jwt', user: null })
    expect(useAuthStore.getState().isOIDC()).toBe(false)
  })
})
