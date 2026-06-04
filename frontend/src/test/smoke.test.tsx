/**
 * smoke.test.tsx — proves the entire test stack works end-to-end:
 *   1. Vitest runs and basic assertions work.
 *   2. RTL + jsdom + CSS modules + path alias + providers all work.
 *   3. MSW intercepts axios requests.
 */
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { HoloButton } from '@/components/holo/holo'
import { apiClient } from '@/api/client'
import { renderWithProviders } from './renderUtils'
import { fixtures } from './fixtures'

// ── Test 1: vitest runs ──────────────────────────────────────────────────────
describe('Smoke: vitest runs', () => {
  it('basic arithmetic works', () => {
    expect(1 + 1).toBe(2)
  })
})

// ── Test 2: RTL + jsdom + CSS modules + path alias ───────────────────────────
describe('Smoke: RTL renders HoloButton', () => {
  it('renders a button with the given label via renderWithProviders', () => {
    renderWithProviders(<HoloButton>Hello Smoke</HoloButton>)
    expect(screen.getByRole('button', { name: 'Hello Smoke' })).toBeInTheDocument()
  })

  it('renders a button with icon via plain render', () => {
    render(<HoloButton icon={<span data-testid="ico" />}>With Icon</HoloButton>)
    expect(screen.getByTestId('ico')).toBeInTheDocument()
    expect(screen.getByText('With Icon')).toBeInTheDocument()
  })
})

// ── Test 3: MSW intercepts axios ─────────────────────────────────────────────
describe('Smoke: MSW intercepts axios', () => {
  it('GET /api/v1/me returns the fixture user', async () => {
    // Seed a token so the auth interceptor attaches it
    localStorage.setItem('nexspence_token', fixtures.loginResponse().token)
    const res = await apiClient.get('/api/v1/me')
    expect(res.status).toBe(200)
    expect(res.data.username).toBe('admin')
    expect(res.data.roles).toContain('nx-admin')
  })

  it('POST /api/v1/login returns the fixture loginResponse', async () => {
    const res = await apiClient.post('/api/v1/login', { username: 'admin', password: 'admin123' })
    expect(res.status).toBe(200)
    expect(res.data.token).toBeTruthy()
    expect(res.data.user.username).toBe('admin')
  })
})
