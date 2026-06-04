### ✨ Features


### 🐛 Bug Fixes

_No bug fixes in this release._

### 🔧 Quality / Tooling

- **Storage layer test coverage (Track B Phase 3)** — added unit tests for `LocalBlobStore` (Put/Get/Delete/Exists/Size/ListKeys/UsedBytes via `t.TempDir()`), `Registry` (Get cache hit/miss/invalidate, NewFromConfig, unknown type), and `NewBlobStoreFromConfig` (local + s3 validation). Added `integration`-tagged S3 tests via dockertest MinIO covering the full S3BlobStore interface (Put/Get/Delete/Exists/Size/ListKeys/UsedBytes/PresignGetURL/PresignPutURL/ConfigureLifecycle). Combined unit+integration coverage: **85.9%**. CI `integration` job extended to run storage tests with a ≥80% floor.
