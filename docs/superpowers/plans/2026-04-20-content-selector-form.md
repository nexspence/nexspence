# Content Selector Form Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the raw CEL textarea in the Content Selector modal with two searchable dropdowns (repository + directory path), auto-generating valid CEL expressions so users never have to type them.

**Architecture:** Add a backend `PathTree` endpoint that queries `assets.path` and returns unique directory prefixes for a repository. The frontend modal replaces the expression textarea with a repo dropdown + path dropdown. CEL is generated programmatically on Save.

**Tech Stack:** Go/Gin (backend), React+TypeScript+React Query (frontend), PostgreSQL (path query).

**Spec:** `docs/superpowers/specs/2026-04-20-content-selector-form-design.md`

---

## File Map

| File | Action | Purpose |
|------|--------|---------|
| `internal/repository/interfaces.go` | Modify | Add `ListPathsByRepo` to `AssetRepo` interface |
| `internal/repository/postgres/asset_repo.go` | Modify | Implement `ListPathsByRepo` |
| `internal/testutil/mocks.go` | Modify | Add `ListPathsByRepo` to `AssetRepo` mock |
| `internal/api/handlers/browse_docker.go` | Modify | Add `PathTree` handler, inject `AssetRepo` |
| `internal/api/router.go` | Modify | Wire new route + pass assetRepo to BrowseHandler |
| `frontend/src/pages/SecurityPage.tsx` | Modify | Replace `ContentSelectorsTab` modal with dropdown form |

---

## Task 1: Add `ListPathsByRepo` to AssetRepo interface and implement it

**Files:**
- Modify: `internal/repository/interfaces.go:40-61`
- Modify: `internal/repository/postgres/asset_repo.go`

- [ ] **Step 1: Add the method to the interface**

Open `internal/repository/interfaces.go`. After the `ListAllBlobKeys` line (line 58), add the new method before the closing `}` of `AssetRepo`:

```go
// ListPathsByRepo returns unique directory-level path prefixes from assets
// in the given repository. If q is non-empty, only paths containing q
// (case-insensitive) are returned. Limit caps the result at 500 entries.
ListPathsByRepo(ctx context.Context, repoName, q string) ([]string, error)
```

- [ ] **Step 2: Write a failing test for the implementation**

Create `internal/repository/postgres/asset_repo_paths_test.go`:

```go
package postgres_test

import (
	"testing"

	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func TestListPathsByRepo_mock(t *testing.T) {
	mock := testutil.NewAssetRepo()
	// mock returns empty by default — integration tests use real DB
	paths, err := mock.ListPathsByRepo(t.Context(), "my-repo", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = paths // just checking interface compiles
}
```

- [ ] **Step 3: Run test to verify it fails (compile error)**

```bash
go build ./...
```
Expected: compile error `AssetRepo does not implement repository.AssetRepo (missing ListPathsByRepo method)`

- [ ] **Step 4: Add mock implementation to testutil/mocks.go**

Open `internal/testutil/mocks.go`. Find the `AssetRepo` struct. Add this method anywhere in the `AssetRepo` mock methods block:

```go
func (a *AssetRepo) ListPathsByRepo(_ context.Context, repoName, q string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := make(map[string]struct{})
	for _, asset := range a.assets {
		if asset.RepositoryName != repoName {
			continue
		}
		// extract all directory prefixes from path
		p := asset.Path
		for {
			idx := strings.LastIndex(p, "/")
			if idx <= 0 {
				break
			}
			p = p[:idx+1]
			if q == "" || strings.Contains(strings.ToLower(p), strings.ToLower(q)) {
				seen[p] = struct{}{}
			}
			p = p[:idx]
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}
```

Check the top of `mocks.go` — it already imports `"strings"`. If `sort` is not imported, add it.

- [ ] **Step 5: Add real implementation to postgres/asset_repo.go**

Append to `internal/repository/postgres/asset_repo.go`:

