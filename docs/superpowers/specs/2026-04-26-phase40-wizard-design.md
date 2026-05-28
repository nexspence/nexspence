# Phase 40 — Stepped Wizard for Create Forms

**Date:** 2026-04-26  
**Status:** Approved

## Scope

Introduce a reusable `<Wizard>` component (Variant 3 — Stepped Wizard) for three specific Create flows:

| Modal | Location | Steps |
|-------|----------|-------|
| Create Repository | `RepositoriesPage.tsx` → `CreateRepoModal` | Type → Fields → Storage & Policies |
| New Migration Job | `AdminPage.tsx` → `CreateMigrationJobModal` | Source → Scope → Options & Review |
| New Cleanup Policy | `CleanupPage.tsx` → `PolicyModal` (create branch only) | Identity → Criteria → Schedule & Options |

All Edit modals remain unchanged.

## Component: `<Wizard>`

**File:** `frontend/src/components/holo/Wizard.tsx`

### Props

```ts
interface WizardStep {
  label: string
  content: React.ReactNode
}

interface WizardProps {
  steps: WizardStep[]
  onFinish: () => void | Promise<void>
  finishLabel?: string           // default: "Создать"
  onValidateStep?: (stepIndex: number) => boolean | Promise<boolean>
  onClose: () => void
  loading?: boolean
  error?: string
}
```

### Behaviour

- **Progress header**: numbered dots (done ✓ / active / pending) with connecting lines. Clicking a dot navigates back to a completed step only (no skipping forward).
- **Footer**: Back button (hidden on step 1) + step counter + Next/Finish button.
- **Next**: calls `onValidateStep(currentStep)` if provided; blocks advance on false.
- **Finish**: calls `onFinish()`; shows spinner when `loading=true`.
- **Esc / close button**: calls `onClose()`. If any field is dirty, the parent is responsible for confirming (wizard itself does not block close).
- **Animation**: CSS `translateX` slide — forward slides left, back slides right. 200 ms `ease-out`.

### Styles

Added to `frontend/src/components/holo/holo.css` under a `/* Wizard */` section:
- `.holo-wizard` — outer container, matches existing glassmorphism token set
- `.holo-wizard__progress` — flex row, `rgba(0,0,0,0.35)` bg, bottom border
- `.holo-wizard__dot` — 24px circle, variants: `--done` (blue filled), `--active` (blue outline glow), `--pending` (grey)
- `.holo-wizard__line` — flex `1`, 1.5px, turns blue when step is done
- `.holo-wizard__body` — `padding: 20px`, `min-height: 180px`
- `.holo-wizard__footer` — `justify-content: space-between`, top border, `rgba(0,0,0,0.15)` bg
- `.holo-wizard__step` — `display: none` / `.holo-wizard__step--active { display: block }` + slide animation

## Step Breakdown

### Create Repository — 3 steps

**Step 1 — Тип**
- Format picker: grid of chips (maven2, npm, docker, pypi, helm, go, nuget, raw, cargo, conan, yum, apt)
- Type cards: Hosted / Proxy / Group (icon + name + one-line description)
- Validation: format and type must be selected (both have defaults so Next is always enabled)

**Step 2 — Основные поля**
- `name` (required), `description`
- Conditional: `remoteUrl` shown only when type=proxy (pre-filled with `PROXY_DEFAULTS[format]`)
- Conditional: `memberNames` multi-select shown only when type=group
- Validation: name non-empty; if proxy then remoteUrl non-empty; if group then ≥1 member

**Step 3 — Хранилище и политики**
- `blobStoreId` select (hidden for group)
- `quotaGB` number input with store-quota guard (hidden for group)
- `cleanupPolicyIds` multi-select (hidden for group)
- `allowAnonymous` checkbox
- Validation: non-group requires a blob store selected

---

### New Migration Job — 3 steps

**Step 1 — Источник**
- `sourceUrl` (required), `username`, `password` (required)
- Validation: sourceUrl and password non-empty

**Step 2 — Область миграции**
- Four toggle cards: Repositories / Users & Roles / Cleanup Policies / Artifacts (blobs)
- All checked by default
- Validation: at least one scope item checked

**Step 3 — Параметры и ревью**
- `concurrency` number input (1–16, default 4)
- Read-only summary card showing: Source URL, selected scope items, concurrency
- Submit button label: "Start Migration"
- Validation: none (concurrency already has min/max)

---

### New Cleanup Policy — 3 steps

**Step 1 — Идентификация**
- `name` (required), `description`, `format` select
- Validation: name non-empty

**Step 2 — Критерии**
- `lastDownloadedDays`, `artifactAgeDays` (number inputs, optional)
- `pathPrefix`, `nameGlob` (text inputs, optional)
- Validation: none — all fields optional (matches existing behaviour; a policy with no criteria matches everything)

**Step 3 — Расписание и опции**
- `scheduleCron` text input with placeholder hint
- `enabled` checkbox (default: true)
- `dryRun` checkbox (default: false)
- Validation: none (all optional)

---

## Files Changed

| File | Change |
|------|--------|
| `frontend/src/components/holo/Wizard.tsx` | **New** — component |
| `frontend/src/components/holo/holo.css` | **Mod** — wizard CSS section |
| `frontend/src/pages/RepositoriesPage.tsx` | **Mod** — `CreateRepoModal` uses `<Wizard>` |
| `frontend/src/pages/AdminPage.tsx` | **Mod** — `CreateMigrationJobModal` uses `<Wizard>` |
| `frontend/src/pages/CleanupPage.tsx` | **Mod** — `PolicyModal` create-branch uses `<Wizard>` |

## Out of Scope

- Edit modals (EditRepoModal, Edit PolicyModal, Edit Migration) — unchanged
- All other modals (Users, Roles, Webhooks, Content Selectors, Blob Stores) — unchanged
- Mobile/responsive layout — not addressed in this phase
- Keyboard navigation between steps (Tab through fields is native; step navigation via buttons only)
