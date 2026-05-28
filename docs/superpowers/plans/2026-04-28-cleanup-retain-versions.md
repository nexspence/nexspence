# Phase 44: Retain N Versions — Cleanup Policy Extension

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `retainNVersions` field to cleanup policies so the cleanup engine never deletes the N newest versions of each artifact (by `group_id + name`), even if they match other stale criteria.

**Architecture:** New `retain_n_versions INT` column on `cleanup_policies`. When non-zero, `AssetRepo.ListStale` prepends a CTE that identifies the N newest component versions per `(repository_id, group_id, name)` using a window function, then excludes those component IDs from the stale candidates. The service reads `p.RetainNVersions` and passes it through to the repo. Frontend adds the field to the wizard Step 2 and the Edit modal.

**Tech Stack:** Go 1.22, pgx/v5, React 18 + TypeScript, goose migrations.

---

## File Map

| File | Action |
|------|--------|
| `internal/db/migrations/012_cleanup_retain_versions.sql` | Create — DB migration |
| `internal/domain/types.go` | Modify — add `RetainNVersions int` to `CleanupPolicy` |
| `internal/repository/interfaces.go` | Modify — update `ListStale` signature (+1 param) |
| `internal/repository/postgres/cleanup_repo.go` | Modify — include column in all 4 queries |
| `internal/repository/postgres/asset_repo.go` | Modify — CTE exclusion in `ListStale` |
| `internal/testutil/mocks.go` | Modify — update `ListStale` signature + add `LastRetainN` |
| `internal/service/cleanup_service.go` | Modify — pass `p.RetainNVersions` to `ListStale` |
| `internal/service/cleanup_service_test.go` | Modify — add retain test |
| `frontend/src/pages/CleanupPage.tsx` | Modify — new field in form, wizard, edit modal, card |

---

## Task 1: DB migration + domain field

**Files:**
- Create: `internal/db/migrations/012_cleanup_retain_versions.sql`
- Modify: `internal/domain/types.go`

- [ ] **Step 1: Create migration file**

```sql
-- internal/db/migrations/012_cleanup_retain_versions.sql
-- +goose Up
ALTER TABLE cleanup_policies ADD COLUMN retain_n_versions INT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE cleanup_policies DROP COLUMN retain_n_versions;
```

- [ ] **Step 2: Add field to domain type**

In `internal/domain/types.go`, find `CleanupPolicy` struct (around line 344) and add the field after `DryRun`:

```go
type CleanupPolicy struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Description     string         `json:"description,omitempty"`
	Format          string         `json:"format"`
	Criteria        map[string]any `json:"criteria"`
	ScheduleCron    string         `json:"scheduleCron,omitempty"`
	Enabled         bool           `json:"enabled"`
	DryRun          bool           `json:"dryRun"`
	RetainNVersions int            `json:"retainNVersions,omitempty"`
	LastRunAt       *time.Time     `json:"lastRunAt,omitempty"`
	LastRunFreed    int64          `json:"lastRunFreedBytes,omitempty"`
	LastRunCount    int            `json:"lastRunCount,omitempty"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
}
```

- [ ] **Step 3: Verify build**

```bash
cd /home/skensel/AI/self_nexus && go build ./internal/domain/...
```

Expected: no output (success).

- [ ] **Step 4: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add internal/db/migrations/012_cleanup_retain_versions.sql internal/domain/types.go
git commit -m "feat(cleanup): add retain_n_versions column + domain field"
```

---

## Task 2: Update CleanupPolicyRepo + ListStale interface + mock

**Files:**
- Modify: `internal/repository/interfaces.go`
- Modify: `internal/repository/postgres/cleanup_repo.go`
- Modify: `internal/testutil/mocks.go`

- [ ] **Step 1: Update ListStale interface signature**

In `internal/repository/interfaces.go`, replace the `ListStale` line (around line 62):

