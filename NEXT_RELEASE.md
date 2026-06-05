### ✨ Features


### 🐛 Bug Fixes

- **Audit log: client IP no longer shows a `/32` suffix.** The `remote_ip` column is a Postgres `INET`, and reading it back via `remote_ip::text` returned CIDR form (e.g. `192.168.1.100/32`). The audit list/stream queries now read it via `host(remote_ip)`, so the bare client IP is returned.

### 🔧 Quality / Tooling

- **Lint debt cleared — three more linters now gate CI.** Enabled revive `unused-parameter` and `exported`, plus `nilnil`, in `.golangci.yml` and drove all findings to zero: renamed 13 unused parameters to `_`, documented 281 exported symbols with Go doc comments (mocks exempted), and converted the repository layer off the `(nil, nil)` "not found" convention.
- **Repository layer now reports not-found via a `repository.ErrNotFound` sentinel** instead of `(nil, nil)`. All 27 postgres lookup sites and the in-memory mocks return the sentinel; `service.ErrNotFound` aliases it so existing handler 404 mapping is unchanged; service/handler/format callers translate it where the old nil branch carried specific behavior (idempotent delete, proxy cache-miss, existence pre-checks, group-member validation). `nilnil` is now enabled to keep the convention from creeping back.
