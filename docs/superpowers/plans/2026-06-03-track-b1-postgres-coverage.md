# Track B — Phase 1: Postgres Repository Layer ≥80% Coverage (dockertest) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring `internal/repository/postgres` from ~0% to ≥80% line coverage by adding a dockertest-based integration-test harness and comprehensive tests for every repository, run against a real ephemeral Postgres.

**Architecture:** A new `internal/testutil/pgtest` package boots one ephemeral Postgres container per test binary via `ory/dockertest/v3`, applies the real embedded migrations through the existing `db.Migrate(dsn, "up")`, and exposes a shared `*pgxpool.Pool`. All postgres-repo tests carry the `//go:build integration` build tag, so the default `go test ./...` (and the Track A lint/test gates) stay Docker-free; `make test-integration` runs them. Tests are independent: each truncates the tables it touches via a `pgtest.Truncate` helper. This is Phase 1 of Track B — handlers, storage, service/formats, and the global ≥80% CI gate are later phases.

**Tech Stack:** Go 1.26.3, pgx v5.9.2, pressly/goose v3 (existing migration runner), ory/dockertest/v3 v3.12.0 (new), the project's existing `internal/db` (`Connect`, `Migrate`) and `internal/domain` types.

**Branch:** Create and work on `track-b1-postgres-coverage` (worktree). Do NOT commit to `main` directly.

**Reused project facts (verified):**
- `internal/db/db.go` embeds migrations (`//go:embed migrations/*.sql`) and exposes `Connect(ctx, dsn) (*pgxpool.Pool, error)` and `Migrate(dsn, direction string) error` (goose; direction `"up"`).
- Module path: `github.com/nexspence-oss/nexspence`.
- Repos are constructed as `postgres.NewXxxRepo(pool *pgxpool.Pool)`.
- The `rtk` hook collapses `go test` output — rely on exit codes; read coverage from a profile via `go tool cover`, and use `rtk proxy go test ...` if full output is needed.
- `make test-integration` already exists: `go test -race -count=1 -tags=integration ./...`.

---

## Pre-flight

- [ ] **Step 0: Branch + Docker check**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core
git checkout -b track-b1-postgres-coverage
docker info >/dev/null 2>&1 && echo "docker-ok" || echo "DOCKER NOT RUNNING — start Docker Desktop before integration tests"
git status   # expect clean on the new branch
```

If Docker is not running, the harness tasks cannot be verified — start Docker Desktop first.

---

## Task 1: Add dockertest dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/ory/dockertest/v3@v3.12.0
go mod tidy
```

- [ ] **Step 2: Verify it resolves and builds**

```bash
go build ./... ; echo "build=$?"
grep dockertest go.mod
```

Expected: `build=0`, and `go.mod` lists `github.com/ory/dockertest/v3`.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "build(test): add ory/dockertest/v3 for DB integration tests"
```

---

## Task 2: The `pgtest` harness

**Files:**
- Create: `internal/testutil/pgtest/pgtest.go`

This package is only compiled under the `integration` tag, so it never affects the default build/test.

- [ ] **Step 1: Write the harness**

Create `internal/testutil/pgtest/pgtest.go`:

```go
//go:build integration

// Package pgtest boots an ephemeral PostgreSQL container for integration tests,
// applies the project's real migrations, and hands back a ready pgx pool.
package pgtest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"

	appdb "github.com/nexspence-oss/nexspence/internal/db"
)

var (
	once     sync.Once
	pool     *pgxpool.Pool
	purge    func()
	startErr error
)

// Pool returns a shared, migrated pool. The container is started once per test
// binary; the first caller pays the startup cost. Fails the test on error.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	once.Do(start)
	if startErr != nil {
		t.Fatalf("pgtest: start postgres: %v", startErr)
	}
	return pool
}

// Cleanup tears down the container. Call from TestMain after m.Run().
func Cleanup() {
	if purge != nil {
		purge()
	}
}