Old:
```go
ListStale(ctx context.Context, format string, repoNames []string, lastDownloadedDays, artifactAgeDays int, pathPrefix, nameGlob string, limit int) ([]domain.Asset, error)
```

New:
```go
// retainNVersions — when > 0, the N newest versions of each (group_id, name) are excluded from results.
ListStale(ctx context.Context, format string, repoNames []string, lastDownloadedDays, artifactAgeDays int, pathPrefix, nameGlob string, retainNVersions int, limit int) ([]domain.Asset, error)
```

- [ ] **Step 2: Update CleanupPolicyRepo — List query**

In `internal/repository/postgres/cleanup_repo.go`, update the `List` query and scan to include `retain_n_versions`:

```go
func (r *CleanupPolicyRepo) List(ctx context.Context) ([]domain.CleanupPolicy, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, description, format, criteria, COALESCE(schedule_cron,''),
		       enabled, dry_run, COALESCE(retain_n_versions,0),
		       last_run_at, COALESCE(last_run_freed,0), COALESCE(last_run_count,0),
		       created_at, updated_at
		FROM cleanup_policies ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CleanupPolicy
	for rows.Next() {
		var p domain.CleanupPolicy
		var criteriaJSON []byte
		if err := rows.Scan(
			&p.ID, &p.Name, &p.Description, &p.Format, &criteriaJSON,
			&p.ScheduleCron, &p.Enabled, &p.DryRun, &p.RetainNVersions,
			&p.LastRunAt, &p.LastRunFreed, &p.LastRunCount,
			&p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(criteriaJSON, &p.Criteria)
		out = append(out, p)
	}
	return out, rows.Err()
}
```

- [ ] **Step 3: Update CleanupPolicyRepo — Get query**

