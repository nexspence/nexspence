# Docs Page Extension: Guides + Conda/Terraform Design

## Goal

Extend the existing in-app documentation page (`frontend/src/pages/DocsPage.tsx`) with:
1. Two new format sections — **Conda** and **Terraform** — already implemented in the backend
2. A **Guides** section with 7 step-by-step how-to articles covering core Nexspence workflows
3. A **Screenshot placeholder** system: styled dashed-border boxes showing the expected filename in `frontend/public/docs/screenshots/`; replaced with a real `<img>` once the file exists

All documentation text is in **English**.

---

## Architecture

### Left Nav Reorganization

The left nav gains a `GUIDES` section between "Getting Started" and `FORMATS`. Section labels are uppercase, 10px, faint color — same as the existing `docsNavSection` class.

```
Getting Started

GUIDES
  Creating Repositories
  Managing Users
  Roles & Privileges
  Content Selectors
  Security Scanning
  Cleanup Policies
  API Tokens

FORMATS  (14 total — was 12)
  Maven / npm / PyPI / Docker / Go / NuGet / Raw
  Helm / Cargo / Apt / Yum / Conan / Conda ← new / Terraform ← new
```

All navigation uses the same state-based switching (`active` string) already implemented — no routing changes needed.

### Screenshot Placeholder System

A `Screenshot` component renders either a real image or a styled placeholder:

```tsx
function Screenshot({ src, alt, caption }: { src: string; alt: string; caption?: string }) {
  const [failed, setFailed] = useState(false)
  if (failed || !src) return <ScreenshotPlaceholder alt={alt} src={src} />
  return (
    <div className={styles.screenshotWrap}>
      <img src={src} alt={alt} onError={() => setFailed(true)} className={styles.screenshotImg} />
      {caption && <p className={styles.screenshotCaption}>{caption}</p>}
    </div>
  )
}
```

`ScreenshotPlaceholder` shows:
- Camera icon + "Screenshot" label (uppercase, purple)
- Alt text (italic, faint)
- File path hint: `→ frontend/public/docs/screenshots/<filename>`

Screenshots stored in `frontend/public/docs/screenshots/` (served as static assets by Vite). An empty `.gitkeep` ensures the directory exists in git.

### Guide Step Structure

Each guide is a function component receiving `{ base: string }`. Steps use a numbered badge + body layout:

```tsx
interface GuideStep {
  num: number
  title: string
  text: string                              // plain text or JSX
  screenshot?: { src: string; alt: string; caption?: string }
  code?: { lang: string; content: string }  // optional curl/config example
  note?: string                             // optional amber warning box
}
```

Step numbers use a small gradient circle badge (purple→cyan gradient, matching existing holo aesthetic).

### New CSS Classes

Added to `DocsPage.module.css`:
- `.step` — flex row, gap 16px
- `.stepNum` — 28×28px gradient circle, white text
- `.stepBody` — flex 1
- `.stepTitle` — 13.5px, 600 weight, full text color
- `.stepText` — 13px, dim color, line-height 1.6
- `.screenshotWrap` — border-radius 10px, border, margin-top 8px
- `.screenshotImg` — width 100%, display block
- `.screenshotCaption` — 11px, faint, italic, centered
- `.screenshotPlaceholder` — dashed border (rgba purple 0.28), rounded 10px, padding 20px, centered
- `.screenshotPlaceholderLabel` — 11px uppercase, purple, 600 weight
- `.screenshotPlaceholderName` — 12px italic, faint
- `.screenshotPlaceholderPath` — 10px, monospace, very faint
- `.typeCards` — 3-column grid for Hosted/Proxy/Group type explanation cards

### Format Icons

Nav buttons for all 14 formats use `<img>` from Simple Icons CDN (same approach as the landing page already uses). Each `<img>` has `width={14} height={14}` and an `onError` fallback to emoji. Raw and Conan fall back to emoji since they have no Simple Icons entry that renders well at 14px.

| Format | Icon URL |
|--------|----------|
| Maven | `https://cdn.simpleicons.org/apachemaven/C71A36` |
| npm | `https://cdn.simpleicons.org/npm/CB3837` |
| PyPI | `https://cdn.simpleicons.org/pypi/3775A9` |
| Docker | `https://cdn.simpleicons.org/docker/2496ED` |
| Go | `https://cdn.simpleicons.org/go/00ADD8` |
| NuGet | `https://cdn.simpleicons.org/nuget/004880` |
| Helm | `https://cdn.simpleicons.org/helm/0F1689` |
| Cargo | `https://cdn.simpleicons.org/rust/b7410e` |
| Apt | `https://cdn.simpleicons.org/debian/A81D33` |
| Yum | `https://cdn.simpleicons.org/fedora/51A2DA` |
| Conan | `https://cdn.simpleicons.org/conan/6699CB` |
| Conda | `https://cdn.simpleicons.org/anaconda/44A833` |
| Terraform | `https://cdn.simpleicons.org/terraform/7B42BC` |
| Raw | emoji `📄` |

The existing 12 format nav buttons currently use emoji — they will be upgraded to `<img>` icons as part of this change. The icon `<img>` elements in the nav do **not** cause an API call per nav item since Simple Icons CDN returns SVG synchronously and browsers cache aggressively.

---

## Guide Content Outline

