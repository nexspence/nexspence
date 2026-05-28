# Docs Page: Guides + Conda/Terraform Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the existing DocsPage with 7 how-to guide articles, Conda and Terraform format docs, real brand icons for all format nav items via Simple Icons CDN, and a screenshot placeholder system that shows a styled dashed box when a PNG file is missing from `frontend/public/docs/screenshots/`.

**Architecture:** All changes are confined to two existing files (`DocsPage.tsx`, `DocsPage.module.css`) plus one new directory (`frontend/public/docs/screenshots/`). New `Screenshot`, `ScreenshotPlaceholder`, and `Step` components are added to `DocsPage.tsx`. The `Format` interface gains an optional `iconUrl` field. The left nav gains a `GUIDES` section above `FORMATS`. No backend changes — Conda and Terraform are already wired in `internal/api/router.go`.

**Tech Stack:** React + TypeScript, CSS Modules, Simple Icons CDN (SVG icons at `cdn.simpleicons.org`), lucide-react (existing), Vite static asset serving

**Working directory for all commands:** `/Users/skensel/WORKING/AI/nexspence-core`

---

## File Structure

**Modified:**
- `frontend/src/pages/DocsPage.tsx` (currently 664 lines) — add Screenshot/Step components, StepProps interface, iconUrl on Format, 7 guide components, 2 new format entries, nav restructure
- `frontend/src/pages/DocsPage.module.css` (currently 236 lines) — append step + screenshot + typeCard + navBrandIcon classes

**Created:**
- `frontend/public/docs/screenshots/.gitkeep` — tracks the screenshots directory in git; PNG files added here later replace the dashed placeholders automatically

---

### Task 1: Add CSS classes for steps, screenshots, and brand icons

**Files:**
- Modify: `frontend/src/pages/DocsPage.module.css` — append ~90 lines at the end

- [ ] **Step 1: Append new CSS classes to DocsPage.module.css**

Open `frontend/src/pages/DocsPage.module.css` and append the following at the very end (after the last existing rule):

```css
/* ─── Guide Steps ─────────────────────────────── */
.step {
  display: flex;
  gap: 16px;
  margin-bottom: 20px;
}

.stepNum {
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: linear-gradient(135deg, #7c5cff, #22d3ee);
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  margin-top: 2px;
}

.stepBody {
  flex: 1;
  min-width: 0;
}

.stepTitle {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--holo-text);
  margin: 0 0 4px;
}

.stepText {
  font-size: 13px;
  color: var(--holo-text-dim);
  line-height: 1.65;
  margin: 0 0 10px;
}

/* ─── Screenshots ─────────────────────────────── */
.screenshotWrap {
  border-radius: 10px;
  overflow: hidden;
  border: 1px solid var(--holo-border);
  margin-top: 10px;
  margin-bottom: 4px;
}

.screenshotImg {
  width: 100%;
  display: block;
}

.screenshotCaption {
  font-size: 11px;
  color: var(--holo-text-faint);
  text-align: center;
  padding: 5px 0;
  font-style: italic;
  background: rgba(255,255,255,0.015);
  border-top: 1px solid var(--holo-border);
}

.screenshotPlaceholder {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 5px;
  padding: 18px 20px;
  background: rgba(124,92,255,0.04);
  border: 2px dashed rgba(124,92,255,0.25);
  border-radius: 10px;
  margin-top: 10px;
  text-align: center;
}

.screenshotPlaceholderLabel {
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: rgba(124,92,255,0.6);
}

.screenshotPlaceholderName {
  font-size: 12px;
  color: var(--holo-text-faint);
  font-style: italic;
}

.screenshotPlaceholderPath {
  font-family: 'Geist Mono', monospace;
  font-size: 10px;
  color: rgba(124,92,255,0.4);
  background: rgba(124,92,255,0.08);
  padding: 2px 8px;
  border-radius: 4px;
}

/* ─── Type cards (Hosted / Proxy / Group) ─────── */
.typeCards {
  display: grid;
  grid-template-columns: 1fr 1fr 1fr;
  gap: 8px;
  margin: 10px 0 14px 44px;
}

.typeCard {
  padding: 10px 12px;
  border-radius: 8px;
  border: 1px solid var(--holo-border);
  background: rgba(255,255,255,0.02);
}

.typeCardName {
  font-size: 12.5px;
  font-weight: 700;
  color: var(--holo-text);
  margin-bottom: 4px;
}

.typeCardDesc {
  font-size: 11.5px;
  color: var(--holo-text-dim);
  line-height: 1.5;
}

/* ─── Format brand icons in nav ───────────────── */
.navBrandIcon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  object-fit: contain;
  filter: brightness(0.85);
}

.docsNavBtn.active .navBrandIcon {
  filter: brightness(1.2);
}
```

- [ ] **Step 2: Verify build passes**