```go
func (r *CleanupPolicyRepo) Get(ctx context.Context, id string) (*domain.CleanupPolicy, error) {
	var p domain.CleanupPolicy
	var criteriaJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, description, format, criteria, COALESCE(schedule_cron,''),
		       enabled, dry_run, COALESCE(retain_n_versions,0),
		       last_run_at, COALESCE(last_run_freed,0), COALESCE(last_run_count,0),
		       created_at, updated_at
		FROM cleanup_policies WHERE id=$1`, id).
		Scan(&p.ID, &p.Name, &p.Description, &p.Format, &criteriaJSON,
			&p.ScheduleCron, &p.Enabled, &p.DryRun, &p.RetainNVersions,
			&p.LastRunAt, &p.LastRunFreed, &p.LastRunCount,
			&p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(criteriaJSON, &p.Criteria)
	return &p, nil
}
```

- [ ] **Step 4: Update CleanupPolicyRepo — Create query**

```go
func (r *CleanupPolicyRepo) Create(ctx context.Context, p *domain.CleanupPolicy) error {
	criteriaJSON, _ := json.Marshal(p.Criteria)
	return r.pool.QueryRow(ctx, `
		INSERT INTO cleanup_policies (name, description, format, criteria, schedule_cron, enabled, dry_run, retain_n_versions)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, created_at, updated_at`,
		p.Name, p.Description, p.Format, criteriaJSON,
		p.ScheduleCron, p.Enabled, p.DryRun, p.RetainNVersions,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}
```

- [ ] **Step 5: Update CleanupPolicyRepo — Update query**

```go
func (r *CleanupPolicyRepo) Update(ctx context.Context, p *domain.CleanupPolicy) error {
	criteriaJSON, _ := json.Marshal(p.Criteria)
	tag, err := r.pool.Exec(ctx, `
		UPDATE cleanup_policies
		SET name=$1, description=$2, format=$3, criteria=$4,
		    schedule_cron=$5, enabled=$6, dry_run=$7, retain_n_versions=$8, updated_at=NOW()
		WHERE id=$9`,
		p.Name, p.Description, p.Format, criteriaJSON,
		p.ScheduleCron, p.Enabled, p.DryRun, p.RetainNVersions, p.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("cleanup policy not found: %s", p.ID)
	}
	return nil
}
```

- [ ] **Step 6: Update mock AssetRepo**

In `internal/testutil/mocks.go`, find the `AssetRepo` struct (around line 329) and add `LastRetainN`:

```go
type AssetRepo struct {
	mu          sync.Mutex
	assets      map[string]*domain.Asset // key: "repoName:path"
	Stale       []domain.Asset
	LastRetainN int
	// ... existing fields unchanged
}
```

Then update the `ListStale` method signature (find it around line 361):

```go
func (a *AssetRepo) ListStale(_ context.Context, _ string, _ []string, _, _ int, _, _ string, retainNVersions int, limit int) ([]domain.Asset, error) {
	a.mu.Lock()
	a.LastRetainN = retainNVersions
	defer a.mu.Unlock()
	if len(a.Stale) == 0 {
		return nil, nil
	}
	out := a.Stale
	a.Stale = nil
	return out, nil
}
```

(Preserve the existing body — only the signature and the `LastRetainN` assignment change.)

- [ ] **Step 7: Verify build**

```bash
cd /home/skensel/AI/self_nexus && go build ./...
```

Expected: no output. If compile errors appear, they will be in `cleanup_service.go` (next task).

- [ ] **Step 8: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add internal/repository/interfaces.go \
        internal/repository/postgres/cleanup_repo.go \
        internal/testutil/mocks.go
git commit -m "feat(cleanup): update CleanupRepo + ListStale interface for retain_n_versions"
```

---

## Task 3: AssetRepo.ListStale — CTE window-function exclusion

**Files:**
- Modify: `internal/repository/postgres/asset_repo.go`

- [ ] **Step 1: Update ListStale implementation**

In `internal/repository/postgres/asset_repo.go`, replace the entire `ListStale` function (starting at line 177):

```go
func (r *assetRepo) ListStale(ctx context.Context, format string, repoNames []string, lastDownloadedDays, artifactAgeDays int, pathPrefix, nameGlob string, retainNVersions int, limit int) ([]domain.Asset, error) {
	if limit <= 0 {
		limit = 500
	}
	args := []any{}
	i := 1

	// When retainNVersions > 0, build a CTE that finds the N newest component
	// versions per (repository_id, group_id, name). We pass repoNames as $1
	// and retainNVersions as $2, then reuse $1 in the WHERE clause below.
	var ctePrefix, cteExclude string
	repoArgIdx := 0
	if retainNVersions > 0 && len(repoNames) > 0 {
		ctePrefix = fmt.Sprintf(`
WITH retained_comps AS (
  SELECT id FROM (
    SELECT comp2.id,
      ROW_NUMBER() OVER (
        PARTITION BY comp2.repository_id, comp2.group_id, comp2.name
        ORDER BY comp2.version_sort DESC, comp2.created_at DESC
      ) rn
    FROM components comp2
    WHERE comp2.repository_id IN (
      SELECT id FROM repositories WHERE name = ANY($%d::text[])
    )
  ) r WHERE rn <= $%d
)
`, i, i+1)
		repoArgIdx = i
		args = append(args, repoNames, retainNVersions)
		i += 2
		cteExclude = " AND comp.id NOT IN (SELECT id FROM retained_comps)"
	}

	where := "WHERE 1=1"

	if len(repoNames) > 0 {
		if repoArgIdx > 0 {
			// repoNames already in args as $repoArgIdx — reuse it
			where += fmt.Sprintf(" AND rep.name = ANY($%d::text[])", repoArgIdx)
		} else {
			where += fmt.Sprintf(" AND rep.name = ANY($%d::text[])", i)
			args = append(args, repoNames)
			i++
		}
	}

	if format != "" && format != "*" {
		where += fmt.Sprintf(" AND comp.format = $%d", i)
		args = append(args, format)
		i++
	}
	if lastDownloadedDays > 0 {
		where += fmt.Sprintf(" AND (a.last_downloaded IS NULL OR a.last_downloaded < NOW() - INTERVAL '1 day' * $%d)", i)
		args = append(args, lastDownloadedDays)
		i++
	}
	if artifactAgeDays > 0 {
		where += fmt.Sprintf(" AND a.created_at < NOW() - INTERVAL '1 day' * $%d", i)
		args = append(args, artifactAgeDays)
		i++
	}
	if pathPrefix != "" {
		escaped := strings.ReplaceAll(strings.ReplaceAll(pathPrefix, `\`, `\\`), "%", `\%`)
		escaped = strings.ReplaceAll(escaped, "_", `\_`)
		where += fmt.Sprintf(` AND a.path LIKE $%d ESCAPE '\'`, i)
		args = append(args, escaped+"%")
		i++
	}
	if nameGlob != "" {
		like := globToLike(nameGlob)
		where += fmt.Sprintf(" AND a.path LIKE $%d", i)
		args = append(args, like)
		i++
	}
	where += cteExclude
	args = append(args, limit)

	q := fmt.Sprintf(`%sSELECT %s %s %s ORDER BY a.created_at ASC LIMIT $%d`,
		ctePrefix, assetSelectCols, assetFromJoin, where, i)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}