func start() {
	dpool, err := dockertest.NewPool("")
	if err != nil {
		startErr = fmt.Errorf("connect to docker: %w", err)
		return
	}
	if err := dpool.Client.Ping(); err != nil {
		startErr = fmt.Errorf("docker ping (is Docker running?): %w", err)
		return
	}

	resource, err := dpool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "16-alpine",
		Env: []string{
			"POSTGRES_USER=test",
			"POSTGRES_PASSWORD=test",
			"POSTGRES_DB=nexspence_test",
			"listen_addresses='*'",
		},
	}, func(c *docker.HostConfig) {
		c.AutoRemove = true
		c.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		startErr = fmt.Errorf("start postgres container: %w", err)
		return
	}
	_ = resource.Expire(600) // self-destruct after 10m as a safety net

	hostPort := resource.GetHostPort("5432/tcp")
	dsn := fmt.Sprintf("postgres://test:test@%s/nexspence_test?sslmode=disable", hostPort)

	dpool.MaxWait = 60 * time.Second
	if err := dpool.Retry(func() error {
		p, perr := appdb.Connect(context.Background(), dsn)
		if perr != nil {
			return perr
		}
		p.Close()
		return nil
	}); err != nil {
		_ = dpool.Purge(resource)
		startErr = fmt.Errorf("wait for postgres: %w", err)
		return
	}

	if err := appdb.Migrate(dsn, "up"); err != nil {
		_ = dpool.Purge(resource)
		startErr = fmt.Errorf("migrate: %w", err)
		return
	}

	p, err := appdb.Connect(context.Background(), dsn)
	if err != nil {
		_ = dpool.Purge(resource)
		startErr = fmt.Errorf("connect pool: %w", err)
		return
	}

	pool = p
	purge = func() {
		pool.Close()
		_ = dpool.Purge(resource)
	}
}

// Truncate empties the named tables (RESTART IDENTITY CASCADE) so each test
// starts from a known-empty state. Call at the top of a test or via t.Cleanup.
func Truncate(t *testing.T, p *pgxpool.Pool, tables ...string) {
	t.Helper()
	if len(tables) == 0 {
		return
	}
	stmt := fmt.Sprintf("TRUNCATE %s RESTART IDENTITY CASCADE", strings.Join(tables, ", "))
	if _, err := p.Exec(context.Background(), stmt); err != nil {
		t.Fatalf("pgtest: truncate %v: %v", tables, err)
	}
}
```

- [ ] **Step 2: Verify it compiles under the integration tag**

```bash
go build -tags=integration ./internal/testutil/pgtest/ ; echo "build=$?"
go vet -tags=integration ./internal/testutil/pgtest/ ; echo "vet=$?"
```

Expected: both `=0`. (No container is started by a build/vet — `start()` only runs when `Pool(t)` is first called in a test.)

- [ ] **Step 3: Commit**

```bash
git add internal/testutil/pgtest/pgtest.go
git commit -m "test(pgtest): dockertest harness — ephemeral Postgres + migrations + pool"
```

---

## Task 3: Postgres package `TestMain` + exemplar `MigrationRepo` tests

This task establishes the pattern every later repo test follows: a single `TestMain` (integration-tagged) owns the container lifecycle; each test grabs `pgtest.Pool(t)`, truncates its tables, exercises the repo, and asserts.

**Files:**
- Create: `internal/repository/postgres/main_integration_test.go`
- Create: `internal/repository/postgres/migration_repo_integration_test.go`

Reference — `MigrationRepo` (`internal/repository/postgres/migration_repo.go`) methods: `List(ctx)`, `Get(ctx, id)`, `Create(ctx, *domain.MigrationJob)` (RETURNING id/created_at/updated_at), `UpdateStatus(ctx, id, status)`, `Delete(ctx, id)`. `Get`/`UpdateStatus`/`Delete` return a "migration job not found" error when the id is absent. Table: `migration_jobs`. Domain: `domain.MigrationJob{SourceURL, SourceUser, MigrateRepos/Users/Blobs/Policies bool, Status, ...}`, statuses `domain.MigrationPending/Running/Paused/Done/Error`.

- [ ] **Step 1: Write `TestMain`**

Create `internal/repository/postgres/main_integration_test.go`:

```go
//go:build integration

package postgres

import (
	"os"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Cleanup()
	os.Exit(code)
}
```

- [ ] **Step 2: Write the exemplar repo test (failing — repo behavior asserted)**

Create `internal/repository/postgres/migration_repo_integration_test.go`:

```go
//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