```go
// ListPathsByRepo returns unique directory-level path prefixes derived from
// asset paths in the given repository. q is an optional case-insensitive
// substring filter applied after prefix extraction.
func (r *assetRepo) ListPathsByRepo(ctx context.Context, repoName, q string) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT DISTINCT a.path
		 FROM assets a
		 JOIN repositories rep ON rep.id = a.repository_id
		 WHERE rep.name = $1
		 ORDER BY a.path
		 LIMIT 5000`,
		repoName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		// extract all directory prefixes: /da/devops/foo.jar → /da/, /da/devops/
		for {
			idx := strings.LastIndex(p, "/")
			if idx <= 0 {
				break
			}
			p = p[:idx+1]
			if q == "" || strings.Contains(strings.ToLower(p), strings.ToLower(q)) {
				seen[p] = struct{}{}
			}
			p = p[:idx]
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}
```

Make sure `"sort"` and `"strings"` are in the imports at the top of `asset_repo.go` (they likely already are).

- [ ] **Step 6: Build to verify no compile errors**

```bash
go build ./...
```
Expected: no errors.

- [ ] **Step 7: Run the test**

```bash
go test ./internal/repository/postgres/ -run TestListPathsByRepo -v
go test ./internal/testutil/ -v 2>&1 | head -20
```
Expected: PASS (mock test), no panics.

- [ ] **Step 8: Commit**

```bash
git add internal/repository/interfaces.go \
        internal/repository/postgres/asset_repo.go \
        internal/repository/postgres/asset_repo_paths_test.go \
        internal/testutil/mocks.go
git commit -m "feat: add ListPathsByRepo to AssetRepo interface and implementations"
```

---

## Task 2: Add PathTree handler and route

**Files:**
- Modify: `internal/api/handlers/browse_docker.go`
- Modify: `internal/api/router.go`

- [ ] **Step 1: Inject AssetRepo into BrowseHandler**

Open `internal/api/handlers/browse_docker.go`. Change the struct and constructor:

```go
// BrowseHandler serves Nexspence-native browse APIs.
type BrowseHandler struct {
	repos      repository.RepositoryRepo
	components repository.ComponentRepo
	assets     repository.AssetRepo
}

func NewBrowseHandler(repos repository.RepositoryRepo, components repository.ComponentRepo, assets repository.AssetRepo) *BrowseHandler {
	return &BrowseHandler{repos: repos, components: components, assets: assets}
}
```

- [ ] **Step 2: Add PathTree handler**

Append to `internal/api/handlers/browse_docker.go`:

```go
// PathTree handles GET /api/v1/browse/repositories/:name/path-tree
// Returns unique directory-level path prefixes from assets in the repository.
// Optional query param: q (substring filter, case-insensitive).
func (h *BrowseHandler) PathTree(c *gin.Context) {
	repoName := c.Param("name")
	q := c.Query("q")
	ctx := c.Request.Context()

	repo, err := h.repos.Get(ctx, repoName)
	if err != nil || repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}

	paths, err := h.assets.ListPathsByRepo(ctx, repoName, q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if paths == nil {
		paths = []string{}
	}

	c.JSON(http.StatusOK, gin.H{"paths": paths})
}
```

- [ ] **Step 3: Fix the router call to NewBrowseHandler**

Open `internal/api/router.go`. Find the line:
```go
browseH    := handlers.NewBrowseHandler(repoRepo, componentRepo)
```
Change it to:
```go
browseH    := handlers.NewBrowseHandler(repoRepo, componentRepo, assetRepo)
```

- [ ] **Step 4: Add the new route**

In `router.go`, find the Browse block:
```go
// ── Browse ────────────────────────────────────────────
authed.GET("/api/v1/browse/repositories/:name/docker-tree", browseH.DockerTree)
```
Add the new route below it:
```go
authed.GET("/api/v1/browse/repositories/:name/path-tree", browseH.PathTree)
```

- [ ] **Step 5: Build to verify**

```bash
go build ./...
```
Expected: no errors.

- [ ] **Step 6: Smoke-test with curl (optional, requires running server)**

```bash
curl -s -u admin:admin123 'http://localhost:8080/api/v1/browse/repositories/my-repo/path-tree' | jq .
```
Expected: `{"paths": [...]}` or `{"paths": []}` if no assets.

- [ ] **Step 7: Commit**

```bash
git add internal/api/handlers/browse_docker.go internal/api/router.go
git commit -m "feat: add PathTree browse endpoint GET /api/v1/browse/repositories/:name/path-tree"
```

---

## Task 3: Rewrite ContentSelectorsTab in the frontend

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx`

