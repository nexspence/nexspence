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
