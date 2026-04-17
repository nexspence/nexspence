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
  login: (username: string, password: string) => Promise<void>
  logout: () => void
  isAdmin: () => boolean
}

export const useAuthStore = create<AuthState>((set, get) => ({
  token: localStorage.getItem('nexspence_token'),
  user: null,

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
}))