func TestMigrationRepo_CRUD(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "migration_jobs")
	ctx := context.Background()
	repo := NewMigrationRepo(pool)

	// Create
	job := &domain.MigrationJob{
		SourceURL:    "https://nexus.example.com",
		SourceUser:   "admin",
		MigrateRepos: true,
		MigrateUsers: true,
	}
	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if job.ID == "" {
		t.Fatal("Create did not populate ID")
	}
	if job.CreatedAt.IsZero() || job.UpdatedAt.IsZero() {
		t.Fatal("Create did not populate timestamps")
	}

	// Get
	got, err := repo.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SourceURL != job.SourceURL || got.SourceUser != "admin" {
		t.Fatalf("Get mismatch: %+v", got)
	}
	if !got.MigrateRepos || !got.MigrateUsers || got.MigrateBlobs || got.MigratePolicies {
		t.Fatalf("Get bool flags mismatch: %+v", got)
	}

	// UpdateStatus
	if err := repo.UpdateStatus(ctx, job.ID, domain.MigrationRunning); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ = repo.Get(ctx, job.ID)
	if got.Status != domain.MigrationRunning {
		t.Fatalf("status not updated: %s", got.Status)
	}

	// List
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != job.ID {
		t.Fatalf("List mismatch: %+v", list)
	}

	// Delete
	if err := repo.Delete(ctx, job.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, job.ID); err == nil {
		t.Fatal("Get after Delete should error (not found)")
	}
}

func TestMigrationRepo_NotFound(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "migration_jobs")
	ctx := context.Background()
	repo := NewMigrationRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	if _, err := repo.Get(ctx, missing); err == nil {
		t.Fatal("Get(missing) should error")
	}
	if err := repo.UpdateStatus(ctx, missing, domain.MigrationDone); err == nil {
		t.Fatal("UpdateStatus(missing) should error")
	}
	if err := repo.Delete(ctx, missing); err == nil {
		t.Fatal("Delete(missing) should error")
	}
}

