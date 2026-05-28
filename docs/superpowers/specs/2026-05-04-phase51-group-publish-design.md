# Phase 51: Publish to Group Repositories (npm & Docker)

## Goal

Group repositories currently return 405 for all write methods. This phase enables forwarding write requests to the first hosted member, making group repos writable for npm and Docker.

## Architecture

**Single change location:** `internal/formats/group/handler.go`

The existing fan-out mechanism (sub gin context + `httptest.NewRecorder()`) already works for reads. The same approach forwards writes to the member handler, which stores blobs and updates DB via its own `ServeHTTP`.

## Write flow

1. Client sends PUT/POST/PATCH to a group repo
2. `GroupHandler.serveWrite` loads the group repo definition
3. Resolves writable member: `FormatConfig["writable_member"]` if set, else first `type=hosted` member with matching format
4. Substitutes `repoName` in gin params and forwards to that member's handler via sub-context
5. Returns 405 if no hosted member exists (read-only group)

## Domain helper

`GroupWritableMember(repo *Repository) string` in `internal/domain/types.go` — returns `FormatConfig["writable_member"]` or empty string.

## Docker multi-step blob upload

Docker upload sessions (POST→PATCH→PUT) are stored in the `docker.Handler`'s `sync.Map`. Because we always resolve to the same hosted member (config override or first-hosted = deterministic), all steps hit the same handler instance and sessions stay consistent.

## Config

Optional `writable_member` key in group `FormatConfig`. No migration needed — stored in existing `repositories.format_config` JSONB column.

## Tests

- npm publish through group → reaches hosted member (httptest recorder captures 200)
- Docker POST blob upload through group → reaches hosted member
- Group with no hosted members → 405
- Explicit `writable_member` config → overrides auto-detect