Run: `cd frontend && npm run build 2>&1 | tail -3`
Expected: `✓ built in ...` with no errors (new classes aren't used yet, that's fine)

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/DocsPage.module.css
git commit -m "feat(docs): add CSS for guide steps, screenshot placeholders, type cards, brand nav icons"
```

---

### Task 2: Add Screenshot + Step components and update Format interface

**Files:**
- Modify: `frontend/src/pages/DocsPage.tsx`

- [ ] **Step 1: Read the current interfaces block (lines 5–13)**

Run: `sed -n '5,13p' frontend/src/pages/DocsPage.tsx`
Expected output:
```
interface CodeExample { label?: string; lang: string; content: string }
interface FormatSection { title: string; text?: string; note?: string; codes: CodeExample[] }
interface Format {
  id: string
  name: string
  icon: string
  description: string
  sections: (base: string) => FormatSection[]
}
```

- [ ] **Step 2: Replace the interfaces block with the extended version**

Replace lines 5–13 in `frontend/src/pages/DocsPage.tsx` (the three existing interfaces) with:

```tsx
interface CodeExample { label?: string; lang: string; content: string }
interface FormatSection { title: string; text?: string; note?: string; codes: CodeExample[] }
interface Format {
  id: string
  name: string
  icon: string
  iconUrl?: string
  description: string
  sections: (base: string) => FormatSection[]
}
interface StepProps {
  num: number
  title: string
  text: string
  screenshot?: { src: string; alt: string; caption?: string }
  code?: { lang: string; content: string }
  note?: string
}
```

- [ ] **Step 3: Add ScreenshotPlaceholder + Screenshot + Step components after SectionBlock (after line 72)**

Find the closing `}` of `function SectionBlock` (currently at line 72) and insert the following three components immediately after it (before the blank line that precedes `const FORMATS`):

```tsx
function ScreenshotPlaceholder({ alt, src }: { alt: string; src: string }) {
  const filename = src.split('/').pop() ?? src
  return (
    <div className={styles.screenshotPlaceholder}>
      <span className={styles.screenshotPlaceholderLabel}>📸 Screenshot</span>
      <span className={styles.screenshotPlaceholderName}>{alt}</span>
      <span className={styles.screenshotPlaceholderPath}>
        frontend/public/docs/screenshots/{filename}
      </span>
    </div>
  )
}

function Screenshot({ src, alt, caption }: { src: string; alt: string; caption?: string }) {
  const [failed, setFailed] = useState(false)
  if (failed) return <ScreenshotPlaceholder alt={alt} src={src} />
  return (
    <div className={styles.screenshotWrap}>
      <img
        src={src}
        alt={alt}
        className={styles.screenshotImg}
        onError={() => setFailed(true)}
      />
      {caption && <p className={styles.screenshotCaption}>{caption}</p>}
    </div>
  )
}

function Step({ num, title, text, screenshot, code, note }: StepProps) {
  return (
    <div className={styles.step}>
      <div className={styles.stepNum}>{num}</div>
      <div className={styles.stepBody}>
        <p className={styles.stepTitle}>{title}</p>
        <p className={styles.stepText}>{text}</p>
        {note && <div className={styles.noteBox}>⚠ {note}</div>}
        {code && <CodeBlock lang={code.lang} content={code.content} />}
        {screenshot && <Screenshot {...screenshot} />}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: TypeScript check**

Run: `cd frontend && npx tsc --noEmit 2>&1 | head -10`
Expected: no output

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/DocsPage.tsx
git commit -m "feat(docs): add Screenshot, ScreenshotPlaceholder, Step components + iconUrl on Format interface"
```

---

### Task 3: Add 7 guide stubs and restructure the nav

**Files:**
- Modify: `frontend/src/pages/DocsPage.tsx`

This task adds 7 minimal stub guide components (returning just a header div) and rewires the DocsPage nav to show `GUIDES` then `FORMATS` sections with brand icons. Guide content is filled in Task 4.

- [ ] **Step 1: Insert 7 stub guide components before GettingStarted**

Find `function GettingStarted` (currently around line 553, but shifted down by the insertions in Task 2 — use `grep -n "function GettingStarted"` to find the exact line). Insert the following 7 stubs immediately before that function:

```tsx
function GuideRepositories() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Creating Repositories</h1>
        <p className={styles.sectionDesc}>Step-by-step guide to creating Hosted, Proxy, and Group repositories for any supported format.</p>
      </div>
    </>
  )
}
function GuideUsers() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Managing Users</h1>
        <p className={styles.sectionDesc}>Create local user accounts, assign roles, and manage API token access.</p>
      </div>
    </>
  )
}
function GuideRolesPrivileges() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Roles &amp; Privileges</h1>
        <p className={styles.sectionDesc}>Set up RBAC with Content Selectors → Privileges → Roles → Users.</p>
      </div>
    </>
  )
}
function GuideContentSelectors() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Content Selectors</h1>
        <p className={styles.sectionDesc}>Write CEL expressions to scope artifact path access for privileges.</p>
      </div>
    </>
  )
}
function GuideSecurityScanning() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Security Scanning</h1>
        <p className={styles.sectionDesc}>Scan artifacts for CVE vulnerabilities using the OSV database.</p>
      </div>
    </>
  )
}
function GuideCleanupPolicies() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Cleanup Policies</h1>
        <p className={styles.sectionDesc}>Automate artifact retention with scheduled cleanup rules.</p>
      </div>
    </>
  )
}
function GuideApiTokens() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>API Tokens</h1>
        <p className={styles.sectionDesc}>Generate nxs_* tokens and use them for Basic Auth or Bearer authentication.</p>
      </div>
    </>
  )
}
```

- [ ] **Step 2: Replace the DocsPage return block with the restructured nav**

Find `export default function DocsPage()` and replace its entire `return (...)` block with:

```tsx
  return (
    <div className={styles.docsLayout}>
      <nav className={styles.docsNav}>
        <button
          className={`${styles.docsNavBtn} ${active === 'getting-started' ? styles.active : ''}`}
          onClick={() => setActive('getting-started')}
        >
          <BookOpen size={14} style={{ flexShrink: 0 }} />
          Getting Started
        </button>

        <div className={styles.docsNavSection}>Guides</div>
        {([
          { id: 'guide-repos',     label: '🗄 Creating Repositories' },
          { id: 'guide-users',     label: '👥 Managing Users' },
          { id: 'guide-roles',     label: '🛡 Roles & Privileges' },
          { id: 'guide-selectors', label: '🔍 Content Selectors' },
          { id: 'guide-security',  label: '🔐 Security Scanning' },
          { id: 'guide-cleanup',   label: '🗑 Cleanup Policies' },
          { id: 'guide-tokens',    label: '🔑 API Tokens' },
        ] as const).map(g => (
          <button
            key={g.id}
            className={`${styles.docsNavBtn} ${active === g.id ? styles.active : ''}`}
            onClick={() => setActive(g.id)}
          >
            {g.label}
          </button>
        ))}

        <div className={styles.docsNavSection}>Formats</div>
        {FORMATS.map(f => (
          <button
            key={f.id}
            className={`${styles.docsNavBtn} ${active === f.id ? styles.active : ''}`}
            onClick={() => setActive(f.id)}
          >
            {f.iconUrl
              ? <img src={f.iconUrl} alt="" width={14} height={14} className={styles.navBrandIcon} onError={(e) => { (e.currentTarget as HTMLImageElement).style.display = 'none' }} />
              : <span style={{ fontSize: 14, lineHeight: 1, flexShrink: 0 }}>{f.icon}</span>
            }
            {f.name}
          </button>
        ))}
      </nav>

      <div className={styles.docsContent}>
        {active === 'getting-started' && <GettingStarted base={base} />}
        {active === 'guide-repos'     && <GuideRepositories />}
        {active === 'guide-users'     && <GuideUsers />}
        {active === 'guide-roles'     && <GuideRolesPrivileges />}
        {active === 'guide-selectors' && <GuideContentSelectors />}
        {active === 'guide-security'  && <GuideSecurityScanning />}
        {active === 'guide-cleanup'   && <GuideCleanupPolicies />}
        {active === 'guide-tokens'    && <GuideApiTokens />}
        {FORMATS.map(f => active === f.id && <FormatContent key={f.id} format={f} base={base} />)}
      </div>
    </div>
  )