```

- [ ] **Step 2: Verify build**

```bash
cd /home/skensel/AI/self_nexus && go build ./internal/repository/...
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add internal/repository/postgres/asset_repo.go
git commit -m "feat(cleanup): ListStale CTE excludes N newest versions per (group_id, name)"
```

---

## Task 4: CleanupService pass-through + tests

**Files:**
- Modify: `internal/service/cleanup_service.go`
- Modify: `internal/service/cleanup_service_test.go`

- [ ] **Step 1: Write failing test**

Add to `internal/service/cleanup_service_test.go`:

```go
func TestRunPolicy_RetainNVersions_PassedToListStale(t *testing.T) {
	// Policy with retainNVersions=3 should pass that value to ListStale.
	staleAssets := []domain.Asset{
		{ID: "a10", BlobKey: "bk10", SizeBytes: 10, Path: "/old.jar"},
	}
	policies := testutil.NewCleanupPolicyRepo(
		&domain.CleanupPolicy{
			ID: "p20", Name: "retain-test", Enabled: true, Format: "*",
			Criteria:        map[string]any{"artifactAgeDays": float64(30)},
			RetainNVersions: 3,
		},
	)
	assets := testutil.NewAssetRepo()
	assets.Stale = staleAssets
	blobs := testutil.NewBlobStore()
	blobRepo := testutil.NewBlobStoreRepo()
	_ = blobs.Put(context.Background(), "bk10", testutil.MakeReader("x"), 1)
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "hosted", ID: "r1", Format: domain.FormatRaw,
		CleanupPolicyIDs: []string{"p20"},
	})

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
	require.NoError(t, svc.RunPolicy(context.Background(), "p20"))

	// Verify the retain value was forwarded to the repo
	assert.Equal(t, 3, assets.LastRetainN)
	// Asset was still deleted (mock ignores retain, simulates it was excluded)
	assert.Contains(t, blobs.Deleted, "bk10")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/skensel/AI/self_nexus && go test ./internal/service/... -run TestRunPolicy_RetainNVersions -v 2>&1 | tail -20