### 1. Creating Repositories
Steps:
1. Navigate to Repositories → click **+ New Repository** — screenshot: repo list with button
2. Choose type: Hosted / Proxy / Group — type cards explanation + screenshot: wizard step 1
3. Enter name and select format — screenshot: wizard step 2
4. *(Proxy only)* Set Remote URL — list of common upstream URLs per format
5. *(Proxy only)* Set HTTP auth if upstream requires credentials — note box
6. *(Group only)* Add member repositories — note: members must share same format, ordered by priority
7. Choose blob store (leave default for most setups)
8. Click **Create** — screenshot: completed repo card with URL

### 2. Managing Users
Steps:
1. Navigate to **System Admin → Users** tab (admin only)
2. Click **+ Create User** — fill name, email, username, password
3. Screenshot: user creation form
4. Assign roles via the transfer list — screenshot: role assignment dialog
5. View and revoke API tokens for a user

### 3. Roles & Privileges
Steps:
1. Understand the model: Content Selector → Privilege → Role → User
2. Navigate to **Security → Content Selectors** — create a selector first (required for privilege)
3. Navigate to **Security → Privileges** — create a privilege referencing the selector
4. Navigate to **Security → Roles** — create a role, add privileges
5. Assign role to user (Security → Users or via user profile)
6. Screenshot: full chain visualization — selector → privilege → role → user

### 4. Content Selectors
Steps:
1. Navigate to **Security → Content Selectors**
2. Click **+ New Content Selector**
3. CEL expression syntax reference (table: fields, operators, examples)
4. Example selectors: Maven group filter, npm scoped packages, Docker repo restrict, all-paths wildcard
5. Screenshot: content selector form with CEL preview
6. Test the selector: paste a sample path and verify it matches

### 5. Security Scanning
Steps:
1. Navigate to **Security → CVE Scan** tab
2. Run a scan on a single component: browse to component → click Scan
3. Screenshot: component scan result with severity badges
4. View the Vulnerability Dashboard: 6 severity cards, filter toolbar, paginated table
5. Screenshot: vulnerability dashboard overview
6. Run Bulk Scan across all components — note: queries OSV API (api.osv.dev) for Maven/npm/PyPI/Cargo
7. Interpret results: severity levels (CRITICAL / HIGH / MEDIUM / LOW / NEGLIGIBLE / UNKNOWN)

### 6. Cleanup Policies
Steps:
1. Navigate to **Cleanup Policies**
2. Click **+ New Policy** — wizard: Identification → Criteria → Schedule
3. Set criteria: Last Downloaded (days), Last Modified (days), Retain N Versions
4. Set schedule: cron expression (e.g. `0 2 * * *` = daily at 2am) or leave blank for manual only
5. Attach policy to a repository: go to repo settings → Cleanup Policy field
6. Screenshot: policy attached to a repository in repo settings
7. Run manually: **Run Now** button on the policy card

### 7. API Tokens
Steps:
1. Click the key icon (🔑) in the sidebar → **API Tokens** tab
2. Click **+ Create Token** — set name and optional expiry (max days shown)
3. Copy the token immediately — it starts with `nxs_` and is shown only once
4. Screenshot: token creation dialog with copy button
5. Use as Basic Auth password: `curl -u admin:nxs_your_token_here ...`
6. Use as Bearer token: `curl -H "Authorization: Bearer nxs_your_token_here" ...`
7. Revoke a token: click ✕ next to the token in the list

---

## Conda Format Documentation

Conda is a cross-platform package manager used in data science (Python, R, Julia). The Nexspence Conda handler implements the **Conda channel protocol** — it serves `repodata.json` index files and `.conda`/`.tar.bz2` package binaries.

Sections:
1. Repository URL — `{base}/repository/conda-hosted/{platform}/` (platform: `linux-64`, `osx-arm64`, `win-64`, `noarch`)
2. Configure conda client — `~/.condarc`
3. Publish a package — `conda build` + `curl -T` upload
4. Install a package — `conda install`, `mamba install`
5. Download with curl — direct `.conda` file fetch

## Terraform Format Documentation

Terraform is HashiCorp's infrastructure-as-code tool. The Nexspence Terraform handler implements the **Terraform Registry Protocol v1** — serving providers and modules with service discovery at `/.well-known/terraform.json`.

Sections:
1. Repository URL — `{base}/repository/terraform-hosted/`
2. Service discovery — how Terraform finds the registry
3. Configure `.terraformrc` — `credentials` block
4. Publish a provider — upload binary + JSON metadata via `curl PUT`
5. Publish a module — `curl PUT` with `.tar.gz`
6. Use a provider — `terraform { required_providers { ... } }` + `terraform init`
7. Use a module — `module "..." { source = "..." }`

---

## Files Changed

**New:**
- `frontend/public/docs/screenshots/.gitkeep` — ensures directory exists
- (screenshot PNGs added by user over time)

**Modified:**
- `frontend/src/pages/DocsPage.tsx` — add `Screenshot` component, `GuideStep` type, 7 guide components, 2 new format entries, icon upgrades in nav, nav section reorganization
- `frontend/src/pages/DocsPage.module.css` — add step + screenshot CSS classes

**No backend changes** — Conda and Terraform are already fully wired in the router.

---

## Out of Scope

- Syntax highlighting for code blocks (existing plain text is sufficient)
- Search within docs
- PDF export
- Versioned docs
- Internationalization (docs are English-only)