```

- [ ] **Step 3: TypeScript check**

Run: `cd frontend && npx tsc --noEmit 2>&1 | head -10`
Expected: no output

- [ ] **Step 4: Build check**

Run: `cd frontend && npm run build 2>&1 | tail -3`
Expected: `✓ built in ...`

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/DocsPage.tsx
git commit -m "feat(docs): add 7 guide stubs + GUIDES/FORMATS nav sections + brand icon rendering in nav"
```

---

### Task 4: Fill all 7 guide components with real content

**Files:**
- Modify: `frontend/src/pages/DocsPage.tsx` — replace each stub with the full component

Replace each stub function one at a time. Each stub currently just renders a `sectionHeader` div. Replace the entire function body for each.

- [ ] **Step 1: Replace GuideRepositories**

Find `function GuideRepositories()` and replace it entirely with:

```tsx
function GuideRepositories() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Creating Repositories</h1>
        <p className={styles.sectionDesc}>
          Repositories are the core building blocks of Nexspence. Choose from Hosted (store your own artifacts), Proxy (cache a remote registry), or Group (combine multiple repos under one URL).
        </p>
      </div>
      <Step num={1} title="Open the Repositories page"
        text='Click "Repositories" in the sidebar. Then click the "+ New Repository" button in the top-right corner.'
        screenshot={{ src: '/docs/screenshots/repo-list-new-btn.png', alt: 'Repositories page with + New Repository button' }}
      />
      <hr className={styles.divider} />
      <Step num={2} title="Select the repository type"
        text="The wizard opens on the Type step. Choose one of three types:"
      />
      <div className={styles.typeCards}>
        <div className={styles.typeCard}>
          <div className={styles.typeCardName}>🗄 Hosted</div>
          <div className={styles.typeCardDesc}>Stores artifacts locally. Use for publishing your own packages.</div>
        </div>
        <div className={styles.typeCard} style={{ borderColor: 'rgba(34,211,238,0.2)', background: 'rgba(34,211,238,0.03)' }}>
          <div className={styles.typeCardName}>🔄 Proxy</div>
          <div className={styles.typeCardDesc}>Caches a remote registry (npmjs.com, PyPI, Docker Hub, etc.)</div>
        </div>
        <div className={styles.typeCard} style={{ borderColor: 'rgba(255,92,240,0.18)', background: 'rgba(255,92,240,0.03)' }}>
          <div className={styles.typeCardName}>🗂 Group</div>
          <div className={styles.typeCardDesc}>Merges several repos into one URL. Single endpoint for clients.</div>
        </div>
      </div>
      <Screenshot src="/docs/screenshots/create-repo-step1-type.png" alt="Wizard Step 1 — select Hosted, Proxy, or Group" />
      <hr className={styles.divider} />
      <Step num={3} title="Enter a name and select a format"
        text="On the Details step, enter a unique repository name (used in the URL) and select the format (Maven, npm, PyPI, Docker, etc.)."
        screenshot={{ src: '/docs/screenshots/create-repo-step2-details.png', alt: 'Wizard Step 2 — name and format fields' }}
      />
      <hr className={styles.divider} />
      <Step num={4} title="(Proxy) Set the Remote URL"
        text="If you chose Proxy, enter the upstream registry URL on the Storage step. Common values:"
        code={{ lang: 'text', content: `Maven Central  → https://repo1.maven.org/maven2/
