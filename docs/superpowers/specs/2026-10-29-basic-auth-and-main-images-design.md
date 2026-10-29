# Basic Authentication Throughput and Main-Branch Images

## Intent

Address GitHub issues #713 and #714 so package-manager traffic authenticated with HTTP Basic does not spend bcrypt and JWT work on every artifact request, and users can test every merge to `main` from GHCR before the next version release.

## Goals

- Reuse only successful local-password verification for at most 60 seconds per server process.
- Keep database user lookup on every request so current account status, roles, password hash, and `tokens_valid_after` remain authoritative.
- Key the bounded, expiring cache with an HMAC over username and password; never retain the plaintext password.
- Do not create a JWT for local Basic authentication on repository data routes.
- Rate-limit `last_login` updates atomically to at most one successful update per user per minute across replicas.
- Publish Linux amd64 and arm64 images for each push to `main` under `main` and the short commit SHA.
- Preserve release workflow behavior: `latest` continues to mean the latest versioned release.
- Document API tokens as the preferred credential for package-manager/build traffic and explain the `main`/SHA image tags.

## Non-goals

- Cache failures, LDAP authentication, API-token authentication, or authorization decisions.
- Change password-login JWT semantics or release image tags.
- Publish `latest`, version, or semver tags from branch builds.
- Add a configurable cache duration in this change.

## Approaches

1. **Per-request bcrypt and JWT (current behavior):** no cache complexity, but leaves the reported CPU bottleneck and unnecessary token/database work.
2. **Cache complete authentication/authorization results:** faster, but stale roles, disabled accounts, or revoked credentials could remain usable until expiry.
3. **Cache successful password verification only (chosen):** fetch the current user each time; reuse bcrypt only when the HMAC credential key and current password hash, active status, and revocation timestamp still match. This preserves current request-level RBAC while removing bcrypt and JWT generation from hot data routes.

For issue #714, extend the existing release workflow versus a separate branch workflow were considered. A separate, branch-scoped workflow is chosen: it cannot change `latest` or interfere with semver release jobs and has narrowly scoped package permissions.

## Behavior and constraints

- Cache lifetime: 60 seconds; maximum entries: 4096, pruning expired entries before insertion and rejecting further cache insertion when full after pruning.
- Cache keys are HMAC-SHA-256 values using a cryptographically random process-local secret. If secure secret initialization fails, authentication still works but successful verifications are not cached.
- Cache entries include the password hash, user status, and `TokensValidAfter`; mismatches delete the matching entry and force password verification. Inactive users are rejected before cache use. The current user object, including current roles, is returned on every request.
- An entry is created only after password verification succeeds. LDAP flows retain their existing authentication path and are not cached; this change targets bcrypt-backed local passwords.
- `last_login` updates use one conditional SQL update (`NULL` or older than one minute) so concurrent requests and multiple replicas cannot update more than once per user per minute.
- `main` image builds run only on pushes to branch `main`, publish amd64+arm64, and produce exactly `main` and short-SHA tags. They do not run for pull requests or version tags.
- Both docs website locales explain API-token preference; the website and release documentation describe the development image tags.

## Files and responsibilities

- `internal/service/user_service.go`: data-route credential verification, bounded HMAC cache, conditional last-login touch.
- `internal/service/basic_auth_cache_test.go`: cache expiry, credential secrecy/keying, and invalidation behavior.
- `internal/service/user_service_basic_auth_test.go`: successful cache reuse, cache invalidation, no JWT requirement, active-user behavior.
- `internal/repository/postgres/user_repo.go` and integration tests: atomic once-per-minute last-login update.
- `internal/api/handlers/auth.go` and handler tests: route Basic auth through the no-JWT service method while preserving optional-auth and throttling behavior.
- `.github/workflows/main-image.yml`: branch-only GHCR publishing.
- `docs/deployment.md` and `website/docs/index.html`: English/Russian operator guidance.

## Verification

- TDD each behavior: observe failing tests before implementation, then focused green tests.
- Run full Go tests, Go build/vet/lint, frontend tests/build, workflow YAML validation, and documentation consistency checks.
- Exercise the workflow's metadata rules locally or through a workflow run and verify the image tags before declaring issue #714 complete.
- Release through the repository's tag workflow; verify the GitHub Release, GHCR release tags, website deployment at the same source SHA, then complete the required Telegram announcement workflow and verify its state record.
