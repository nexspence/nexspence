# Documentation Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a beautiful in-app documentation page accessible to all authenticated users, reachable via a "Documentation" button in the sidebar footer (above user info), showing usage examples for all 12 repository formats with curl commands and configuration snippets.

**Architecture:** A single static React page (`DocsPage.tsx`) with a two-column layout — fixed left nav listing all formats, scrollable right content showing the active format's docs. All content is static (no API calls). The base URL is detected from `window.location.origin` so examples show the correct deployment URL automatically.

**Tech Stack:** React + TypeScript, CSS Modules, lucide-react (BookOpen, Copy, Check icons), existing holo design system variables

---

## UI Design Preview

### Sidebar — Docs button placement (above user info)

```
┌────────────────────────────┐
│  ◈ Nexspence          [<] │
├────────────────────────────┤
│  BROWSE                    │
│  ⊞ Repositories            │
│  ⊟ Browse                  │
│  ⊠ Search                  │
│                            │
│  SYSTEM (admin only)       │
│  ⊡ Security                │
│  ⚙ System Admin            │
│  ≡ Audit Log               │
│  ⌫ Cleanup Policies        │
│                            │
│           ↕ (flex grow)    │
├────────────────────────────┤
│  📖 Documentation          │  ← NEW button above user info
├────────────────────────────┤
│  John Smith  [Admin]  [🔑]│
│  [Sign Out]                │
│  Nexspence v1.2.0          │
└────────────────────────────┘
```

### Documentation Page Layout

```
┌─────────────────────────────────────────────────────────────────────┐
│                                                                     │
├──────────────────────┬──────────────────────────────────────────────┤
│                      │                                              │
│  ○ Getting Started   │  ☕ Maven 2/3                                │
│                      │  ──────────────────────────────────────────  │
│  FORMATS             │  Host, proxy, and group Maven repositories   │
│  ● Maven             │                                              │
│  ○ npm               │  REPOSITORY URL                              │
│  ○ PyPI              │  ┌─────────────────────────────────── [Copy]┐│
│  ○ Docker            │  │ http://localhost:8080/repository/         ││
│  ○ Go Modules        │  │ maven-releases/                           ││
│  ○ NuGet             │  └───────────────────────────────────────────┘│
│  ○ Raw               │                                              │
│  ○ Helm              │  CONFIGURE ~/.M2/SETTINGS.XML                │
│  ○ Cargo             │  ┌─────────────────────── xml ───── [Copy] ─┐│
│  ○ Apt/Debian        │  │ <settings>                               ││
│  ○ Yum/RPM           │  │   <servers>...                           ││
│  ○ Conan C/C++       │  │   <mirrors>...                           ││
│                      │  └───────────────────────────────────────────┘│
│                      │                                              │
│                      │  PUBLISH AN ARTIFACT                         │
│                      │  Using mvn deploy plugin:                    │
│                      │  ┌──────────────────── bash ───── [Copy] ───┐│
│                      │  │ mvn deploy:deploy-file \                 ││
│                      │  │   -DrepositoryId=nexspence \             ││
│                      │  │   -Durl=http://.../maven-releases/ \     ││
│                      │  │   -Dfile=myapp-1.0.jar               📋 ││
│                      │  └───────────────────────────────────────────┘│
│                      │  Using curl:                                 │
│                      │  ┌──────────────────── bash ───── [Copy] ───┐│
│                      │  │ curl -u admin:admin123 \                 ││
│                      │  │   -T myapp-1.0.jar \                     ││
│                      │  │   "http://.../myapp-1.0.jar"         📋 ││
│                      │  └───────────────────────────────────────────┘│
└──────────────────────┴──────────────────────────────────────────────┘
```

---

## File Structure

**New files:**
- `frontend/src/pages/DocsPage.tsx` — full documentation page: layout + CodeBlock component + all 12 format sections + Getting Started
- `frontend/src/pages/DocsPage.module.css` — two-column layout, code block styles, nav styles

**Modified files:**
- `frontend/src/App.tsx` — add lazy `/docs` route inside Layout (line ~17 for import, line ~85 for route)
- `frontend/src/components/Layout.tsx` — add `BookOpen` icon import + Docs NavLink in footer above userInfo block

---

### Task 1: Add Route and Sidebar Documentation Button

