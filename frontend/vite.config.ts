import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'
import fs from 'fs'
import { createRequire } from 'module'

const require = createRequire(import.meta.url)

// es-toolkit v1.47's "./compat/*" export condition only maps to the CommonJS
// build (.js); the ESM .mjs sibling is unreachable through the package exports
// map. recharts v3 imports es-toolkit/compat/* (get, sortBy, throttle, …), so
// rolldown takes its CJS-interop path, which miscompiles these modules into a
// self-shadowing `var require_X = require_X()` that throws "X is not a function"
// at chunk init (crashing the Monitoring page). Redirect each compat subpath to
// its ESM .mjs build with a default-export interop shim — recharts uses default
// imports while the .mjs files use named exports — so the whole subtree bundles
// as pure ESM and never hits the broken interop.
function esToolkitCompatEsm(): Plugin {
  const VIRT = '\0es-toolkit-compat:'
  const resolved = new Map<string, { mjs: string; name: string }>()
  return {
    name: 'es-toolkit-compat-esm',
    enforce: 'pre',
    resolveId(source) {
      const m = /^es-toolkit\/compat\/([A-Za-z0-9]+)$/.exec(source)
      if (!m) return null
      const key = m[1]
      if (!resolved.has(key)) {
        const shim = require.resolve(`es-toolkit/compat/${key}`)
        const re = /require\(['"](.+?)['"]\)\.(\w+)/.exec(fs.readFileSync(shim, 'utf8'))
        if (!re) return null
        const mjs = path.resolve(path.dirname(shim), re[1]).replace(/\.js$/, '.mjs')
        resolved.set(key, { mjs, name: re[2] })
      }
      return VIRT + key
    },
    load(id) {
      if (!id.startsWith(VIRT)) return null
      const e = resolved.get(id.slice(VIRT.length))!
      return `export { ${e.name} as default, ${e.name} } from ${JSON.stringify(e.mjs)}`
    },
  }
}

export default defineConfig({
  plugins: [esToolkitCompatEsm(), react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 5173,
    // Proxy API calls to Go backend in dev mode
    proxy: {
      '/service': {
        target: 'http://localhost:8081',
        changeOrigin: true,
      },
      '/api': {
        target: 'http://localhost:8081',
        changeOrigin: true,
      },
      '/repository': {
        target: 'http://localhost:8081',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('react') || id.includes('react-dom') || id.includes('react-router-dom')) {
            return 'vendor'
          }
          if (id.includes('@tanstack/react-query')) {
            return 'query'
          }
        },
      },
    },
  },
})
