# Lint Debt + 80% Test Coverage (Backend + Frontend) — Design

**Date:** 2026-06-03
**Status:** Approved (design)
**Goal:** Pay down the Go lint debt and bring both Backend (Go) and Frontend (React/TS) to a hard, CI-enforced **≥80% line coverage** floor — a "super production" quality bar.

## Baseline (measured 2026-06-03)

| Area | State |
|------|-------|
| ESLint (strict react-hooks v7) + `tsc` | ✅ clean |
| `go vet ./...` | ✅ clean |
| `golangci-lint` | ❌ **not installed, no `.golangci.yml`** |
| Go tests | 474 pass, 247 test files |
| **Go total coverage** | **34.8%** |
| Frontend tests | **none** — no Vitest/Jest/RTL, 0 test files |

Lowest Go package coverage:

| Package | Coverage | Notes |
|---|---|---|
| `internal/repository/postgres` | **0%** | DB layer — needs real Postgres |
| `cmd/server`, `internal/db`, `internal/logger`, `internal/redisclient` | 0% | wiring/adapters |
| `internal/storage` | 6.7% | blob store |
| `internal/api/handlers` | 27.7% | REST handlers |
| `internal/service` | 55% | business logic |
| formats/* | 54–78% | per-format handlers |
| `internal/domain` / `distlock` / `events` | 92–97% | already strong |

## Locked Decisions

1. **Scope:** All three tracks, **sequenced** — execute one at a time with a review checkpoint between each.
2. **Order:** **Lint → Backend → Frontend.** Lint first so the large volume of new test code in B/C is held to the standard from line one.
3. **Postgres testing:** **dockertest** — boot an ephemeral real Postgres in Docker, apply migrations, run repo tests against it. Gated behind a build tag (`//go:build integration`) so the fast unit suite stays Docker-free.
4. **Coverage bar:** **Hard ≥80% line-coverage CI gate** (absolute floor, build fails below it — not just a ratchet).
5. **80% scope:** Enforce ≥80% on every package **that contains logic**. Explicit, documented exclusion list for wiring: `cmd/server`, `internal/db`, `internal/logger`, `internal/redisclient`, and any vendored/generated code. Exclusions live in the CI coverage-check config, not silent.
6. **Frontend depth:** **Vitest + React Testing Library + jsdom + MSW** (mock the axios layer). Coverage via `@vitest/coverage-v8`. No Playwright/E2E this round (deferred).

---

## Track A — golangci-lint

**Purpose:** Establish and enforce the Go lint floor; reach zero findings.

- Add pinned `golangci-lint` — preferred: `tool` directive in `go.mod` (Go 1.26 `go tool`) so the version is reproducible without a separate install step; fall back to a pinned binary install in CI if the tool-directive route is awkward.
- Author `.golangci.yml` enabling a production-grade linter set:
  `errcheck, govet, staticcheck, revive, gocritic, ineffassign, unused, bodyclose, sqlclosecheck, rowserrcheck, errorlint, gosec, misspell, unconvert, nakedret, gocyclo (high threshold), gosimple, nilerr`.
  - `exclude-rules` for `_test.go` where appropriate; exclude vendored/generated paths.
- Wire into `Makefile`: `make lint` → `golangci-lint run ./...`.
- Add a `lint` job to GitHub Actions.
- Fix every finding to zero (surgical, no behavior changes).

**Done when:** `golangci-lint run ./...` exits 0 locally and in CI; `make lint` works; all 474 existing tests still pass.

---

## Track B — Backend ≥80%

**Purpose:** Raise Go coverage from 34.8% to ≥80% on every logic package.

### B1. dockertest harness
- New helper package `internal/testutil/pgtest` (build tag `integration`):
  - Boots ephemeral Postgres via `ory/dockertest/v3`.
  - Runs the existing migrations against it.
  - Returns a live `*pgxpool.Pool` + cleanup func.
- Unit suite stays hermetic: integration tests carry `//go:build integration`; default `go test ./...` ignores them. New Make targets: `make test` (unit), `make test-integration` (with the tag + Docker), `make cover`.

### B2. Coverage fill (priority order)
1. `internal/api/handlers` 27.7% → ≥80% — table-driven handler tests with httptest + existing mocks (`internal/testutil/mocks.go`); cover success + auth/RBAC + error/validation branches.
2. `internal/storage` 6.7% → ≥80% — LocalBlobStore store/fetch/delete, error paths, the S3 adapter where unit-testable (or behind integration tag against MinIO if needed).
3. `internal/repository/postgres` 0% → ≥80% — integration tests against the dockertest Postgres for each repo (CRUD + the non-trivial queries: `ListByRepoNames`, `ListStale` retain-N CTE, search, blob GC, audit stream).
4. `internal/service` 55% → ≥80% — uncovered branches (validation, error mapping, scheduler logic).
5. `internal/formats/*` → ≥80% each — fill per-format gaps.

### B3. CI gate
- A `coverage` job runs unit + integration with `-coverprofile`, merges profiles, and a script enforces **per-package ≥80%** with the documented exclusion list. Build fails below floor.

**Done when:** every non-excluded Go package ≥80%; `make test` and `make test-integration` green; CI coverage gate passes.

---

## Track C — Frontend ≥80%

**Purpose:** Stand up frontend test infra from zero and reach ≥80% line coverage.

### C1. Infra
- Add dev deps: `vitest`, `@testing-library/react`, `@testing-library/user-event`, `@testing-library/jest-dom`, `jsdom`, `msw`, `@vitest/coverage-v8`.
- `vitest.config.ts` (or extend `vite.config.ts`): jsdom env, setup file (jest-dom + MSW server start/stop), `coverage` thresholds `{ lines: 80, ... }`.
- `package.json` scripts: `test`, `test:watch`, `test:coverage`.
- MSW handlers mirror the API the axios client calls.

### C2. Test order (risk-first)
1. `api/client.ts` — interceptors: 401 redirect, `/login` exclusion, FormData `Content-Type` deletion, token attach.
2. `store/authStore` — login/logout, token persistence, `isAdmin()`.
3. `components/*` (6) — render + key interactions.
4. `pages/*` (19) — render against MSW, assert key states (loading/empty/error/success) and primary interactions; prioritize Login, Repos, Browse, Users, Security, Admin.

### C3. CI gate
- `frontend` CI job runs `vitest run --coverage`; Vitest's own threshold fails the build below 80% lines.

**Done when:** `vitest run --coverage` ≥80% lines; CI frontend job passes; ESLint + `tsc` still clean.

---

## Out of scope (this effort)

- Playwright / E2E browser tests (possible later track).
- Refactoring application logic beyond what a test legitimately forces (surgical changes only).
- Raising the bar above 80% or adding mutation testing.

## Risks / watch-items

- **dockertest requires Docker** in CI runners and locally for integration tests — documented; unit suite stays Docker-free so the common path is unaffected.
- **Handler/service tests may surface real bugs** — fix them surgically as found; note any in `NEXT_RELEASE.md`.
- **80% is a floor, not a target** — prioritize meaningful assertions on critical paths (auth, RBAC, storage, format protocol correctness) over padding trivial lines to clear the bar.
- **rtk hook collapses `go test` stdout** — write coverage to a profile file and read it with `go tool cover`, not piped stdout.
