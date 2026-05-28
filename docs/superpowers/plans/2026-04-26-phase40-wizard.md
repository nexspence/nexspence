# Phase 40 — Stepped Wizard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a reusable `<Wizard>` component to the holo library and replace three Create modals (Repository, Migration Job, Cleanup Policy) with step-by-step wizard flows.

**Architecture:** A new `Wizard.tsx` component renders its own overlay (reusing `.holo-overlay`) and manages step state internally. Parent components pass `steps[]` (label + JSX content), `onValidateStep`, and `onFinish`. The three existing Create modals are refactored to use Wizard while keeping all state, data fetching, and API calls unchanged. Edit modals are untouched.

**Tech Stack:** React 18, TypeScript strict, CSS (holo design system tokens), no new dependencies.

**Parallelism:** Task 1 must complete first. Tasks 2, 3, 4 are independent and can run in parallel. Task 5 runs after all of 2–4.

---

## Task 1: Wizard component + CSS + export

**Files:**
- Create: `frontend/src/components/holo/Wizard.tsx`
- Modify: `frontend/src/components/holo/holo.css` (append wizard section)
- Modify: `frontend/src/components/holo/index.ts` (add export)

- [ ] **Step 1: Create `Wizard.tsx`**

```tsx
// frontend/src/components/holo/Wizard.tsx
import React, { useState } from 'react'

export interface WizardStep {
  label: string
  content: React.ReactNode
}

export interface WizardProps {
  steps: WizardStep[]
  onFinish: () => void | Promise<void>
  finishLabel?: string
  onValidateStep?: (stepIndex: number) => boolean | Promise<boolean>
  onClose: () => void
  loading?: boolean
  error?: string
}

export function Wizard({
  steps,
  onFinish,
  finishLabel = 'Создать',
  onValidateStep,
  onClose,
  loading,
  error,
}: WizardProps) {
  const [step, setStep] = useState(0)
  const [sliding, setSliding] = useState<'left' | 'right' | null>(null)
  const total = steps.length

  const goTo = (dir: 'left' | 'right', target: number) => {
    setSliding(dir)
    setTimeout(() => {
      setStep(target)
      setSliding(null)
    }, 200)
  }

  const handleNext = async () => {
    if (onValidateStep) {
      const ok = await onValidateStep(step)
      if (!ok) return
    }
    if (step < total - 1) {
      goTo('left', step + 1)
    } else {
      await onFinish()
    }
  }

  const handleBack = () => {
    if (step > 0) goTo('right', step - 1)
  }

  return (
    <div className="holo-overlay" onClick={onClose}>
      <div className="holo-wizard" onClick={e => e.stopPropagation()}>
        <div className="holo-wizard__progress">
          {steps.map((s, i) => (
            <React.Fragment key={i}>
              <div
                className="holo-wizard__step-meta"
                onClick={() => { if (i < step) goTo('right', i) }}
                style={{ cursor: i < step ? 'pointer' : 'default' }}
              >
                <div className={`holo-wizard__dot holo-wizard__dot--${i < step ? 'done' : i === step ? 'active' : 'pending'}`}>
                  {i < step ? '✓' : i + 1}
                </div>
                <div className="holo-wizard__step-text">
                  <span className="holo-wizard__step-num">Шаг {i + 1}</span>
                  <span className={`holo-wizard__step-name holo-wizard__step-name--${i < step ? 'done' : i === step ? 'active' : 'pending'}`}>
                    {s.label}
                  </span>
                </div>
              </div>
              {i < total - 1 && (
                <div className={`holo-wizard__line${i < step ? ' holo-wizard__line--done' : ''}`} />
              )}
            </React.Fragment>
          ))}
        </div>

        <div className={`holo-wizard__body${sliding ? ` holo-wizard__body--${sliding}` : ''}`}>
          {steps[step].content}
        </div>

        {error && <div className="holo-wizard__error">{error}</div>}

        <div className="holo-wizard__footer">
          <button
            type="button"
            className="holo-btn holo-wizard__back"
            onClick={handleBack}
            style={{ visibility: step > 0 ? 'visible' : 'hidden' }}
          >
            ← Назад
          </button>
          <span className="holo-wizard__step-info">Шаг {step + 1} из {total}</span>
          <button
            type="button"
            className="holo-btn holo-btn--primary"
            onClick={handleNext}
            disabled={!!loading}
          >
            {loading ? 'Загрузка…' : step === total - 1 ? finishLabel : 'Далее →'}
          </button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Append wizard CSS to `holo.css`**

Open `frontend/src/components/holo/holo.css` and append at the very end (after the `.holo-section-label` block):

```css
/* ── Wizard ──────────────────────────────────────────────────────────── */
.holo-wizard {
  background: linear-gradient(135deg, rgba(14,12,35,0.99) 0%, rgba(10,8,28,0.99) 100%);
  border: 1px solid var(--holo-border);
  border-radius: 16px;
  overflow: hidden;
  min-width: 560px;
  max-width: 90vw;
  max-height: 85vh;
  box-shadow: 0 24px 64px rgba(0,0,0,0.7), 0 0 0 1px rgba(124,92,255,0.08);
  position: relative;
  display: flex;
  flex-direction: column;
}
.holo-wizard::before {
  content: ''; position: absolute; top: 0; left: 0; right: 0; height: 1px;
  background: var(--holo-gradient); background-size: 200% 200%;
  animation: holoShift 5s ease infinite; z-index: 1;
}
.holo-wizard__progress {
  display: flex; align-items: center;
  padding: 14px 20px;
  background: rgba(0,0,0,0.35);
  border-bottom: 1px solid rgba(124,92,255,0.12);
}
.holo-wizard__step-meta { display: flex; align-items: center; gap: 7px; }
.holo-wizard__dot {
  width: 24px; height: 24px; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  font-size: 10px; font-weight: 700; flex-shrink: 0;
  transition: all 0.2s ease;
}
.holo-wizard__dot--done  { background: #3b82f6; color: #fff; box-shadow: 0 0 10px rgba(59,130,246,0.4); }
.holo-wizard__dot--active { background: rgba(59,130,246,0.2); border: 2px solid #3b82f6; color: #60a5fa; box-shadow: 0 0 12px rgba(59,130,246,0.3); }
.holo-wizard__dot--pending { background: rgba(255,255,255,0.04); border: 1.5px solid rgba(255,255,255,0.1); color: #334155; }
.holo-wizard__step-text { display: flex; flex-direction: column; }
.holo-wizard__step-num { font-size: 9px; color: #475569; font-weight: 600; letter-spacing: 0.04em; text-transform: uppercase; }
.holo-wizard__step-name { font-size: 12px; font-weight: 600; }
.holo-wizard__step-name--active  { color: #93c5fd; }
.holo-wizard__step-name--done    { color: #64748b; }
.holo-wizard__step-name--pending { color: #334155; }
.holo-wizard__line { flex: 1; height: 1.5px; margin: 0 8px; background: rgba(124,92,255,0.12); transition: background 0.3s; }
.holo-wizard__line--done { background: rgba(59,130,246,0.4); }
.holo-wizard__body { padding: 20px; min-height: 200px; overflow-y: auto; flex: 1; }
@keyframes wizardSlideLeft  { from { transform: translateX(0); opacity: 1; } to { transform: translateX(-16px); opacity: 0; } }
@keyframes wizardSlideRight { from { transform: translateX(0); opacity: 1; } to { transform: translateX( 16px); opacity: 0; } }
.holo-wizard__body--left  { animation: wizardSlideLeft  0.2s ease-out forwards; }
.holo-wizard__body--right { animation: wizardSlideRight 0.2s ease-out forwards; }
.holo-wizard__error {
  margin: 0 20px 4px;
  background: rgba(239,68,68,0.1); border: 1px solid rgba(239,68,68,0.25);
  border-radius: 8px; padding: 10px 12px; color: #fca5a5; font-size: 13px;
}
.holo-wizard__footer {
  display: flex; justify-content: space-between; align-items: center;
  padding: 12px 20px; border-top: 1px solid rgba(124,92,255,0.1); background: rgba(0,0,0,0.15);
}
.holo-wizard__back { background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.08); color: var(--holo-text-dim); }
.holo-wizard__step-info { font-size: 10px; color: #475569; font-weight: 600; }
```

- [ ] **Step 3: Add export to `index.ts`**

Current content of `frontend/src/components/holo/index.ts`:
```ts
export * from './holo';
```

Replace with:
```ts
export * from './holo';
export * from './Wizard';
```

- [ ] **Step 4: Verify TypeScript compiles**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit 2>&1 | head -30
```

Expected: no errors (zero output or only warnings unrelated to Wizard.tsx).

- [ ] **Step 5: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add frontend/src/components/holo/Wizard.tsx \
        frontend/src/components/holo/holo.css \
        frontend/src/components/holo/index.ts
git commit -m "feat(ui): Phase 40 — Wizard holo component + CSS"
```

---

## Task 2: CreateRepoModal → Wizard

**Depends on:** Task 1 complete  
**Files:**
- Modify: `frontend/src/pages/RepositoriesPage.tsx`

The existing `CreateRepoModal` (lines ~323–637) is a single flat `<HoloModal>` + `<form>`. We split its rendering into three JSX nodes (step1/step2/step3) and wrap with `<Wizard>`. All state, queries, and API logic stay identical. We add a `validateStep(stepIdx)` function and a `handleFinish()` that contains the current submit logic.

- [ ] **Step 1: Add `Wizard` to imports**

Find this line near the top of `RepositoriesPage.tsx`:
```ts
import { HoloButton, HoloInput, HoloPill, HoloModal } from '@/components/holo'
```

Replace with:
```ts
import { HoloButton, HoloInput, HoloPill, HoloModal, Wizard } from '@/components/holo'
```

(`HoloModal` stays because `EditRepoModal` still uses it.)

- [ ] **Step 2: Replace `CreateRepoModal` body**

Find the entire `CreateRepoModal` function (starts at `function CreateRepoModal(` and ends at the closing `}` after `</HoloModal>`). Replace the full function with:

```tsx
function CreateRepoModal({ onClose, onCreated }: {
  onClose: () => void
  onCreated: () => void
}) {
  const { data: allRepos = [] } = useQuery<Repository[]>({
    queryKey: ['repositories'],
    queryFn: () => nexusApi.listRepositories({}).then(r => r.data),
  })
  const { data: cleanupPolicies = [] } = useQuery<CleanupPolicyRow[]>({
    queryKey: ['cleanupPolicies'],
    queryFn: () => nexusApi.listCleanupPolicies().then(r => r.data),
  })
  const { data: blobStores = [] } = useQuery<BlobStoreLite[]>({
    queryKey: ['blobstores'],
    queryFn: () => nexusApi.listBlobStores().then(r => r.data),
  })

  const defaultStoreId = blobStores.find(b => b.name === 'default')?.id ?? blobStores[0]?.id ?? ''

  const [form, setForm] = useState({
    name: '', format: 'maven2', type: 'hosted', description: '',
    remoteUrl: PROXY_DEFAULTS['maven2'],
    memberNames: [] as string[],
    cleanupPolicyIds: [] as string[],
    quotaGB: '',
    allowAnonymous: false,
    blobStoreId: '',
  })
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const setField = (field: string, value: unknown) =>
    setForm(f => ({ ...f, [field]: value }))

  const handleFormatChange = (fmt: string) => {
    setForm(f => ({
      ...f,
      format: fmt,
      remoteUrl: f.type === 'proxy' ? (PROXY_DEFAULTS[fmt] ?? '') : f.remoteUrl,
      cleanupPolicyIds: [],
    }))
  }

  const memberCandidates = allRepos.filter(
    r => r.format === form.format && r.type !== 'group'
  )
  const applicableCreate = cleanupPoliciesForFormat(cleanupPolicies, form.format)

  const validateStep = (stepIdx: number): boolean => {
    setError('')
    if (stepIdx === 1) {
      if (!form.name.trim()) { setError('Name is required'); return false }
      if (form.type === 'proxy' && !form.remoteUrl.trim()) { setError('Remote URL is required for proxy repositories'); return false }
      if (form.type === 'group' && form.memberNames.length === 0) { setError('Select at least one member repository'); return false }
    }
    if (stepIdx === 2) {
      const effectiveStoreId = form.type === 'group' ? '' : (form.blobStoreId || defaultStoreId)
      if (form.type !== 'group' && !effectiveStoreId) { setError('Select a blob store'); return false }
      const quotaValue = form.quotaGB.trim() !== '' ? parseFloat(form.quotaGB) : NaN
      if (!isNaN(quotaValue) && quotaValue > 0 && effectiveStoreId) {
        const store = blobStores.find(b => b.id === effectiveStoreId)
        if (store?.quotaBytes != null) {
          const repoBytes = Math.round(quotaValue * 1024 * 1024 * 1024)
          if (repoBytes > store.quotaBytes) {
            setError(`Repository quota (${formatBytes(repoBytes)}) exceeds blob store "${store.name}" quota (${formatBytes(store.quotaBytes)})`)
            return false
          }
        }
      }
    }
    return true
  }

  const handleFinish = async () => {
    setError('')
    const effectiveStoreId = form.type === 'group' ? '' : (form.blobStoreId || defaultStoreId)
    setLoading(true)
    try {
      const body: Record<string, unknown> = {
        name: form.name,
        description: form.description,
      }
      if (effectiveStoreId) body.blobStoreId = effectiveStoreId
      if (form.type === 'proxy') body.proxyConfig = { remote_url: form.remoteUrl.trim() }
      if (form.type === 'group') body.formatConfig = { member_names: form.memberNames }
      if (form.type !== 'group' && form.cleanupPolicyIds.length > 0) body.cleanupPolicyIds = form.cleanupPolicyIds
      if (form.quotaGB.trim() !== '') {
        const gb = parseFloat(form.quotaGB)
        if (!isNaN(gb) && gb > 0) body.quotaBytes = Math.round(gb * 1024 * 1024 * 1024)
      }
      body.allowAnonymous = form.allowAnonymous
      await apiClient.post(`/service/rest/v1/repositories/${form.format}/${form.type}`, body)
      onCreated()
    } catch (err: any) {
      setError(err.response?.data?.error ?? 'Failed to create repository')
    } finally {
      setLoading(false)
    }
  }

  const step1 = (
    <div className={styles.form}>
      <div className={styles.formRow}>
        <label style={LABEL_STYLE}>Format</label>
        <Select
          options={['maven2','npm','docker','pypi','go','nuget','helm','raw','apt','yum','cargo','conan'].map(f => ({ value: f, label: f }))}
          value={form.format}
          onChange={handleFormatChange}
        />
      </div>
      <div className={styles.formRow}>
        <label style={LABEL_STYLE}>Type</label>
        <Select
          options={[
            { value: 'hosted', label: 'Hosted — store artifacts locally' },
            { value: 'proxy',  label: 'Proxy — cache from remote registry' },
            { value: 'group',  label: 'Group — combine multiple repos' },
          ]}
          value={form.type}
          onChange={t => setForm(f => ({
            ...f,
            type: t,
            remoteUrl: t === 'proxy' ? (PROXY_DEFAULTS[f.format] ?? '') : '',
          }))}
        />
      </div>
    </div>
  )

  const step2 = (
    <div className={styles.form}>
      <div className={styles.formRow}>
        <label style={LABEL_STYLE}>Name *</label>
        <HoloInput
          value={form.name}
          onChange={e => setField('name', e.target.value)}
          placeholder="my-repo"
          autoFocus
        />
      </div>
      <div className={styles.formRow}>
        <label style={LABEL_STYLE}>Description</label>
        <HoloInput
          value={form.description}
          onChange={e => setField('description', e.target.value)}
          placeholder="Optional description"
        />
      </div>
      {form.type === 'proxy' && (
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Remote URL *</label>
          <HoloInput
            type="url"
            value={form.remoteUrl}
            onChange={e => setField('remoteUrl', e.target.value)}
            placeholder="https://registry.example.com/"
          />
          <span className={styles.hint}>URL of the upstream registry to proxy and cache</span>
        </div>
      )}
      {form.type === 'group' && (
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Member Repositories *</label>
          {memberCandidates.length === 0 ? (
            <p className={styles.hint}>No {form.format} hosted/proxy repos found. Create them first.</p>
          ) : (
            <div className={styles.memberList}>
              {memberCandidates.map(r => (
                <label key={r.id} className={styles.memberItem}>
                  <input
                    type="checkbox"
                    checked={form.memberNames.includes(r.name)}
                    onChange={() =>
                      setField('memberNames',
                        form.memberNames.includes(r.name)
                          ? form.memberNames.filter(n => n !== r.name)
                          : [...form.memberNames, r.name]
                      )
                    }
                  />
                  <span className={styles.memberName}>{r.name}</span>
                  <span className={styles.memberType}>{r.type}</span>
                </label>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )

  const step3 = (
    <div className={styles.form}>
      {form.type !== 'group' && applicableCreate.length > 0 && (
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Cleanup policies</label>
          <div className={styles.memberList}>
            {applicableCreate.map(p => (
              <label key={p.id} className={styles.memberItem}>
                <input
                  type="checkbox"
                  checked={form.cleanupPolicyIds.includes(p.id)}
                  onChange={() =>
                    setForm(f => ({
                      ...f,
                      cleanupPolicyIds: f.cleanupPolicyIds.includes(p.id)
                        ? f.cleanupPolicyIds.filter(x => x !== p.id)
                        : [...f.cleanupPolicyIds, p.id],
                    }))
                  }
                />
                <span className={styles.memberName}>{p.name}</span>
                <span className={styles.memberType}>{p.format === '*' ? 'all' : p.format}</span>
              </label>
            ))}
          </div>
        </div>
      )}
      {form.type !== 'group' && (
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Blob Store *</label>
          {blobStores.length === 0 ? (
            <span className={styles.hint}>No blob stores configured. Create one in System Admin → Blob Stores.</span>
          ) : (
            <Select
              options={blobStores.map(b => ({ value: b.id, label: `${b.name} (${b.type})` }))}
              value={form.blobStoreId || defaultStoreId}
              onChange={v => setField('blobStoreId', v)}
            />
          )}
          {(() => {
            const sel = blobStores.find(b => b.id === (form.blobStoreId || defaultStoreId))
            if (!sel) return <span className={styles.hint}>Physical storage backend where artifacts are written.</span>
            if (sel.quotaBytes == null) return <span className={styles.hint}>Store quota: unlimited.</span>
            const free = sel.quotaBytes - (sel.usedBytes ?? 0)
            return <span className={styles.hint}>Store quota: {formatBytes(sel.quotaBytes)} · free {formatBytes(free)}</span>
          })()}
        </div>
      )}
      {form.type !== 'group' && (
        <div className={styles.formRow}>
          <label style={LABEL_STYLE}>Storage quota (GB)</label>
          <HoloInput
            type="number" min="0" step="0.1"
            value={form.quotaGB}
            onChange={e => setField('quotaGB', e.target.value)}
            placeholder="No limit"
          />
          <span className={styles.hint}>Leave blank for unlimited storage</span>
        </div>
      )}
      <div className={styles.formRow}>
        <label style={LABEL_STYLE}>Anonymous access</label>
        <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text)', cursor: 'pointer' }}>
          <input
            type="checkbox"
            checked={form.allowAnonymous}
            onChange={e => setField('allowAnonymous', e.target.checked)}
          />
          Allow unauthenticated read access
        </label>
        <span className={styles.hint}>When disabled, only users with an assigned role can read this repository.</span>
      </div>
    </div>
  )

  return (
    <Wizard
      steps={[
        { label: 'Тип', content: step1 },
        { label: 'Основные поля', content: step2 },
        { label: 'Хранилище', content: step3 },
      ]}
      onFinish={handleFinish}
      finishLabel="Create"
      onValidateStep={validateStep}
      onClose={onClose}
      loading={loading}
      error={error}
    />
  )
}
```

Note: `toggleMember` helper that previously existed is inlined into the `step2` JSX above via `setField`. Remove the separate `const toggleMember` if it exists between the old state and return — it's no longer needed.

- [ ] **Step 3: Verify TypeScript**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit 2>&1 | head -30
```

Expected: zero errors.

- [ ] **Step 4: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add frontend/src/pages/RepositoriesPage.tsx
git commit -m "feat(ui): Phase 40 — CreateRepoModal stepped wizard"
```

---

## Task 3: CreateMigrationJobModal → Wizard

**Depends on:** Task 1 complete  
**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

- [ ] **Step 1: Add `Wizard` to imports**

Find:
```ts
import { HoloButton, HoloInput, HoloModal, HoloTabs, HoloCard, HoloTabItem } from '@/components/holo'
```

Replace with:
```ts
import { HoloButton, HoloInput, HoloModal, HoloTabs, HoloCard, HoloTabItem, Wizard } from '@/components/holo'
```

(`HoloModal` stays — other modals in AdminPage still use it.)

- [ ] **Step 2: Replace `CreateMigrationJobModal` body**

Find the entire `function CreateMigrationJobModal(` function and replace with:

```tsx
function CreateMigrationJobModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [form, setForm] = useState({
    sourceUrl: '', username: 'admin', password: '', concurrency: '4',
  })
  const [scope, setScope] = useState({
    migrateRepos: true, migrateUsers: true, migratePolicies: true, migrateBlobs: true,
  })
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm(f => ({ ...f, [k]: e.target.value }))

  const toggleScope = (k: keyof typeof scope) =>
    setScope(s => ({ ...s, [k]: !s[k] }))

  const validateStep = (stepIdx: number): boolean => {
    setError('')
    if (stepIdx === 0) {
      if (!form.sourceUrl.trim()) { setError('Nexus URL is required'); return false }
      if (!form.password.trim()) { setError('Password is required'); return false }
    }
    if (stepIdx === 1) {
      if (!Object.values(scope).some(Boolean)) { setError('Select at least one scope item'); return false }
    }
    return true
  }

  const handleFinish = async () => {
    setError('')
    setLoading(true)
    try {
      await nexspenceApi.createMigrationJob({
        sourceUrl: form.sourceUrl,
        credentials: { username: form.username, password: form.password },
        options: { concurrency: parseInt(form.concurrency) || 4 },
        scope: {
          migrateRepos: scope.migrateRepos,
          migrateUsers: scope.migrateUsers,
          migratePolicies: scope.migratePolicies,
          migrateBlobs: scope.migrateBlobs,
        },
      })
      onCreated()
    } catch (err: unknown) {
      const e = err as { response?: { data?: { error?: string } } }
      setError(e.response?.data?.error ?? 'Failed to create migration job')
    } finally {
      setLoading(false)
    }
  }

  const LABEL = { fontSize: 11, fontWeight: 600 as const, color: 'var(--holo-text-dim)', textTransform: 'uppercase' as const, letterSpacing: '0.04em' }

  const step1 = (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
        <label style={LABEL}>Nexus URL *</label>
        <HoloInput placeholder="https://nexus.example.com" value={form.sourceUrl} onChange={set('sourceUrl')} autoFocus />
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <label style={LABEL}>Username</label>
          <HoloInput value={form.username} onChange={set('username')} />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
          <label style={LABEL}>Password *</label>
          <HoloInput type="password" value={form.password} onChange={set('password')} />
        </div>
      </div>
    </div>
  )

  const scopeItems: { key: keyof typeof scope; label: string }[] = [
    { key: 'migrateRepos',    label: 'Repositories' },
    { key: 'migrateUsers',    label: 'Users & Roles' },
    { key: 'migratePolicies', label: 'Cleanup Policies' },
    { key: 'migrateBlobs',   label: 'Artifacts (blobs)' },
  ]

  const step2 = (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <label style={LABEL}>Migration Scope</label>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
        {scopeItems.map(({ key, label }) => (
          <label key={key} style={{
            display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer',
            padding: '8px 10px',
            background: scope[key] ? 'rgba(59,130,246,0.1)' : 'rgba(255,255,255,0.03)',
            border: `1px solid ${scope[key] ? 'rgba(59,130,246,0.3)' : 'rgba(255,255,255,0.08)'}`,
            borderRadius: 8, transition: 'background 0.15s, border-color 0.15s', userSelect: 'none',
          }}>
            <input type="checkbox" checked={scope[key]} onChange={() => toggleScope(key)} style={{ accentColor: '#3b82f6', width: 14, height: 14 }} />
            <span style={{ fontSize: 13, color: scope[key] ? 'var(--holo-text)' : 'var(--holo-text-faint)', fontWeight: scope[key] ? 600 : 400 }}>{label}</span>
          </label>
        ))}
      </div>
    </div>
  )

  const step3 = (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
        <label style={LABEL}>Concurrency</label>
        <HoloInput type="number" min={1} max={16} value={form.concurrency} onChange={set('concurrency')} />
      </div>
      <div style={{
        background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(124,92,255,0.15)',
        borderRadius: 10, padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: 6,
      }}>
        <div style={{ fontSize: 11, fontWeight: 700, color: '#7c5cff', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 4 }}>Summary</div>
        <div style={{ fontSize: 12, color: 'var(--holo-text-dim)' }}>
          <b style={{ color: 'var(--holo-text)' }}>Source:</b> {form.sourceUrl || '—'}
        </div>
        <div style={{ fontSize: 12, color: 'var(--holo-text-dim)' }}>
          <b style={{ color: 'var(--holo-text)' }}>Scope:</b>{' '}
          {scopeItems.filter(i => scope[i.key]).map(i => i.label).join(', ') || 'none'}
        </div>
        <div style={{ fontSize: 12, color: 'var(--holo-text-dim)' }}>
          <b style={{ color: 'var(--holo-text)' }}>Concurrency:</b> {form.concurrency}
        </div>
      </div>
    </div>
  )

  return (
    <Wizard
      steps={[
        { label: 'Источник', content: step1 },
        { label: 'Область', content: step2 },
        { label: 'Параметры', content: step3 },
      ]}
      onFinish={handleFinish}
      finishLabel="Start Migration"
      onValidateStep={validateStep}
      onClose={onClose}
      loading={loading}
      error={error}
    />
  )
}
```

- [ ] **Step 3: Verify TypeScript**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit 2>&1 | head -30
```

Expected: zero errors.

- [ ] **Step 4: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add frontend/src/pages/AdminPage.tsx
git commit -m "feat(ui): Phase 40 — CreateMigrationJobModal stepped wizard"
```

---

## Task 4: PolicyModal create-branch → Wizard

**Depends on:** Task 1 complete  
**Files:**
- Modify: `frontend/src/pages/CleanupPage.tsx`

Strategy: `PolicyModal` receives `initial?: CleanupPolicy | null`. When `!initial` (create mode), render `<Wizard>`. When `initial` (edit mode), keep the existing `<HoloModal>` rendering unchanged.

- [ ] **Step 1: Add `Wizard` to imports**

Find:
```ts
import { HoloButton, HoloInput, HoloModal, HoloPill } from '@/components/holo'
```

Replace with:
```ts
import { HoloButton, HoloInput, HoloModal, HoloPill, Wizard } from '@/components/holo'
```

- [ ] **Step 2: Add wizard create-branch to `PolicyModal`**

Find the `return (` statement that begins the existing JSX in `PolicyModal` (the line that opens `<HoloModal open={true} onClose={onClose}>`). Insert a new early return for create mode **before** the existing return:

Add these new constants and the early return **after** the `handleSave` function and `set` helper, immediately before the existing `return (` line:

```tsx
  // ── Create mode: stepped wizard ───────────────────────────────────────
  if (!initial) {
    const LABEL = { fontSize: 12, fontWeight: 600 as const, color: 'var(--holo-text-dim)', textTransform: 'uppercase' as const, letterSpacing: '0.04em' }

    const [wizardError, setWizardError] = useState('')
    const [wizardLoading, setWizardLoading] = useState(false)

    const validateStep = (stepIdx: number): boolean => {
      setWizardError('')
      if (stepIdx === 0 && !form.name.trim()) {
        setWizardError('Name is required')
        return false
      }
      return true
    }

    const handleFinish = async () => {
      setWizardError('')
      if (!form.name.trim()) { setWizardError('Name is required'); return }
      setWizardLoading(true)
      try {
        await nexusApi.createCleanupPolicy(payload())
        onSaved()
        onClose()
      } catch (e: any) {
        setWizardError(e?.response?.data?.error ?? 'Save failed')
      } finally {
        setWizardLoading(false)
      }
    }

    const wizStep1 = (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Name *</label>
          <HoloInput value={form.name} onChange={set('name')} placeholder="e.g. delete-old-snapshots" autoFocus />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Description</label>
          <HoloInput value={form.description} onChange={set('description')} placeholder="Optional description" />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Format</label>
          <Select
            options={FORMATS.map(f => ({ value: f, label: f === '*' ? 'All formats' : f }))}
            value={form.format}
            onChange={v => setForm(f => ({ ...f, format: v }))}
          />
        </div>
      </div>
    )

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
      </div>
    )

    const wizStep3 = (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <label style={LABEL}>Schedule (cron)</label>
          <HoloInput value={form.scheduleCron} onChange={set('scheduleCron')} placeholder="e.g. 0 2 * * * (default: every 6 hours)" />
          <span style={{ fontSize: 11, color: 'rgba(229,231,235,0.35)' }}>Leave blank to use the global default. Format: minute hour day month weekday</span>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <label style={LABEL}>Options</label>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text-dim)', cursor: 'pointer' }}>
            <input type="checkbox" checked={form.enabled} onChange={e => setForm(f => ({ ...f, enabled: e.target.checked }))} />
            Enabled
          </label>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--holo-text-dim)', cursor: 'pointer' }}>
            <input type="checkbox" checked={form.dryRun} onChange={e => setForm(f => ({ ...f, dryRun: e.target.checked }))} />
            Dry run (no deletes)
          </label>
        </div>
      </div>
    )

    return (
      <Wizard
        steps={[
          { label: 'Идентификация', content: wizStep1 },
          { label: 'Критерии', content: wizStep2 },
          { label: 'Расписание', content: wizStep3 },
        ]}
        onFinish={handleFinish}
        finishLabel="Create Policy"
        onValidateStep={validateStep}
        onClose={onClose}
        loading={wizardLoading}
        error={wizardError}
      />
    )
  }
```

**Important:** The `useState` calls (`wizardError`, `wizardLoading`) must be moved outside the `if (!initial)` block — React hooks cannot be called conditionally. Declare them at the top of `PolicyModal` alongside the existing `form` and `err` state:

```tsx
const [wizardError, setWizardError] = useState('')
const [wizardLoading, setWizardLoading] = useState(false)
```

And inside the `if (!initial)` block, reference them without re-declaring. The `handleFinish` and `validateStep` for the wizard branch are regular (non-hook) functions so they can live inside the `if` block.

- [ ] **Step 3: Verify TypeScript**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit 2>&1 | head -30
```

Expected: zero errors.

- [ ] **Step 4: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add frontend/src/pages/CleanupPage.tsx
git commit -m "feat(ui): Phase 40 — New Cleanup Policy stepped wizard"
```

---

## Task 5: Final verification

**Depends on:** Tasks 2, 3, 4 all complete

- [ ] **Step 1: Full TypeScript check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit 2>&1
```

Expected: zero errors.

- [ ] **Step 2: Production build**

```bash
cd /home/skensel/AI/self_nexus/frontend && npm run build 2>&1 | tail -20
```

Expected: build succeeds, no errors, bundle sizes similar to before (~249 kB JS).

- [ ] **Step 3: Update task_plan.md**

In `task_plan.md`, find `## Phase 40:` and change its `**Status:** pending` to:

```
**Status:** complete (2026-04-26)
```

- [ ] **Step 4: Final commit**

```bash
cd /home/skensel/AI/self_nexus
git add task_plan.md
git commit -m "docs: mark Phase 40 complete — stepped wizard for Create forms"
```
