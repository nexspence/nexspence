## Webhook handler tests (Phase 14B.1)

New `internal/api/handlers/webhooks_test.go` with 5 httptest-based cases:

- `GET /webhooks/:id` — 200 with full body, 404 on unknown ID
- `POST /webhooks/:id/test` — 200 with `TestResult` (status + latency_ms) against a live local receiver, 404 on unknown ID, 502 when delivery fails (connection refused)

## Webhook events: repo.updated and repo.deleted (Phase 14B.2+14B.3)

`RepositoryService` now dispatches webhook events on repository lifecycle changes:

- `repo.updated` fired after a successful `Update()` call
- `repo.deleted` fired after a successful `Delete()` call
- New domain constants: `EventRepoUpdated`, `EventRepoDeleted`
- Consistent with existing `repo.created` — fire-and-forget async dispatch, only when a `WebhookDispatcher` is wired

## Search — Last Downloaded timestamp

SearchPage now shows when an artifact was last downloaded:

- Main component row: small `↓ <date>` line under "Modified" date (only when the artifact has been downloaded at least once)
- Expanded asset rows: `↓ <date>` appended inline after `lastModified`
- Falls back to component-level `lastDownloaded` when asset-level is absent
- No backend changes — API already returned the field; frontend types updated to expose it

## Routing Rules (Phase 14C)

Group repositories now enforce routing rules during artifact resolution:

- Full CRUD API: `GET/POST/PUT/DELETE /service/rest/v1/routing-rules`
- `mode=BLOCK`: members whose paths match any regex matcher are skipped
- `mode=ALLOW`: only members whose paths match at least one matcher are tried
- Fail-open: missing or unconfigured rule allows all paths through
- AdminPage → Routing Rules tab: create/edit/delete rules with dynamic matcher list
- RepositoriesPage: group repo create/edit modals expose a Routing Rule selector
