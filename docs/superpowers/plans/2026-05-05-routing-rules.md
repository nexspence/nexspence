# Routing Rules Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the already-built RoutingRule backend into the router and group handler, then add a management UI in AdminPage and a selector in the repo create/edit modals.

**Architecture:** Three backend files touched (deps.go, router.go, group/handler.go); three frontend files touched (client.ts, AdminPage.tsx, RepositoriesPage.tsx). The service/repo/handler layers already exist — this plan connects them.

**Tech Stack:** Go (Gin), React + TypeScript, React Query, Zustand, lucide-react, holo component library.

---

## Files

| File | Change |
|------|--------|
| `internal/formats/deps.go` | add `RoutingRules repository.RoutingRuleRepo` field |
| `internal/api/router.go` | instantiate rrRepo/rrSvc/rrH, replace stub, add 5 routes, add to formatDeps |
| `internal/formats/group/handler.go` | load rule in `serveGet`, skip members when `Allow` returns false |
| `internal/formats/group/handler_test.go` | add two tests: BLOCK and ALLOW rule enforcement |
| `frontend/src/api/client.ts` | add `RoutingRule` interface + 4 CRUD methods to `nexusApi` |
| `frontend/src/pages/AdminPage.tsx` | add `'routing-rules'` tab + inline `RoutingRulesTab` component |
| `frontend/src/pages/RepositoriesPage.tsx` | add `routingRuleId` to create/edit form, show selector only for group repos |

---

## Task 1: Add RoutingRules to formats.Deps

**Files:**
- Modify: `internal/formats/deps.go`

- [ ] **Step 1: Add the field**

Open `internal/formats/deps.go`. Add one field after `Webhooks`:

```go
// Deps holds all dependencies injected into every format handler.
type Deps struct {
	Repos      repository.RepositoryRepo
	Components repository.ComponentRepo
	Assets     repository.AssetRepo
	Blobs      repository.BlobStoreRepo
	BlobStore  storage.BlobStore    // default / fallback store
	Registry   *storage.Registry   // optional: per-blob-store routing; nil disables
	BaseURL    string
	// Webhooks is optional — nil disables event delivery.
	Webhooks     domain.WebhookDispatcher
	// RoutingRules is optional — nil disables routing rule enforcement in group repos.
	RoutingRules repository.RoutingRuleRepo
}
```

- [ ] **Step 2: Verify build**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core && go build ./...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/formats/deps.go
git commit -m "feat(routing-rules): add RoutingRules field to formats.Deps"
```

---

## Task 2: Wire router — instantiate and register routes

**Files:**
- Modify: `internal/api/router.go`

- [ ] **Step 1: Instantiate repo/service/handler**

In `router.go`, after the existing repo instantiations (around line 58, after `csRepo`), add:

```go
rrRepo := postgres.NewRoutingRuleRepo(pool)
rrSvc  := service.NewRoutingRuleService(rrRepo)
rrH    := handlers.NewRoutingRuleHandler(rrSvc)
```

- [ ] **Step 2: Add RoutingRules to formatDeps**

In the `formatDeps` struct literal (around line 114), add the new field:

```go
formatDeps := formats.Deps{
    Repos:        repoRepo,
    Components:   componentRepo,
    Assets:       assetRepo,
    Blobs:        blobRepo,
    BlobStore:    localBlob,
    Registry:     blobRegistry,
    BaseURL:      cfg.HTTP.BaseURL,
    Webhooks:     webhookSvc,
    RoutingRules: rrRepo,
}
```

- [ ] **Step 3: Replace stub with full routes**

Find and replace this line (around line 397):

```go
admin.GET("/service/rest/v1/routing-rules", stubHandler("routing"))
```

with:

```go
admin.GET("/service/rest/v1/routing-rules",        rrH.List)
admin.GET("/service/rest/v1/routing-rules/:id",    rrH.Get)
admin.POST("/service/rest/v1/routing-rules",       rrH.Create)
admin.PUT("/service/rest/v1/routing-rules/:id",    rrH.Update)
admin.DELETE("/service/rest/v1/routing-rules/:id", rrH.Delete)
```

- [ ] **Step 4: Verify build**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core && go build ./...
```