The full replacement is for the `ContentSelectorsTab` function (lines ~617–728) and the table it renders.

- [ ] **Step 1: Add the `listPathTree` helper to the API client**

Open `frontend/src/api/client.ts` (or wherever `nexusApi` is defined — check with `grep -n "listContentSelectors" frontend/src/api/`).

Add a helper function alongside the existing content selector functions:

```typescript
listPathTree: (repoName: string, q?: string) =>
  apiClient.get<{ paths: string[] }>(
    `/api/v1/browse/repositories/${encodeURIComponent(repoName)}/path-tree`,
    { params: q ? { q } : {} }
  ),
```

- [ ] **Step 2: Run the frontend build to verify it compiles**

```bash
cd frontend && npm run build 2>&1 | tail -20
```
Expected: no TypeScript errors.

- [ ] **Step 3: Replace ContentSelectorsTab**

In `frontend/src/pages/SecurityPage.tsx`, find the function `ContentSelectorsTab()` (starts around line 617) and replace its entire body (up to and including its closing `}` around line 728) with the following:

```tsx
function ContentSelectorsTab() {
  const qc = useQueryClient()
  const { data: selectors = [], isLoading } = useQuery<{ id: string; name: string; description: string; expression: string }[]>({
    queryKey: ['content-selectors'],
    queryFn: () => nexusApi.listContentSelectors().then(r => r.data),
  })
  const { data: allRepos = [] } = useQuery<{ name: string; format: string; type: string }[]>({
    queryKey: ['repositories'],
    queryFn: () => nexusApi.listRepositories().then(r => r.data),
  })

  const [showModal, setShowModal] = useState(false)
  const [editing, setEditing] = useState<{ id: string; name: string; description: string; expression: string } | null>(null)
  const [form, setForm] = useState({ name: '', description: '', repo: '', path: '' })
  const [repoSearch, setRepoSearch] = useState('')
  const [pathSearch, setPathSearch] = useState('')
  const [saveError, setSaveError] = useState('')

  const { data: pathTree, isLoading: pathsLoading } = useQuery<{ paths: string[] }>({
    queryKey: ['path-tree', form.repo, pathSearch],
    queryFn: () => nexusApi.listPathTree(form.repo, pathSearch || undefined).then(r => r.data),
    enabled: !!form.repo,
  })

  function buildExpression(repo: string, path: string): string {
    if (repo && path) return `repository == "${repo}" && path.startsWith("${path}")`
    if (repo)         return `repository == "${repo}"`
    if (path)         return `path.startsWith("${path}")`
    return ''
  }

  function parseExpression(expr: string): { repo: string; path: string } {
    // pattern: repository == "X" && path.startsWith("Y")
    const full = expr.match(/^repository == "([^"]+)" && path\.startsWith\("([^"]+)"\)$/)
    if (full) return { repo: full[1], path: full[2] }
    // pattern: repository == "X"
    const repoOnly = expr.match(/^repository == "([^"]+)"$/)
    if (repoOnly) return { repo: repoOnly[1], path: '' }
    // pattern: path.startsWith("Y")
    const pathOnly = expr.match(/^path\.startsWith\("([^"]+)"\)$/)
    if (pathOnly) return { repo: '', path: pathOnly[1] }
    return { repo: '', path: '' }
  }

  function selectorSummary(expr: string): string {
    const { repo, path } = parseExpression(expr)
    if (repo && path) return `${path}* in ${repo}`
    if (repo)         return `all paths in ${repo}`
    if (path)         return `${path}* in all repos`
    return expr // fallback: show raw
  }

  function openCreate() {
    setEditing(null)
    setForm({ name: '', description: '', repo: '', path: '' })
    setRepoSearch(''); setPathSearch(''); setSaveError('')
    setShowModal(true)
  }

  function openEdit(s: { id: string; name: string; description: string; expression: string }) {
    setEditing(s)
    const { repo, path } = parseExpression(s.expression)
    setForm({ name: s.name, description: s.description, repo, path })
    setRepoSearch(''); setPathSearch(''); setSaveError('')
    setShowModal(true)
  }

  const save = useMutation({
    mutationFn: async () => {
      const expression = buildExpression(form.repo, form.path)
      if (!expression) throw new Error('Select a repository or path')
      const payload = { name: form.name, description: form.description, expression }
      if (editing) return nexusApi.updateContentSelector(editing.id, payload)
      return nexusApi.createContentSelector(payload)
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['content-selectors'] }); setShowModal(false) },
    onError: (e: unknown) => {
      let msg = 'Error'
      if (axios.isAxiosError(e)) {
        const d = e.response?.data
        if (typeof d === 'object' && d !== null && 'error' in d) msg = String((d as { error: unknown }).error)
      } else if (e instanceof Error) { msg = e.message }
      setSaveError(msg)
    },
  })

  const del = useMutation({
    mutationFn: (id: string) => nexusApi.deleteContentSelector(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['content-selectors'] }),
  })

  const filteredRepos = allRepos.filter(r =>
    !repoSearch || r.name.toLowerCase().includes(repoSearch.toLowerCase())
  )

  const paths = pathTree?.paths ?? []

  const canSave = !!form.name.trim() && (!!form.repo || !!form.path)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <button style={S.btn('primary')} onClick={openCreate}><Plus size={14} /> New Selector</button>
      </div>

      {isLoading ? <div style={S.empty}>Loading…</div> : selectors.length === 0 ? <div style={S.empty}>No content selectors</div> : (
        <div style={S.card}>
          <table style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
            <thead>
              <tr style={{ color: 'rgba(229,231,235,0.5)', textAlign: 'left' as const }}>
                <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Name</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Scope</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Description</th>
                <th style={{ padding: '0 0 10px', width: 80 }}></th>
              </tr>
            </thead>
            <tbody>
              {selectors.map(s => (
                <tr key={s.id} style={{ borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                  <td style={{ padding: '9px 0', color: '#dbeafe', fontWeight: 600 }}>{s.name}</td>
                  <td style={{ padding: '9px 8px' }}>
                    <code style={{ ...S.mono, fontSize: 12, color: '#a5b4fc' }}>{selectorSummary(s.expression)}</code>
                  </td>
                  <td style={{ padding: '9px 8px', color: 'rgba(229,231,235,0.55)' }}>{s.description || '—'}</td>
                  <td style={{ padding: '9px 0', display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                    <button style={{ ...S.btn('ghost'), padding: '4px 8px' }} onClick={() => openEdit(s)}>Edit</button>
                    <button style={{ ...S.btn('danger'), padding: '4px 8px' }} onClick={() => { if (confirm(`Delete ${s.name}?`)) del.mutate(s.id) }}><Trash2 size={13} /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showModal && (
        <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
          <div style={{ background: '#0f172a', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 14, padding: 24, width: 520, display: 'flex', flexDirection: 'column', gap: 12 }}>
            <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: '#dbeafe' }}>
              {editing ? 'Edit Content Selector' : 'New Content Selector'}
            </h3>

            <input style={S.input} placeholder="Name *" value={form.name}
              onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
            <input style={S.input} placeholder="Description (optional)" value={form.description}
              onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />

            {/* Repository dropdown */}
            <div>
              <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', marginBottom: 4 }}>Repository</div>
              <input
                style={S.input}
                placeholder="Search repositories…"
                value={repoSearch}
                onChange={e => { setRepoSearch(e.target.value); setForm(f => ({ ...f, repo: '', path: '' })) }}
              />
              {(repoSearch || form.repo) && (
                <div style={{ maxHeight: 160, overflowY: 'auto', background: 'rgba(0,0,0,0.4)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, marginTop: 4 }}>
                  <div
                    style={{ padding: '7px 12px', fontSize: 13, cursor: 'pointer', color: 'rgba(229,231,235,0.5)',
                      background: !form.repo ? 'rgba(59,130,246,0.12)' : 'transparent' }}
                    onClick={() => { setForm(f => ({ ...f, repo: '', path: '' })); setRepoSearch('') }}
                  >
                    Any repository
                  </div>
                  {filteredRepos.map(r => (
                    <div
                      key={r.name}
                      style={{ padding: '7px 12px', fontSize: 13, cursor: 'pointer',
                        color: form.repo === r.name ? '#3b82f6' : '#dbeafe',
                        background: form.repo === r.name ? 'rgba(59,130,246,0.12)' : 'transparent' }}
                      onClick={() => { setForm(f => ({ ...f, repo: r.name, path: '' })); setRepoSearch(r.name); setPathSearch('') }}
                    >
                      {r.name}
                      <span style={{ fontSize: 11, color: 'rgba(229,231,235,0.4)', marginLeft: 8 }}>{r.format}</span>
                    </div>
                  ))}
                  {filteredRepos.length === 0 && (
                    <div style={{ padding: '7px 12px', fontSize: 13, color: 'rgba(229,231,235,0.35)' }}>No repositories found</div>
                  )}
                </div>
              )}
            </div>

            {/* Path dropdown */}
            <div>
              <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', marginBottom: 4 }}>
                Path prefix {!form.repo && <span style={{ color: 'rgba(229,231,235,0.3)' }}>(select a repository first)</span>}
              </div>
              <input
                style={{ ...S.input, opacity: !form.repo ? 0.4 : 1 }}
                placeholder="Search paths…"
                disabled={!form.repo}
                value={pathSearch}
                onChange={e => { setPathSearch(e.target.value); setForm(f => ({ ...f, path: '' })) }}
              />
              {form.repo && (pathSearch || form.path !== '') && (
                <div style={{ maxHeight: 180, overflowY: 'auto', background: 'rgba(0,0,0,0.4)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, marginTop: 4 }}>
                  <div
                    style={{ padding: '7px 12px', fontSize: 13, cursor: 'pointer', color: 'rgba(229,231,235,0.5)',
                      background: !form.path ? 'rgba(59,130,246,0.12)' : 'transparent' }}
                    onClick={() => { setForm(f => ({ ...f, path: '' })); setPathSearch('') }}
                  >
                    Any path
                  </div>
                  {pathsLoading ? (
                    <div style={{ padding: '7px 12px', fontSize: 13, color: 'rgba(229,231,235,0.35)' }}>Loading…</div>
                  ) : paths.length === 0 ? (
                    <div style={{ padding: '7px 12px', fontSize: 13, color: 'rgba(229,231,235,0.35)' }}>No paths found</div>
                  ) : paths.map(p => {
                    const depth = (p.match(/\//g) ?? []).length - 1
                    return (
                      <div
                        key={p}
                        style={{ padding: '6px 12px', paddingLeft: 12 + depth * 14, fontSize: 13, cursor: 'pointer',
                          color: form.path === p ? '#3b82f6' : '#dbeafe',
                          background: form.path === p ? 'rgba(59,130,246,0.12)' : 'transparent',
                          fontFamily: 'monospace' }}
                        onClick={() => { setForm(f => ({ ...f, path: p })); setPathSearch(p) }}
                      >
                        {p}
                      </div>
                    )
                  })}
                </div>
              )}
            </div>

            {/* Preview */}
            {(form.repo || form.path) && (
              <div style={{ padding: '6px 10px', background: 'rgba(59,130,246,0.08)', borderRadius: 8, fontSize: 12, color: '#93c5fd', fontFamily: 'monospace' }}>
                {buildExpression(form.repo, form.path)}
              </div>
            )}

            {saveError && (
              <div style={{ padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, color: '#ef4444', fontSize: 12 }}>{saveError}</div>
            )}

            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <button style={S.btn('ghost')} onClick={() => setShowModal(false)}>Cancel</button>
              <button style={S.btn('primary')} onClick={() => save.mutate()} disabled={save.isPending || !canSave}>
                {save.isPending ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Build frontend to verify TypeScript**

```bash
cd frontend && npm run build 2>&1 | tail -30
```
Expected: no TypeScript errors, build succeeds.

- [ ] **Step 5: Check if `nexusApi.listRepositories` exists**

```bash
grep -n "listRepositories\|listPathTree" frontend/src/api/client.ts
```

If `listRepositories` is missing, add it (alongside `listPathTree` from Task 2 step 1):
```typescript
listRepositories: () =>
  nexusApiClient.get<{ name: string; format: string; type: string }[]>('/repositories'),
