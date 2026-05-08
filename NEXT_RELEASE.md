### ✨ Features

- **Demo & Seed Scripts** (Phase 59) — `scripts/seed-all.sh` orchestrates full demo setup: `seed-repos.sh` creates hosted+proxy+group for all 14 formats (maven2, npm, pypi, docker, go, nuget, raw, apt, yum, helm, cargo, conan, conda, terraform); `seed-packages.sh` uploads minimal test artifacts to each hosted repo via curl+python3; `seed-rbac.sh` creates CS + 2 privileges + 2 roles + 2 users for environments dev/stage/test/prod. Repositories split across `s3-primary` (maven/npm/pypi/docker/helm/cargo/conda) and `s3-secondary` (go/nuget/raw/apt/yum/conan/terraform) for blob store migration testing. All existing `create-*.sh` scripts updated to default port 8080.

### 🐛 Bug Fixes

_No bug fixes in this release._