**Files:**
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/components/Layout.tsx`

- [ ] **Step 1: Read App.tsx to see exact insertion points**

Run: `head -120 frontend/src/App.tsx`
Expected: See the lazy import block (lines 11-17) and the routes block inside Layout (lines 51-110)

- [ ] **Step 2: Add lazy DocsPage import in App.tsx**

In `frontend/src/App.tsx`, after line 17 (after `const AuditPage = lazy(...)`), add:

```tsx
const DocsPage = lazy(() => import('@/pages/DocsPage'))
```

- [ ] **Step 3: Add /docs route in App.tsx**

Inside the Layout route children (after the AuditPage route, around line 110), add:

```tsx
<Route path="docs" element={<DocsPage />} />
```

- [ ] **Step 4: Add BookOpen import in Layout.tsx**

In `frontend/src/components/Layout.tsx`, add `BookOpen` to the existing lucide-react import line:

```tsx
import {
  Home, Search, FolderOpen, Trash2,
  Settings, Shield, FileText, LogOut,
  Key, Plus, X, Copy, Check,
  ChevronLeft, ChevronRight, BookOpen,
} from 'lucide-react'
```

- [ ] **Step 5: Add Docs NavLink in Layout.tsx footer (above userInfo)**

In `frontend/src/components/Layout.tsx`, find the footer section (line ~282):
```tsx
{/* Footer */}
<div className={styles.footer}>
  {user && (
    <div className={styles.userInfo}>
```

Insert the Docs NavLink BEFORE the `{user && <div className={styles.userInfo}>` block:

```tsx
{/* Footer */}
<div className={styles.footer}>
  <NavLink
    to="/docs"
    className={({ isActive }) =>
      `${styles.navBtn} ${isActive ? styles.active : ''}`
    }
    title={collapsed ? 'Documentation' : undefined}
  >
    <BookOpen size={16} />
    <span className={styles.navLabel}>Documentation</span>
  </NavLink>
  {user && (
    <div className={styles.userInfo}>
```

- [ ] **Step 6: Create empty DocsPage placeholder so build succeeds**

Create `frontend/src/pages/DocsPage.tsx`:

```tsx
export default function DocsPage() {
  return <div style={{ padding: 32, color: 'var(--holo-text)' }}>Documentation coming soon…</div>
}
```

- [ ] **Step 7: Verify TypeScript builds without errors**

Run: `cd frontend && npm run build 2>&1 | tail -20`
Expected: Build succeeds (the placeholder page is valid TSX)

- [ ] **Step 8: Commit**

```bash
git add frontend/src/App.tsx frontend/src/components/Layout.tsx frontend/src/pages/DocsPage.tsx
git commit -m "feat(docs): add /docs route and Documentation button in sidebar footer"
```

---

### Task 2: Create DocsPage.module.css

**Files:**
- Create: `frontend/src/pages/DocsPage.module.css`

- [ ] **Step 1: Create the CSS file**

Create `frontend/src/pages/DocsPage.module.css` with the following content:

```css
.docsLayout {
  display: grid;
  grid-template-columns: 220px 1fr;
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

.docsNav {
  overflow-y: auto;
  padding: 12px 8px;
  border-right: 1px solid var(--holo-border);
  display: flex;
  flex-direction: column;
  gap: 2px;
  scrollbar-width: thin;
  scrollbar-color: var(--holo-a) transparent;
}

.docsNavSection {
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--holo-text-faint);
  padding: 12px 8px 4px;
}

.docsNavBtn {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 10px;
  border-radius: 8px;
  border: 1px solid transparent;
  background: none;
  color: var(--holo-text-dim);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  width: 100%;
  text-align: left;
  transition: all 0.15s;
}

.docsNavBtn:hover {
  background: rgba(255,255,255,0.04);
  color: var(--holo-text);
}

.docsNavBtn.active {
  background: linear-gradient(90deg, rgba(124,92,255,0.18), rgba(34,211,238,0.06));
  border-color: rgba(124,92,255,0.30);
  color: var(--holo-text);
  font-weight: 600;
}

.docsContent {
  overflow-y: auto;
  padding: 28px 36px;
  scrollbar-width: thin;
  scrollbar-color: var(--holo-a) transparent;
}

.sectionHeader {
  margin-bottom: 28px;
  padding-bottom: 20px;
  border-bottom: 1px solid var(--holo-border);
}

.sectionTitle {
  font-size: 22px;
  font-weight: 700;
  color: var(--holo-text);
  margin: 0 0 8px;
}

.sectionDesc {
  font-size: 14px;
  color: var(--holo-text-dim);
  margin: 0;
  line-height: 1.6;
}

.blockTitle {
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.07em;
  text-transform: uppercase;
  color: var(--holo-text-dim);
  margin: 20px 0 8px;
}

.blockText {
  font-size: 13px;
  color: var(--holo-text-dim);
  margin: 0 0 8px;
  line-height: 1.6;
}

/* URL display block */
.urlBlock {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
  background: rgba(34,211,238,0.05);
  border: 1px solid rgba(34,211,238,0.18);
  border-radius: 8px;
  margin-bottom: 20px;
}

.urlValue {
  flex: 1;
  font-family: 'Geist Mono', 'Fira Code', monospace;
  font-size: 13px;
  color: var(--holo-b);
  word-break: break-all;
}

.urlCopyBtn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  border-radius: 6px;
  border: 1px solid rgba(34,211,238,0.25);
  background: rgba(34,211,238,0.08);
  color: var(--holo-b);
  font-size: 11px;
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
  transition: all 0.15s;
}

.urlCopyBtn:hover {
  background: rgba(34,211,238,0.15);
}

/* Code block */
.codeBlock {
  background: rgba(0,0,0,0.35);
  border: 1px solid var(--holo-border);
  border-radius: 10px;
  overflow: hidden;
  margin-bottom: 12px;
}

.codeHeader {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 5px 12px;
  background: rgba(255,255,255,0.02);
  border-bottom: 1px solid var(--holo-border);
}

.codeLang {
  font-size: 10px;
  font-weight: 700;
  color: var(--holo-a);
  font-family: 'Geist Mono', monospace;
  text-transform: uppercase;
  letter-spacing: 0.06em;
}

.copyBtn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 3px 8px;
  border-radius: 5px;
  border: 1px solid rgba(124,92,255,0.25);
  background: rgba(124,92,255,0.08);
  color: var(--holo-text-dim);
  font-size: 11px;
  cursor: pointer;
  transition: all 0.15s;
}

.copyBtn:hover {
  background: rgba(124,92,255,0.15);
  color: var(--holo-text);
}

.copyBtn.copied {
  color: #5effb8;
  border-color: rgba(94,255,184,0.30);
  background: rgba(94,255,184,0.08);
}

.codeBody {
  padding: 14px 16px;
  overflow-x: auto;
  scrollbar-width: thin;
}

.codeBody pre {
  margin: 0;
  font-family: 'Geist Mono', 'Fira Code', 'Cascadia Code', monospace;
  font-size: 12.5px;
  line-height: 1.65;
  color: #e2e8f0;
  white-space: pre;
}

.codeLabel {
  font-size: 12px;
  color: var(--holo-text-faint);
  margin: 8px 0 4px;
}

.divider {
  border: none;
  border-top: 1px solid var(--holo-border);
  margin: 28px 0;
}

.noteBox {
  padding: 10px 14px;
  background: rgba(255,200,87,0.06);
  border: 1px solid rgba(255,200,87,0.20);
  border-radius: 8px;
  font-size: 13px;
  color: #ffc857;
  margin-bottom: 12px;
  line-height: 1.5;
}

.inlineCode {
  font-family: 'Geist Mono', monospace;
  background: rgba(124,92,255,0.12);
  padding: 1px 6px;
  border-radius: 4px;
  font-size: 12px;
  color: var(--holo-a);
}
```

- [ ] **Step 2: Verify CSS file created correctly**

Run: `wc -l frontend/src/pages/DocsPage.module.css`
Expected: ~150 lines

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/DocsPage.module.css
git commit -m "feat(docs): add DocsPage CSS — two-column layout, code blocks, nav styles"
```

---

### Task 3: Implement Full DocsPage with All Format Sections

**Files:**
- Replace: `frontend/src/pages/DocsPage.tsx` — full implementation

- [ ] **Step 1: Write the complete DocsPage.tsx**

Replace the entire contents of `frontend/src/pages/DocsPage.tsx` with:

```tsx
import { useState } from 'react'
import { BookOpen, Check, Copy } from 'lucide-react'
import styles from './DocsPage.module.css'

interface CodeExample { label?: string; lang: string; content: string }
interface FormatSection { title: string; text?: string; note?: string; codes: CodeExample[] }
interface Format {
  id: string
  name: string
  icon: string
  description: string
  sections: (base: string) => FormatSection[]
}

function CodeBlock({ lang, content }: { lang: string; content: string }) {
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard.writeText(content)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }
  return (
    <div className={styles.codeBlock}>
      <div className={styles.codeHeader}>
        <span className={styles.codeLang}>{lang}</span>
        <button className={`${styles.copyBtn} ${copied ? styles.copied : ''}`} onClick={copy}>
          {copied ? <Check size={11} /> : <Copy size={11} />}
          {copied ? 'Copied!' : 'Copy'}
        </button>
      </div>
      <div className={styles.codeBody}>
        <pre>{content}</pre>
      </div>
    </div>
  )
}

function UrlBlock({ url }: { url: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className={styles.urlBlock}>
      <span className={styles.urlValue}>{url}</span>
      <button
        className={styles.urlCopyBtn}
        onClick={() => { navigator.clipboard.writeText(url); setCopied(true); setTimeout(() => setCopied(false), 2000) }}
      >
        {copied ? <Check size={11} /> : <Copy size={11} />}
        {copied ? 'Copied' : 'Copy'}
      </button>
    </div>
  )
}

function SectionBlock({ section }: { section: FormatSection }) {
  return (
    <div>
      <p className={styles.blockTitle}>{section.title}</p>
      {section.text && <p className={styles.blockText}>{section.text}</p>}
      {section.note && <div className={styles.noteBox}>⚠ {section.note}</div>}
      {section.codes.map((c, i) => (
        <div key={i}>
          {c.label && <p className={styles.codeLabel}>{c.label}</p>}
          <CodeBlock lang={c.lang} content={c.content} />
        </div>
      ))}
    </div>
  )
}

const FORMATS: Format[] = [
  {
    id: 'maven',
    name: 'Maven 2/3',
    icon: '☕',
    description: 'Host, proxy, and group Maven repositories for Java artifacts (JAR, WAR, POM files). Fully compatible with Maven 2 and Maven 3.',
    sections: (base) => [
      {
        title: 'Repository URL',
        text: 'Use these endpoints in settings.xml or pom.xml:',
        codes: [{ lang: 'text', content: `${base}/repository/maven-releases/\n${base}/repository/maven-snapshots/\n${base}/repository/maven-central/   ← proxy cache` }],
      },
      {
        title: 'Configure ~/.m2/settings.xml',
        text: 'Route all Maven traffic through Nexspence and set credentials:',
        codes: [{ lang: 'xml', content: `<settings>
  <servers>
    <server>
      <id>nexspence</id>
      <username>admin</username>
      <password>admin123</password>
    </server>
  </servers>
  <mirrors>
    <mirror>
      <id>nexspence</id>
      <url>${base}/repository/maven-public/</url>
      <mirrorOf>*</mirrorOf>
    </mirror>
  </mirrors>
</settings>` }],
      },
      {
        title: 'Publish an Artifact',
        codes: [
          { label: 'Using mvn deploy plugin:', lang: 'bash', content: `mvn deploy:deploy-file \\
  -DrepositoryId=nexspence \\
  -Durl=${base}/repository/maven-releases/ \\
  -Dfile=myapp-1.0.jar \\
  -DgroupId=com.example \\
  -DartifactId=myapp \\
  -Dversion=1.0` },
          { label: 'Using curl (direct PUT):', lang: 'bash', content: `curl -u admin:admin123 \\
  -T myapp-1.0.jar \\
  "${base}/repository/maven-releases/com/example/myapp/1.0/myapp-1.0.jar"` },
        ],
      },
      {
        title: 'Download an Artifact',
        codes: [{ lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/maven-releases/com/example/myapp/1.0/myapp-1.0.jar"` }],
      },
    ],
  },
  {
    id: 'npm',
    name: 'npm',
    icon: '📦',
    description: 'Host and proxy npm packages. Supports npm publish, install, and the full npm registry protocol.',
    sections: (base) => [
      {
        title: 'Repository URLs',
        codes: [{ lang: 'text', content: `${base}/repository/npm-hosted/   ← publish target\n${base}/repository/npm-proxy/    ← proxy to npmjs.com\n${base}/repository/npm-group/    ← combined group` }],
      },
      {
        title: 'Configure .npmrc',
        text: 'Add to your project .npmrc or ~/.npmrc:',
        codes: [{ lang: 'ini', content: `registry=${base}/repository/npm-group/
//${base.replace(/^https?:\/\//, '')}/repository/npm-hosted/:_authToken=nxs_your_token_here` }],
      },
      {
        title: 'Publish a Package',
        codes: [
          { label: 'Using npm publish:', lang: 'bash', content: `npm publish --registry ${base}/repository/npm-hosted/` },
          { label: 'Using curl (upload tarball):', lang: 'bash', content: `npm pack
curl -u admin:admin123 \\
  -H "Content-Type: application/octet-stream" \\
  -T mypackage-1.0.0.tgz \\
  "${base}/repository/npm-hosted/mypackage/-/mypackage-1.0.0.tgz"` },
        ],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Using npm:', lang: 'bash', content: `npm install mypackage --registry ${base}/repository/npm-group/` },
          { label: 'Download tarball with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/npm-group/mypackage/-/mypackage-1.0.0.tgz"` },
        ],
      },
    ],
  },
  {
    id: 'pypi',
    name: 'PyPI',
    icon: '🐍',
    description: 'Host Python packages and proxy PyPI. Supports pip, twine, and the PyPI Simple API.',
    sections: (base) => {
      const host = base.replace(/^https?:\/\//, '').split(':')[0]
      return [
        {
          title: 'Repository URLs',
          codes: [{ lang: 'text', content: `${base}/repository/pypi-hosted/\n${base}/repository/pypi-proxy/simple/\n${base}/repository/pypi-group/simple/` }],
        },
        {
          title: 'Configure pip (~/.config/pip/pip.conf)',
          codes: [{ lang: 'ini', content: `[global]
index-url = ${base}/repository/pypi-group/simple/
trusted-host = ${host}` }],
        },
        {
          title: 'Publish a Package',
          codes: [
            { label: 'Using twine:', lang: 'bash', content: `python -m build
twine upload \\
  --repository-url ${base}/repository/pypi-hosted/ \\
  --username admin \\
  --password admin123 \\
  dist/*` },
            { label: 'Using curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -F "content=@dist/mypackage-1.0.0.tar.gz" \\
  "${base}/repository/pypi-hosted/"` },
          ],
        },
        {
          title: 'Install a Package',
          codes: [{ lang: 'bash', content: `pip install mypackage \\
  --index-url ${base}/repository/pypi-group/simple/ \\
  --trusted-host ${host}` }],
        },
      ]
    },
  },
  {
    id: 'docker',
    name: 'Docker / OCI',
    icon: '🐳',
    description: 'OCI Distribution Spec v2 compliant registry. Supports docker pull/push, image tagging, and multi-arch manifests.',
    sections: (base) => {
      const regHost = base.replace(/^https?:\/\//, '')
      return [
        {
          title: 'Registry Host',
          text: 'Docker uses the host:port directly — no /repository/ prefix. The image name includes the repository.',
          codes: [{ lang: 'text', content: `${regHost}/<image-name>:<tag>` }],
        },
        {
          title: 'Login',
          codes: [{ lang: 'bash', content: `docker login ${regHost} -u admin -p admin123
# Or with an API token:
docker login ${regHost} -u admin -p nxs_your_token_here` }],
        },
        {
          title: 'Push an Image',
          codes: [{ lang: 'bash', content: `# Tag your local image
docker tag myapp:latest ${regHost}/myapp:latest

# Push to Nexspence
docker push ${regHost}/myapp:latest` }],
        },
        {
          title: 'Pull an Image',
          codes: [
            { label: 'Using docker pull:', lang: 'bash', content: `docker pull ${regHost}/myapp:latest` },
            { label: 'Inspect manifest with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  "${base}/v2/myapp/manifests/latest" \\
  -H "Accept: application/vnd.docker.distribution.manifest.v2+json"` },
          ],
        },
        {
          title: 'List Tags',
          codes: [{ lang: 'bash', content: `curl -u admin:admin123 "${base}/v2/myapp/tags/list"` }],
        },
      ]
    },
  },
  {
    id: 'go',
    name: 'Go Modules',
    icon: '🔵',
    description: 'GOPROXY v2 protocol. Cache and proxy Go modules with version resolution and mod file serving.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/go-proxy/` }],
      },
      {
        title: 'Configure Go Proxy',
        codes: [
          { label: 'Set environment variable:', lang: 'bash', content: `export GOPROXY="${base}/repository/go-proxy/|direct"
export GONOSUMCHECK="*"  # for self-signed TLS` },
          { label: 'Persist with go env -w:', lang: 'bash', content: `go env -w GOPROXY="${base}/repository/go-proxy/|direct"` },
        ],
      },
      {
        title: 'Download a Module',
        codes: [
          { label: 'Using go get:', lang: 'bash', content: `go get github.com/some/module@v1.2.3` },
          { label: 'GOPROXY v2 protocol via curl:', lang: 'bash', content: `# List available versions
curl -u admin:admin123 \\
  "${base}/repository/go-proxy/github.com/some/module/@v/list"

# Download module zip
curl -u admin:admin123 \\
  -O "${base}/repository/go-proxy/github.com/some/module/@v/v1.2.3.zip"

# Fetch go.mod
curl -u admin:admin123 \\
  "${base}/repository/go-proxy/github.com/some/module/@v/v1.2.3.mod"` },
        ],
      },
    ],
  },
  {
    id: 'nuget',
    name: 'NuGet',
    icon: '💜',
    description: 'NuGet v2/v3 repository for .NET packages. Compatible with dotnet CLI, nuget.exe, and MSBuild PackageReference.',
    sections: (base) => [
      {
        title: 'Repository URLs',
        codes: [{ lang: 'text', content: `${base}/repository/nuget-hosted/index.json    ← v3 API\n${base}/repository/nuget-hosted/              ← v2 OData\n${base}/repository/nuget-group/index.json     ← group` }],
      },
      {
        title: 'Configure nuget.config',
        codes: [{ lang: 'xml', content: `<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <packageSources>
    <add key="nexspence" value="${base}/repository/nuget-group/index.json" />
  </packageSources>
  <packageSourceCredentials>
    <nexspence>
      <add key="Username" value="admin" />
      <add key="ClearTextPassword" value="admin123" />
    </nexspence>
  </packageSourceCredentials>
</configuration>` }],
      },
      {
        title: 'Publish a Package',
        codes: [
          { label: 'Using dotnet CLI:', lang: 'bash', content: `dotnet nuget push mypackage.1.0.0.nupkg \\
  --source ${base}/repository/nuget-hosted/ \\
  --api-key admin:admin123` },
          { label: 'Using curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -F "package=@mypackage.1.0.0.nupkg" \\
  "${base}/repository/nuget-hosted/"` },
        ],
      },
      {
        title: 'Install a Package',
        codes: [{ lang: 'bash', content: `dotnet add package MyPackage \\
  --source ${base}/repository/nuget-group/index.json` }],
      },
    ],
  },
  {
    id: 'raw',
    name: 'Raw',
    icon: '📄',
    description: 'Generic file storage at any path. Ideal for scripts, release tarballs, configuration files, and binary assets.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/raw-hosted/<path/to/your/file>` }],
      },
      {
        title: 'Upload a File',
        codes: [
          { label: 'Using curl PUT:', lang: 'bash', content: `curl -u admin:admin123 \\
  -T myfile.tar.gz \\
  "${base}/repository/raw-hosted/releases/v1.0/myfile.tar.gz"` },
          { label: 'Upload binary with --data-binary:', lang: 'bash', content: `curl -u admin:admin123 \\
  -X PUT \\
  --data-binary @deploy.sh \\
  "${base}/repository/raw-hosted/scripts/deploy.sh"` },
        ],
      },
      {
        title: 'Download a File',
        codes: [
          { label: 'Using curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/raw-hosted/releases/v1.0/myfile.tar.gz"` },
          { label: 'Using wget:', lang: 'bash', content: `wget --user=admin --password=admin123 \\
  "${base}/repository/raw-hosted/scripts/deploy.sh"` },
        ],
      },
      {
        title: 'List Files',
        codes: [{ lang: 'bash', content: `curl -u admin:admin123 \\
  "${base}/service/rest/v1/components?repository=raw-hosted" \\
  | python3 -m json.tool` }],
      },
    ],
  },
  {
    id: 'helm',
    name: 'Helm',
    icon: '⚓',
    description: 'Helm chart repository for Kubernetes. Serves Helm charts with auto-generated index.yaml.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/helm-hosted/` }],
      },
      {
        title: 'Add Repository',
        codes: [{ lang: 'bash', content: `helm repo add nexspence ${base}/repository/helm-hosted/ \\
  --username admin \\
  --password admin123

helm repo update` }],
      },
      {
        title: 'Publish a Chart',
        codes: [
          { label: 'Package then upload with curl:', lang: 'bash', content: `helm package mychart/
curl -u admin:admin123 \\
  -T mychart-1.0.0.tgz \\
  "${base}/repository/helm-hosted/mychart-1.0.0.tgz"` },
          { label: 'Using helm cm-push plugin:', lang: 'bash', content: `helm plugin install https://github.com/chartmuseum/helm-push
helm cm-push mychart/ nexspence` },
        ],
      },
      {
        title: 'Install a Chart',
        codes: [
          { label: 'Using helm install:', lang: 'bash', content: `helm install my-release nexspence/mychart --version 1.0.0` },
          { label: 'Download chart with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/helm-hosted/mychart-1.0.0.tgz"` },
        ],
      },
    ],
  },
  {
    id: 'cargo',
    name: 'Cargo (Rust)',
    icon: '🦀',
    description: 'Rust Cargo sparse registry. Supports cargo publish, cargo add, and the sparse index protocol.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/cargo-hosted/` }],
      },
      {
        title: 'Configure ~/.cargo/config.toml',
        codes: [{ lang: 'toml', content: `[registries.nexspence]
index = "sparse+${base}/repository/cargo-hosted/"
credential-provider = "cargo:token"

[registry]
default = "nexspence"` }],
      },
      {
        title: 'Authenticate',
        codes: [{ lang: 'bash', content: `cargo login --registry nexspence nxs_your_token_here` }],
      },
      {
        title: 'Publish a Crate',
        codes: [
          { label: 'Using cargo publish:', lang: 'bash', content: `cargo publish --registry nexspence` },
          { label: 'Manual upload with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  -T mycrate-0.1.0.crate \\
  "${base}/repository/cargo-hosted/api/v1/crates/new"` },
        ],
      },
      {
        title: 'Add a Dependency',
        codes: [{ lang: 'bash', content: `cargo add mycrate --registry nexspence` }],
      },
    ],
  },
  {
    id: 'apt',
    name: 'Apt / Debian',
    icon: '🐧',
    description: 'Debian APT repository. Serves .deb packages with auto-generated Packages index.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/apt-hosted/` }],
      },
      {
        title: 'Configure /etc/apt/sources.list.d/nexspence.list',
        codes: [{ lang: 'bash', content: `echo "deb [trusted=yes] ${base}/repository/apt-hosted/ focal main" \\
  | sudo tee /etc/apt/sources.list.d/nexspence.list

sudo apt-get update` }],
        note: 'Replace "focal main" with your distribution codename and component (e.g. "jammy main", "bullseye contrib").',
      },
      {
        title: 'Publish a .deb Package',
        codes: [{ lang: 'bash', content: `curl -u admin:admin123 \\
  -H "Content-Type: application/octet-stream" \\
  -T mypackage_1.0_amd64.deb \\
  "${base}/repository/apt-hosted/"` }],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Using apt-get:', lang: 'bash', content: `sudo apt-get install mypackage` },
          { label: 'Direct .deb download and install:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/apt-hosted/pool/main/m/mypackage/mypackage_1.0_amd64.deb"
sudo dpkg -i mypackage_1.0_amd64.deb` },
        ],
      },
    ],
  },
  {
    id: 'yum',
    name: 'Yum / RPM',
    icon: '🔴',
    description: 'Yum/DNF RPM repository. Serves RPM packages with auto-generated repomd.xml metadata.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/yum-hosted/` }],
      },
      {
        title: 'Configure /etc/yum.repos.d/nexspence.repo',
        codes: [{ lang: 'ini', content: `[nexspence]
name=Nexspence Repository
baseurl=${base}/repository/yum-hosted/
enabled=1
gpgcheck=0
username=admin
password=admin123` }],
      },
      {
        title: 'Publish an RPM Package',
        codes: [{ lang: 'bash', content: `curl -u admin:admin123 \\
  -H "Content-Type: application/x-rpm" \\
  -T mypackage-1.0-1.x86_64.rpm \\
  "${base}/repository/yum-hosted/"` }],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Using yum/dnf:', lang: 'bash', content: `sudo yum install mypackage
# or
sudo dnf install mypackage` },
          { label: 'Direct RPM download and install:', lang: 'bash', content: `curl -u admin:admin123 \\
  -O "${base}/repository/yum-hosted/mypackage-1.0-1.x86_64.rpm"
sudo rpm -ivh mypackage-1.0-1.x86_64.rpm` },
        ],
      },
    ],
  },
  {
    id: 'conan',
    name: 'Conan C/C++',
    icon: '🔧',
    description: 'Conan v1 package manager repository for C and C++ libraries. Supports upload/download protocol.',
    sections: (base) => [
      {
        title: 'Repository URL',
        codes: [{ lang: 'text', content: `${base}/repository/conan-hosted/` }],
      },
      {
        title: 'Add Remote and Authenticate',
        codes: [{ lang: 'bash', content: `conan remote add nexspence ${base}/repository/conan-hosted/
conan user admin -p admin123 -r nexspence` }],
      },
      {
        title: 'Publish a Package',
        codes: [
          { label: 'Using conan upload:', lang: 'bash', content: `conan upload mylib/1.0@user/stable -r nexspence --all` },
          { label: 'Get upload URLs with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  "${base}/repository/conan-hosted/v1/conans/mylib/1.0/user/stable/upload_urls"` },
        ],
      },
      {
        title: 'Install a Package',
        codes: [
          { label: 'Using conan install:', lang: 'bash', content: `conan install mylib/1.0@user/stable -r nexspence` },
          { label: 'Get download URLs with curl:', lang: 'bash', content: `curl -u admin:admin123 \\
  "${base}/repository/conan-hosted/v1/conans/mylib/1.0/user/stable/download_urls"` },
        ],
      },
    ],
  },
]

