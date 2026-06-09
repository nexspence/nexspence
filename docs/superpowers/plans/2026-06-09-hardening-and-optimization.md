# Hardening & Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close concrete security, performance, correctness, and frontend gaps surfaced by a four-dimension codebase audit (2026-06-09), in independently-shippable phases.

**Architecture:** Each phase is one branch + one PR, ordered by value/risk. Phases 1–4 are small, high-value "quick wins"; Phase 5 collects bigger-ticket follow-ups that each need a little design. No phase depends on another — they can ship in any order, though the listed order is recommended (security first).

**Tech Stack:** Go 1.26 (Gin, pgx, goose migrations, golangci-lint v2.12.2), React 19 + TypeScript + Vite 8, PostgreSQL.

**Conventions (verified 2026-06-09):**
- Branch per phase, PR per phase — never commit to `main` directly. PR titles are Conventional Commits (the changelog is generated from them).
- `rtk` shell hook collapses `go test` stdout — rely on exit codes; read coverage from a `-coverprofile` via `go tool cover -func=FILE -o OUT` then Read OUT; use `rtk proxy go test …` for full failure output. Add `$(go env GOPATH)/bin` to PATH for tools.
- Migrations are goose-style (`-- +goose Up` / `-- +goose Down`), numbered `0NN_name.sql` under `internal/db/migrations/`. Next free number is **020**.
- Validation pattern in `internal/config`: standalone exported `ValidateOIDC(OIDCConfig) error` / `ValidateSAML(SAMLConfig) error`, unit-tested in `config_test.go`. Mirror this for new validation.
- After each phase: `go test ./...` (exit 0) and `make lint` (0 issues) must both pass; for frontend phases `cd frontend && npm run build && npm run test:coverage`.

---

## Phase 1 — Security Hardening

**Branch:** `harden/security-config`
**Covers:** S1 (JWT placeholder/weak-secret), S3 (Go 1.26.4 stdlib CVEs), S4 (LDAP InsecureSkipVerify justification + warning), S5 (anonymous-access startup warning).

### Task 1.1: Reject the example JWT secret and enforce minimum length (S1)

**Files:**
- Modify: `internal/config/config.go` (add `ValidateAuth`; call it in `Load` replacing the inline empty-check at lines 403–405)
- Test: `internal/config/config_test.go`

> Verify the field type name first: the struct holding `JWTSecret` (and `AnonymousEnabled` at config.go:92) — confirm it is `AuthConfig` via `grep -n "Auth .*Config\|type AuthConfig" internal/config/config.go`. Use the real type name below.

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func validAuth() AuthConfig {
	return AuthConfig{JWTSecret: "a-sufficiently-long-unique-secret-value-123"}
}

