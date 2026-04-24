import { create } from 'zustand'
import { apiClient } from '@/api/client'

interface User {
  id: string
  username: string
  email: string
  firstName: string
  lastName: string
  roles: string[]
}

interface AuthState {
  token: string | null
  user: User | null
  init: () => Promise<void>
  login: (username: string, password: string) => Promise<void>
  logout: () => void
  isAdmin: () => boolean
  isOIDC: () => boolean
}

export const useAuthStore = create<AuthState>((set, get) => ({
  token: localStorage.getItem('nexspence_token'),
  user: null,

  // Rehydrate user from server after page refresh (token is in localStorage but user is null).
  init: async () => {
    if (!get().token || get().user) return
    try {
      const res = await apiClient.get('/api/v1/me')
      set({ user: res.data })
    } catch {
      // Token is invalid/expired — clear it
      localStorage.removeItem('nexspence_token')
      set({ token: null, user: null })
    }
  },

  login: async (username, password) => {
    const res = await apiClient.post('/api/v1/login', { username, password })
    const { token, user } = res.data
    localStorage.setItem('nexspence_token', token)
    set({ token, user })
  },

  logout: () => {
    localStorage.removeItem('nexspence_token')
    set({ token: null, user: null })
    window.location.href = '/login'
  },

  isAdmin: () => {
    const user = get().user
    return user?.roles?.includes('nx-admin') ?? false
  },

  isOIDC: () => {
    const token = get().token
    if (!token) return false
    try {
      const payload = JSON.parse(atob(token.split('.')[1]))
      return payload.auth_method === 'oidc'
    } catch {
      return false
    }
  },
}))