npm            → https://registry.npmjs.org/
PyPI           → https://pypi.org/
Docker Hub     → https://registry-1.docker.io/
Go proxy       → https://proxy.golang.org/
Helm stable    → https://charts.helm.sh/stable/
Cargo          → https://index.crates.io/` }}
      />
      <hr className={styles.divider} />
      <Step num={5} title="(Group) Add member repositories"
        text="If you chose Group, select member repositories on the Storage step. All members must share the same format. Order determines lookup priority — first match wins."
        note="A group cannot contain another group. Members must already exist."
        screenshot={{ src: '/docs/screenshots/create-repo-step3-group.png', alt: 'Wizard Step 3 — group member selection' }}
      />
      <hr className={styles.divider} />
      <Step num={6} title="Choose a blob store (optional)"
        text='The Storage step lets you pick which blob store holds the artifacts. Leave as "default" unless you have multiple blob stores configured (System Admin → Blob Stores).'
      />
      <hr className={styles.divider} />
      <Step num={7} title='Click Create and copy the URL'
        text="Click Create Repository. The new repo appears in the list. Click on it to see its URL — copy it to configure your build tool."
        screenshot={{ src: '/docs/screenshots/repo-detail-url.png', alt: 'Repository detail card with URL and copy button' }}
      />
    </>
  )
}
```

- [ ] **Step 2: Replace GuideUsers**

Find `function GuideUsers()` and replace it entirely with:

```tsx
function GuideUsers() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Managing Users</h1>
        <p className={styles.sectionDesc}>
          Create local user accounts, assign roles, and manage API token access. Requires admin. Users can also be provisioned automatically via LDAP or OIDC/SAML SSO.
        </p>
      </div>
      <Step num={1} title="Open System Admin → Users"
        text='Click "System Admin" in the sidebar (admin only), then select the "Users" tab.'
        screenshot={{ src: '/docs/screenshots/admin-users-tab.png', alt: 'System Admin page with Users tab selected' }}
      />
      <hr className={styles.divider} />
      <Step num={2} title="Create a new user"
        text='Click "+ Create User". Fill in username, first name, last name, email, and password. The username must be unique and is used for login and Basic Auth.'
        screenshot={{ src: '/docs/screenshots/create-user-form.png', alt: 'Create User form with all fields' }}
      />
      <hr className={styles.divider} />
      <Step num={3} title="Assign roles"
        text='After saving, click the shield icon (Assign Roles) in the user row. The transfer list lets you move roles from Available to Assigned. Click Save.'
        screenshot={{ src: '/docs/screenshots/assign-roles-dialog.png', alt: 'Assign Roles transfer list dialog' }}
      />
      <hr className={styles.divider} />
      <Step num={4} title="View or revoke a user's API tokens"
        text="Each user manages their own API tokens from their profile. As an admin you can view token names and last-used timestamps, and revoke any token from the Users tab."
        note="Token values are shown only once at creation. If a user loses a token, they must create a new one."
      />
    </>
  )
}
```

- [ ] **Step 3: Replace GuideRolesPrivileges**

Find `function GuideRolesPrivileges()` and replace it entirely with:

```tsx
function GuideRolesPrivileges() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Roles & Privileges</h1>
        <p className={styles.sectionDesc}>
          Nexspence uses a three-layer RBAC model: Content Selector (what paths) → Privilege (permission scoped to a selector) → Role (bundle of privileges) → User (assigned roles).
        </p>
      </div>
      <Step num={1} title="Understand the model"
        text="Before creating anything, understand the chain: a Content Selector defines which artifact paths are in scope (via CEL expression). A Privilege links a Content Selector to a permission type. A Role bundles multiple privileges. A User is assigned one or more roles."
      />
      <hr className={styles.divider} />
      <Step num={2} title="Create a Content Selector first"
        text='Go to Security → Content Selectors → click "+ New". Write a CEL expression. Example — allow all Maven artifacts:'
        code={{ lang: 'cel', content: 'format == "maven2"' }}
        screenshot={{ src: '/docs/screenshots/content-selector-form.png', alt: 'Content Selector form with CEL expression input' }}
      />
      <hr className={styles.divider} />
      <Step num={3} title="Create a Privilege"
        text='Go to Security → Privileges → click "+ New". Select the Content Selector you just created. The privilege is automatically scoped to the paths matched by that selector.'
        screenshot={{ src: '/docs/screenshots/create-privilege-form.png', alt: 'Create Privilege form with Content Selector dropdown' }}
      />
      <hr className={styles.divider} />
      <Step num={4} title="Create a Role"
        text='Go to Security → Roles → click "+ New Role". Give it a name (e.g. "maven-reader"), then add the privilege from Step 3.'
        screenshot={{ src: '/docs/screenshots/create-role-form.png', alt: 'Create Role form with privilege assignment list' }}
      />
      <hr className={styles.divider} />
      <Step num={5} title="Assign the Role to a User"
        text="Go to Security → Users (or System Admin → Users). Click the Assign Roles button for the target user and add the new role. Changes take effect on the user's next API request."
        screenshot={{ src: '/docs/screenshots/assign-roles-dialog.png', alt: 'Assign Roles dialog with the new role selected' }}
      />
    </>
  )
}
```

- [ ] **Step 4: Replace GuideContentSelectors**

Find `function GuideContentSelectors()` and replace it entirely with:

```tsx
function GuideContentSelectors() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Content Selectors</h1>
        <p className={styles.sectionDesc}>
          Content Selectors use CEL (Common Expression Language) to match artifact paths. They are the foundation of Nexspence's privilege system — every privilege must reference a selector.
        </p>
      </div>
      <Step num={1} title="Open Content Selectors"
        text='Navigate to Security → Content Selectors. Click "+ New Content Selector".'
        screenshot={{ src: '/docs/screenshots/content-selectors-list.png', alt: 'Content Selectors list page with + New button' }}
      />
      <hr className={styles.divider} />
      <Step num={2} title="CEL expression fields"
        text="Expressions can reference these fields to match artifacts:"
        code={{ lang: 'text', content: `format      — repository format  ("maven2", "npm", "docker", "pypi", "helm", …)
path        — artifact path      ("/com/example/myapp/1.0/myapp-1.0.jar")
repository  — repository name   ("maven-releases", "docker-hosted", …)` }}
      />
      <hr className={styles.divider} />
      <Step num={3} title="Example expressions"
        code={{ lang: 'cel', content: `# All artifacts (wildcard — grants access to everything)
true

# All Maven artifacts in any repository
format == "maven2"

# Specific Maven group only (company packages)
format == "maven2" && path.startsWith("/com/mycompany/")

# npm scoped packages only
format == "npm" && path.startsWith("/@myorg/")

# Docker images in a specific repository
format == "docker" && repository == "docker-hosted"

# All Python packages from the proxy cache
format == "pypi" && repository == "pypi-proxy"

# Helm charts from any hosted repo
format == "helm" && repository.endsWith("-hosted")` }}
      />
      <hr className={styles.divider} />
      <Step num={4} title="Save and use in a Privilege"
        text='Click Save. The selector appears in the Content Selector dropdown when creating a Privilege. See the Roles & Privileges guide for the next steps.'
        screenshot={{ src: '/docs/screenshots/content-selector-saved.png', alt: 'Content Selectors list showing the newly created selector' }}
      />
    </>
  )
}
```

- [ ] **Step 5: Replace GuideSecurityScanning**

Find `function GuideSecurityScanning()` and replace it entirely with:

```tsx
function GuideSecurityScanning() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Security Scanning</h1>
        <p className={styles.sectionDesc}>
          Nexspence scans artifacts for known CVE vulnerabilities using the OSV (Open Source Vulnerabilities) database at api.osv.dev. Supported formats: Maven, npm, PyPI, Cargo.
        </p>
      </div>
      <Step num={1} title="Open the Vulnerability Dashboard"
        text='Navigate to Security → CVE Scan tab. The dashboard shows 6 severity cards and a paginated table of all findings across your repositories.'
        screenshot={{ src: '/docs/screenshots/vuln-dashboard.png', alt: 'Vulnerability Dashboard with severity cards (Critical, High, Medium, Low, Negligible, Unknown)' }}
      />
      <hr className={styles.divider} />
      <Step num={2} title="Run a bulk scan"
        text='Click "Rescan All" to queue a scan of all components in supported formats. Nexspence queries api.osv.dev for each package name + version. Results appear as the scan progresses.'
        note="Bulk scans call an external API (api.osv.dev). Ensure outbound HTTPS is allowed from your Nexspence host."
      />
      <hr className={styles.divider} />
      <Step num={3} title="Filter and inspect findings"
        text="Use the severity filter buttons to narrow the table. Each row shows the CVE ID, package, affected version, fix version (if available), and severity."
        screenshot={{ src: '/docs/screenshots/vuln-table-filter.png', alt: 'Vulnerability table with severity filter toolbar' }}
      />
      <hr className={styles.divider} />
      <Step num={4} title="Scan a single component"
        text='From Search or Browse, open a component detail panel and click "Scan". Results are cached — click "Rescan" to force a fresh check against OSV.'
        screenshot={{ src: '/docs/screenshots/component-scan-result.png', alt: 'Component detail panel showing scan result with severity badges' }}
      />
      <hr className={styles.divider} />
      <Step num={5} title="Interpret severity levels"
        code={{ lang: 'text', content: `CRITICAL    — Actively exploitable. Immediate action required.
HIGH        — Serious risk. Patch as soon as possible.
MEDIUM      — Moderate risk. Patch in next release cycle.
LOW         — Minor risk. Patch opportunistically.
NEGLIGIBLE  — Theoretical risk. No known active exploits.
UNKNOWN     — Severity not determined by OSV database.` }}
      />
    </>
  )
}
```

- [ ] **Step 6: Replace GuideCleanupPolicies**

Find `function GuideCleanupPolicies()` and replace it entirely with:

```tsx
function GuideCleanupPolicies() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Cleanup Policies</h1>
        <p className={styles.sectionDesc}>
          Cleanup Policies delete old or unused artifacts automatically based on age, download inactivity, or version count. Policies run on a cron schedule or on demand.
        </p>
      </div>
      <Step num={1} title="Open Cleanup Policies"
        text='Click "Cleanup Policies" in the sidebar. Existing policies appear as cards showing their criteria and schedule.'
        screenshot={{ src: '/docs/screenshots/cleanup-policies-list.png', alt: 'Cleanup Policies page with policy cards' }}
      />
      <hr className={styles.divider} />
      <Step num={2} title="Create a new policy"
        text='Click "+ New Policy". The wizard has three steps: Identification (name + format filter), Criteria, and Schedule.'
        screenshot={{ src: '/docs/screenshots/cleanup-policy-wizard.png', alt: 'Create Policy wizard — Identification step with name and format fields' }}
      />
      <hr className={styles.divider} />
      <Step num={3} title="Set cleanup criteria"
        text="On the Criteria step, configure one or more rules. An artifact is a deletion candidate if it matches ANY enabled criterion (set to 0 to disable a criterion):"
        code={{ lang: 'text', content: `Last Downloaded  — delete if not downloaded in N days
Last Modified    — delete if not updated in N days
Retain N Versions — keep only the N newest versions per artifact name
                    (older versions are deleted regardless of age)` }}
      />
      <hr className={styles.divider} />
      <Step num={4} title="Set a schedule"
        text='On the Schedule step, enter a cron expression for automatic runs. Leave blank to run manually only. Examples:'
        code={{ lang: 'text', content: `0 2 * * *    — daily at 2:00 AM
0 3 * * 0    — every Sunday at 3:00 AM
0 0 1 * *    — first of every month at midnight` }}
      />
      <hr className={styles.divider} />
      <Step num={5} title="Attach the policy to a repository"
        text="On the Repositories page, click the gear icon on a repository card. Select your policy from the Cleanup Policy dropdown and save. One policy can be shared across multiple repositories."
        screenshot={{ src: '/docs/screenshots/repo-attach-cleanup.png', alt: 'Repository settings panel with Cleanup Policy dropdown' }}
      />
      <hr className={styles.divider} />
      <Step num={6} title='Run manually with "Run Now"'
        text='On the Cleanup Policies page, click "Run Now" on any policy card to execute immediately. A summary shows how many artifacts were deleted.'
        screenshot={{ src: '/docs/screenshots/cleanup-run-now.png', alt: 'Policy card with Run Now button and deletion summary' }}
      />
    </>
  )
}
```

- [ ] **Step 7: Replace GuideApiTokens**

Find `function GuideApiTokens()` and replace it entirely with:

```tsx
function GuideApiTokens() {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>API Tokens</h1>
        <p className={styles.sectionDesc}>
          API tokens let you authenticate without using your password. Tokens start with <span className={styles.inlineCode}>nxs_</span> and work as a Basic Auth password or as a Bearer token header.
        </p>
      </div>
      <Step num={1} title="Open your profile"
        text="Click the key icon (🔑) at the bottom of the sidebar to open your profile modal. Select the API Tokens tab."
        screenshot={{ src: '/docs/screenshots/profile-api-tokens.png', alt: 'Profile modal with API Tokens tab open' }}
      />
      <hr className={styles.divider} />
      <Step num={2} title="Create a token"
        text='Click "+ Create Token". Enter a descriptive name (e.g. "ci-pipeline") and an optional expiry in days. The maximum allowed expiry is shown next to the input field.'
        screenshot={{ src: '/docs/screenshots/create-token-form.png', alt: 'Create Token dialog with name and expiry day fields' }}
      />
      <hr className={styles.divider} />
      <Step num={3} title="Copy the token immediately"
        text="After creation, the full token is displayed once. Click the Copy button — it will not be shown again. Store it in a secrets manager or CI secrets vault."
        note="If you lose the token value, delete it and create a new one. There is no way to retrieve the value after closing this dialog."
        screenshot={{ src: '/docs/screenshots/token-created-copy.png', alt: 'Newly created token value with copy button highlighted' }}
      />
      <hr className={styles.divider} />
      <Step num={4} title="Use the token as Basic Auth password"
        text="Pass the token as the HTTP Basic Auth password. Your username stays the same:"
        code={{ lang: 'bash', content: `# curl
curl -u admin:nxs_your_token_here \\
  "https://nexspence.example.com/service/rest/v1/repositories"

# Maven ~/.m2/settings.xml
<password>nxs_your_token_here</password>

# npm ~/.npmrc
//nexspence.example.com/repository/npm-hosted/:_authToken=nxs_your_token_here` }}
      />
      <hr className={styles.divider} />
      <Step num={5} title="Use the token as a Bearer header"
        text="Alternatively, pass the token as an Authorization: Bearer header (no username needed):"
        code={{ lang: 'bash', content: `curl -H "Authorization: Bearer nxs_your_token_here" \\
  "https://nexspence.example.com/service/rest/v1/repositories"` }}
      />
      <hr className={styles.divider} />
      <Step num={6} title="Revoke a token"
        text="On the API Tokens tab, click the ✕ button next to any token to revoke it immediately. Revoked tokens are rejected on the next API call."
        screenshot={{ src: '/docs/screenshots/token-revoke.png', alt: 'API Tokens list with revoke (X) button visible' }}
      />
    </>
  )
}
```

- [ ] **Step 8: TypeScript check**

Run: `cd frontend && npx tsc --noEmit 2>&1 | head -10`
Expected: no output

- [ ] **Step 9: Build check**

Run: `cd frontend && npm run build 2>&1 | tail -3`
Expected: `✓ built in ...`

- [ ] **Step 10: Commit**

```bash
git add frontend/src/pages/DocsPage.tsx
git commit -m "feat(docs): fill all 7 guide components — steps, screenshots, code examples"
```

---

### Task 5: Add iconUrl to existing formats + Conda + Terraform

**Files:**
- Modify: `frontend/src/pages/DocsPage.tsx`

- [ ] **Step 1: Add iconUrl to all 12 existing FORMATS entries**

For each format entry in `const FORMATS`, add the `iconUrl` field after the `icon` field. Use the Edit tool to make each change individually. The exact edits for each format:

**maven** — find `icon: '☕',` inside the maven entry and add the line after it:
```tsx
    iconUrl: 'https://cdn.simpleicons.org/apachemaven/C71A36',