func TestValidateAuth_Empty_Fails(t *testing.T) {
	err := ValidateAuth(AuthConfig{JWTSecret: ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jwt_secret")
}

func TestValidateAuth_Placeholder_Fails(t *testing.T) {
	err := ValidateAuth(AuthConfig{JWTSecret: "CHANGE_ME_AT_LEAST_32_CHARACTERS_LONG"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "placeholder")
}

func TestValidateAuth_TooShort_Fails(t *testing.T) {
	err := ValidateAuth(AuthConfig{JWTSecret: "short"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "32")
}

func TestValidateAuth_Valid_Passes(t *testing.T) {
	require.NoError(t, ValidateAuth(validAuth()))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestValidateAuth ; echo exit=$?`
Expected: FAIL (undefined: `ValidateAuth`).

- [ ] **Step 3: Implement `ValidateAuth` and wire it into `Load`**

Add near `ValidateOIDC` in `internal/config/config.go`:

```go
// exampleJWTSecret is the placeholder shipped in config.yaml.example; booting
// with it means the HS256 signing key is publicly known (forgeable tokens).
const exampleJWTSecret = "CHANGE_ME_AT_LEAST_32_CHARACTERS_LONG"

// jwtSecretMinLen is the minimum acceptable HS256 signing-secret length.
const jwtSecretMinLen = 32

// ValidateAuth rejects an empty, placeholder, or too-short JWT signing secret.
func ValidateAuth(a AuthConfig) error {
	if a.JWTSecret == "" {
		return fmt.Errorf("auth.jwt_secret is required (or set NEXSPENCE_AUTH_JWT_SECRET)")
	}
	if a.JWTSecret == exampleJWTSecret {
		return fmt.Errorf("auth.jwt_secret is set to the example placeholder; set a unique secret of at least %d characters", jwtSecretMinLen)
	}
	if len(a.JWTSecret) < jwtSecretMinLen {
		return fmt.Errorf("auth.jwt_secret must be at least %d characters", jwtSecretMinLen)
	}
	return nil
}
```

Then in `Load`, replace the existing block at lines 403–405:

```go
	if cfg.Auth.JWTSecret == "" {
		return nil, fmt.Errorf("auth.jwt_secret is required (or set NEXSPENCE_AUTH_JWT_SECRET)")
	}
```

with:

```go
	if err := ValidateAuth(cfg.Auth); err != nil {
		return nil, err
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestValidateAuth ; echo exit=$?`
Expected: PASS (exit=0).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): reject example/weak auth.jwt_secret at startup"
```

### Task 1.2: Bump Go toolchain to 1.26.4 to clear called stdlib CVEs (S3)

**Files:**
- Modify: `go.mod` (line 3: `go 1.26.3` → `go 1.26.4`)
- Modify: `.github/workflows/ci.yml` (every `go-version: '1.26.3'` → `'1.26.4'`), `.github/workflows/release.yml`, `.github/workflows/tag.yml` (any pinned `1.26.3`)
- Modify: `Dockerfile` (base `golang:1.26.3*` → `1.26.4*` if pinned)

> `govulncheck ./...` flagged GO-2026-5037 (`crypto/x509`, reached via LDAP StartTLS + S3 test-connection) and GO-2026-5039 (`net/textproto`) as **called**; both are fixed in go1.26.4.

- [ ] **Step 1: Find every pinned `1.26.3`**

Run: `grep -rn "1.26.3" go.mod Dockerfile* .github/ ; echo done`

- [ ] **Step 2: Replace each with `1.26.4`** (edit each file the grep reported).

- [ ] **Step 3: Verify the toolchain builds and the CVEs clear**

```bash
go install golang.org/x/vuln/cmd/govulncheck@latest
go build ./... ; echo build=$?
govulncheck ./... > /tmp/vuln.txt 2>&1 ; grep -c "GO-2026-5037\|GO-2026-5039" /tmp/vuln.txt
```
Expected: build=0; the grep count is 0 (no longer reported as called). If the local Go SDK is still 1.26.3, note that the bump takes effect in CI; build must still pass locally.

- [ ] **Step 4: Run the suite**

Run: `go test ./... ; echo exit=$?`
Expected: exit=0.

- [ ] **Step 5: Commit**

```bash
git add go.mod .github/ Dockerfile*
git commit -m "build: bump Go toolchain to 1.26.4 (clears called crypto/x509 + net/textproto CVEs)"
```

### Task 1.3: Justify the LDAP InsecureSkipVerify suppression + warn at startup (S4)

**Files:**
- Modify: `internal/auth/ldap.go:53` (add a justification to the `//nolint:gosec`)
- Modify: `cmd/server/main.go` (warn when LDAP is enabled with `insecure_skip_verify: true`)
- Modify: `config.yaml` (working dev config, gitignored) — flip `:84` to `false` for hygiene

> The shipped `config.yaml.example:84` is already `false` and there is no `SetDefault` for it (Go zero-value = `false`), so the **code default is safe**. This task is about an honest suppression comment + an operator warning, not a default change.

- [ ] **Step 1: Add the justification comment**

In `internal/auth/ldap.go`, change line 53:

```go
		InsecureSkipVerify: s.cfg.InsecureSkipVerify, //nolint:gosec // operator opt-in for self-signed dev LDAPS; defaults false, a startup warning is emitted when true
```

- [ ] **Step 2: Emit a startup warning**

In `cmd/server/main.go`, where LDAP config is read / `ldapSvc.TestConnection` is called, add (using the existing zap logger variable — confirm its name, e.g. `log`):

```go
	if cfg.LDAP.Enabled && cfg.LDAP.InsecureSkipVerify {
		log.Warn("LDAP insecure_skip_verify is enabled — TLS certificate validation is OFF; use only with self-signed certs in development")
	}
```

> Confirm the config path (`cfg.LDAP.Enabled` / `cfg.LDAP.InsecureSkipVerify`) and logger name by reading the surrounding `main.go` LDAP block first.

- [ ] **Step 3: Set the dev config to safe default**

In `config.yaml` line 84: `insecure_skip_verify: true` → `false`.

- [ ] **Step 4: Build + lint**

```bash
go build ./... ; echo build=$?
make lint ; echo lint=$?
```
Expected: both 0.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/ldap.go cmd/server/main.go config.yaml
git commit -m "security(ldap): justify InsecureSkipVerify suppression + warn at startup when enabled"
```

### Task 1.4: Warn at startup when anonymous access is enabled (S5)

**Files:**
- Modify: `cmd/server/main.go` (warn when `auth.anonymous_enabled` is true)

> The default is `true` (`config.go:325` `v.SetDefault("auth.anonymous_enabled", true)`). **Flipping the default is a breaking change** for existing deployments that rely on anonymous reads — so this task only adds a visible warning; flipping the default is deferred to a major release (note it in the v2.0.0 roadmap, see [[project-v2-roadmap]]).

- [ ] **Step 1: Add the warning** in `cmd/server/main.go` after config load:

```go
	if cfg.Auth.AnonymousEnabled {
		log.Warn("auth.anonymous_enabled is true — unauthenticated artifact access is allowed; set false to require authentication")
	}
```

- [ ] **Step 2: Build**

Run: `go build ./... ; echo build=$?` — expected 0.

- [ ] **Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "security(config): warn at startup when anonymous access is enabled"
```

### Phase 1 close-out

- [ ] `go test ./... ; echo $?` = 0 and `make lint ; echo $?` = 0.
- [ ] Open PR: `gh pr create --title "security: startup secret/anonymous/LDAP hardening + Go 1.26.4 CVE bump" --body "<summarize S1/S3/S4/S5 with evidence>"`.

---

## Phase 2 — Bug Fixes & Dead Code

**Branch:** `fix/bugs-and-deadcode`
**Covers:** C1 (swallowed LDAP rebind error — real bug), C2 (rate-limiter: wire + fix Retry-After), C3 (dead vars), C5 (docs drift).

### Task 2.1: Record the swallowed LDAP rebind error (C1)

**Files:**
- Modify: `internal/auth/ldap.go:144`
- Test: `internal/auth/ldap_test.go` (create if absent) — see note

> `internal/auth/ldap.go:140-145`: on group-membership fetch, `serviceBind` failure is discarded via `_ = fmt.Errorf(...)` — no log, no signal. `LDAPUser` already has a `GroupSearchErr string` field (set on search failure at line 154). The fix: record the rebind failure into the same field so it surfaces, instead of silently degrading RBAC role mapping.

- [ ] **Step 1: Apply the fix**

Replace `internal/auth/ldap.go:142-145`:

```go
		if err := s.serviceBind(conn); err != nil {
			// Non-fatal: log and try to search under user credentials.
			_ = fmt.Errorf("ldap group search: service rebind failed (searching as user): %w", err)
		}
```

with:

```go
		if err := s.serviceBind(conn); err != nil {
			// Non-fatal: record and fall through to search under user credentials.
			lu.GroupSearchErr = fmt.Sprintf("service rebind failed (searching as user): %v", err)
		}
```

> This requires `"fmt"` (already imported). If a later successful search overwrites `GroupSearchErr`, that is correct (the rebind warning is moot once the search succeeds) — verify the search branch at line 152-155 only sets `GroupSearchErr` on `gErr != nil`, so a clean search leaves the rebind note; that is acceptable. If you prefer the rebind note not to persist on success, clear it in the success branch.

- [ ] **Step 2: Build + lint to confirm the dead `fmt.Errorf` is gone**

```bash
go build ./internal/auth/ ; echo build=$?
make lint ; echo lint=$?
```
Expected: both 0 (no more `Error return value of fmt.Errorf is not checked`-style noise; the value is now used).

- [ ] **Step 3 (optional test):** If `internal/auth/ldap_test.go` has a seam to exercise group search without a live server, add an assertion that `GroupSearchErr` is populated on rebind failure. If no such seam exists (LDAP is live-server-bound), skip — note in the commit that this path is covered by the C4 auth-hardening task (Phase 5) only if a fake LDAP is introduced.

- [ ] **Step 4: Commit**

```bash
git add internal/auth/ldap.go
git commit -m "fix(ldap): record service-rebind failure in GroupSearchErr instead of discarding it"
```

### Task 2.2: Wire the rate limiter + fix the Retry-After header (C2)

**Files:**
- Modify: `internal/api/ratelimit_middleware.go:89` (Retry-After bug)
- Modify: `internal/api/router.go` (wire `RateLimitMiddleware` under config gate)
- Modify: `internal/config/config.go` (+ `config.yaml.example`) — add `auth.rate_limit` config
- Test: `internal/api/ratelimit_middleware_test.go` (create)

> `RateLimitMiddleware` is fully implemented but **never wired** (`router.go` only `r.Use`s recovery/requestLogger/cors/metrics/audit at lines 257-261). It also has a bug: line 89 `c.Header("Retry-After", http.StatusText(retryAfter))` passes an int to `http.StatusText` (expects an HTTP status code) → emits wrong/empty text. **Decision: wire it, gated by config (disabled by default so behavior is unchanged unless opted in).**

- [ ] **Step 1: Write the failing test** in `internal/api/ratelimit_middleware_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRateLimitMiddleware_BlocksOverBurst(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// rate very low, burst 2 → 3rd immediate request is throttled.
	r.Use(RateLimitMiddleware(0.0001, 2))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	codes := []int{}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		r.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	assert.Equal(t, http.StatusOK, codes[0])
	assert.Equal(t, http.StatusOK, codes[1])
	assert.Equal(t, http.StatusTooManyRequests, codes[2])
}

func TestRateLimitMiddleware_RetryAfterIsNumeric(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimitMiddleware(0.0001, 1))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	var throttled *httptest.ResponseRecorder
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "10.0.0.2:1234"
		r.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			throttled = rec
		}
	}
	if assert.NotNil(t, throttled) {
		// Retry-After must be a number of seconds, not arbitrary status text.
		assert.Regexp(t, `^\d+$`, throttled.Header().Get("Retry-After"))
	}
}
```

- [ ] **Step 2: Run to verify the Retry-After test fails**

Run: `go test ./internal/api/ -run TestRateLimitMiddleware ; echo exit=$?`
Expected: `BlocksOverBurst` passes, `RetryAfterIsNumeric` FAILS (header is non-numeric text).

- [ ] **Step 3: Fix the Retry-After header**

In `internal/api/ratelimit_middleware.go`, add `"strconv"` to imports and change line 89:

```go
			c.Header("Retry-After", http.StatusText(retryAfter))
```
to:
```go
			c.Header("Retry-After", strconv.Itoa(retryAfter))
```

- [ ] **Step 4: Run to verify both pass**

Run: `go test ./internal/api/ -run TestRateLimitMiddleware ; echo exit=$?` — expected exit=0.

- [ ] **Step 5: Add config + wire it (gated, default off)**

In `internal/config/config.go`, add to the auth config struct:

```go
	RateLimitEnabled bool    `mapstructure:"rate_limit_enabled"`
	RateLimitRPS     float64 `mapstructure:"rate_limit_rps"`
	RateLimitBurst   float64 `mapstructure:"rate_limit_burst"`
```

and defaults near the other `v.SetDefault` calls:

```go
	v.SetDefault("auth.rate_limit_enabled", false)
	v.SetDefault("auth.rate_limit_rps", 50.0)
	v.SetDefault("auth.rate_limit_burst", 100.0)
```

In `internal/api/router.go`, after line 261 (`r.Use(AuditMiddleware(...))`), add:

```go
	if cfg.Auth.RateLimitEnabled {
		r.Use(RateLimitMiddleware(cfg.Auth.RateLimitRPS, cfg.Auth.RateLimitBurst))
	}
```

> Confirm the `cfg`/config variable is in scope in `NewRouter`. If `NewRouter` does not receive the full `*config.Config`, pass the three values through its existing deps struct instead — read the `NewRouter` signature first and adapt.

Document the new keys in `config.yaml.example` under the `auth:` block:

```yaml
  rate_limit_enabled: false   # per-user/IP token-bucket throttling
  rate_limit_rps: 50          # sustained requests/sec
  rate_limit_burst: 100       # burst capacity
```

- [ ] **Step 6: Full build + lint + suite**

```bash
go test ./internal/api/ ./internal/config/ ; echo api=$?
make lint ; echo lint=$?
```
Expected: both 0.

- [ ] **Step 7: Commit**

```bash
git add internal/api/ratelimit_middleware.go internal/api/ratelimit_middleware_test.go internal/api/router.go internal/config/config.go config.yaml.example
git commit -m "feat(api): wire opt-in rate limiter + fix numeric Retry-After header"
```

### Task 2.3: Remove dead computed variables (C3)

**Files:**
- Modify: `internal/formats/npm/handler.go:225-233`
- Modify: `internal/formats/gomod/handler.go:141,152-153`

- [ ] **Step 1: npm — delete the dead `scope` block**

Remove lines 225-233 (the `scope` computation ending in `_ = scope`):

```go
	// Determine scope from scoped package name (@scope/name)
	scope := ""
	if strings.HasPrefix(pkgName, "@") {
		parts := strings.SplitN(pkgName, "/", 2)
		if len(parts) == 2 {
			scope = parts[0]
		}
	}
	_ = scope
```

> If `strings` becomes unused in the file after deletion, remove its import. Run `go build ./internal/formats/npm/` to check.

- [ ] **Step 2: gomod — drop the redundant blank assignments**

In `internal/formats/gomod/handler.go`, `serveFile` is `func (h *Handler) serveFile(c *gin.Context, repoName, filePath, modulePath, version string)`. Delete lines 152-153 (`_ = modulePath` / `_ = version`). Then build+lint:

```bash
go build ./internal/formats/gomod/ ; echo build=$?
make lint ; echo lint=$?
```

> If lint now reports `modulePath`/`version` as unused parameters, rename them to `_` in the `serveFile` signature: `func (h *Handler) serveFile(c *gin.Context, repoName, filePath, _, _ string)` and update the call site accordingly. Re-run lint to confirm 0.

- [ ] **Step 3: Run suite**

Run: `go test ./internal/formats/... ; echo exit=$?` — expected 0.

- [ ] **Step 4: Commit**

```bash
git add internal/formats/npm/handler.go internal/formats/gomod/handler.go
git commit -m "refactor(formats): remove dead computed variables in npm/gomod handlers"
```

### Task 2.4: Fix stale Track-B status in root CLAUDE.md (C5)

**Files:**
- Modify: `CLAUDE.md` (the "Current Phase" blurb, ~line 157)

- [ ] **Step 1: Update the line** — change the stale `Track B = backend ≥80% coverage + dockertest for postgres layer (NEXT)` to reflect reality:

```markdown
**Track B — backend ≥80% coverage COMPLETE (2026-06-04)**: postgres 87% (dockertest), handlers 81%, service 80%, storage ≥80% (MinIO integration), formats all ≥80%; CI `coverage`/`integration` gates enforce it. **Track C — frontend Vitest+RTL+MSW COMPLETE** (95% lines, 449 tests). The coverage initiative is fully shipped; `internal/api` and `internal/storage` low default-build numbers are by design (integration-tagged / excluded — see `.github/workflows/ci.yml`).
```

> Read the exact surrounding text at CLAUDE.md:155-160 first and splice this in surgically without disturbing adjacent sentences.

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: mark Track B/C coverage initiative complete in CLAUDE.md"
```

### Phase 2 close-out

- [ ] `go test ./... ; echo $?` = 0 and `make lint ; echo $?` = 0.
- [ ] Open PR: `gh pr create --title "fix: LDAP error logging, rate-limiter wiring, dead-code cleanup" --body "<summarize C1/C2/C3/C5>"`.

---

## Phase 3 — Backend Performance

**Branch:** `perf/db-and-regex`
**Covers:** P2 (`assets.blob_key` index), P4 (`audit_events.username` index), P5 (precompile routing-rule regexes).

### Task 3.1: Add the missing `assets.blob_key` index (P2)

**Files:**
- Create: `internal/db/migrations/020_perf_indexes.sql`

> `assetRepo.CountByBlobKey` (`asset_repo.go:547`, GC ref-count on every artifact delete) and `UpdateBlobStoreForBlobKey` (blob-store migration) filter `WHERE blob_key = $1` with no index (existing `assets` indexes: component, repo_path, sha256, last_downloaded — `001_initial.sql:102-105`). On a large `assets` table this is a sequential scan per delete/migration. This same migration also adds the audit username index (Task 3.2) so there is a single new migration file.

- [ ] **Step 1: Create the migration**

`internal/db/migrations/020_perf_indexes.sql`:

```sql
-- +goose Up
CREATE INDEX IF NOT EXISTS idx_assets_blob_key ON assets (blob_key);
CREATE INDEX IF NOT EXISTS idx_audit_username ON audit_events (username);

-- +goose Down
DROP INDEX IF EXISTS idx_assets_blob_key;
DROP INDEX IF EXISTS idx_audit_username;
```

> `audit_events` is partitioned (monthly, per the audit phase). `CREATE INDEX` on a partitioned parent creates it on all partitions; if the goose runner errors on the partitioned parent, switch to `CREATE INDEX ... ON ONLY audit_events` plus per-partition creation, or document that the index is declared on the parent template. Verify by applying migrations against a scratch DB in Step 2.

- [ ] **Step 2: Apply migrations against a scratch DB and confirm the indexes exist**

```bash
# Uses the integration dockertest harness path or a local PG; example with a throwaway container:
go run ./cmd/server migrate 2>&1 | tail -5   # against a dev DB with NEXSPENCE_DATABASE_DSN set
```
Then verify (psql): `\d assets` shows `idx_assets_blob_key`; `\d audit_events` shows `idx_audit_username`. Expected: both present, migration applies cleanly.

- [ ] **Step 3: Run the postgres integration tests** (they apply the real migrations):

```bash
go test -tags=integration -count=1 ./internal/repository/postgres/... ; echo exit=$?
```
Expected: exit=0 (migration 020 applies in the harness without error).

- [ ] **Step 4: Commit**

```bash
git add internal/db/migrations/020_perf_indexes.sql
git commit -m "perf(db): index assets.blob_key (GC ref-count) and audit_events.username (filter)"
```

### Task 3.2: (covered by the same migration — P4 audit username index)

The `idx_audit_username` index is created in Task 3.1's `020_perf_indexes.sql`. No separate task.

> Note (no code change): `audit_repo.List` runs `SELECT COUNT(*)` per page (`audit_repo.go:97`) to power the total-aware "Showing X-Y of N" UI pagination. **Do not remove it** — the UI contract depends on the total. The username index above is the safe win; revisiting the count is out of scope.

### Task 3.3: Precompile routing-rule matcher regexes (P5)

**Files:**
- Modify: `internal/service/routing_rule_service.go` (cache compiled regexes in `matchesAny`)
- Test: `internal/service/routing_rule_service_test.go` (add/extend)

> `matchesAny` (`routing_rule_service.go:78`) calls `regexp.Compile(m)` for every matcher on every `Allow()` call; `Allow` runs per GET on group repos (`group/handler.go:75`). Compilation dominates matching. Cache compiled patterns by matcher string in a package-level `sync.Map` — keeps the `Allow(rule, path)` signature intact and is safe for concurrent use.

- [ ] **Step 1: Write the failing test** (asserts behavior is unchanged + the cache helper exists). Add to `internal/service/routing_rule_service_test.go`:

```go
func TestMatchesAny_CachedCompile_Behaviour(t *testing.T) {
	matchers := []string{`^/v2/library/.*`, `\.tgz$`}
	// matches
	assert.True(t, exportedMatchesAny(matchers, "/v2/library/alpine/manifests/latest"))
	assert.True(t, exportedMatchesAny(matchers, "/charts/app-1.2.3.tgz"))
	// no match
	assert.False(t, exportedMatchesAny(matchers, "/maven2/com/foo/1.0/foo.jar"))
	// invalid regex is skipped, not fatal
	assert.False(t, exportedMatchesAny([]string{`(`}, "anything"))
}
```

> `matchesAny` is unexported. Either (a) add a tiny exported test shim `func ExportedMatchesAny(...)` in a `export_test.go`, or (b) test through the public `Allow(rule, path)` with crafted rules. Prefer (b) to avoid production API surface — rewrite the test to build `*domain.RoutingRule{Mode:"ALLOW", Matchers: ...}` and assert `Allow(rule, path)`. Use whichever the existing test file already does.

- [ ] **Step 2: Run to confirm current behavior passes** (this is a refactor — behavior must not change):

Run: `go test ./internal/service/ -run RoutingRule ; echo exit=$?` — expected exit=0 with the existing implementation.

- [ ] **Step 3: Introduce the compile cache**

In `internal/service/routing_rule_service.go`, add a package-level cache and a helper, and use it in both `matchesAny` and `validateMatchers`:

```go
// compiledMatchers caches compiled matcher regexes keyed by pattern string.
// Routing-rule Allow() runs per request on group repos; recompiling each call
// dominates the actual match, so cache the compiled form.
var compiledMatchers sync.Map // string -> *regexp.Regexp

func compileMatcher(pattern string) (*regexp.Regexp, error) {
	if v, ok := compiledMatchers.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	compiledMatchers.Store(pattern, re)
	return re, nil
}
```

Update `matchesAny`:

```go
func matchesAny(matchers []string, path string) bool {
	for _, m := range matchers {
		re, err := compileMatcher(m)
		if err != nil {
			continue
		}
		if re.MatchString(path) {
			return true
		}
	}
	return false
}
```

Add `"sync"` to the imports.

- [ ] **Step 4: Run tests + lint**

```bash
go test ./internal/service/ -run RoutingRule ; echo svc=$?
make lint ; echo lint=$?
```
Expected: both 0 (behavior unchanged, now cached).

- [ ] **Step 5: Commit**

```bash
git add internal/service/routing_rule_service.go internal/service/routing_rule_service_test.go
git commit -m "perf(routing): cache compiled matcher regexes (per-request group-repo eval)"
```

### Phase 3 close-out

- [ ] `go test ./... ; echo $?` = 0; `go test -tags=integration ./internal/repository/postgres/... ; echo $?` = 0; `make lint ; echo $?` = 0.
- [ ] Open PR: `gh pr create --title "perf: index hot blob_key/username columns + cache routing regexes" --body "<summarize P2/P4/P5>"`.

---

## Phase 4 — Frontend Quick Wins

**Branch:** `fe/quick-wins`
**Covers:** F1 (oversized logo PNGs), F3 (SearchPage unpaginated render), F4 (render-blocking font @import), F5 (modal a11y).

### Task 4.1: Downscale the logo assets (F1)

**Files:**
- Replace: `frontend/src/assets/logo.png` (8192×2048, 848 KB) and `frontend/src/assets/mini_logo.png` (4096×4096, 1.64 MB)
- Delete: any `frontend/src/assets/*.pngZone.Identifier` / `*.png:Zone.Identifier` stray files
- Also: `frontend/public/favicon.png` is a copy of mini_logo — re-derive from the downscaled mini

> CSS renders `logo` at ~180px (`_brandLogo`) and `mini_logo` at 30×30 (`_brandLogoMini`). Shipping a 4096² PNG for a 30px icon wastes ~2.4 MB. Target: ≤2× display size, WebP or optimized PNG.

- [ ] **Step 1: List the actual asset files and any Windows artifacts**

```bash
ls -la frontend/src/assets/ ; ls -la frontend/public/ | grep -i favicon
find frontend -name "*Zone.Identifier*" -o -name "*:Zone.Identifier"
```

- [ ] **Step 2: Downscale** (requires `sips` on macOS or `magick`/ImageMagick). Produce ≤512px-wide `logo.png` and ≤64px `mini_logo.png` (2× the 30px draw), keeping aspect ratio:

```bash
# macOS sips (in place on copies):
sips -Z 512 frontend/src/assets/logo.png
sips -Z 64  frontend/src/assets/mini_logo.png
cp frontend/src/assets/mini_logo.png frontend/public/favicon.png
```
> If neither `sips` nor ImageMagick is available, STOP and ask the user to provide downscaled assets, or skip F1 to a follow-up. Do not commit a broken/empty image.

- [ ] **Step 3: Remove stray Zone.Identifier files** (if Step 1 found any):

```bash
find frontend -name "*Zone.Identifier*" -delete
```

- [ ] **Step 4: Rebuild and confirm the asset sizes dropped**

```bash
cd frontend && npm run build > /tmp/fe_build.txt 2>&1 ; echo build=$? ; ls -la dist/assets/ | grep -iE "logo|mini"
```
Expected: build=0; the emitted logo/mini chunks are now KB-scale, not MB.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/assets/logo.png frontend/src/assets/mini_logo.png frontend/public/favicon.png
git rm --cached $(find frontend -name "*Zone.Identifier*") 2>/dev/null || true
git commit -m "perf(frontend): downscale logo assets (~2.4MB saved) and drop Zone.Identifier strays"
```

### Task 4.2: Paginate / cap SearchPage results (F3)

**Files:**
- Modify: `frontend/src/pages/SearchPage.tsx` (around the render at lines 328/363/379/459)
- Test: `frontend/src/pages/__tests__/SearchPage.test.tsx` (extend existing — Track C added page tests)

> BrowsePage and AuditPage paginate; SearchPage renders every grouped component + asset + digest with no cap. A broad `*` query on a large repo floods the DOM. Add a client-side cap with a "show more" affordance (simplest correct fix; no new dependency).

- [ ] **Step 1: Write the failing test** — render SearchPage with a mocked result set larger than the cap and assert only the cap is shown plus a "show more" control. Mirror the existing SearchPage test's MSW/fixture setup:

```tsx
it("caps the number of rendered results and reveals more on demand", async () => {
  // seed the search handler with > PAGE_SIZE components (see test fixtures)
  renderWithProviders(<SearchPage />);
  // ...perform a search that returns e.g. 120 components...
  // assert at most PAGE_SIZE (e.g. 50) result rows initially
  const rows = await screen.findAllByTestId("search-result-row");
  expect(rows.length).toBeLessThanOrEqual(50);
  // a "show more" button exists when there are more
  expect(screen.getByRole("button", { name: /show more/i })).toBeInTheDocument();
});
```

> Read `frontend/src/pages/__tests__/SearchPage.test.tsx` (or the Track C SearchPage test) first for the real render helper, MSW handler, and fixture shape; adapt selectors. Add a `data-testid="search-result-row"` to the row element if one is needed for the assertion.

- [ ] **Step 2: Run to verify it fails**

Run: `cd frontend && npx vitest run src/pages/__tests__/SearchPage.test.tsx ; echo exit=$?`
Expected: FAIL (all rows rendered, no cap / no show-more).

- [ ] **Step 3: Implement the cap**

In `SearchPage.tsx`, add a `const PAGE_SIZE = 50` and a `visibleCount` state initialized to `PAGE_SIZE`, reset on each new search. Slice the grouped/flattened result list to `visibleCount` before mapping (lines ~363/379), and render a "Show more" button when `total > visibleCount` that does `setVisibleCount(v => v + PAGE_SIZE)`. Keep the existing `{items.length} results` total label accurate (show full count, render only the slice).

- [ ] **Step 4: Run to verify it passes**

Run: `cd frontend && npx vitest run src/pages/__tests__/SearchPage.test.tsx ; echo exit=$?` — expected 0.

- [ ] **Step 5: Typecheck + full frontend suite**

```bash
cd frontend && npm run typecheck:test && npm run test:coverage ; echo exit=$?
```
Expected: 0 and coverage gate still green.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/SearchPage.tsx frontend/src/pages/__tests__/SearchPage.test.tsx
git commit -m "perf(frontend): cap SearchPage rendered results with show-more"
```

### Task 4.3: De-block the Google Fonts import (F4)

**Files:**
- Modify: `frontend/src/components/holo/holo.css:11` (remove the `@import`)
- Modify: `frontend/index.html` (add preconnect + stylesheet link in `<head>`)

> CSS `@import url('https://fonts.googleapis.com/...Geist...')` blocks render with a serial round-trip. Moving it to `<link rel="preconnect">` + `<link rel="stylesheet">` in `index.html` unblocks paint (`display=swap` is already set).

- [ ] **Step 1: Read the exact `@import` line** at `holo.css:11` to capture the full font URL.

- [ ] **Step 2: Remove the `@import`** from `holo.css` (delete line 11).

- [ ] **Step 3: Add to `frontend/index.html` `<head>`** (using the URL captured in Step 1):

```html
    <link rel="preconnect" href="https://fonts.googleapis.com" />
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
    <link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Geist:wght@100..900&display=swap" />
```

> Use the exact `family=...` querystring from the removed `@import`, not the example above.

- [ ] **Step 4: Build + visually confirm the font still loads**

```bash
cd frontend && npm run build ; echo build=$?
```
Expected: build=0. (Font rendering is visual; the move is behavior-preserving.)

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/holo/holo.css frontend/index.html
git commit -m "perf(frontend): load Geist font via <link> preconnect instead of blocking @import"
```

### Task 4.4: Make modals announce as dialogs (F5)

**Files:**
- Modify: the shared modal container component (find it: `grep -rl "holo-modal\|holo-overlay" frontend/src`)
- Test: extend a page test that opens a modal

> No `.tsx` uses `role="dialog"`/`aria-modal`. Add these to the shared `.holo-modal` container so screen readers announce modals, plus `aria-labelledby` pointing at the modal title. Focus-trap is a larger change — out of scope here; this task does the cheap, high-value ARIA wiring. (Phase 47 a11y work — skip link, reduced-motion, focus-visible — is already present.)

- [ ] **Step 1: Locate the shared modal** — `grep -rn "holo-modal" frontend/src/components` to find the reusable `<Modal>`/overlay component (likely `frontend/src/components/holo/`). Read it.

- [ ] **Step 2: Add ARIA attributes** to the modal container element:

```tsx
<div
  className="holo-modal"
  role="dialog"
  aria-modal="true"
  aria-labelledby={titleId}
  /* ...existing props... */
>
```

Give the modal title element `id={titleId}` (generate a stable id, e.g. via `useId()`). If the shared component doesn't own the title, accept an optional `titleId`/`ariaLabel` prop and fall back to `aria-label` when no title id is provided.

- [ ] **Step 3: Test** — in an existing page test that opens a modal (e.g. Repositories create modal), assert `screen.getByRole("dialog")` is present after opening. Add to that test file:

```tsx
expect(await screen.findByRole("dialog")).toBeInTheDocument();
```

- [ ] **Step 4: Typecheck + suite**

```bash
cd frontend && npm run typecheck:test && npm run test:coverage ; echo exit=$?
```
Expected: 0.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/holo/ frontend/src/pages/__tests__/
git commit -m "a11y(frontend): mark modals as role=dialog aria-modal with labelled title"
```

### Phase 4 close-out

- [ ] `cd frontend && npm run build && npm run typecheck:test && npm run test:coverage` all green.
- [ ] Open PR: `gh pr create --title "perf+a11y(frontend): asset downscale, search cap, font preconnect, modal dialog roles" --body "<summarize F1/F3/F4/F5>"`.

---

## Phase 5 — Bigger-Ticket Follow-ups

These are larger and each benefits from a short design pass before coding. Listed with concrete approach + effort so they can be scheduled individually. **Each is its own branch + PR.** Recommend running `superpowers:brainstorming` on items marked ⚙️ before implementation.

### Task 5.1: Per-user privilege cache on the artifact hot path (P1) — ❌ DROPPED (2026-06-09)

> **DROPPED after brainstorming.** The `GetUserPrivilegesWithSelectors` query is already index-optimal: `user_roles` PK `(user_id, role_id)` serves the `WHERE user_id = $1`, `role_privileges` PK `(role_id, privilege_id)` serves the JOIN, and `privileges`/`content_selectors` are joined by PK. It is a series of index scans returning a handful of rows (sub-millisecond) — **not** a heavy JOIN; the audit's "HIGH at scale" was speculative. Caching authorization decisions for 30s carries a real security cost (privilege revocation would lag by ≤30s) for a negligible gain on an already-fast query. Revisit ONLY if profiling on real load shows privilege round-trips are a hot path — and then use **explicit invalidation** (immediate revocation), not TTL. (The `HasAnyAnonymousDocker` TTL cache is a different case: unauthenticated, aggressive `/v2/` polling of one global flag.)

`RBACMiddleware` → `CanAccessRepo` → `GetUserPrivilegesWithSelectors` runs a 4-table JOIN **per artifact request** (`rbac_repo.go:19`, plus a `repoRepo.Get` per request). Mirror the existing `HasAnyAnonymousDocker` 30s atomic cache (`auth.go:273`): a short-TTL per-user privilege cache keyed by userID, invalidated on role/privilege/content-selector change. Design decision: TTL vs explicit invalidation (TTL is simpler and matches the existing pattern). **Approach:** add a `privilegeCache` (sync.Map of userID → {privs, expiry}) in the RBAC service; on change-write paths (role/privilege/selector mutate), bump a global generation counter or call `Invalidate(userID)`. Verify with a benchmark or a request-count assertion against a mock repo. Highest scale impact of all findings.

### Task 5.2: Batch the search N+1 (P3) — effort M

`components.go:241` loops up to 50 components issuing one `ListByComponentID` each. Add `AssetRepo.ListByComponentIDs(ctx, ids []string) (map[string][]Asset, error)` using `WHERE component_id = ANY($1)`, group in memory, and have the search handler call it once. Add a postgres integration test for the new repo method and a handler test asserting a single asset query. Backward-compatible (keep the singular method).

### Task 5.3: Decouple replication credential encryption from the JWT secret (S2) — effort M ⚙️

`replication_service.go:110` derives the AES-256-GCM key as `sha256(jwt_secret)`, so (a) a weak JWT secret weakens credential encryption and (b) rotating the JWT secret silently corrupts all stored replication credentials. **Design decision needed:** introduce a dedicated `auth.encryption_key` (env-sourced, base64, validated 32 bytes like the OIDC cookie key) and a one-time re-encryption/migration path for existing stored credentials. Until the migration story is decided, do NOT change the derivation (it would brick existing creds). Brainstorm the rotation/migration approach first.

### Task 5.4: Harden `internal/auth` test coverage (C4) — effort M

`internal/auth` is 47% (CI-excluded). `GenerateTokenWithMethod` (`auth.go:63`, used by OIDC/SAML/LDAP login) is 0%; `ValidateToken` error branches (expired / wrong-alg / tampered) and `HashPassword`/bcrypt round-trip are pure and trivially unit-testable. Add table tests in `internal/auth/auth_test.go`. Security-critical code; no production change expected. Optionally introduce a fake LDAP seam to also cover the C1 GroupSearchErr path.

### Task 5.5: Lighten the MonitoringPage chart bundle (F2) — effort M ⚙️

`MonitoringPage.tsx` pulls all of recharts → a 352 KB / 102 KB-gzip lazy chunk (35% of total JS) for an 8-panel dashboard, plus the `es-toolkit/compat` ESM interop shim in `vite.config.ts`. **Design decision:** evaluate replacing recharts with a lighter lib (uPlot, visx primitives, or a small sparkline lib) vs. verifying recharts tree-shaking. It's lazy-loaded (off critical path), so this is optimization, not a blocker — schedule only if the dashboard bundle matters. Brainstorm the chart-lib choice first.

### Task 5.6: Debounce download-counter writes (P6) — effort M

Every artifact fetch fires a detached goroutine doing `BEGIN` + 2 `UPDATE`s + `COMMIT` (`asset_repo.go:340`). Off the response path but high-QPS downloads churn WAL and contend row locks on hot artifacts. Aggregate increments in memory and flush periodically (or collapse to a single `UPDATE ... FROM`). Needs care around restart durability (lost in-flight counts on crash) — acceptable for download stats. Low-to-medium impact.

### Task 5.7: Stream conda/terraform uploads (P7) — effort M

`conda/handler.go:93` and `terraform/handler.go:122,292` buffer the full upload via `io.ReadAll` instead of streaming through `base.StoreArtifact` (like maven/npm/raw). Artifacts are usually small so impact is limited, but it breaks the streaming invariant and risks memory spikes on large uploads. Route these through the streaming store path. Low impact.

### Task 5.8: Split `backup_service.go` archive functions (CQ-debt) — effort L

`backup_service.go` (732 lines) has three gocyclo-suppressed functions (`Export`/`Import`/`Restore` at lines 60/320/497). Extracting the per-entity tar loops into helpers would improve testability and let the suppressions be removed. Pure refactor — guard with the existing backup tests before/after. Lowest priority.

---

## Self-Review

**Spec coverage** (audit finding → task):
- S1→1.1, S3→1.2, S4→1.3, S5→1.4 ✓
- C1→2.1, C2→2.2, C3→2.3, C5→2.4 ✓
- P2→3.1, P4→3.1 (same migration), P5→3.3 ✓
- F1→4.1, F3→4.2, F4→4.3, F5→4.4 ✓
- P1→5.1, P3→5.2, S2→5.3, C4→5.4, F2→5.5, P6→5.6, P7→5.7, backup-split→5.8 ✓
- Not separately tasked (intentional): audit COUNT(*) removal (UI contract — explicitly out of scope, noted in 3.2); gosec suppressions other than LDAP (audited sound); frontend memoization F-finding #5 (folded into 5.5/case-by-case, not worth a standalone task); icon-button aria-label spot-check (folded into F5/4.4 scope as a follow-up sweep).

**Type/name consistency:** `ValidateAuth(AuthConfig)` mirrors `ValidateOIDC(OIDCConfig)`; `compileMatcher`/`compiledMatchers` consistent; migration `020_perf_indexes.sql` creates both indexes referenced in 3.1/3.2; `RateLimitMiddleware(rate, burst float64)` matches the existing signature.

**Open verifications flagged inline** (must check during execution, not guessed): the `AuthConfig` type name; `NewRouter`'s access to config for rate-limit wiring; the `main.go` logger variable name + LDAP config path; the goose behavior of `CREATE INDEX` on the partitioned `audit_events`; availability of `sips`/ImageMagick for F1; the exact SearchPage test/render helper and the shared Modal component location.
