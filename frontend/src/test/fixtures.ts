import type { AuthConfig } from '@/api/client'

export const fixtures = {
  authConfig: (overrides?: Partial<AuthConfig>): AuthConfig => ({
    oidcEnabled: false,
    oidcDisplayName: 'SSO',
    oidcLoginUrl: '/api/v1/auth/oidc/login',
    ldapEnabled: false,
    samlEnabled: false,
    ...overrides,
  }),

  user: (overrides?: Record<string, unknown>) => ({
    id: 'user-1',
    username: 'admin',
    email: 'admin@test.com',
    firstName: 'Admin',
    lastName: 'User',
    roles: ['nx-admin'],
    source: 'local',
    ...overrides,
  }),

  repository: (overrides?: Record<string, unknown>) => ({
    id: 'repo-1',
    name: 'maven-hosted',
    format: 'maven2',
    type: 'hosted',
    url: 'http://localhost:8081/repository/maven-hosted',
    online: true,
    ...overrides,
  }),

  loginResponse: (overrides?: Record<string, unknown>) => ({
    token: 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTEiLCJ1c2VybmFtZSI6ImFkbWluIiwicm9sZXMiOlsibngtYWRtaW4iXX0.sig',
    user: fixtures.user(),
    ...overrides,
  }),
}