func TestMigrationRepo_List_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "migration_jobs")
	repo := NewMigrationRepo(pool)
	list, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty, got %d", len(list))
	}
}
```

- [ ] **Step 3: Run the exemplar against a real container**

```bash
docker info >/dev/null 2>&1 || { echo "start Docker first"; exit 1; }
go test -tags=integration -count=1 -run 'TestMigrationRepo|TestMain' ./internal/repository/postgres/ ; echo "exit=$?"
```

Expected: `exit=0` (container boots ~once, migrations apply, all three tests pass). If it fails to connect, confirm Docker Desktop is running and `postgres:16-alpine` can be pulled.

- [ ] **Step 4: Confirm coverage tooling works**

```bash
go test -tags=integration -count=1 -coverprofile=/tmp/pg_cov.out -run 'TestMigrationRepo' ./internal/repository/postgres/ >/dev/null 2>&1
go tool cover -func=/tmp/pg_cov.out | grep migration_repo.go
```

Expected: `migration_repo.go` functions show non-zero coverage (List/Get/Create/UpdateStatus/Delete near 100%).

- [ ] **Step 5: Commit**

```bash
git add internal/repository/postgres/main_integration_test.go internal/repository/postgres/migration_repo_integration_test.go
git commit -m "test(postgres): TestMain + exemplar MigrationRepo integration tests"
```

---

## Tasks 4–N: Per-repository integration tests (follow the Task 3 pattern)

> **Pattern for every task below** (this is the concrete instruction, not a placeholder): create `internal/repository/postgres/<name>_integration_test.go` with `//go:build integration` and `package postgres`. For each exported method of the repo, write table-or-sequence tests that:
> 1. `pool := pgtest.Pool(t)`; `pgtest.Truncate(t, pool, <tables this repo and its FKs touch>)`; `ctx := context.Background()`; construct the repo with its `New…(pool)` constructor.
> 2. Cover the **happy path** (create→get→update→list→delete as applicable), asserting every non-trivial field round-trips (including bool flags, slices/arrays, JSON columns, nullable `*T` fields set to both nil and non-nil).
> 3. Cover **not-found** paths (Get/Update/Delete on a random UUID returns the repo's not-found error) and **filtering/ordering/pagination** where the method takes params.
> 4. Cover **constraint/error** paths where cheap and meaningful (e.g. unique-violation on duplicate insert, FK dependency, empty-list result).
> Before writing, READ the repo file (`internal/repository/postgres/<name>.go`), its domain type(s) in `internal/domain/`, and the relevant `internal/db/migrations/*.sql` to learn exact columns, constraints, and which parent rows must exist first (insert parents directly via `pool.Exec` or via the parent repo). Aim each file at ≥80% of its repo's lines; verify with `go tool cover`.

Tasks are ordered to build dependency fixtures first (blob stores, repositories, users, roles) so later repos (assets, components, promotion) have parents to reference. **Group = one task/commit.** Each task ends with:
```bash
go test -tags=integration -count=1 -run '<RepoName>' ./internal/repository/postgres/ ; echo "exit=$?"   # expect 0
git add internal/repository/postgres/<name>_integration_test.go && git commit -m "test(postgres): <RepoName> integration tests"
```

- [ ] **Task 4 — `UserRepo`** (`user_repo.go`, 184 lines). Methods incl. Create/Get/GetByUsername/GetByEmail/Update/Delete/List/SetUserRoles/GetUserRoles (read the file for the exact set). Tables: `users`, `user_roles`, `roles`. Cover username/email lookups, role assignment round-trip, source (local/oidc) field, not-found.
- [ ] **Task 5 — `RoleRepo` + `rbac_repo.go` + `PrivilegeRepo`** (`role_repo.go` 179, `rbac_repo.go` 43, `privilege_repo.go` 151). Tables: `roles`, `privileges`, `role_privileges`. Cover role CRUD, privilege CRUD (content-selector type), role↔privilege linking, built-in vs custom.
- [ ] **Task 6 — `ContentSelectorRepo` + `RoutingRuleRepo`** (`content_selector_repo.go` 154, `routing_rule_repo.go` 99). Tables: `content_selectors`, `routing_rules`. Cover CEL expression round-trip, ordering, CRUD, not-found.
- [ ] **Task 7 — `BlobStoreRepo` + `blob_ref_repo.go`** (`blobstore_repo.go` 107, `blob_ref_repo.go` 62). Tables: `blob_stores`, `blob_refs` (ref-count). Cover create/list/get/update used_bytes, ref increment/decrement, the `errors.Is(pgx.ErrNoRows)` paths.
- [ ] **Task 8 — `RepositoryRepo`** (`repository_repo.go` 230). Table: `repositories` (+ FK to `blob_stores`). Cover CRUD, `ListByRepoNames`, `HasAnyAnonymousDocker`, cleanup_policy_ids array, format/type filters, blob-store FK. Insert a `blob_stores` parent first.
- [ ] **Task 9 — `AssetRepo` part 1: CRUD + lookups** (`asset_repo.go` 586 — the largest; split across Tasks 9–10). Table: `assets` (+ FK `components`,`repositories`). Cover Create/Get/GetByPath/Update/Delete/List, `download_count` increment, blob-key dedup. Insert parent `repositories`+`components` first.
- [ ] **Task 10 — `AssetRepo` part 2: queries** — `ListStale` (the retain-N CTE — cover `retainNVersions` = 0 and > 0), `ListByRepoNames`, `ListAllBlobKeys` (GC), `ListDockerBrowseRows`, search/pagination. Pre-seed multiple versions to exercise the `ROW_NUMBER() … version_sort` window.
- [ ] **Task 11 — `ComponentRepo`** (`component_repo.go` 305). Table: `components`. Cover CRUD, `ListByRepoNames`, `SetTags` (text[] + GIN), `UpdateExtra` (merged JSON), search by name/tag, group expansion inputs.
- [ ] **Task 12 — `AuditRepo`** (`audit_repo.go` 190). Table: `audit_events` (partitioned). Cover `List(AuditQuery) (items,total,err)` with from/to/username filters + pagination, `Stream(q, fn)`, insert via the middleware's shape. Note partitioning — insert rows within an existing monthly partition.
- [ ] **Task 13 — `PromotionRepo`** (`promotion_repo.go` 202). Tables: `promotion_rules`, `promotion_requests`. Cover rule CRUD, request queue create/list/approve/reject status transitions, CEL filter field.
- [ ] **Task 14 — `ReplicationRepo` + `scan_result_repo.go`** (`replication_repo.go` 136, `scan_result_repo.go` 145). Tables: `replication_rules`, `replication_history`, `scan_results`. Cover rule CRUD, history append/list, scan-result upsert + summary/list queries.
- [ ] **Task 15 — `UserTokenRepo` + `WebhookRepo`** (`user_token_repo.go` 99, `webhook_repo.go` 114). Tables: `user_tokens`, `webhooks`. Cover token create (sha-256 hash stored), lookup-by-hash, expiry, delete; webhook CRUD + events array.
- [ ] **Task 16 — `blob_store_migration_repo.go`** (`blob_store_migration_repo.go` 144). Table: `blob_store_migrations`. Cover Create/Get/List/UpdateProgress/UpdateStatus, status transitions, not-found.

---

## Task 17: CI integration-test job + coverage check

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Add an `integration` job**

Add to `.github/workflows/ci.yml` under `jobs:` (Docker is available on `ubuntu-latest` runners, so dockertest works):

```yaml
  integration:
    name: integration tests (postgres)
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.3'
          cache: true
      - name: Run integration tests with coverage
        run: |
          go test -tags=integration -count=1 \
            -coverpkg=./internal/repository/postgres/... \
            -coverprofile=pg_cov.out \
            ./internal/repository/postgres/...
      - name: Enforce postgres package ≥80%
        run: |
          pct=$(go tool cover -func=pg_cov.out | awk '/^total:/ {gsub(/%/,"",$3); print $3}')
          echo "postgres coverage: ${pct}%"
          awk -v p="$pct" 'BEGIN { exit (p+0 >= 80) ? 0 : 1 }' || {
            echo "::error::postgres repository coverage ${pct}% is below the 80% floor"; exit 1; }
```

- [ ] **Step 2: Validate YAML**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml')); print('yaml ok')"
```

Expected: `yaml ok`.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: run postgres integration tests with an 80% coverage floor"
```

---

## Task 18: Verify the ≥80% floor locally + finalize

**Files:**
- Modify: `NEXT_RELEASE.md`

- [ ] **Step 1: Full postgres coverage run**

```bash
docker info >/dev/null 2>&1 || { echo "start Docker"; exit 1; }
go test -tags=integration -count=1 \
  -coverpkg=./internal/repository/postgres/... \
  -coverprofile=/tmp/pg_full.out \
  ./internal/repository/postgres/... ; echo "exit=$?"
go tool cover -func=/tmp/pg_full.out | tail -1
```

Expected: `exit=0` and `total:` ≥ **80.0%**. If below, identify the lowest-covered files (`go tool cover -func=/tmp/pg_full.out | sort -t$'\t' -k3 -n | head`) and add tests for their uncovered methods (loop back to the relevant Task 4–16).

- [ ] **Step 2: Confirm the default (non-integration) suite is unaffected**

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
go test -count=1 ./... ; echo "unit-test=$?"      # Docker-free, still 474+ pass
make lint ; echo "lint=$?"                          # Track A gate still 0
```

Expected: both `=0` (integration tests are tag-gated, so they don't run here).

- [ ] **Step 3: Append to `NEXT_RELEASE.md`** (under a new or existing `### 🔧 Quality / Tooling` heading):

```markdown
- **Postgres repository layer test coverage** — added a dockertest-based integration harness (`internal/testutil/pgtest`, `integration` build tag) that boots an ephemeral Postgres, applies the real migrations, and runs comprehensive tests for all 21 repositories (`internal/repository/postgres`), taking the package from ~0% to ≥80% line coverage. New CI `integration` job enforces the 80% floor. Default `go test ./...` stays Docker-free. (Track B Phase 1.)
```

- [ ] **Step 4: Commit**

```bash
git add NEXT_RELEASE.md
git commit -m "docs: record postgres integration coverage (Track B Phase 1)"
```

---

## Self-Review checklist (run before declaring Phase 1 done)

- [ ] `go test -tags=integration -count=1 ./internal/repository/postgres/...` passes
- [ ] `internal/repository/postgres` total coverage ≥ 80% (`go tool cover -func`)
- [ ] Default `go test ./...` still green and Docker-free; `make lint` = 0
- [ ] Every new test file carries `//go:build integration` and `package postgres`
- [ ] Exactly one `TestMain` in the package; container started once, purged on exit
- [ ] Tests are independent (each truncates its tables); no cross-test ordering assumptions
- [ ] CI `integration` job present and YAML-valid; coverage gate enforces 80%
- [ ] `NEXT_RELEASE.md` updated

## Notes / deferred

- This phase does **not** turn on the global per-package ≥80% gate (Track B Phase 5) — handlers/storage/service still need their phases first.
- BUG-33 (the `TestAuditMiddleware` test-only race) is **not** in this phase; it lives in `internal/api`, addressed when the handler/api phase runs.
- `ErrNotFound` sentinel refactor (replacing `(nil,nil)`/ad-hoc not-found errors) is intentionally **out of scope** here — these tests pin the *current* not-found behavior; the sentinel refactor (with these tests as the safety net) can follow as its own change.