```

Expected: FAIL — compile error because `ListStale` in service still has old signature.

- [ ] **Step 3: Update service to pass RetainNVersions**

In `internal/service/cleanup_service.go`, find the `runPolicy` method (around line 154). Update the `ListStale` call inside the batch loop:

Old:
```go
stale, err := s.assets.ListStale(ctx, p.Format, repoNames, lastDownloadedDays, artifactAgeDays, pathPrefix, nameGlob, batchLimit)
```

New:
```go
stale, err := s.assets.ListStale(ctx, p.Format, repoNames, lastDownloadedDays, artifactAgeDays, pathPrefix, nameGlob, p.RetainNVersions, batchLimit)
```

- [ ] **Step 4: Run all tests**

```bash
cd /home/skensel/AI/self_nexus && go test ./... 2>&1 | tail -20
```

Expected: all pass (≥322 tests + 1 new).

- [ ] **Step 5: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add internal/service/cleanup_service.go internal/service/cleanup_service_test.go
git commit -m "feat(cleanup): service passes retain_n_versions to ListStale"
```

---

## Task 5: Frontend — form, wizard, edit modal, card display

**Files:**
- Modify: `frontend/src/pages/CleanupPage.tsx`

- [ ] **Step 1: Update CleanupPolicy interface**

Find the `CleanupPolicy` interface (line 8) and add the new field:

```typescript
interface CleanupPolicy {
  id: string
  name: string
  description?: string
  format: string
  criteria: Record<string, number | string>
  scheduleCron?: string
  enabled: boolean
  dryRun: boolean
  retainNVersions?: number
  lastRunAt?: string
  lastRunFreedBytes?: number
  lastRunCount?: number
}
```

- [ ] **Step 2: Update PolicyForm interface**

Find `PolicyForm` (line 22) and add the field:

```typescript
interface PolicyForm {
  name: string
  description: string
  format: string
  enabled: boolean
  dryRun: boolean
  lastDownloadedDays: string
  artifactAgeDays: string
  pathPrefix: string
  nameGlob: string
  scheduleCron: string
  retainNVersions: string
}
```

- [ ] **Step 3: Update emptyForm()**

Find `emptyForm` (line 44):

```typescript
const emptyForm = (): PolicyForm => ({
  name: '', description: '', format: '*',
  enabled: true, dryRun: false,
  lastDownloadedDays: '', artifactAgeDays: '',
  pathPrefix: '', nameGlob: '',
  scheduleCron: '',
  retainNVersions: '',
})
```

- [ ] **Step 4: Update initial state population in PolicyModal**

Find the `if (!initial) return emptyForm()` block (line 63). The else branch that reads from `initial` — add:

```typescript
retainNVersions: String(initial.retainNVersions ?? ''),
```

(Add after the `scheduleCron` line in the return object.)

- [ ] **Step 5: Update payload() to include retainNVersions**

Find the `payload` function (line 81). Add `retainNVersions` to the returned object:

```typescript
const payload = () => ({
  name: form.name.trim(),
  description: form.description.trim(),
  format: form.format,
  enabled: form.enabled,
  dryRun: form.dryRun,
  scheduleCron: form.scheduleCron.trim(),
  retainNVersions: form.retainNVersions ? Number(form.retainNVersions) : 0,
  criteria: {
    ...(form.lastDownloadedDays ? { lastDownloadedDays: Number(form.lastDownloadedDays) } : {}),
    ...(form.artifactAgeDays ? { artifactAgeDays: Number(form.artifactAgeDays) } : {}),
    ...(form.pathPrefix.trim() ? { pathPrefix: form.pathPrefix.trim() } : {}),
    ...(form.nameGlob.trim() ? { nameGlob: form.nameGlob.trim() } : {}),
  },
})
```

- [ ] **Step 6: Add field to wizard Step 2 (Criteria)**

Find `wizStep2` (line 163). Add the "Retain N versions" input as a new row in the existing 2-column grid, after the `nameGlob` field:

