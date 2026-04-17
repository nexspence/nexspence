import axios from 'axios'

export const apiClient = axios.create({
  baseURL: import.meta.env.VITE_API_BASE || '',
  headers: { 'Content-Type': 'application/json' },
})

// Attach JWT token from localStorage
apiClient.interceptors.request.use((config) => {
  const token = localStorage.getItem('nexspence_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// Redirect to login on 401
apiClient.interceptors.response.use(
  (r) => r,
  (err) => {
    if (err.response?.status === 401) {
      localStorage.removeItem('nexspence_token')
      window.location.href = '/login'
    }
    return Promise.reject(err)
  },
)

// ── API helpers ──────────────────────────────────────────────

export const nexusApi = {
  // Auth
  login: (username: string, password: string) =>
    apiClient.post('/api/v1/login', { username, password }),
  me: () => apiClient.get('/api/v1/me'),

  // Repositories
  listRepositories: (params?: { format?: string; type?: string }) =>
    apiClient.get('/service/rest/v1/repositories', { params }),
  getRepository: (name: string) =>
    apiClient.get(`/service/rest/v1/repositories/${name}`),
  createRepository: (format: string, type: string, data: unknown) =>
    apiClient.post(`/service/rest/v1/repositories/${format}/${type}`, data),
  deleteRepository: (name: string) =>
    apiClient.delete(`/service/rest/v1/repositories/${name}`),

  // Components
  listComponents: (repository: string, continuationToken?: string) =>
    apiClient.get('/service/rest/v1/components', {
      params: { repository, continuationToken },
    }),
  deleteComponent: (id: string) =>
    apiClient.delete(`/service/rest/v1/components/${id}`),

  // Search
  search: (params: Record<string, string | undefined>) =>
    apiClient.get('/service/rest/v1/search', { params }),

  // Users
  listUsers: () => apiClient.get('/service/rest/v1/security/users'),
  getUser: (userId: string) =>
    apiClient.get(`/service/rest/v1/security/users/${userId}`),
  createUser: (data: unknown) =>
    apiClient.post('/service/rest/v1/security/users', data),
  updateUser: (userId: string, data: unknown) =>
    apiClient.put(`/service/rest/v1/security/users/${userId}`, data),
  deleteUser: (userId: string) =>
    apiClient.delete(`/service/rest/v1/security/users/${userId}`),
  changePassword: (userId: string, password: string) =>
    apiClient.put(
      `/service/rest/v1/security/users/${userId}/change-password`,
      password,
      { headers: { 'Content-Type': 'text/plain' } },
    ),

  // Roles
  listRoles: () => apiClient.get('/service/rest/v1/security/roles'),

  // Blob stores
  listBlobStores: () => apiClient.get('/service/rest/v1/blobstores'),

  // Cleanup policies
  listCleanupPolicies: () => apiClient.get('/service/rest/v1/cleanup-policies'),
  getCleanupPolicy: (id: string) =>
    apiClient.get(`/service/rest/v1/cleanup-policies/${id}`),
  createCleanupPolicy: (data: unknown) =>
    apiClient.post('/service/rest/v1/cleanup-policies', data),
  updateCleanupPolicy: (id: string, data: unknown) =>
    apiClient.put(`/service/rest/v1/cleanup-policies/${id}`, data),
  deleteCleanupPolicy: (id: string) =>
    apiClient.delete(`/service/rest/v1/cleanup-policies/${id}`),
  runCleanupPolicy: (id: string) =>
    apiClient.post(`/service/rest/v1/cleanup-policies/${id}/run`),

  // Audit log
  listAuditEvents: (params?: {
    domain?: string
    action?: string
    limit?: number
    offset?: number
  }) => apiClient.get('/service/rest/v1/audit', { params }),

  // Metrics
  getMetrics: () => apiClient.get('/api/v1/metrics'),

  // System status
  getStatus: () => apiClient.get('/service/rest/v1/status'),
}

export const nexspenceApi = {
  // Migration (Nexspence-native)
  listMigrationJobs: () => apiClient.get('/api/v1/migration/jobs'),
  createMigrationJob: (data: unknown) =>
    apiClient.post('/api/v1/migration/jobs', data),
  getMigrationJob: (id: string) =>
    apiClient.get(`/api/v1/migration/jobs/${id}`),
  pauseMigrationJob: (id: string) =>
    apiClient.post(`/api/v1/migration/jobs/${id}/pause`),
  resumeMigrationJob: (id: string) =>
    apiClient.post(`/api/v1/migration/jobs/${id}/resume`),
  previewMigration: (data: unknown) =>
    apiClient.post('/api/v1/migration/preview', data),

  // System info
  getSystemInfo: () => apiClient.get('/api/v1/system/info'),
}
