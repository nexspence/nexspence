### ✨ Features


### 🐛 Bug Fixes

_No bug fixes in this release._

### 🔧 Quality / Tooling

- **Format handlers + global CI coverage gate (Track B Phase 5)** — brought all 17 `internal/formats/*` packages from 38–77% to **≥80%** (repoproxy 38%→85%, npm 54%→94%, conda 57%→88%, terraform 57%→87%, pypi 64%→86%, yum 62%→89%, maven 64%→85%, gomod 66%→88%, docker 66%→92%, and 8 more). Added a new CI `coverage` job that enforces a per-package ≥80% unit-test floor across all `internal/*` packages (excluding db/logger/config/storage/testutil which have integration or no-logic exclusions). Fixed BUG-33: `TestAuditMiddleware` test-only data race — replaced `time.Sleep` + direct slice reads with `awaitAudit()` polling via the new thread-safe `AuditRepo.Snapshot()` method; all tests now pass cleanly under `-race`. 1280 unit tests passing. Website counter 1005→1280.
- **Service layer test coverage (Track B Phase 4)** — added table-driven tests for every previously-uncovered or low-coverage service (58.9% → **80.4%**): new `rbac_service_test.go` (0%→93%; CanAccessRepo admin-bypass, CEL expression evaluation, FilterRepos/Paths/DockerRows/Components/Assets), `user_service_crud_test.go` (0%→88%; Create/List/Get/GetByID/Update/ChangePassword/SetPassword/Delete/GetUserRoles/SetUserRoles/ValidateToken), plus gap-filling extra test files for repository_service, promotion_service, cleanup_service, backup_service, replication_service, scan_service, and blob_store_migration_service. 1005 unit tests pass. Website counter 815→1005.
- **Storage layer test coverage (Track B Phase 3)** — added unit tests for `LocalBlobStore` (Put/Get/Delete/Exists/Size/ListKeys/UsedBytes via `t.TempDir()`), `Registry` (Get cache hit/miss/invalidate, NewFromConfig, unknown type), and `NewBlobStoreFromConfig` (local + s3 validation). Added `integration`-tagged S3 tests via dockertest MinIO covering the full S3BlobStore interface (Put/Get/Delete/Exists/Size/ListKeys/UsedBytes/PresignGetURL/PresignPutURL/ConfigureLifecycle). Combined unit+integration coverage: **85.9%**. CI `integration` job extended to run storage tests with a ≥80% floor.
