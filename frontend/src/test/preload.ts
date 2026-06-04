/**
 * preload.ts — runs FIRST in setupFiles, before setup.ts.
 *
 * Node 25 ships a native `localStorage` object that lacks `.getItem()` /
 * `.setItem()` etc. (it only works when `--localstorage-file=<path>` is
 * passed to the Node process). Vitest's jsdom environment replaces
 * `globalThis.localStorage` with a proper in-memory implementation, but that
 * replacement happens at the *environment* level — AFTER the module graph is
 * resolved. Zustand's `authStore` calls `localStorage.getItem()` at module
 * evaluation time, so it runs before jsdom's replacement and hits the broken
 * native object.
 *
 * Solution: polyfill `globalThis.localStorage` here (still inside the jsdom
 * worker context) BEFORE any test file imports `authStore` transitively. By
 * listing this file first in `setupFiles`, it runs before setup.ts (which
 * imports the MSW server, which imports handlers, which imports fixtures, which
 * triggers authStore evaluation through renderUtils).
 *
 * We only patch when `getItem` is missing — if jsdom has already provided a
 * proper implementation we leave it alone.
 */
if (typeof globalThis.localStorage === 'undefined' ||
    typeof (globalThis.localStorage as Storage).getItem !== 'function') {
  const store: Record<string, string> = {}
  Object.defineProperty(globalThis, 'localStorage', {
    writable: true,
    configurable: true,
    value: {
      getItem: (k: string): string | null => store[k] ?? null,
      setItem: (k: string, v: string): void => { store[k] = String(v) },
      removeItem: (k: string): void => { delete store[k] },
      clear: (): void => { for (const k of Object.keys(store)) delete store[k] },
      key: (i: number): string | null => Object.keys(store)[i] ?? null,
      get length(): number { return Object.keys(store).length },
    } satisfies Storage,
  })
}