```

**npm** — find `icon: '📦',` inside the npm entry:
```tsx
    iconUrl: 'https://cdn.simpleicons.org/npm/CB3837',
```

**pypi** — find `icon: '🐍',` inside the pypi entry:
```tsx
    iconUrl: 'https://cdn.simpleicons.org/pypi/3775A9',
```

**docker** — find `icon: '🐳',` inside the docker entry:
```tsx
    iconUrl: 'https://cdn.simpleicons.org/docker/2496ED',
```

**go** — find `icon: '🔵',` inside the go entry:
```tsx
    iconUrl: 'https://cdn.simpleicons.org/go/00ADD8',
```

**nuget** — find `icon: '💜',` inside the nuget entry:
```tsx
    iconUrl: 'https://cdn.simpleicons.org/nuget/004880',
```

**raw** — `icon: '📄'` — leave without iconUrl (no clean Simple Icons entry for generic files)

**helm** — find `icon: '⚓',` inside the helm entry:
```tsx
    iconUrl: 'https://cdn.simpleicons.org/helm/0F1689',
```

**cargo** — find `icon: '🦀',` inside the cargo entry:
```tsx
    iconUrl: 'https://cdn.simpleicons.org/rust/b7410e',
```

**apt** — find `icon: '🐧',` inside the apt entry:
```tsx
    iconUrl: 'https://cdn.simpleicons.org/debian/A81D33',