function GettingStarted({ base }: { base: string }) {
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>Getting Started</h1>
        <p className={styles.sectionDesc}>
          Learn how to authenticate and connect your build tools to Nexspence.
        </p>
      </div>

      <p className={styles.blockTitle}>Your Base URL</p>
      <UrlBlock url={base} />

      <p className={styles.blockTitle}>Authentication</p>
      <p className={styles.blockText}>Three methods are supported — use any one:</p>
      <CodeBlock lang="bash" content={`# 1. Username + password
curl -u admin:admin123 "${base}/api/v1/repositories"

# 2. API token as password (get from Profile → API Tokens)
curl -u admin:nxs_your_token_here "${base}/api/v1/repositories"

# 3. Bearer token
curl -H "Authorization: Bearer nxs_your_token_here" \\
  "${base}/api/v1/repositories"`} />

      <p className={styles.blockTitle}>Generate an API Token</p>
      <p className={styles.blockText}>
        Click the key icon in the sidebar → <strong style={{ color: 'var(--holo-text)' }}>API Tokens</strong> → Create Token.
        Tokens start with <span className={styles.inlineCode}>nxs_</span> and work as a password in Basic Auth
        or as a Bearer token header.
      </p>

      <p className={styles.blockTitle}>List Repositories</p>
      <CodeBlock lang="bash" content={`curl -u admin:admin123 \\
  "${base}/service/rest/v1/repositories" | python3 -m json.tool`} />

      <p className={styles.blockTitle}>Nexus API Compatibility</p>
      <p className={styles.blockText}>
        Nexspence is a drop-in replacement for Sonatype Nexus OSS.
        All Nexus REST API endpoints under <span className={styles.inlineCode}>/service/rest/v1/</span> are supported.
        Tools already configured for Nexus work without modification.
      </p>

      <p className={styles.blockTitle}>Browse & Search</p>
      <p className={styles.blockText}>
        Use the <strong style={{ color: 'var(--holo-text)' }}>Browse</strong> and <strong style={{ color: 'var(--holo-text)' }}>Search</strong> pages
        in the sidebar to explore artifacts visually, or use the Nexus REST API:
      </p>
      <CodeBlock lang="bash" content={`# Search by name
curl -u admin:admin123 \\
  "${base}/service/rest/v1/search?name=myapp" | python3 -m json.tool

# List assets in a repository
curl -u admin:admin123 \\
  "${base}/service/rest/v1/components?repository=maven-releases"`} />
    </>
  )
}

