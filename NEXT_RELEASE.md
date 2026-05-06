### ✨ Features

- **Content Replication (Phase 55):** Push artifacts from a local Nexspence repository to a remote Nexspence instance on a cron schedule. Duplicate detection by asset path, AES-256-GCM-encrypted credentials, skip-and-continue error handling. New `replication_rules` and `replication_history` tables (migration 017). `ReplicationService` with cron scheduler, `TestConnection`, `ListHistory`. Six API endpoints: list/create/update/delete rules, manual run, test connection, list history. Frontend `ReplicationTab` in System Admin with rule cards, inline history table, and create/edit modal.

### 🐛 Bug Fixes

- **Monitoring page blank screen:** Backend never included `artifacts_deleted` in `GET /api/v1/metrics`; frontend called `.toLocaleString()` on `undefined`, throwing `TypeError` and crashing the React tree. Fixed by adding `ArtifactsDeleted` atomic counter to the metrics package (incremented on every `DeleteArtifact` call) and guarding the frontend render with `?? 0`.
- **Replication tab blank screen:** Go nil slices serialise to JSON `null`; `ListRules`/`ListHistory` returned `null` instead of `[]`, causing `rules.map()` to throw `TypeError`. Fixed with `make([]T, 0)` on the backend and a `?? []` guard in the frontend query function.