```

**yum** — find `icon: '🔴',` inside the yum entry:
```tsx
    iconUrl: 'https://cdn.simpleicons.org/fedora/51A2DA',
```

**conan** — find `icon: '🔧',` inside the conan entry:
```tsx
    iconUrl: 'https://cdn.simpleicons.org/conan/6699CB',
```

- [ ] **Step 2: Append the Conda format entry to FORMATS**

Find the closing `},` of the conan entry (the last entry in the array, before the `]`). After that `},` and before the `]`, insert:

```tsx
  {
    id: 'conda',
    name: 'Conda',
    icon: '🐍',
    iconUrl: 'https://cdn.simpleicons.org/anaconda/44A833',
    description: 'Conda channel repository for Python and data science packages. Serves repodata.json index and .conda/.tar.bz2 binaries organized by platform subdirectory.',
    sections: (base) => [
      {
        title: 'Channel URL',
        text: 'Channels are organized by platform. Replace the subdirectory with your target architecture:',
        codes: [{ lang: 'text', content: `${base}/repository/conda-hosted/linux-64/
${base}/repository/conda-hosted/osx-arm64/
${base}/repository/conda-hosted/win-64/
${base}/repository/conda-hosted/noarch/      ← platform-independent packages` }],
      },
      {
        title: 'Configure ~/.condarc',
        text: 'Add Nexspence as a channel. Prepend it so your hosted packages take priority:',
        codes: [{ lang: 'yaml', content: `channels:
  - ${base}/repository/conda-hosted/
  - defaults
ssl_verify: true` }],
      },
      {
        title: 'Publish a Package',
        codes: [
          { label: 'Build the package:', lang: 'bash', content: `conda build myrecipe/` },
          { label: 'Upload the built .conda file:', lang: 'bash', content: `# Find the output path
PKG=$(conda build myrecipe/ --output)

curl -u admin:admin123 \\
  -T "$PKG" \\
  "${base}/repository/conda-hosted/linux-64/$(basename $PKG)"` },
        ],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Using conda:', lang: 'bash', content: `conda install mypackage \\
  -c ${base}/repository/conda-hosted/ \\
  --override-channels` },
          { label: 'Using mamba (faster resolver):', lang: 'bash', content: `mamba install mypackage \\
  -c ${base}/repository/conda-hosted/ \\
  --override-channels` },
          { label: 'Direct download with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/conda-hosted/linux-64/mypackage-1.0.0-py311_0.conda"` },
        ],
      },
    ],
  },
