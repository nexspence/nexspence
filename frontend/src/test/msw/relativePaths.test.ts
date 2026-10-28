import { expect, it } from 'vitest'
import { http, HttpResponse } from '@/test/msw/http'

it('resolves relative handler paths to the API origin for Node interception', () => {
  const handler = http.get('/api/v1/tokens', () => HttpResponse.json([]))

  expect(handler.info.path).toBe('http://localhost/api/v1/tokens')
})
