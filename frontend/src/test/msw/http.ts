import { http as mswHttp } from 'msw'

const resolveNodePath = (path: string | RegExp): string | RegExp =>
  typeof path === 'string' && path.startsWith('/')
    ? `http://localhost${path}`
    : path

export const http = new Proxy(mswHttp, {
  get(target, property, receiver) {
    const method = Reflect.get(target, property, receiver)
    if (typeof method !== 'function') return method

    return (path: string | RegExp, ...args: unknown[]) =>
      Reflect.apply(method, target, [resolveNodePath(path), ...args])
  },
}) as typeof mswHttp

export { HttpResponse } from 'msw'