```

- [ ] **Step 3: Append the Terraform format entry to FORMATS**

After the conda entry's closing `},`, insert:

```tsx
  {
    id: 'terraform',
    name: 'Terraform',
    icon: '🏗',
    iconUrl: 'https://cdn.simpleicons.org/terraform/7B42BC',
    description: 'Terraform Registry Protocol v1 for providers and modules. Supports service discovery at /.well-known/terraform.json, version listing, and binary hosting for both hosted and proxy types.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/terraform-hosted/
Service discovery: ${base}/.well-known/terraform.json` }],
      },
      {
        title: 'Configure .terraformrc',
        text: 'Tell Terraform to use Nexspence for provider installation. Add to ~/.terraformrc (macOS/Linux) or %APPDATA%/terraform.rc (Windows):',
        codes: [{ lang: 'hcl', content: `credentials "${base.replace(/^https?:\/\//, '')}" {
  token = "nxs_your_token_here"
}

provider_installation {
  network_mirror {
    url = "${base}/repository/terraform-hosted/"
  }
}` }],
      },
      {
        title: 'Use a Provider',
        text: 'Reference the provider in your Terraform configuration, then run terraform init — Terraform fetches it from Nexspence:',
        codes: [
          { label: 'main.tf:', lang: 'hcl', content: `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}` },
          { label: 'Initialize:', lang: 'bash', content: `terraform init` },
        ],
      },
      {
        title: 'Use a Module',
        codes: [
          { label: 'main.tf:', lang: 'hcl', content: `module "vpc" {
  source  = "${base.replace(/^https?:\/\//, '')}/myorg/vpc/aws"
  version = "1.0.0"
}` },
          { label: 'Initialize:', lang: 'bash', content: `terraform init` },
        ],
      },
      {
        title: 'Publish a Provider',
        codes: [{ label: 'Upload binary for linux_amd64:', lang: 'bash', content: `curl -u admin:admin123 \\
  -X PUT \\
  --data-binary @terraform-provider-myprovider_1.0.0_linux_amd64.zip \\
  "${base}/repository/terraform-hosted/v1/providers/myorg/myprovider/1.0.0/upload/linux/amd64"` }],
      },
      {
        title: 'Publish a Module',
        codes: [{ label: 'Upload module archive:', lang: 'bash', content: `tar -czf mymodule-1.0.0.tar.gz -C mymodule/ .
