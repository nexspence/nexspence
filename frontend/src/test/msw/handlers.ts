import { http, HttpResponse } from 'msw'
import { fixtures } from '../fixtures'

export const handlers = [
  // Auth
  http.get('/api/v1/auth/config', () =>
    HttpResponse.json(fixtures.authConfig())
  ),
  http.post('/api/v1/login', () =>
    HttpResponse.json(fixtures.loginResponse())
  ),
  http.get('/api/v1/me', () =>
    HttpResponse.json(fixtures.user())
  ),
  http.post('/api/v1/logout', () =>
    new HttpResponse(null, { status: 204 })
  ),
  http.get('/api/v1/auth/token-policy', () =>
    HttpResponse.json({ tokenMaxDays: 365 })
  ),
  http.get('/api/v1/me/privileges', () =>
    HttpResponse.json([])
  ),

  // Repositories
  http.get('/service/rest/v1/repositories', () =>
    HttpResponse.json([fixtures.repository()])
  ),
  http.post('/service/rest/v1/repositories/:format/:type', () =>
    HttpResponse.json(fixtures.repository(), { status: 201 })
  ),
  http.delete('/service/rest/v1/repositories/:name', () =>
    new HttpResponse(null, { status: 204 })
  ),
  http.put('/service/rest/v1/repositories/:format/:type/:name', () =>
    HttpResponse.json(fixtures.repository())
  ),
  http.patch('/service/rest/v1/repositories/:name', () =>
    HttpResponse.json(fixtures.repository())
  ),
  http.get('/service/rest/v1/repositories/:name', () =>
    HttpResponse.json(fixtures.repository())
  ),

  // Users
  http.get('/service/rest/v1/security/users', () =>
    HttpResponse.json([fixtures.user()])
  ),
  http.post('/service/rest/v1/security/users', () =>
    HttpResponse.json(fixtures.user(), { status: 201 })
  ),
  http.put('/service/rest/v1/security/users/:userId', () =>
    HttpResponse.json(fixtures.user())
  ),
  http.delete('/service/rest/v1/security/users/:userId', () =>
    new HttpResponse(null, { status: 204 })
  ),
  http.put('/service/rest/v1/security/users/:userId/roles', () =>
    new HttpResponse(null, { status: 204 })
  ),

  // Roles
  http.get('/service/rest/v1/security/roles', () =>
    HttpResponse.json([{ id: 'role-1', name: 'nx-admin', source: 'default', description: 'Admin role' }])
  ),
  http.post('/service/rest/v1/security/roles', () =>
    HttpResponse.json({ id: 'role-2', name: 'new-role' }, { status: 201 })
  ),
  http.delete('/service/rest/v1/security/roles/:id', () =>
    new HttpResponse(null, { status: 204 })
  ),
  http.get('/service/rest/v1/security/roles/:roleId/privileges', () =>
    HttpResponse.json([])
  ),
  http.put('/service/rest/v1/security/roles/:roleId/privileges', () =>
    new HttpResponse(null, { status: 204 })
  ),

  // Components / Search
  http.get('/service/rest/v1/components', () =>
    HttpResponse.json({ items: [], continuationToken: null })
  ),
  http.get('/service/rest/v1/search', () =>
    HttpResponse.json({ items: [], continuationToken: null })
  ),
  http.get('/service/rest/v1/search/assets', () =>
    HttpResponse.json({ items: [], continuationToken: null })
  ),

  // Audit
  http.get('/service/rest/v1/audit', () =>
    HttpResponse.json({ items: [], total: 0 })
  ),

  // Blob stores — uses /service/rest/v1/blobstores (from nexusApi.listBlobStores)
  http.get('/service/rest/v1/blobstores', () => HttpResponse.json([])),
  http.post('/service/rest/v1/blobstores/:type', () =>
    HttpResponse.json({ name: 'test-store' }, { status: 201 })
  ),
  http.delete('/service/rest/v1/blobstores/:name', () =>
    new HttpResponse(null, { status: 204 })
  ),
  http.post('/api/v1/blobstores/test', () =>
    HttpResponse.json({ ok: true })
  ),
  http.get('/api/v1/blob-stores/:name/usage', () =>
    HttpResponse.json({ usedBytes: 0, quotaBytes: null })
  ),

  // System / services
  http.get('/api/v1/system/services', () => HttpResponse.json([])),
  http.get('/api/v1/system/info', () =>
    HttpResponse.json({ version: '1.9.0', uptime: 1000 })
  ),
  http.get('/service/rest/v1/status', () =>
    HttpResponse.json({ edition: 'OSS', version: '1.9.0' })
  ),

  // Cleanup policies
  http.get('/service/rest/v1/cleanup-policies', () => HttpResponse.json([])),
  http.post('/service/rest/v1/cleanup-policies', () =>
    HttpResponse.json({ id: 'cp-1' }, { status: 201 })
  ),
  http.delete('/service/rest/v1/cleanup-policies/:id', () =>
    new HttpResponse(null, { status: 204 })
  ),

  // Routing rules
  http.get('/service/rest/v1/routing-rules', () => HttpResponse.json([])),

  // Migration
  http.get('/api/v1/migration/jobs', () => HttpResponse.json([])),
  http.post('/api/v1/migration/jobs', () =>
    HttpResponse.json({ id: 'job-1' }, { status: 201 })
  ),

  // Security / privileges
  http.get('/service/rest/v1/security/privileges', () => HttpResponse.json([])),
  http.post('/service/rest/v1/security/privileges', () =>
    HttpResponse.json({ id: 'priv-1' }, { status: 201 })
  ),
  http.delete('/service/rest/v1/security/privileges/:id', () =>
    new HttpResponse(null, { status: 204 })
  ),

  // Content selectors — uses /service/rest/v1/security/content-selectors (from nexusApi)
  http.get('/service/rest/v1/security/content-selectors', () => HttpResponse.json([])),
  http.post('/service/rest/v1/security/content-selectors', () =>
    HttpResponse.json({ id: 'cs-1' }, { status: 201 })
  ),
  http.delete('/service/rest/v1/security/content-selectors/:id', () =>
    new HttpResponse(null, { status: 204 })
  ),
  http.get('/api/v1/security/privilege-role-map', () =>
    HttpResponse.json({})
  ),

  // Metrics
  http.get('/api/v1/metrics/history', () => HttpResponse.json({ points: [] })),
  http.get('/api/v1/metrics/repos', () => HttpResponse.json({ repos: [] })),
  http.get('/api/v1/metrics', () => HttpResponse.json({ requests: 0 })),

  // Browse
  http.get('/api/v1/browse/repositories/:name/tree', () =>
    HttpResponse.json({ rows: [] })
  ),
  http.get('/api/v1/browse/repositories/:name/docker-tree', () =>
    HttpResponse.json({ rows: [] })
  ),
  http.get('/api/v1/browse/repositories/:name/raw-tree', () =>
    HttpResponse.json({ rows: [] })
  ),
  http.get('/api/v1/browse/repositories/:name/path-tree', () =>
    HttpResponse.json({ paths: [] })
  ),

  // Repository quota & blob store migration
  http.get('/api/v1/repositories/:name/quota', () =>
    HttpResponse.json({ usedBytes: 0, quotaBytes: null, percentUsed: null })
  ),
  http.get('/api/v1/repositories/:name/blob-store-migration', () =>
    new HttpResponse(null, { status: 404 })
  ),

  // Replication
  http.get('/api/v1/replication/rules', () => HttpResponse.json([])),

  // Scan
  http.get('/api/v1/components/:id/scan', () =>
    new HttpResponse(null, { status: 204 })
  ),
]