function FormatContent({ format, base }: { format: Format; base: string }) {
  const sections = format.sections(base)
  return (
    <>
      <div className={styles.sectionHeader}>
        <h1 className={styles.sectionTitle}>{format.icon} {format.name}</h1>
        <p className={styles.sectionDesc}>{format.description}</p>
      </div>
      {sections.map((s, i) => (
        <div key={i}>
          <SectionBlock section={s} />
          {i < sections.length - 1 && <hr className={styles.divider} />}
        </div>
      ))}
    </>
  )
}

export default function DocsPage() {
  const [active, setActive] = useState('getting-started')
  const base = window.location.origin

  return (
    <div className={styles.docsLayout}>
      <nav className={styles.docsNav}>
        <button
          className={`${styles.docsNavBtn} ${active === 'getting-started' ? styles.active : ''}`}
          onClick={() => setActive('getting-started')}
        >
          <BookOpen size={14} />
          Getting Started
        </button>
        <div className={styles.docsNavSection}>Formats</div>
        {FORMATS.map(f => (
          <button
            key={f.id}
            className={`${styles.docsNavBtn} ${active === f.id ? styles.active : ''}`}
            onClick={() => setActive(f.id)}
          >
            <span style={{ fontSize: 14, lineHeight: 1 }}>{f.icon}</span>
            {f.name}
          </button>
        ))}
      </nav>
      <div className={styles.docsContent}>
        {active === 'getting-started'
          ? <GettingStarted base={base} />
          : FORMATS.map(f => active === f.id && <FormatContent key={f.id} format={f} base={base} />)
        }
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `cd frontend && npm run build 2>&1 | tail -30`
Expected: Build succeeds, no TypeScript errors, bundle size output shown

- [ ] **Step 3: Check for TypeScript strict errors**

Run: `cd frontend && npx tsc --noEmit 2>&1 | head -20`
Expected: No output (zero errors)

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/DocsPage.tsx
git commit -m "feat(docs): implement full DocsPage — all 12 formats, CodeBlock with copy button, Getting Started"
```

---

### Task 4: Final Verification

- [ ] **Step 1: Full production build**

Run: `cd frontend && npm run build`
Expected: Build succeeds, all chunks emitted with sizes

- [ ] **Step 2: Confirm route exists in the built output**

Run: `grep -r "docs" frontend/dist/index.html 2>/dev/null || echo "SPA routes not in index.html — expected for Vite"`
Expected: "expected for Vite" (routes are client-side)

- [ ] **Step 3: Verify sidebar button is wired**

Run: `grep -n "BookOpen\|/docs\|Documentation" frontend/src/components/Layout.tsx`
Expected: Lines showing BookOpen import, the NavLink to="/docs", and Documentation label text

- [ ] **Step 4: Verify all 12 formats are in FORMATS array**

Run: `grep "id:" frontend/src/pages/DocsPage.tsx`
Expected: 12 id lines: maven, npm, pypi, docker, go, nuget, raw, helm, cargo, apt, yum, conan

- [ ] **Step 5: Final commit if any last fixes, then done**

```bash
git add frontend/src/pages/DocsPage.tsx frontend/src/pages/DocsPage.module.css
git commit -m "feat(docs): documentation page complete — all formats, sidebar button, copy-to-clipboard"
```

---

## Self-Review

**Spec coverage:**
- ✓ Documentation accessible from app (route `/docs` inside Layout)
- ✓ All users can access (no admin guard, same as SecurityPage)
- ✓ Docs button above user info (NavLink added before `userInfo` div in footer)
- ✓ curl examples in every format section (all 12 formats have curl code blocks)
- ✓ UI usage examples (Getting Started explains profile/browse/search UI navigation)
- ✓ Beautiful design (holo CSS variables, glassmorphism code blocks, gradient nav active state)
- ✓ Dynamic base URL (all examples use `window.location.origin`)

**Placeholder scan:** No TBD, TODO, or "similar to" references found. All code blocks contain actual content.

**Type consistency:**
- `Format.sections: (base: string) => FormatSection[]` — called as `format.sections(base)` in `FormatContent` ✓
- `FormatSection.codes: CodeExample[]` — `CodeExample` has `{ label?, lang, content }` — all usages match ✓
- `CodeBlock({ lang, content })` — called as `<CodeBlock lang={c.lang} content={c.content} />` ✓
- `SectionBlock({ section })` — called as `<SectionBlock section={s} />` ✓
- `export default function DocsPage()` — lazy import `lazy(() => import('@/pages/DocsPage'))` matches ✓
