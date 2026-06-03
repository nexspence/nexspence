### ✨ Features


### 🐛 Bug Fixes

- **Frontend lint restored** — added a flat `eslint.config.js` (ESLint 10 dropped `.eslintrc`/`--ext`), wired `typescript-eslint` + react-hooks + react-refresh, and fixed the `lint` script. `npm run lint` now passes clean (0 problems). Cleared all findings it surfaced: replaced ~15 `any` error/catch sites with a typed `apiErrorMessage(e, fallback)` helper + `ApiError` interface in `api/client.ts`, fixed empty catch blocks and a side-effect ternary in SearchPage, and wrapped render-derived arrays (`items`/`allItems`) in `useMemo` to satisfy `exhaustive-deps`. Enabled the full react-hooks v7 strict ruleset (React Compiler lints: purity, static-components, refs, set-state-in-render, …) — fixed the one real finding (`SidebarDetail` was declared inside render; now a `renderSidebarDetail()` helper) and turned off the two dataflow rules that misfire on idiomatic hand-written React (`set-state-in-effect`, `immutability`).
- **Backup archive truncation now surfaced** — `BackupService.Export`/`ExportRepo` used `defer gw.Close()`/`tw.Close()` and returned `nil` even if the final gzip trailer or tar end-of-archive blocks failed to flush, silently producing a truncated/corrupt archive. Both now use named-return error propagation so a Close error surfaces when the body otherwise succeeded. (Surfaced by the golangci-lint errcheck pass.)
- **Proxy cache-write error formatting** — `repoproxy` built its cache-write error with `fmt.Errorf("...copyErr=%w putErr=%w", copyErr, putErr)` where one error can be nil when the branch fires, rendering the malformed `%!w(<nil>)`. Now uses `errors.Join(copyErr, putErr)`, which drops nil errors and yields a clean wrappable error.
- **Blob-store migration context leak** — the `context.WithCancel` cancel func in `BlobStoreMigrationService` was never invoked on the migration goroutine's exit paths (a context resource leak). `cancel()` now runs in the goroutine's cleanup defer on every exit path.

### 🔒 Security

- **Tighter blob directory permissions** — `LocalBlobStore` now creates blob directories with `0o750` (was `0o755`); these are app-owned and served through the app, so group/other write/traverse is unnecessary.

### 🔧 Quality / Tooling

- **golangci-lint v2.12.2 adopted** — added a tuned `.golangci.yml` (v2 schema) enabling the standard set plus error-handling (errorlint, bodyclose, sqlclosecheck, rowserrcheck, nilerr) and style/security linters (revive [curated rule subset], gocritic, gosec, misspell, unconvert, nakedret, gocyclo, whitespace, usestdlibvars), with `max-same-issues`/`max-issues-per-linter` uncapped so the gate never hides findings. Pinned via `make lint` (Go module proxy, `v2.12.2`) and enforced by a new CI `lint` job (`.github/workflows/ci.yml`). All findings resolved to zero; security suppressions (md5/sha1 protocol checksums, content-addressed blob path) carry inline justifications.
- **Deferred (future tracks):** revive's `exported` doc-comment rule (~446 sites), `unused-parameter`, `unexported-return`, and `var-naming` were intentionally left disabled (high-churn or behavior-changing); migrating the repository layer from `(nil, nil)`-means-not-found to an `ErrNotFound` sentinel, and fixing a pre-existing `TestAuditMiddleware` test-only data race (production code is correct — the test reads a mock's slice without synchronizing with the fire-and-forget audit goroutine), are tracked for the backend test-coverage track.