```tsx
const wizStep2 = (
  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <label style={LABEL}>Not downloaded for (days)</label>
      <HoloInput type="number" min="1" value={form.lastDownloadedDays} onChange={set('lastDownloadedDays')} placeholder="e.g. 30" />
    </div>
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <label style={LABEL}>Artifact age (days)</label>
      <HoloInput type="number" min="1" value={form.artifactAgeDays} onChange={set('artifactAgeDays')} placeholder="e.g. 90" />
    </div>
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <label style={LABEL}>Path prefix</label>
      <HoloInput value={form.pathPrefix} onChange={set('pathPrefix')} placeholder="e.g. /releases/" />
    </div>
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <label style={LABEL}>Name glob</label>
      <HoloInput value={form.nameGlob} onChange={set('nameGlob')} placeholder="e.g. *-SNAPSHOT*" />
    </div>
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, gridColumn: '1 / -1' }}>
      <label style={LABEL}>Retain N newest versions</label>
      <HoloInput type="number" min="0" value={form.retainNVersions} onChange={set('retainNVersions')} placeholder="e.g. 3 (0 = disabled)" />
      <span style={{ fontSize: 11, color: 'rgba(229,231,235,0.35)' }}>Keep the N most recent versions of each artifact even if they match other criteria.</span>
    </div>
  </div>
)
```

- [ ] **Step 7: Add field to Edit modal (flat form)**

In the Edit modal section (after line 220), find the criteria section — the 2-column grid with `lastDownloadedDays`, `artifactAgeDays`, `pathPrefix`, `nameGlob`. Add the retain field after it, before the `scheduleCron` section. Find the existing block that contains the `nameGlob` input and add after it:

```tsx
<div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
  <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>Retain N newest versions</label>
  <HoloInput type="number" min="0" value={form.retainNVersions} onChange={set('retainNVersions')} placeholder="0 = disabled" />
</div>
```

(Place it as a full-width row spanning the 2-column grid — either outside the grid or with `style={{ gridColumn: '1 / -1' }}`.)

- [ ] **Step 8: Add chip to policy card**

Find the `criteria` array (line 372) in the card render. Add a retain chip:

```typescript
const criteria = [
  p.criteria?.lastDownloadedDays && `≥${p.criteria.lastDownloadedDays}d not downloaded`,
  p.criteria?.artifactAgeDays && `age >${p.criteria.artifactAgeDays}d`,
  p.criteria?.pathPrefix && `path: ${p.criteria.pathPrefix}`,
  p.criteria?.nameGlob && `glob: ${p.criteria.nameGlob}`,
  p.retainNVersions && p.retainNVersions > 0 && `retain ≥${p.retainNVersions}`,
].filter(Boolean) as string[]
```

- [ ] **Step 9: Build frontend**

```bash
cd /home/skensel/AI/self_nexus/frontend && npm run build 2>&1 | tail -20
```

Expected: `✓ built in ...` with 0 TypeScript errors.

- [ ] **Step 10: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add frontend/src/pages/CleanupPage.tsx
git commit -m "feat(cleanup): retain N versions field in UI — wizard, edit modal, card chip"
```

---

## Task 6: Update task_plan.md + final verification

**Files:**
- Modify: `task_plan.md`

- [ ] **Step 1: Run full test suite**

```bash
cd /home/skensel/AI/self_nexus && go test ./... 2>&1 | tail -5
```

Expected: all pass, no failures.

- [ ] **Step 2: Update task_plan.md**

Find the Phase 44 block in `task_plan.md` and change:

```
**Status:** backlog
```
to:
```
**Status:** complete (2026-04-28)
```

And update the tasks checklist to show all items done:
```markdown
- [x] DB: поле `retain_n_versions int` в `cleanup_policies` (migration `012_cleanup_retain_versions.sql`)
- [x] `CleanupService`: передаёт `p.RetainNVersions` в `ListStale`; SQL CTE исключает N новейших версий каждого `(group_id, name)` через window function
- [x] API: поддержка нового поля в `POST/PUT /service/rest/v1/cleanup-policies` (top-level JSON, не в `criteria`)
- [x] Frontend: CleanupPage — поле "Retain N versions" в wizard Step 2 и Edit modal; chip на карточке политики
```

- [ ] **Step 3: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add task_plan.md
git commit -m "chore: mark Phase 44 complete"
```