curl -u admin:admin123 \\
  -X PUT \\
  --data-binary @mymodule-1.0.0.tar.gz \\
  "${base}/repository/terraform-hosted/v1/modules/myorg/mymodule/aws/1.0.0"` }],
      },
    ],
  },
```

- [ ] **Step 4: TypeScript check**

Run: `cd frontend && npx tsc --noEmit 2>&1 | head -10`
Expected: no output

- [ ] **Step 5: Build check — verify chunk grew**

Run: `cd frontend && npm run build 2>&1 | grep -E "DocsPage|built"`
Expected: DocsPage chunk size larger than ~20 kB, `✓ built in ...`

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/DocsPage.tsx
git commit -m "feat(docs): add Conda + Terraform format docs, add Simple Icons iconUrl to all 12 existing formats"
```

---

### Task 6: Create screenshots directory + final verification

**Files:**
- Create: `frontend/public/docs/screenshots/.gitkeep`

- [ ] **Step 1: Create the screenshots directory**

```bash
mkdir -p frontend/public/docs/screenshots
touch frontend/public/docs/screenshots/.gitkeep
```

- [ ] **Step 2: Verify the directory is inside frontend/public**

Run: `ls frontend/public/docs/screenshots/`
Expected: `.gitkeep`

- [ ] **Step 3: Full production build**

Run: `cd frontend && npm run build 2>&1 | tail -6`
Expected: build succeeds, no errors or warnings

- [ ] **Step 4: Strict TypeScript check**

Run: `cd frontend && npx tsc --noEmit 2>&1`
Expected: no output (zero errors)

- [ ] **Step 5: Verify guide and format counts**

```bash
# Count guide IDs in nav array (should be 7)
grep -c "guide-" frontend/src/pages/DocsPage.tsx

# Count format entries (should be 14: 12 original + conda + terraform)
grep "id: '" frontend/src/pages/DocsPage.tsx | grep -v "interface\|StepProps" | wc -l
```
Expected: first ≥ 7, second ≥ 14

- [ ] **Step 6: Verify all 6 CSS guide-related class names are in the stylesheet**

```bash
grep -c "\.step\|\.screenshot\|\.typeCard\|\.navBrandIcon" frontend/src/pages/DocsPage.module.css
```
Expected: ≥ 10

- [ ] **Step 7: Final commit**

```bash
git add frontend/public/docs/screenshots/.gitkeep
git commit -m "feat(docs): add screenshots directory — place PNGs here to replace placeholders"
```

---

## Self-Review

**Spec coverage:**
- ✅ 7 guide components — Tasks 3 and 4
- ✅ Screenshot placeholder system (dashed box, filename hint, real img on load) — Tasks 1 and 2
- ✅ Screenshots stored at `frontend/public/docs/screenshots/` — Task 6
- ✅ Conda format with channel URL, .condarc, publish, install — Task 5
- ✅ Terraform format with service discovery, .terraformrc, provider + module use + publish — Task 5
- ✅ iconUrl on all 12 existing formats — Task 5
- ✅ Nav restructure: Getting Started → GUIDES → FORMATS — Task 3
- ✅ New CSS classes (step, screenshot, typeCard, navBrandIcon) — Task 1
- ✅ All content in English — confirmed throughout
- ✅ No backend changes — Conda and Terraform already in router.go

**Placeholder scan:** No TBD, TODO, or "implement later" phrases. All guide steps, code examples, and screenshot src paths are concrete.

**Type consistency:**
- `StepProps` defined in Task 2, `Step` component uses it in Task 2, guide components call `<Step num={N} ...>` in Task 4 ✅
- `Screenshot({ src, alt, caption })` defined in Task 2, called with spread `{...screenshot}` where `screenshot` matches the type ✅
- `Format.iconUrl?: string` added in Task 2, set in Task 5, consumed in nav rendering added in Task 3 ✅
- `styles.typeCards` / `styles.typeCard` / `styles.typeCardName` / `styles.typeCardDesc` defined in Task 1, used in `GuideRepositories` in Task 4 ✅
- `styles.screenshotPlaceholder` / `styles.screenshotPlaceholderLabel` / `styles.screenshotPlaceholderName` / `styles.screenshotPlaceholderPath` defined in Task 1, used in `ScreenshotPlaceholder` in Task 2 ✅
- `styles.navBrandIcon` defined in Task 1, used in nav in Task 3 ✅
