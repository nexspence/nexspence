# Basic Authentication Throughput and Main-Branch Images Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix issues #713 and #714 with safe, short-lived Basic password-verification reuse and per-merge GHCR images, then update docs/site and release.

**Architecture:** The data-route authentication path fetches the current user for every request and caches only successful local bcrypt verification under a process-secret HMAC key. PostgreSQL conditionally updates `last_login` once per minute across replicas. A new `main`-only workflow builds/pushes multi-arch images with branch and short-SHA tags, separate from semver releases.

**Tech Stack:** Go, PostgreSQL, Gin, GitHub Actions, Docker Buildx, GHCR, HTML/i18n website docs.

**Spec:** `docs/superpowers/specs/2026-10-29-basic-auth-and-main-images-design.md`

## Global Constraints

- Verification cache TTL: 60 seconds; maximum entries: 4096.
- HMAC-SHA-256 uses a cryptographically random process-local secret; no plaintext password is retained.
- Account status, roles, password hash, and `tokens_valid_after` are read from current user data on every request.
- Cache only successful local-password checks; do not issue JWTs on data routes.
- `last_login` is updated at most once per user per minute across replicas.
- Branch images are Linux amd64+arm64 and tagged only `main` plus short commit SHA; `latest` stays release-only.

## Review Focus

- Password change or token revocation during cache lifetime → metadata mismatch deletes entry and rechecks bcrypt.
- Role changes during cache lifetime → current roles returned on each request; no authorization result cached.
- Disabled user during cache lifetime → current status rejects before cached success.
- High-cardinality valid Basic credentials → cache stays within 4096 entries and expires old keys.
- Concurrent requests across replicas → SQL predicate permits only one `last_login` update per minute.

---

### Task 1: Successful local Basic verification cache

**Files:**
- Modify: `internal/service/user_service.go`
- Create: `internal/service/basic_auth_cache_test.go`
- Create: `internal/service/user_service_basic_auth_test.go`

**Interfaces:**
- Produce `(*UserService).AuthenticateBasic(ctx context.Context, username, password string) (*domain.User, error)`.
- Preserve `Login` unchanged for explicit interactive login and JWT issuance.

- [ ] Write tests for successful cache hit, password-hash/status/revocation invalidation, expired entries, HMAC key separation, and bounded size.
- [ ] Run the focused tests and observe expected failures before implementation.
- [ ] Implement a 60-second, 4096-entry process-local cache keyed by HMAC-SHA-256 over username/password, storing only metadata and expiry.
- [ ] Fetch current user on every call; reject missing/inactive users; only reuse verification for local users when cached metadata exactly matches; return current roles.
- [ ] Run focused service tests; record RED→GREEN output.
- [ ] Commit as `fix(auth): cache successful Basic password verification`.

### Task 2: Data-route authentication without JWT and throttled login timestamp

**Files:**
- Modify: `internal/api/handlers/auth.go`
- Modify: `internal/api/handlers/auth_test.go`
- Modify: `internal/repository/postgres/user_repo.go`
- Modify: `internal/repository/postgres/user_repo_integration_test.go`
- Modify: `internal/service/user_service.go`

- [ ] Add handler test proving valid local Basic authentication sets current user context without invoking interactive JWT login; preserve failed-auth guard behavior.
- [ ] Run the focused handler test and observe RED.
- [ ] Route `OptionalAuth` local Basic credentials through `AuthenticateBasic`; keep LDAP fallback behavior unchanged.
- [ ] Change `UpdateLastLogin` SQL to update only when `last_login` is null or older than one minute.
- [ ] Add an integration test proving a second update within one minute preserves the original timestamp, and an eligible update refreshes it.
- [ ] Run focused handler and repository tests; record RED→GREEN output.
- [ ] Commit as `fix(auth): throttle repository Basic-auth work`.

### Task 3: Publish main-branch container images

**Files:**
- Create: `.github/workflows/main-image.yml`

- [ ] Write `scripts/test-main-image-promotion.sh` with fake `gh` and `docker` commands; assert current commit promotes `:main`, while a stale commit does not. Run it before implementation and observe the missing promotion helper.
- [ ] Add `scripts/promote-main-image.sh` to query current `main` head and promote the immutable short-SHA image to `:main` only when the workflow commit is still current.
- [ ] Add a push-to-main-only workflow with `contents: read`, `packages: write`, pinned Docker action SHAs, GHCR login, amd64+arm64 Buildx, immutable short-SHA image build, serialized `main` promotion, and latest-head guard.
- [ ] Verify the promotion test, actionlint, event/permission scope, and that `latest` appears only in release workflow.
- [ ] Commit as `ci: publish main-branch container images`.

### Task 4: Operator documentation and website

**Files:**
- Modify: `docs/deployment.md`
- Modify: `website/docs/index.html`
- Modify: `docs/api-spec.yaml` only if authentication performance guidance belongs in API description

- [ ] Add docs assertions/checks for API tokens as the recommended package-manager credential and explain `main`/short-SHA image tags versus release `latest`.
- [ ] Run documentation checks and observe missing guidance.
- [ ] Update deployment docs and both website locales with commands/examples and explain password Basic remains supported.
- [ ] Run HTML/i18n and docs checks; verify all documentation examples point to correct image tags.
- [ ] Commit as `docs: document API-token guidance and main images`.

### Task 5: Full verification and release

**Files:**
- Modify: `docs/superpowers/specs/2026-10-29-basic-auth-and-main-images-design.md` only if implementation discoveries require a ruling.
- Modify: `docs/superpowers/plans/2026-10-29-basic-auth-and-main-images.md` to record completion.

- [ ] Run `go test ./...`, `go vet ./...`, project Go lint, `npm test -- --run`, frontend build/typecheck, workflow YAML validation, and `git diff --check`.
- [ ] Create a PR linking #713 and #714; merge after all required CI checks pass.
- [ ] Confirm the `main` image workflow publishes both expected tags.
- [ ] Derive and run the next version through `.github/workflows/tag.yml`; the expected bump is patch unless the repository's `svu` output says otherwise.
- [ ] Verify release workflow, GHCR versioned/latest tags, website deployment at release commit, and Telegram announcement workflow/state record.
- [ ] Close #713 and #714 with English release notes and mark plan complete.
