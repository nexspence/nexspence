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

// Redirect to login on 401, but NOT when the 401 comes from the login endpoint itself
// (otherwise the error never reaches the form and "nothing happens").
apiClient.interceptors.response.use(
  (r) => r,
  (err) => {
    const url: string = err.config?.url ?? ''
    if (err.response?.status === 401 && !url.endsWith('/login')) {
      localStorage.removeItem('nexspence_token')
      window.location.href = '/login'
    }
    return Promise.reject(err)
  },
)

// ── Domain types ─────────────────────────────────────────────

export interface Privilege {
  id: string
  type?: string
  contentSelectorId?: string
  attrs?: { actions?: string[] }
}

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
  updateRepository: (
    format: string,
    type: string,
    name: string,
    data: Record<string, unknown>,
  ) =>
    apiClient.put(`/service/rest/v1/repositories/${format}/${type}/${name}`, data),

  // Components
  listComponents: (repository: string, continuationToken?: string) =>
    apiClient.get('/service/rest/v1/components', {
      params: { repository, continuationToken },
    }),
  getComponent: (id: string) => apiClient.get(`/service/rest/v1/components/${id}`),
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
  createRole: (data: { name: string; description?: string }) =>
    apiClient.post('/service/rest/v1/security/roles', data),
  deleteRole: (id: string) =>
    apiClient.delete(`/service/rest/v1/security/roles/${id}`),
  setUserRoles: (userId: string, roleIds: string[]) =>
    apiClient.put(`/service/rest/v1/security/users/${userId}/roles`, { roleIds }),

  // Role privileges
  listRolePrivileges: (roleId: string) =>
    apiClient.get(`/service/rest/v1/security/roles/${roleId}/privileges`),
  setRolePrivileges: (roleId: string, privilegeIds: string[]) =>
    apiClient.put(`/service/rest/v1/security/roles/${roleId}/privileges`, { privilegeIds }),
  updateRole: (id: string, data: { name: string; description?: string }) =>
    apiClient.put(`/service/rest/v1/security/roles/${id}`, data),

  // Privileges
  listPrivileges: () =>
    apiClient.get('/service/rest/v1/security/privileges'),
  createPrivilege: (data: unknown) =>
    apiClient.post('/service/rest/v1/security/privileges', data),
  updatePrivilege: (id: string, data: unknown) =>
    apiClient.put(`/service/rest/v1/security/privileges/${id}`, data),
  deletePrivilege: (id: string) =>
    apiClient.delete(`/service/rest/v1/security/privileges/${id}`),

  // Content selectors
  listContentSelectors: () =>
    apiClient.get('/service/rest/v1/security/content-selectors'),
  createContentSelector: (data: unknown) =>
    apiClient.post('/service/rest/v1/security/content-selectors', data),
  updateContentSelector: (id: string, data: unknown) =>
    apiClient.put(`/service/rest/v1/security/content-selectors/${id}`, data),
  deleteContentSelector: (id: string) =>
    apiClient.delete(`/service/rest/v1/security/content-selectors/${id}`),
  attachContentSelector: (privilegeName: string, selectorId: string) =>
    apiClient.put(`/service/rest/v1/security/privileges/${privilegeName}/content-selector/${selectorId}`),
  detachContentSelector: (privilegeName: string) =>
    apiClient.delete(`/service/rest/v1/security/privileges/${privilegeName}/content-selector`),

  // Blob stores
  listBlobStores: () => apiClient.get('/service/rest/v1/blobstores'),
  updateBlobStore: (type: string, name: string, data: unknown) =>
    apiClient.put(`/service/rest/v1/blobstores/${type}/${name}`, data),

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

  // Browse — path tree for content selector dropdowns
  listPathTree: (repoName: string, q?: string) =>
    apiClient.get<{ paths: string[] }>(
      `/api/v1/browse/repositories/${encodeURIComponent(repoName)}/path-tree`,
      { params: q ? { q } : {} },
    ),
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

  // Backup / restore (admin) — see handlers/backup.go
  exportBackup: () =>
    apiClient.get('/api/v1/backup/export', { responseType: 'blob' }),
  restoreBackup: (file: File) => {
    const fd = new FormData()
    fd.append('file', file)
    return apiClient.post<{ restored: Record<string, number> }>('/api/v1/backup/restore', fd)
  },

  // Repository storage usage vs quota — GET /api/v1/repositories/:name/quota
  getRepositoryQuota: (repoName: string) =>
    apiClient.get<{
      usedBytes: number
      quotaBytes: number | null
      percentUsed: number | null
    }>(`/api/v1/repositories/${encodeURIComponent(repoName)}/quota`),

  // Browse — Nexus-style Docker tree (image / Tags | Manifests | Blobs)
  getDockerBrowseTree: (repository: string) =>
    apiClient.get(`/api/v1/browse/repositories/${encodeURIComponent(repository)}/docker-tree`),

  // Security — privilege → role membership map
  privilegeRoleMap: () =>
    apiClient.get<Record<string, string[]>>('/api/v1/security/privilege-role-map').then(r => r.data),

  // Current user's effective privileges
  myPrivileges: () =>
    apiClient.get<Privilege[]>('/api/v1/me/privileges').then(r => r.data),

  // Delete artifact by path (non-docker)
  deleteByPath: (repoName: string, path: string) =>
    apiClient.delete(`/api/v1/browse/repositories/${encodeURIComponent(repoName)}/path`, {
      params: { path },
    }),

  // Delete Docker tag: cascades manifest + digest alias + unreferenced blobs
  deleteDockerTag: (repoName: string, image: string, ref: string) =>
    apiClient.delete(`/api/v1/browse/repositories/${encodeURIComponent(repoName)}/docker-tag`, {
      params: { image, ref },
    }),

  // Delete all tags/manifests/blobs for a Docker image or namespace prefix
  deleteDockerImage: (repoName: string, image: string) =>
    apiClient.delete(`/api/v1/browse/repositories/${encodeURIComponent(repoName)}/docker-image`, {
      params: { image },
    }),

  // Vulnerability scan (Trivy) — Docker components
  // GET returns 204 No Content when no cached scan yet (not an error); we map that to data: null.
  getScanResult: (componentId: string) =>
    apiClient
      .get(`/api/v1/components/${encodeURIComponent(componentId)}/scan`)
      .then((r) => (r.status === 204 ? { ...r, data: null } : r)),
  scanComponent: (componentId: string, body?: { imageRef?: string }) =>
    apiClient.post(
      `/api/v1/components/${encodeURIComponent(componentId)}/scan`,
      body ?? {},
    ),
}
