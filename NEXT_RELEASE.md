### ✨ Features


### 🐛 Bug Fixes

- **Frontend lint restored** — added a flat `eslint.config.js` (ESLint 10 dropped `.eslintrc`/`--ext`), wired `typescript-eslint` + react-hooks + react-refresh, and fixed the `lint` script. `npm run lint` now passes clean (0 problems). Cleared all findings it surfaced: replaced ~15 `any` error/catch sites with a typed `apiErrorMessage(e, fallback)` helper + `ApiError` interface in `api/client.ts`, fixed empty catch blocks and a side-effect ternary in SearchPage, and wrapped render-derived arrays (`items`/`allItems`) in `useMemo` to satisfy `exhaustive-deps`.