Expected: no errors.

- [ ] **Step 5: Run existing tests**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core && go test ./internal/service/... -run TestRoutingRule -v
```

Expected: all existing routing rule service tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/api/router.go
git commit -m "feat(routing-rules): wire router — rrRepo/rrSvc/rrH, 5 routes, formatDeps"
```

---

## Task 3: Group handler — enforce routing rule in serveGet

**Files:**
- Modify: `internal/formats/group/handler.go`
- Modify: `internal/formats/group/handler_test.go`

- [ ] **Step 1: Write failing tests**

Open `internal/formats/group/handler_test.go`. The existing `buildEngine` helper doesn't pass `RoutingRules` to `formats.Deps`. Add a new helper and two tests at the end of the file.

**Important:** use a single engine per test so the blob store is shared between the PUT (upload to member) and GET (read via group). Two separate engines would have separate in-memory blob stores.

Add `"context"` to the existing import block (it's not there yet).

```go
func makeBlockRule(id string, matchers ...string) *domain.RoutingRule {
	return &domain.RoutingRule{ID: id, Mode: "BLOCK", Matchers: matchers}
}

func makeAllowRule(id string, matchers ...string) *domain.RoutingRule {
	return &domain.RoutingRule{ID: id, Mode: "ALLOW", Matchers: matchers}
}

func buildEngineWithRule(rule *domain.RoutingRule, repos ...*domain.Repository) *gin.Engine {
	repoRepo := testutil.NewRepoRepo(repos...)
	rrRepo := testutil.NewRoutingRuleRepo()
	if rule != nil {
		_ = rrRepo.Create(context.Background(), rule)
	}
	d := formats.Deps{
		Repos:        repoRepo,
		Blobs:        testutil.NewBlobStoreRepo(),
		Components:   testutil.NewComponentRepo(),
		Assets:       testutil.NewAssetRepo(),
		BlobStore:    testutil.NewBlobStore(),
		BaseURL:      "http://localhost:8080",
		RoutingRules: rrRepo,
	}

	rawH := raw.New(d)
	registry := map[string]formats.FormatHandler{"raw": rawH}
	groupH := group.New(d, registry)

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repoName := c.Param("repoName")
		repo, _ := repoRepo.Get(c.Request.Context(), repoName)
		if repo == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
		} else {
			rawH.ServeHTTP(c)
		}
	})
	return r
}

func TestGroupHandler_RoutingRule_BlocksPath(t *testing.T) {
	// BLOCK rule skips members whose path matches the regex.
	// The rule is applied at GET time on the group; PUT to the member directly is unaffected.
	rule := makeBlockRule("rule-1", `.*-SNAPSHOT.*`)

	member := testutil.SimpleRepo("snapshots", "raw")
	ruleID := "rule-1"
	grp := &domain.Repository{
		ID: "repo-grp", Name: "mygroup", Format: "raw",
		Type: domain.TypeGroup, Online: true,
		RoutingRuleID: &ruleID,
		FormatConfig:  map[string]any{"member_names": []any{"snapshots"}},
	}

	// Single engine — same blob store for upload and group read.
	r := buildEngineWithRule(rule, member, grp)

	// Upload directly to member (PUT on member, not group — routing rule only filters GET on group).
	require.Equal(t, http.StatusCreated, put(r, "snapshots", "/foo-1.0-SNAPSHOT.jar", "data"))

	// GET via group — BLOCK rule matches the path → member skipped → 404.
	req := httptest.NewRequest(http.MethodGet, "/repository/mygroup/foo-1.0-SNAPSHOT.jar", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGroupHandler_RoutingRule_AllowsMatchingPath(t *testing.T) {
	// ALLOW rule: only paths matching the regex reach the member.
	rule := makeAllowRule("rule-2", `^/releases/`)

	member := testutil.SimpleRepo("releases", "raw")
	ruleID := "rule-2"
	grp := &domain.Repository{
		ID: "repo-grp2", Name: "mygroup2", Format: "raw",
		Type: domain.TypeGroup, Online: true,
		RoutingRuleID: &ruleID,
		FormatConfig:  map[string]any{"member_names": []any{"releases"}},
	}

	r := buildEngineWithRule(rule, member, grp)

	// Upload to member directly.
	require.Equal(t, http.StatusCreated, put(r, "releases", "/releases/foo-1.0.jar", "data"))

	// GET via group — path matches ALLOW rule → member tried → 200.
	req := httptest.NewRequest(http.MethodGet, "/repository/mygroup2/releases/foo-1.0.jar", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// GET via group — path does NOT match ALLOW rule → member skipped → 404.
	req2 := httptest.NewRequest(http.MethodGet, "/repository/mygroup2/snapshots/bar.jar", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusNotFound, w2.Code)
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core && go test ./internal/formats/group/... -run TestGroupHandler_RoutingRule -v
```

Expected: FAIL — group handler doesn't load or apply the routing rule yet.

- [ ] **Step 3: Implement routing rule enforcement in serveGet**

In `internal/formats/group/handler.go`, add the import for `service` package at the top:

```go
import (
    "fmt"
    "io"
    "net/http"
    "net/http/httptest"

    "github.com/gin-gonic/gin"
    "github.com/nexspence-oss/nexspence/internal/domain"
    "github.com/nexspence-oss/nexspence/internal/formats"
    "github.com/nexspence-oss/nexspence/internal/service"
)
```

In `serveGet`, after loading `members` and before the `for _, memberName := range members` loop, add:

```go
var rule *domain.RoutingRule
if repoDef.RoutingRuleID != nil && h.deps.RoutingRules != nil {
    rule, _ = h.deps.RoutingRules.Get(ctx, *repoDef.RoutingRuleID)
}
```

Then, as the very first check inside the member loop (before the `memberRepo` fetch), add:

```go
if !service.Allow(rule, filePath) {
    continue
}
```

The full updated loop start looks like:

```go
for _, memberName := range members {
    if !service.Allow(rule, filePath) {
        continue
    }
    memberRepo, err := h.deps.Repos.Get(ctx, memberName)
    if err != nil || memberRepo == nil || !memberRepo.Online {
        continue
    }
    // ... rest unchanged
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core && go test ./internal/formats/group/... -v
```

Expected: all tests pass including the two new ones.

- [ ] **Step 5: Run full test suite**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core && go test ./... 2>&1 | tail -20
```

Expected: no failures.

- [ ] **Step 6: Commit**

```bash
git add internal/formats/group/handler.go internal/formats/group/handler_test.go
git commit -m "feat(routing-rules): enforce routing rule in group serveGet"
```

---

## Task 4: Frontend API types and helpers

**Files:**
- Modify: `frontend/src/api/client.ts`

- [ ] **Step 1: Add RoutingRule interface**

Near the top of `client.ts`, after the existing interfaces (e.g. after `BlobStoreMigration`), add:

```ts
export interface RoutingRule {
  id: string
  name: string
  description?: string
  mode: 'ALLOW' | 'BLOCK'
  matchers: string[]
  createdAt: string
  updatedAt: string
}

export type RoutingRuleInput = Omit<RoutingRule, 'id' | 'createdAt' | 'updatedAt'>
```

- [ ] **Step 2: Add CRUD methods to nexusApi**

In the `nexusApi` object, after the cleanup policies section (around line 229), add:

```ts
// Routing rules
listRoutingRules: () =>
  apiClient.get<RoutingRule[]>('/service/rest/v1/routing-rules'),
createRoutingRule: (data: RoutingRuleInput) =>
  apiClient.post<RoutingRule>('/service/rest/v1/routing-rules', data),
updateRoutingRule: (id: string, data: RoutingRuleInput) =>
  apiClient.put<RoutingRule>(`/service/rest/v1/routing-rules/${id}`, data),
deleteRoutingRule: (id: string) =>
  apiClient.delete(`/service/rest/v1/routing-rules/${id}`),
```

- [ ] **Step 3: TypeScript check**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core/frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/api/client.ts
git commit -m "feat(routing-rules): add RoutingRule types and API helpers to client.ts"
```

---

## Task 5: AdminPage — Routing Rules tab

**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

- [ ] **Step 1: Extend AdminTab type and add tab item**

Find line 26:
```ts
type AdminTab = 'info' | 'blobs' | 'backup' | 'monitoring' | 'migration'
const VALID_TABS: AdminTab[] = ['info', 'blobs', 'backup', 'monitoring', 'migration']
```

Replace with:
```ts
type AdminTab = 'info' | 'blobs' | 'backup' | 'monitoring' | 'migration' | 'routing-rules'
const VALID_TABS: AdminTab[] = ['info', 'blobs', 'backup', 'monitoring', 'migration', 'routing-rules']
```

- [ ] **Step 2: Add tab label**

In the `HoloTabs` items array (around line 165), add after the migration entry:

```tsx
{ value: 'routing-rules', label: <><GitBranch size={13} style={{ marginRight: 5 }} />Routing Rules</> },
```

Add `GitBranch` to the lucide import on line 4 (it already imports from `lucide-react`).

- [ ] **Step 3: Add RoutingRulesTab component and render it**

Before the `export default function AdminPage()` declaration, add the `RoutingRulesTab` component:

```tsx
function RoutingRulesTab() {
  const qc = useQueryClient()
  const { data: rules = [], isLoading } = useQuery<RoutingRule[]>({
    queryKey: ['routing-rules'],
    queryFn: () => nexusApi.listRoutingRules().then(r => r.data),
  })

  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<RoutingRule | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<RoutingRule | null>(null)
  const [form, setForm] = useState<{ name: string; description: string; mode: 'ALLOW' | 'BLOCK'; matchers: string[] }>({
    name: '', description: '', mode: 'ALLOW', matchers: [''],
  })
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  const openCreate = () => {
    setEditing(null)
    setForm({ name: '', description: '', mode: 'ALLOW', matchers: [''] })
    setErr('')
    setModalOpen(true)
  }

  const openEdit = (r: RoutingRule) => {
    setEditing(r)
    setForm({ name: r.name, description: r.description ?? '', mode: r.mode, matchers: r.matchers.length ? r.matchers : [''] })
    setErr('')
    setModalOpen(true)
  }

  const setMatcher = (i: number, v: string) =>
    setForm(f => { const m = [...f.matchers]; m[i] = v; return { ...f, matchers: m } })

  const addMatcher = () =>
    setForm(f => ({ ...f, matchers: [...f.matchers, ''] }))

  const removeMatcher = (i: number) =>
    setForm(f => ({ ...f, matchers: f.matchers.filter((_, idx) => idx !== i) }))

  const handleSave = async () => {
    setErr('')
    if (!form.name.trim()) { setErr('Name is required'); return }
    const matchers = form.matchers.filter(m => m.trim())
    const payload: RoutingRuleInput = {
      name: form.name.trim(),
      description: form.description.trim() || undefined,
      mode: form.mode,
      matchers,
    }
    setSaving(true)
    try {
      if (editing) {
        await nexusApi.updateRoutingRule(editing.id, payload)
      } else {
        await nexusApi.createRoutingRule(payload)
      }
      qc.invalidateQueries({ queryKey: ['routing-rules'] })
      setModalOpen(false)
    } catch (e: any) {
      setErr(e.response?.data?.error ?? 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    try {
      await nexusApi.deleteRoutingRule(deleteTarget.id)
      qc.invalidateQueries({ queryKey: ['routing-rules'] })
    } finally {
      setDeleteTarget(null)
    }
  }

  const modeBadge = (mode: string) => (
    <span style={{
      fontSize: 11, fontWeight: 600, padding: '2px 8px', borderRadius: 4,
      background: mode === 'ALLOW' ? 'rgba(59,130,246,0.15)' : 'rgba(245,158,11,0.15)',
      color: mode === 'ALLOW' ? '#60a5fa' : '#fbbf24',
      border: `1px solid ${mode === 'ALLOW' ? 'rgba(59,130,246,0.3)' : 'rgba(245,158,11,0.3)'}`,
    }}>{mode}</span>
  )

  return (
    <HoloCard>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
        <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
          Routing Rules
        </span>
        <HoloButton variant="primary" icon={<Plus size={13} />} onClick={openCreate}>
          Create Routing Rule
        </HoloButton>
      </div>

      {isLoading ? (
        <div className="holo-skeleton holo-skeleton--text" style={{ width: '60%' }} />
      ) : rules.length === 0 ? (
        <div style={{ color: 'var(--holo-text-faint)', fontSize: 13, textAlign: 'center', padding: '24px 0' }}>
          No routing rules configured
        </div>
      ) : (
        <table style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
          <thead>
            <tr style={{ borderBottom: '1px solid rgba(255,255,255,0.08)' }}>
              {['Name', 'Mode', 'Matchers', 'Actions'].map(h => (
                <th key={h} style={{ textAlign: 'left' as const, padding: '6px 10px', color: 'var(--holo-text-dim)', fontWeight: 600, fontSize: 11, textTransform: 'uppercase' as const }}>{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rules.map(r => (
              <tr key={r.id} style={{ borderBottom: '1px solid rgba(255,255,255,0.04)' }}>
                <td style={{ padding: '8px 10px', color: 'var(--holo-text)', fontWeight: 500 }}>{r.name}</td>
                <td style={{ padding: '8px 10px' }}>{modeBadge(r.mode)}</td>
                <td style={{ padding: '8px 10px', color: 'var(--holo-text-dim)', fontFamily: 'monospace', fontSize: 11 }}>
                  {r.matchers.length === 0 ? '—' : r.matchers.slice(0, 2).join(', ') + (r.matchers.length > 2 ? ` +${r.matchers.length - 2}` : '')}
                </td>
                <td style={{ padding: '8px 10px' }}>
                  <div style={{ display: 'flex', gap: 6 }}>
                    <HoloButton icon={<Pencil size={12} />} onClick={() => openEdit(r)}>Edit</HoloButton>
                    <HoloButton icon={<Trash2 size={12} />} onClick={() => setDeleteTarget(r)}>Delete</HoloButton>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {/* Create / Edit modal */}
      <HoloModal open={modalOpen} onClose={() => setModalOpen(false)}
        title={editing ? `Edit — ${editing.name}` : 'Create Routing Rule'}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14, minWidth: 420 }}>
          <div>
            <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>NAME *</label>
            <HoloInput value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="block-snapshots" />
          </div>
          <div>
            <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>DESCRIPTION</label>
            <HoloInput value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} placeholder="Optional" />
          </div>
          <div>
            <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>MODE *</label>
            <Select
              value={form.mode}
              onChange={v => setForm(f => ({ ...f, mode: v as 'ALLOW' | 'BLOCK' }))}
              options={[
                { value: 'ALLOW', label: 'ALLOW — only matching paths pass' },
                { value: 'BLOCK', label: 'BLOCK — matching paths are skipped' },
              ]}
            />
          </div>
          <div>
            <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>MATCHERS (regex)</label>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              {form.matchers.map((m, i) => (
                <div key={i} style={{ display: 'flex', gap: 6 }}>
                  <HoloInput
                    value={m}
                    onChange={e => setMatcher(i, e.target.value)}
                    placeholder=".*-SNAPSHOT.*"
                    style={{ flex: 1, fontFamily: 'monospace', fontSize: 12 }}
                  />
                  {form.matchers.length > 1 && (
                    <HoloButton icon={<X size={12} />} onClick={() => removeMatcher(i)} />
                  )}
                </div>
              ))}
              <HoloButton icon={<Plus size={12} />} onClick={addMatcher} style={{ alignSelf: 'flex-start' }}>
                Add matcher
              </HoloButton>
            </div>
          </div>
          {err && <div style={{ color: '#ef4444', fontSize: 12 }}>{err}</div>}
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 4 }}>
            <HoloButton onClick={() => setModalOpen(false)}>Cancel</HoloButton>
            <HoloButton variant="primary" onClick={handleSave} disabled={saving}>
              {saving ? 'Saving…' : editing ? 'Save' : 'Create'}
            </HoloButton>
          </div>
        </div>
      </HoloModal>

      {/* Delete confirmation */}
      <HoloModal open={!!deleteTarget} onClose={() => setDeleteTarget(null)} title="Delete Routing Rule">
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16, minWidth: 360 }}>
          <p style={{ margin: 0, fontSize: 13, color: 'var(--holo-text)' }}>
            Delete <strong>{deleteTarget?.name}</strong>? Repositories using this rule will have it removed automatically.
          </p>
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
            <HoloButton onClick={() => setDeleteTarget(null)}>Cancel</HoloButton>
            <HoloButton variant="danger" onClick={handleDelete}>Delete</HoloButton>
          </div>
        </div>
      </HoloModal>
    </HoloCard>
  )
}
```

- [ ] **Step 4: Import RoutingRule types and render tab**

At the top of `AdminPage.tsx`, add `RoutingRule, RoutingRuleInput` to the import from `@/api/client`:

```ts
import { nexusApi, nexspenceApi, ImportRepoStats, ServiceStatus, RoutingRule, RoutingRuleInput } from '@/api/client'
```

Add `GitBranch, Plus, Pencil, Trash2, X` to the lucide import if not already present. `Plus` and `Pencil` and `Trash2` may already exist — check and add only what's missing.

After the `{tab === 'migration' && ...}` block, add:

```tsx
{tab === 'routing-rules' && <RoutingRulesTab />}
```

- [ ] **Step 5: TypeScript check**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core/frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/AdminPage.tsx
git commit -m "feat(routing-rules): add Routing Rules tab to AdminPage"
```

---

## Task 6: RepositoriesPage — routing rule selector in group repo modals

**Files:**
- Modify: `frontend/src/pages/RepositoriesPage.tsx`

- [ ] **Step 1: Add routingRuleId to the Repository interface**

Find the `interface Repository` definition (around line 12). Add:

```ts
routingRuleId?: string | null
```

- [ ] **Step 2: Fetch routing rules**

In `CreateRepoModal`, alongside the existing `blobStores` query (around line 387), add:

```ts
const { data: routingRules = [] } = useQuery<RoutingRule[]>({
  queryKey: ['routing-rules'],
  queryFn: () => nexusApi.listRoutingRules().then(r => r.data),
})
```

Add `RoutingRule` to the import from `@/api/client` at line 6.

- [ ] **Step 3: Add routingRuleId to create form state**

In the `useState` for `form` (around line 394), add:

```ts
const [form, setForm] = useState({
  name: '', format: 'maven2', type: 'hosted', description: '',
  remoteUrl: PROXY_DEFAULTS['maven2'],
  memberNames: [] as string[],
  cleanupPolicyIds: [] as string[],
  quotaGB: '',
  allowAnonymous: false,
  blobStoreId: '',
  routingRuleId: '' as string,
})
```

- [ ] **Step 4: Include routingRuleId in create payload**

In `handleFinish`, after the `formatConfig` assignment (around line 459), add:

```ts
if (form.type === 'group' && form.routingRuleId) {
  body.routingRuleId = form.routingRuleId
}
```

- [ ] **Step 5: Render the selector in the wizard**

In the wizard step where member names are shown (step for group repos), after the members checklist section, add the routing rule selector. Find the section that renders member selection (around line 540) and add after it:

```tsx
{form.type === 'group' && (
  <div style={{ marginTop: 12 }}>
    <label style={{ fontSize: 11, fontWeight: 600, color: 'var(--holo-text-dim)', display: 'block', marginBottom: 5 }}>
      ROUTING RULE
    </label>
    <Select
      value={form.routingRuleId}
      onChange={v => setField('routingRuleId', v)}
      options={[
        { value: '', label: 'None' },
        ...routingRules.map(r => ({ value: r.id, label: `${r.name} (${r.mode})` })),
      ]}
    />
  </div>
)}
```

- [ ] **Step 6: Add routingRuleId to EditRepoModal**

Find `EditRepoModal` component. Add the same `routingRules` query:

```ts
const { data: routingRules = [] } = useQuery<RoutingRule[]>({
  queryKey: ['routing-rules'],
  queryFn: () => nexusApi.listRoutingRules().then(r => r.data),
})
```

In the edit form state initialization, populate from the existing repo:

```ts
routingRuleId: repo.routingRuleId ?? '',
```

In the save payload, include:

```ts
if (form.type === 'group') {
  body.routingRuleId = form.routingRuleId || null
}
```

Render the same selector (only when `form.type === 'group'`) in the edit modal form, in the same position (after members list).

- [ ] **Step 7: TypeScript check**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core/frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/pages/RepositoriesPage.tsx
git commit -m "feat(routing-rules): add routing rule selector to group repo create/edit modals"
```

---

## Task 7: Wrap up

- [ ] **Step 1: Full backend test suite**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core && go test ./... 2>&1 | tail -5
```

Expected: all tests pass.

- [ ] **Step 2: Frontend build**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core/frontend && npm run build 2>&1 | tail -10
```

Expected: build succeeds, no TS errors.

- [ ] **Step 3: Mark Phase 14C complete in task_plan.md**

Find and update the Phase 14C tasks in `task_plan.md` (around line 461). Change all `- [ ]` to `- [x]`:

```
- [x] `RoutingRuleRepo` interface + postgres implementation (table `routing_rules` in schema)
- [x] `RoutingRuleService` — Create / List / Get / Update / Delete
- [x] Routes: `GET/POST /service/rest/v1/routing-rules`, `GET/PUT/DELETE /service/rest/v1/routing-rules/:name`
- [x] Group handler honours `routing_rule` on the repository — skips members that don't match the rule's path-matchers
```

Also update the status line above:
```
**Status:** complete (2026-05-05)
```

- [ ] **Step 4: Append to NEXT_RELEASE.md**

```markdown
## Routing Rules (Phase 14C)

Group repositories now enforce routing rules during artifact resolution:

- Full CRUD API: `GET/POST/PUT/DELETE /service/rest/v1/routing-rules`
- `mode=BLOCK`: members whose paths match any regex matcher are skipped
- `mode=ALLOW`: only members whose paths match at least one matcher are tried
- Fail-open: missing or unconfigured rule allows all paths through
- AdminPage → Routing Rules tab: create/edit/delete rules with dynamic matcher list
- RepositoriesPage: group repo create/edit modals expose a Routing Rule selector
```

- [ ] **Step 5: Final commit**

```bash
git add task_plan.md NEXT_RELEASE.md
git commit -m "chore: mark Phase 14C complete, update NEXT_RELEASE.md"
```
