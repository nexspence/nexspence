import { ReactElement, ReactNode } from 'react'
import { render, RenderOptions, RenderResult } from '@testing-library/react'
import { MemoryRouter, MemoryRouterProps } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useAuthStore } from '@/store/authStore'
import { fixtures } from './fixtures'

// Create a fresh QueryClient per test — no cross-test cache pollution.
function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
}

interface WrapperProps {
  routerProps?: MemoryRouterProps
  queryClient?: QueryClient
  children?: ReactNode
}

export function createWrapper({ routerProps, queryClient }: WrapperProps = {}) {
  const qc = queryClient ?? makeQueryClient()
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={qc}>
        <MemoryRouter {...routerProps}>{children}</MemoryRouter>
      </QueryClientProvider>
    )
  }
}

export function renderWithProviders(
  ui: ReactElement,
  options?: Omit<RenderOptions, 'wrapper'> & WrapperProps,
): RenderResult {
  const { routerProps, queryClient, ...renderOptions } = options ?? {}
  return render(ui, {
    wrapper: createWrapper({ routerProps, queryClient }),
    ...renderOptions,
  })
}

// Seed the Zustand auth store with a logged-in admin user.
export function seedAuthAsAdmin() {
  useAuthStore.setState({
    token: fixtures.loginResponse().token,
    user: fixtures.user() as ReturnType<typeof fixtures.user>,
  })
}

// Seed the Zustand auth store with a logged-out state.
export function seedAuthAsGuest() {
  useAuthStore.setState({ token: null, user: null })
}