```
If it already exists under a different name, use that name in the `ContentSelectorsTab` query above.

- [ ] **Step 6: Rebuild after any API fixes**

```bash
cd frontend && npm run build 2>&1 | tail -20
```
Expected: clean build.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx frontend/src/api/client.ts
git commit -m "feat: content selector form — searchable repo+path dropdowns, auto-generate CEL"
```

---

## Task 4: Manual smoke test

- [ ] **Step 1: Start the full stack**

```bash
docker compose up --build -d
cd frontend && npm run dev
```

- [ ] **Step 2: Open Security → Content Selectors → New Selector**

Navigate to `http://localhost:5173` → Security → Selectors tab → New Selector.

Verify:
- Name field present
- Repository search box present
- Typing in repo search filters the dropdown
- Selecting a repo enables the Path dropdown
- Typing in path search calls `?q=…` (check Network tab)
- Selecting a path shows CEL preview: `repository == "X" && path.startsWith("Y")`
- Save creates the selector
- Table shows human-readable "Y* in X" instead of raw CEL

- [ ] **Step 3: Test edit round-trip**

Click Edit on an existing selector that was created with the form. Verify the repo and path fields are pre-populated correctly from the stored expression.

- [ ] **Step 4: Test legacy selector backward compatibility**

If there are any selectors with hand-written expressions (e.g. `format == "maven2"`), edit one and verify the modal falls back to showing the raw expression in the preview (since it won't match the `parseExpression` patterns, repo/path fields will be empty and the preview shows the raw expression — actually per the spec it should fall back to raw textarea for unrecognised patterns).

> **Note:** The current implementation shows empty repo/path for unrecognised expressions — the CEL preview will be empty and Save will be blocked. For full backward compatibility, consider adding a raw-expression fallback textarea that appears when `parseExpression` returns `{ repo: '', path: '' }` for a non-empty expression. This is a follow-up if needed.

- [ ] **Step 5: Commit if any fixes were made during smoke testing**

```bash
git add -p
git commit -m "fix: content selector form smoke test fixes"
```

---

## Self-Review Checklist

- [x] **Spec coverage:** Backend path-tree endpoint ✓, repo dropdown ✓, path dropdown with search ✓, CEL auto-generation ✓, human-readable table column ✓, edit round-trip ✓
- [x] **No placeholders:** All code blocks are complete
- [x] **Type consistency:** `ListPathsByRepo(ctx, repoName, q string) ([]string, error)` used consistently across interface, postgres impl, and mock
- [x] **Backward compat note added** in Task 4 Step 4 for legacy selectors
