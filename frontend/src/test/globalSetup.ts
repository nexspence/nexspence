/**
 * globalSetup.ts — runs in the Vitest main process before any test workers.
 *
 * Node 25 exposes a native `localStorage` object that is broken without
 * `--localstorage-file` (its `.getItem` / `.setItem` methods are missing).
 * Because Zustand's `authStore` calls `localStorage.getItem()` at module
 * evaluation time, we must polyfill it in the global scope before workers
 * load any modules.
 */
export function setup() {
  // Only patch in environments where the native localStorage is broken.
  if (typeof globalThis.localStorage === 'object' &&
      typeof (globalThis.localStorage as Storage).getItem !== 'function') {
    const store: Record<string, string> = {}
    Object.defineProperty(globalThis, 'localStorage', {
      writable: true,
      configurable: true,
      value: {
        getItem: (k: string) => store[k] ?? null,
        setItem: (k: string, v: string) => { store[k] = String(v) },
        removeItem: (k: string) => { delete store[k] },
        clear: () => { Object.keys(store).forEach(k => delete store[k]) },
        key: (i: number) => Object.keys(store)[i] ?? null,
        get length() { return Object.keys(store).length },
      } satisfies Storage,
    })
  }
}

export function teardown() {
  // nothing to tear down
}
