# Docs Full Content — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fill `website/docs/index.html` with comprehensive user-facing documentation covering every major Nexspence feature — repositories, formats, browse/search, RBAC, cleanup, webhooks, migration, monitoring, build promotion, content replication — in both English and Russian.

**Architecture:** All content lives in `<template id="tpl-{key}">` tags inside `website/docs/index.html`. Translatable elements carry `data-i18n="section.key"` attributes; `applyTranslations(node)` substitutes them from `TRANSLATIONS[state.lang]` after cloning. Sidebar navigation drives section display via `setSection(key)`. The top-level tab bar (currently 5 tabs) is removed — sidebar becomes the sole navigation with 6 groups and ~16 sections.

**Tech Stack:** Vanilla HTML/CSS/JS. No build step. nginx:alpine serves the static file. Test by running `python3 -m http.server 8900` from `website/` and opening `http://localhost:8900/docs/`.

**Source material to read before implementing each task:**
- `docs/security-rbac.md` — RBAC, roles, privileges, CEL examples
- `docs/webhooks.md` — events, payload, HMAC
- `docs/usage-raw-go-helm.md` — per-format curl examples
- `docs/oidc-setup.md` — SSO setup
- `docs/deployment.md` — deployment options
- `CLAUDE.md` — feature descriptions for every phase

---

## File Map

| Action | File | What changes |
|--------|------|-------------|
| Modify | `website/docs/index.html` | Everything: remove tab bar, expand sidebar, add 11 new `<template>` sections, add ~200 translation keys |

---

## Task 1: Remove tab bar, restructure sidebar navigation

**Files:**
- Modify: `website/docs/index.html`

The current tab bar (`#doc-tabs`, rendered by `renderTabs()`) only works for 5 sections. With 16 sections it overflows. Remove it. The sidebar becomes the sole navigation.

### 1a — Remove `#doc-tabs` from HTML

- [ ] Find and delete this element from the HTML (around line 270):
```html
<div class="doc-tabs" role="tablist" id="doc-tabs"></div>
```

### 1b — Remove `renderTabs()` function and all calls to it

- [ ] Delete the entire `renderTabs` function from the script block:
```js
function renderTabs(activeSection){
  const container=document.getElementById('doc-tabs');container.innerHTML='';
  for(const s of SECTIONS){
    ...
  }
}
```

- [ ] Remove every call to `renderTabs(...)` — they appear in `setSection()` and in the bootstrap IIFE (two places). Delete those lines.

### 1c — Remove `.doc-tabs` and `.dtab` CSS

- [ ] Delete these CSS rules (they're no longer needed):
```css
.doc-tabs{display:flex;gap:2px;border-bottom:1px solid var(--border);margin-bottom:20px;overflow-x:auto;scrollbar-width:none}
.doc-tabs::-webkit-scrollbar{display:none}
.dtab{padding:9px 14px;font-size:.78rem;font-weight:600;...}
.dtab:hover{color:var(--text)}
.dtab.active{color:#a78bfa;border-bottom-color:#a78bfa}
```

### 1d — Replace `SECTIONS` array with full 16-section list

- [ ] Replace the current `SECTIONS` array:
```js
const SECTIONS=[
  {key:'quickstart',  templateId:'tpl-quickstart'},
  {key:'install',     templateId:'tpl-install'},
  {key:'repositories',templateId:'tpl-repositories'},
  {key:'formats-guide',templateId:'tpl-formats-guide'},
  {key:'browse-search',templateId:'tpl-browse-search'},
  {key:'users',       templateId:'tpl-users'},
  {key:'rbac',        templateId:'tpl-rbac'},
  {key:'cleanup',     templateId:'tpl-cleanup'},
  {key:'webhooks',    templateId:'tpl-webhooks'},
  {key:'migration',   templateId:'tpl-migration'},
  {key:'monitoring',  templateId:'tpl-monitoring'},
  {key:'promotion',   templateId:'tpl-promotion'},
  {key:'replication', templateId:'tpl-replication'},
  {key:'formats',     templateId:'tpl-formats'},
  {key:'api',         templateId:'tpl-api'},
  {key:'changelog',   templateId:null},
];
```

### 1e — Replace `SIDEBAR_GROUPS` with 6-group structure

- [ ] Replace the current `SIDEBAR_GROUPS` array (currently 3 groups) with:
```js
const SIDEBAR_GROUPS=[
  {id:'getting-started',items:[
    {key:'quickstart',  icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg>'},
    {key:'install',     icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>'},
  ]},
  {id:'using',items:[
    {key:'repositories',icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/></svg>'},
    {key:'formats-guide',icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>'},
    {key:'browse-search',icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><circle cx="11" cy="11" r="8"/><path d="M21 21l-4.35-4.35"/></svg>'},
  ]},
  {id:'admin',items:[
    {key:'users',       icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/></svg>'},
    {key:'rbac',        icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg>'},
    {key:'cleanup',     icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>'},
    {key:'webhooks',    icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/></svg>'},
  ]},
  {id:'advanced',items:[
    {key:'migration',   icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polyline points="16 3 21 3 21 8"/><line x1="4" y1="20" x2="21" y2="3"/><polyline points="21 16 21 21 16 21"/><line x1="15" y1="15" x2="21" y2="21"/></svg>'},
    {key:'monitoring',  icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>'},
    {key:'promotion',   icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polyline points="17 11 21 7 17 3"/><line x1="21" y1="7" x2="9" y2="7"/><polyline points="7 21 3 17 7 13"/><line x1="15" y1="17" x2="3" y2="17"/></svg>'},
    {key:'replication', icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>'},
  ]},
  {id:'reference',items:[
    {key:'formats',     icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>'},
    {key:'api',         icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>'},
  ]},
  {id:'releases',items:[]},
];
```

### 1f — Add translations for new sidebar group labels and section names

- [ ] Add the following keys to **both** `TRANSLATIONS.en` and `TRANSLATIONS.ru` inside the `TRANSLATIONS` const in the script block.

Add to `TRANSLATIONS.en`:
```js
// Sidebar groups
'sb.using':'Using Nexspence',
'sb.admin':'Administration',
'sb.advanced':'Advanced',
// Sidebar section names
'sb.repositories':'Repositories',
'sb.formats-guide':'Format Setup Guides',
'sb.browse-search':'Browse & Search',
'sb.users':'Users & API Tokens',
'sb.rbac':'Roles & Privileges',
'sb.cleanup':'Cleanup Policies',
'sb.webhooks':'Webhooks',
'sb.migration':'Migration from Nexus',
'sb.monitoring':'Monitoring',
'sb.promotion':'Build Promotion',
'sb.replication':'Content Replication',
// Breadcrumbs
'crumb.repositories':'repositories',
'crumb.formats-guide':'format setup',
'crumb.browse-search':'browse & search',
'crumb.users':'users & tokens',
'crumb.rbac':'roles & privileges',
'crumb.cleanup':'cleanup policies',
'crumb.webhooks':'webhooks',
'crumb.migration':'migration',
'crumb.monitoring':'monitoring',
'crumb.promotion':'build promotion',
'crumb.replication':'content replication',
```

Add to `TRANSLATIONS.ru`:
```js
'sb.using':'Использование',
'sb.admin':'Администрирование',
'sb.advanced':'Дополнительно',
'sb.repositories':'Репозитории',
'sb.formats-guide':'Настройка форматов',
'sb.browse-search':'Обзор и поиск',
'sb.users':'Пользователи и токены',
'sb.rbac':'Роли и привилегии',
'sb.cleanup':'Политики очистки',
'sb.webhooks':'Вебхуки',
'sb.migration':'Миграция из Nexus',
'sb.monitoring':'Мониторинг',
'sb.promotion':'Продвижение сборок',
'sb.replication':'Репликация контента',
'crumb.repositories':'репозитории',
'crumb.formats-guide':'настройка форматов',
'crumb.browse-search':'обзор и поиск',
'crumb.users':'пользователи и токены',
'crumb.rbac':'роли и привилегии',
'crumb.cleanup':'политики очистки',
'crumb.webhooks':'вебхуки',
'crumb.migration':'миграция',
'crumb.monitoring':'мониторинг',
'crumb.promotion':'продвижение сборок',
'crumb.replication':'репликация контента',
```

### 1g — Verify navigation works

- [ ] Run: `cd /Users/skensel/WORKING/AI/nexspence-core/website && python3 -m http.server 8900`
- [ ] Open `http://localhost:8900/docs/` — sidebar should show 6 groups with all items
- [ ] Click any existing section (Quick Start, Installation, Formats, API, Changelog) — content loads, no JS errors
- [ ] New sections (Repositories etc.) show empty content or placeholder — that's fine for now
- [ ] Kill server: `lsof -ti:8900 | xargs kill -9`

### 1h — Commit
- [ ] `git add website/docs/index.html && git commit -m "refactor(website/docs): remove tab bar, expand sidebar to 6 groups and 16 sections"`

---

## Task 2: Repositories section

**Files:** Modify `website/docs/index.html`

### 2a — Add template after `</template>` of `tpl-quickstart`

- [ ] Insert the following `<template>` tag after the closing `</template>` of `tpl-quickstart` (around line 358):

```html
<template id="tpl-repositories">
  <div class="doc-section-title" data-i18n="repo.title">Repositories</div>
  <div class="doc-section-sub" data-i18n="repo.sub">Nexspence organises artifacts into repositories. Each repository has a type (Hosted, Proxy, or Group) and a format (Maven, Docker, npm, etc.).</div>

  <div class="inst-sep" data-i18n="repo.types.sep">Repository Types</div>
  <div class="step-list">
    <div class="step-item">
      <div class="step-num" style="background:linear-gradient(135deg,#22c55e,#16a34a)">H</div>
      <div>
        <div class="step-title" data-i18n="repo.hosted.title">Hosted</div>
        <div class="step-desc" data-i18n="repo.hosted.desc">Stores artifacts you publish directly. Use for internal builds, release binaries, private packages. Supports upload via CI/CD pipelines.</div>
      </div>
    </div>
    <div class="step-item">
      <div class="step-num" style="background:linear-gradient(135deg,#f59e0b,#d97706)">P</div>
      <div>
        <div class="step-title" data-i18n="repo.proxy.title">Proxy</div>
        <div class="step-desc" data-i18n="repo.proxy.desc">Caches artifacts from a remote registry (Maven Central, npmjs, PyPI, Docker Hub, etc.). First request fetches from upstream; subsequent requests serve from cache. Reduces bandwidth and provides availability even if upstream is down.</div>
      </div>
    </div>
    <div class="step-item">
      <div class="step-num" style="background:linear-gradient(135deg,#8b5cf6,#7c3aed)">G</div>
      <div>
        <div class="step-title" data-i18n="repo.group.title">Group</div>
        <div class="step-desc" data-i18n="repo.group.desc">Aggregates multiple repositories (hosted + proxy) under one URL. Clients use a single endpoint; Nexspence searches members in order and returns the first match. Ideal for giving developers one URL for all dependencies.</div>
      </div>
    </div>
  </div>

  <div class="inst-sep" data-i18n="repo.create.sep">Creating a Repository</div>
  <div class="step-list">
    <div class="step-item"><div class="step-num">01</div><div><div class="step-title" data-i18n="repo.create.s1.title">Open the Repositories page</div><div class="step-desc" data-i18n="repo.create.s1.desc">Navigate to <strong>Repositories</strong> in the sidebar. Click the <strong>+ Create</strong> button in the top-right corner.</div></div></div>
    <div class="step-item"><div class="step-num">02</div><div><div class="step-title" data-i18n="repo.create.s2.title">Choose type and format</div><div class="step-desc" data-i18n="repo.create.s2.desc">Select the repository type (Hosted / Proxy / Group) and format (Maven, npm, Docker, etc.) in the wizard. The name must be unique and URL-safe.</div></div></div>
    <div class="step-item"><div class="step-num">03</div><div><div class="step-title" data-i18n="repo.create.s3.title">Configure format-specific settings</div><div class="step-desc" data-i18n="repo.create.s3.desc">For <strong>Proxy</strong>: enter the upstream Remote URL (e.g. <code>https://registry.npmjs.org</code>). For <strong>Group</strong>: select member repositories in order. For all types: optionally assign a blob store and cleanup policy.</div></div></div>
    <div class="step-item"><div class="step-num">04</div><div><div class="step-title" data-i18n="repo.create.s4.title">Use the repository URL</div><div class="step-desc" data-i18n="repo.create.s4.desc">After creation the URL is shown on the repository card: <code>http://localhost:8081/repository/{name}/</code>. Point your build tool at this URL.</div></div></div>
  </div>

  <div class="inst-sep" data-i18n="repo.urls.sep">Repository URL Patterns</div>
  <table class="doc-table">
    <thead><tr><th data-i18n="repo.urls.th.format">Format</th><th data-i18n="repo.urls.th.url">URL pattern</th><th data-i18n="repo.urls.th.notes">Notes</th></tr></thead>
    <tbody>
      <tr><td>Maven</td><td><code>/repository/{name}/</code></td><td data-i18n="repo.urls.maven">Use as <code>&lt;url&gt;</code> in <code>pom.xml</code> or <code>settings.xml</code></td></tr>
      <tr><td>npm</td><td><code>/repository/{name}/</code></td><td data-i18n="repo.urls.npm">Use as <code>registry</code> in <code>.npmrc</code></td></tr>
      <tr><td>PyPI</td><td><code>/repository/{name}/simple/</code></td><td data-i18n="repo.urls.pypi">Use as <code>index-url</code> in <code>pip.conf</code></td></tr>
      <tr><td>Docker</td><td><code>localhost:5000/{name}</code></td><td data-i18n="repo.urls.docker">Registry port 5000, image name includes repo</td></tr>
      <tr><td>Helm</td><td><code>/repository/{name}/</code></td><td data-i18n="repo.urls.helm">Add with <code>helm repo add</code></td></tr>
      <tr><td>Go</td><td><code>/repository/{name}/</code></td><td data-i18n="repo.urls.go">Set as <code>GOPROXY</code></td></tr>
      <tr><td>NuGet</td><td><code>/repository/{name}/index.json</code></td><td data-i18n="repo.urls.nuget">v3 flat container endpoint</td></tr>
      <tr><td>Cargo</td><td><code>/repository/{name}/</code></td><td data-i18n="repo.urls.cargo">Sparse index endpoint</td></tr>
      <tr><td>Raw</td><td><code>/repository/{name}/</code></td><td data-i18n="repo.urls.raw">PUT/GET any path</td></tr>
    </tbody>
  </table>

  <div class="inst-sep" data-i18n="repo.anon.sep">Anonymous Access</div>
  <div class="alert-info" data-i18n="repo.anon.info">Enable <strong>Allow anonymous access</strong> on a repository to let unauthenticated users download artifacts. Write operations always require authentication. Configure globally in <strong>Admin → Security → Anonymous Access</strong> and per-repository in the repo settings.</div>
</template>
```

### 2b — Add translations to `TRANSLATIONS.en` and `TRANSLATIONS.ru`

- [ ] Add to `TRANSLATIONS.en`:
```js
'repo.title':'Repositories',
'repo.sub':'Nexspence organises artifacts into repositories. Each repository has a type (Hosted, Proxy, or Group) and a format (Maven, Docker, npm, etc.).',
'repo.types.sep':'Repository Types',
'repo.hosted.title':'Hosted','repo.hosted.desc':'Stores artifacts you publish directly. Use for internal builds, release binaries, private packages.',
'repo.proxy.title':'Proxy','repo.proxy.desc':'Caches artifacts from a remote registry (Maven Central, npmjs, PyPI, Docker Hub, etc.). First request fetches from upstream; subsequent requests serve from cache.',
'repo.group.title':'Group','repo.group.desc':'Aggregates multiple repositories (hosted + proxy) under one URL. Clients use a single endpoint; Nexspence searches members in order and returns the first match.',
'repo.create.sep':'Creating a Repository',
'repo.create.s1.title':'Open the Repositories page','repo.create.s1.desc':'Navigate to <strong>Repositories</strong> in the sidebar. Click the <strong>+ Create</strong> button in the top-right corner.',
'repo.create.s2.title':'Choose type and format','repo.create.s2.desc':'Select the repository type (Hosted / Proxy / Group) and format. The name must be unique and URL-safe.',
'repo.create.s3.title':'Configure format-specific settings','repo.create.s3.desc':'For <strong>Proxy</strong>: enter the upstream Remote URL. For <strong>Group</strong>: select member repositories in order. Optionally assign a blob store and cleanup policy.',
'repo.create.s4.title':'Use the repository URL','repo.create.s4.desc':'After creation the URL is shown on the repository card: <code>http://localhost:8081/repository/{name}/</code>.',
'repo.urls.sep':'Repository URL Patterns',
'repo.urls.th.format':'Format','repo.urls.th.url':'URL pattern','repo.urls.th.notes':'Notes',
'repo.urls.maven':'Use as <code>&lt;url&gt;</code> in <code>pom.xml</code> or <code>settings.xml</code>',
'repo.urls.npm':'Use as <code>registry</code> in <code>.npmrc</code>',
'repo.urls.pypi':'Use as <code>index-url</code> in <code>pip.conf</code>',
'repo.urls.docker':'Registry port 5000, image name includes repo',
'repo.urls.helm':'Add with <code>helm repo add</code>',
'repo.urls.go':'Set as <code>GOPROXY</code>',
'repo.urls.nuget':'v3 flat container endpoint',
'repo.urls.cargo':'Sparse index endpoint',
'repo.urls.raw':'PUT/GET any path',
'repo.anon.sep':'Anonymous Access',
'repo.anon.info':'Enable <strong>Allow anonymous access</strong> on a repository to let unauthenticated users download artifacts. Write operations always require authentication.',
```

- [ ] Add to `TRANSLATIONS.ru`:
```js
'repo.title':'Репозитории',
'repo.sub':'Nexspence хранит артефакты в репозиториях. Каждый репозиторий имеет тип (Hosted, Proxy или Group) и формат (Maven, Docker, npm и др.).',
'repo.types.sep':'Типы репозиториев',
'repo.hosted.title':'Hosted','repo.hosted.desc':'Хранит артефакты, которые вы публикуете напрямую. Используйте для внутренних сборок, релизных бинарей, приватных пакетов.',
'repo.proxy.title':'Proxy','repo.proxy.desc':'Кэширует артефакты из удалённого реестра (Maven Central, npmjs, PyPI, Docker Hub и др.). Первый запрос идёт к upstream, последующие обслуживаются из кэша.',
'repo.group.title':'Group','repo.group.desc':'Объединяет несколько репозиториев (hosted + proxy) под одним URL. Клиенты используют единый endpoint; Nexspence ищет в членах по порядку и возвращает первое совпадение.',
'repo.create.sep':'Создание репозитория',
'repo.create.s1.title':'Откройте страницу Repositories','repo.create.s1.desc':'Перейдите в <strong>Repositories</strong> в сайдбаре. Нажмите кнопку <strong>+ Create</strong> в правом верхнем углу.',
'repo.create.s2.title':'Выберите тип и формат','repo.create.s2.desc':'Выберите тип (Hosted / Proxy / Group) и формат. Имя должно быть уникальным и допустимым для URL.',
'repo.create.s3.title':'Настройте параметры формата','repo.create.s3.desc':'Для <strong>Proxy</strong>: укажите Remote URL upstream. Для <strong>Group</strong>: выберите репозитории-члены по порядку. Опционально назначьте blob store и политику очистки.',
'repo.create.s4.title':'Используйте URL репозитория','repo.create.s4.desc':'После создания URL отображается на карточке репозитория: <code>http://localhost:8081/repository/{name}/</code>.',
'repo.urls.sep':'Шаблоны URL репозиториев',
'repo.urls.th.format':'Формат','repo.urls.th.url':'Шаблон URL','repo.urls.th.notes':'Примечания',
'repo.urls.maven':'Используйте как <code>&lt;url&gt;</code> в <code>pom.xml</code> или <code>settings.xml</code>',
'repo.urls.npm':'Используйте как <code>registry</code> в <code>.npmrc</code>',
'repo.urls.pypi':'Используйте как <code>index-url</code> в <code>pip.conf</code>',
'repo.urls.docker':'Порт реестра 5000, имя образа включает репозиторий',
'repo.urls.helm':'Добавьте через <code>helm repo add</code>',
'repo.urls.go':'Установите как <code>GOPROXY</code>',
'repo.urls.nuget':'Endpoint v3 flat container',
'repo.urls.cargo':'Sparse index endpoint',
'repo.urls.raw':'PUT/GET любого пути',
'repo.anon.sep':'Анонимный доступ',
'repo.anon.info':'Включите <strong>Allow anonymous access</strong> для репозитория, чтобы неаутентифицированные пользователи могли скачивать артефакты. Операции записи всегда требуют аутентификации.',
```

### 2c — Commit
- [ ] `git add website/docs/index.html && git commit -m "docs(website): add Repositories section"`

---

## Task 3: Format Setup Guides section

**Files:** Modify `website/docs/index.html`

This section shows concrete client configuration for each format — what to put in `settings.xml`, `.npmrc`, `pip.conf`, etc.

### 3a — Add `tpl-formats-guide` template

- [ ] Insert after `tpl-repositories` closing `</template>`:

```html
<template id="tpl-formats-guide">
  <div class="doc-section-title" data-i18n="fg.title">Format Setup Guides</div>
  <div class="doc-section-sub" data-i18n="fg.sub">How to configure your build tools and package managers to use Nexspence repositories. Replace <code>localhost:8081</code> with your server URL and <code>{repo}</code> with your repository name.</div>

  <!-- Maven -->
  <div class="inst-sep">Maven / Gradle</div>
  <p style="font-size:.8rem;color:var(--dim);margin-bottom:10px;line-height:1.6" data-i18n="fg.maven.desc">Add Nexspence as a mirror in <code>~/.m2/settings.xml</code> to route all dependency downloads through your proxy repository.</p>
  <div class="cb"><div class="cb-bar"><span>~/.m2/settings.xml</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body">&lt;settings&gt;
  &lt;mirrors&gt;
    &lt;mirror&gt;
      &lt;id&gt;nexspence&lt;/id&gt;
      &lt;url&gt;http://localhost:8081/repository/maven-public/&lt;/url&gt;
      &lt;mirrorOf&gt;*&lt;/mirrorOf&gt;
    &lt;/mirror&gt;
  &lt;/mirrors&gt;
  &lt;servers&gt;
    &lt;server&gt;
      &lt;id&gt;nexspence&lt;/id&gt;
      &lt;username&gt;admin&lt;/username&gt;
      &lt;password&gt;admin123&lt;/password&gt;
    &lt;/server&gt;
  &lt;/servers&gt;
&lt;/settings&gt;</div></div>
  <div class="cb"><div class="cb-bar"><span>pom.xml — deploy</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body">&lt;distributionManagement&gt;
  &lt;repository&gt;
    &lt;id&gt;nexspence&lt;/id&gt;
    &lt;url&gt;http://localhost:8081/repository/maven-releases/&lt;/url&gt;
  &lt;/repository&gt;
  &lt;snapshotRepository&gt;
    &lt;id&gt;nexspence&lt;/id&gt;
    &lt;url&gt;http://localhost:8081/repository/maven-snapshots/&lt;/url&gt;
  &lt;/snapshotRepository&gt;
&lt;/distributionManagement&gt;</div></div>

  <!-- npm -->
  <div class="inst-sep">npm / yarn</div>
  <div class="cb"><div class="cb-bar"><span>.npmrc</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body">registry=http://localhost:8081/repository/npm-public/
//localhost:8081/repository/npm-public/:_auth=YWRtaW46YWRtaW4xMjM=
//localhost:8081/repository/npm-public/:always-auth=true</div></div>
  <div class="alert-info" data-i18n="fg.npm.auth.note"><strong>Auth token:</strong> <code>_auth</code> is base64 of <code>username:password</code>. Generate: <code>echo -n "admin:admin123" | base64</code>. To publish: <code>npm publish --registry http://localhost:8081/repository/npm-hosted/</code></div>

  <!-- PyPI -->
  <div class="inst-sep">PyPI / pip</div>
  <div class="cb"><div class="cb-bar"><span>~/.config/pip/pip.conf</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body">[global]
index-url = http://admin:admin123@localhost:8081/repository/pypi-proxy/simple/
trusted-host = localhost</div></div>
  <div class="cb"><div class="cb-bar"><span>publish with twine</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body">twine upload \
  --repository-url http://localhost:8081/repository/pypi-hosted/ \
  --username admin --password admin123 \
  dist/*</div></div>

  <!-- Docker -->
  <div class="inst-sep">Docker / OCI</div>
  <div class="alert-info" data-i18n="fg.docker.insecure.note"><strong>HTTP registry:</strong> Add <code>"insecure-registries": ["localhost:5000"]</code> to Docker daemon config (<code>/etc/docker/daemon.json</code>) when using HTTP. Restart Docker after.</div>
  <div class="cb"><div class="cb-bar"><span>shell</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body"><span class="cc"># Login</span>
docker login localhost:5000 -u admin -p admin123

<span class="cc"># Pull through proxy</span>
docker pull localhost:5000/docker-proxy/library/nginx:latest

<span class="cc"># Push to hosted</span>
docker tag myimage:1.0 localhost:5000/docker-hosted/myimage:1.0
docker push localhost:5000/docker-hosted/myimage:1.0</div></div>

  <!-- Helm -->
  <div class="inst-sep">Helm Charts</div>
  <div class="cb"><div class="cb-bar"><span>shell</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body"><span class="cc"># Add repository</span>
helm repo add nexspence \
  http://localhost:8081/repository/helm-public/ \
  --username admin --password admin123

helm repo update

<span class="cc"># Search and install</span>
helm search repo nexspence/
helm install my-release nexspence/my-chart

<span class="cc"># Push chart (requires helm-push plugin)</span>
helm plugin install https://github.com/chartmuseum/helm-push
helm cm-push my-chart/ nexspence</div></div>

  <!-- Go -->
  <div class="inst-sep">Go Modules</div>
  <div class="cb"><div class="cb-bar"><span>shell / go.env</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body"><span class="cc"># Set GOPROXY</span>
go env -w GOPROXY=http://localhost:8081/repository/go-proxy/|direct
go env -w GONOSUMCHECK=localhost:8081

<span class="cc"># Use in project</span>
go get github.com/some/module@v1.2.3</div></div>

  <!-- Cargo -->
  <div class="inst-sep">Cargo (Rust)</div>
  <div class="cb"><div class="cb-bar"><span>~/.cargo/config.toml</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body">[source.crates-io]
replace-with = "nexspence"

[source.nexspence]
registry = "sparse+http://localhost:8081/repository/cargo-proxy/"

[net]
git-fetch-with-cli = true</div></div>

  <!-- NuGet -->
  <div class="inst-sep">NuGet (.NET)</div>
  <div class="cb"><div class="cb-bar"><span>shell</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body"><span class="cc"># Add source</span>
dotnet nuget add source \
  http://localhost:8081/repository/nuget-public/index.json \
  --name nexspence \
  --username admin \
  --password admin123 \
  --store-password-in-clear-text

<span class="cc"># Push package</span>
dotnet nuget push mypackage.nupkg \
  --source http://localhost:8081/repository/nuget-hosted/ \
  --api-key admin123</div></div>

  <!-- Raw -->
  <div class="inst-sep">Raw Files</div>
  <div class="cb"><div class="cb-bar"><span>curl</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body"><span class="cc"># Upload a file</span>
curl -u admin:admin123 \
  -T myfile.tar.gz \
  http://localhost:8081/repository/raw-hosted/releases/myfile.tar.gz

<span class="cc"># Download</span>
curl -u admin:admin123 \
  -O http://localhost:8081/repository/raw-hosted/releases/myfile.tar.gz</div></div>
</template>
```

### 3b — Add minimal translations (title/sub only — rest is code and technical)

- [ ] Add to `TRANSLATIONS.en`:
```js
'fg.title':'Format Setup Guides',
'fg.sub':'How to configure your build tools and package managers to use Nexspence repositories. Replace <code>localhost:8081</code> with your server URL.',
'fg.maven.desc':'Add Nexspence as a mirror in <code>~/.m2/settings.xml</code> to route all dependency downloads through your proxy repository.',
'fg.npm.auth.note':'<strong>Auth token:</strong> <code>_auth</code> is base64 of <code>username:password</code>. Generate: <code>echo -n "admin:admin123" | base64</code>.',
'fg.docker.insecure.note':'<strong>HTTP registry:</strong> Add <code>"insecure-registries": ["localhost:5000"]</code> to <code>/etc/docker/daemon.json</code> when using HTTP. Restart Docker after.',
```

- [ ] Add to `TRANSLATIONS.ru`:
```js
'fg.title':'Настройка форматов',
'fg.sub':'Как настроить сборочные инструменты и пакетные менеджеры для работы с репозиториями Nexspence. Замените <code>localhost:8081</code> на адрес вашего сервера.',
'fg.maven.desc':'Добавьте Nexspence как зеркало в <code>~/.m2/settings.xml</code>, чтобы все зависимости загружались через proxy-репозиторий.',
'fg.npm.auth.note':'<strong>Токен авторизации:</strong> <code>_auth</code> — это base64 от <code>username:password</code>. Сгенерировать: <code>echo -n "admin:admin123" | base64</code>.',
'fg.docker.insecure.note':'<strong>HTTP-реестр:</strong> добавьте <code>"insecure-registries": ["localhost:5000"]</code> в <code>/etc/docker/daemon.json</code>. Перезапустите Docker.',
```

### 3c — Commit
- [ ] `git add website/docs/index.html && git commit -m "docs(website): add Format Setup Guides section"`

---

## Task 4: Browse & Search + Users & API Tokens sections

**Files:** Modify `website/docs/index.html`

### 4a — Add `tpl-browse-search` template

- [ ] Insert after `tpl-formats-guide` closing `</template>`:

```html
<template id="tpl-browse-search">
  <div class="doc-section-title" data-i18n="bs.title">Browse &amp; Search</div>
  <div class="doc-section-sub" data-i18n="bs.sub">Find, inspect, and download artifacts stored in Nexspence repositories.</div>

  <div class="inst-sep" data-i18n="bs.browse.sep">Browse</div>
  <div class="step-list">
    <div class="step-item"><div class="step-num">01</div><div><div class="step-title" data-i18n="bs.browse.s1.title">Open Browse</div><div class="step-desc" data-i18n="bs.browse.s1.desc">Click <strong>Browse</strong> in the sidebar. Select a repository from the dropdown on the left.</div></div></div>
    <div class="step-item"><div class="step-num">02</div><div><div class="step-title" data-i18n="bs.browse.s2.title">Navigate the tree</div><div class="step-desc" data-i18n="bs.browse.s2.desc">For most formats a file tree is shown. For Docker repositories a two-column view shows image names on the left and tags on the right.</div></div></div>
    <div class="step-item"><div class="step-num">03</div><div><div class="step-title" data-i18n="bs.browse.s3.title">Inspect a component</div><div class="step-desc" data-i18n="bs.browse.s3.desc">Click any file or component to open a detail panel with metadata (size, SHA-256, upload date), download link, and usage examples for common tools.</div></div></div>
  </div>

  <div class="inst-sep" data-i18n="bs.search.sep">Search</div>
  <div class="step-list">
    <div class="step-item"><div class="step-num">01</div><div><div class="step-title" data-i18n="bs.search.s1.title">Open Search</div><div class="step-desc" data-i18n="bs.search.s1.desc">Click <strong>Search</strong> in the sidebar. Enter a keyword in the search box — matches component names, group IDs, and artifact IDs.</div></div></div>
    <div class="step-item"><div class="step-num">02</div><div><div class="step-title" data-i18n="bs.search.s2.title">Filter by format or repository</div><div class="step-desc" data-i18n="bs.search.s2.desc">Use the <strong>Format</strong> and <strong>Repository</strong> dropdowns to narrow results. You can also filter by tag.</div></div></div>
    <div class="step-item"><div class="step-num">03</div><div><div class="step-title" data-i18n="bs.search.s3.title">Use the API</div><div class="step-desc" data-i18n="bs.search.s3.desc">Search is also available via the REST API for CI/CD integration.</div></div></div>
  </div>
  <div class="cb"><div class="cb-bar"><span>curl — search API</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body"><span class="cc"># Search by keyword</span>
curl -u admin:admin123 \
  "http://localhost:8081/service/rest/v1/search?q=mylib"

<span class="cc"># Filter by format and repository</span>
curl -u admin:admin123 \
  "http://localhost:8081/service/rest/v1/search?format=maven2&repository=maven-releases&q=spring"

<span class="cc"># List all components in a repository</span>
curl -u admin:admin123 \
  "http://localhost:8081/service/rest/v1/components?repository=npm-hosted"</div></div>
</template>
```

### 4b — Add `tpl-users` template

- [ ] Insert after `tpl-browse-search` closing `</template>`:

```html
<template id="tpl-users">
  <div class="doc-section-title" data-i18n="usr.title">Users &amp; API Tokens</div>
  <div class="doc-section-sub" data-i18n="usr.sub">Manage user accounts, passwords, and programmatic API tokens for CI/CD pipelines.</div>

  <div class="inst-sep" data-i18n="usr.users.sep">User Management</div>
  <div class="step-list">
    <div class="step-item"><div class="step-num">01</div><div><div class="step-title" data-i18n="usr.create.title">Create a user</div><div class="step-desc" data-i18n="usr.create.desc">Go to <strong>Admin → Security → Users</strong>. Click <strong>+ Create User</strong>. Fill in username, email, first/last name, and initial password. Assign roles immediately or later.</div></div></div>
    <div class="step-item"><div class="step-num">02</div><div><div class="step-title" data-i18n="usr.roles.title">Assign roles</div><div class="step-desc" data-i18n="usr.roles.desc">Open a user and click <strong>Assign Roles</strong>. Select roles from the transfer list. The built-in <code>nx-admin</code> role grants full access; custom roles control per-repository access via privileges.</div></div></div>
    <div class="step-item"><div class="step-num">03</div><div><div class="step-title" data-i18n="usr.ldap.title">LDAP / Active Directory</div><div class="step-desc" data-i18n="usr.ldap.desc">Configure LDAP in <code>config.yaml</code> under the <code>ldap</code> section. Users are provisioned on first login (JIT). Groups map to Nexspence roles via <code>role_mappings</code>.</div></div></div>
  </div>
  <div class="cb"><div class="cb-bar"><span>curl — create user via API</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body">curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/security/users \
  -H "Content-Type: application/json" \
  -d '{
    "userId": "alice",
    "firstName": "Alice",
    "lastName": "Smith",
    "emailAddress": "alice@example.com",
    "password": "secret",
    "status": "active",
    "roles": ["nx-anonymous"]
  }'</div></div>

  <div class="inst-sep" data-i18n="usr.tokens.sep">API Tokens</div>
  <div class="step-list">
    <div class="step-item"><div class="step-num">01</div><div><div class="step-title" data-i18n="usr.token.create.title">Create a token</div><div class="step-desc" data-i18n="usr.token.create.desc">Open your profile (click your username in the bottom-left). Go to <strong>API Tokens</strong> tab. Click <strong>Generate Token</strong>. Set an optional expiry. The token is shown <strong>once</strong> — copy it immediately.</div></div></div>
    <div class="step-item"><div class="step-num">02</div><div><div class="step-title" data-i18n="usr.token.use.title">Use the token</div><div class="step-desc" data-i18n="usr.token.use.desc">Tokens start with <code>nxs_</code>. Use as Bearer token or as the password in HTTP Basic auth (username = your username, password = token).</div></div></div>
  </div>
  <div class="cb"><div class="cb-bar"><span>curl — using an API token</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body"><span class="cc"># Bearer auth</span>
curl -H "Authorization: Bearer nxs_abc123..." \
  http://localhost:8081/service/rest/v1/repositories

<span class="cc"># Basic auth (username + token as password)</span>
curl -u admin:nxs_abc123... \
  http://localhost:8081/service/rest/v1/components?repository=maven-releases</div></div>
  <div class="alert-info" data-i18n="usr.token.limit.note"><strong>Token expiry:</strong> Maximum lifetime is controlled by <code>auth.token_max_days</code> in <code>config.yaml</code> (default 180 days). Tokens are hashed (SHA-256) — the plaintext is never stored.</div>
</template>
```

### 4c — Add translations

- [ ] Add to `TRANSLATIONS.en`:
```js
'bs.title':'Browse & Search','bs.sub':'Find, inspect, and download artifacts stored in Nexspence repositories.',
'bs.browse.sep':'Browse','bs.search.sep':'Search',
'bs.browse.s1.title':'Open Browse','bs.browse.s1.desc':'Click <strong>Browse</strong> in the sidebar. Select a repository from the dropdown.',
'bs.browse.s2.title':'Navigate the tree','bs.browse.s2.desc':'A file tree is shown for most formats. Docker shows image names and tags.',
'bs.browse.s3.title':'Inspect a component','bs.browse.s3.desc':'Click any file to open a detail panel with metadata, download link, and usage examples.',
'bs.search.s1.title':'Open Search','bs.search.s1.desc':'Click <strong>Search</strong> in the sidebar. Enter a keyword — matches names, group IDs, and artifact IDs.',
'bs.search.s2.title':'Filter by format or repository','bs.search.s2.desc':'Use the <strong>Format</strong> and <strong>Repository</strong> dropdowns to narrow results.',
'bs.search.s3.title':'Use the API','bs.search.s3.desc':'Search is also available via REST API for CI/CD integration.',
'usr.title':'Users & API Tokens','usr.sub':'Manage user accounts, passwords, and programmatic API tokens for CI/CD pipelines.',
'usr.users.sep':'User Management','usr.tokens.sep':'API Tokens',
'usr.create.title':'Create a user','usr.create.desc':'Go to <strong>Admin → Security → Users</strong>. Click <strong>+ Create User</strong>. Assign roles immediately or later.',
'usr.roles.title':'Assign roles','usr.roles.desc':'Open a user and click <strong>Assign Roles</strong>. The built-in <code>nx-admin</code> role grants full access; custom roles control per-repository access.',
'usr.ldap.title':'LDAP / Active Directory','usr.ldap.desc':'Configure LDAP in <code>config.yaml</code> under the <code>ldap</code> section. Users are provisioned on first login (JIT).',
'usr.token.create.title':'Create a token','usr.token.create.desc':'Open your profile → <strong>API Tokens</strong>. Click <strong>Generate Token</strong>. The token is shown <strong>once</strong> — copy it immediately.',
'usr.token.use.title':'Use the token','usr.token.use.desc':'Tokens start with <code>nxs_</code>. Use as Bearer token or as HTTP Basic password.',
'usr.token.limit.note':'<strong>Token expiry:</strong> Maximum lifetime is controlled by <code>auth.token_max_days</code> in <code>config.yaml</code> (default 180 days). Tokens are hashed — never stored as plaintext.',
```

- [ ] Add to `TRANSLATIONS.ru`:
```js
'bs.title':'Обзор и поиск','bs.sub':'Находите, просматривайте и скачивайте артефакты из репозиториев Nexspence.',
'bs.browse.sep':'Обзор','bs.search.sep':'Поиск',
'bs.browse.s1.title':'Откройте Browse','bs.browse.s1.desc':'Нажмите <strong>Browse</strong> в сайдбаре. Выберите репозиторий из выпадающего списка.',
'bs.browse.s2.title':'Навигация по дереву','bs.browse.s2.desc':'Для большинства форматов отображается файловое дерево. Docker показывает имена образов и теги.',
'bs.browse.s3.title':'Инспекция компонента','bs.browse.s3.desc':'Нажмите на файл — откроется панель с метаданными, ссылкой для скачивания и примерами использования.',
'bs.search.s1.title':'Откройте Search','bs.search.s1.desc':'Нажмите <strong>Search</strong> в сайдбаре. Введите ключевое слово — поиск по именам, group ID и artifact ID.',
'bs.search.s2.title':'Фильтрация','bs.search.s2.desc':'Используйте выпадающие списки <strong>Format</strong> и <strong>Repository</strong> для сужения результатов.',
'bs.search.s3.title':'Использование API','bs.search.s3.desc':'Поиск доступен через REST API для интеграции с CI/CD.',
'usr.title':'Пользователи и API-токены','usr.sub':'Управление учётными записями, паролями и программными API-токенами для CI/CD.',
'usr.users.sep':'Управление пользователями','usr.tokens.sep':'API-токены',
'usr.create.title':'Создание пользователя','usr.create.desc':'Перейдите в <strong>Admin → Security → Users</strong>. Нажмите <strong>+ Create User</strong>.',
'usr.roles.title':'Назначение ролей','usr.roles.desc':'Откройте пользователя и нажмите <strong>Assign Roles</strong>. Встроенная роль <code>nx-admin</code> даёт полный доступ.',
'usr.ldap.title':'LDAP / Active Directory','usr.ldap.desc':'Настройте LDAP в <code>config.yaml</code> в разделе <code>ldap</code>. Пользователи создаются при первом входе (JIT).',
'usr.token.create.title':'Создание токена','usr.token.create.desc':'Откройте профиль → <strong>API Tokens</strong>. Нажмите <strong>Generate Token</strong>. Токен показывается <strong>один раз</strong> — скопируйте сразу.',
'usr.token.use.title':'Использование токена','usr.token.use.desc':'Токены начинаются с <code>nxs_</code>. Используйте как Bearer-токен или пароль в HTTP Basic.',
'usr.token.limit.note':'<strong>Срок действия:</strong> максимальный срок задаётся параметром <code>auth.token_max_days</code> в <code>config.yaml</code> (по умолчанию 180 дней). Токены хранятся в виде SHA-256 хеша.',
```

### 4d — Commit
- [ ] `git add website/docs/index.html && git commit -m "docs(website): add Browse & Search and Users sections"`

---

## Task 5: RBAC section

**Files:** Modify `website/docs/index.html`

Source: read `docs/security-rbac.md` for complete CEL examples before implementing.

### 5a — Add `tpl-rbac` template

- [ ] Insert after `tpl-users` closing `</template>`:

```html
<template id="tpl-rbac">
  <div class="doc-section-title" data-i18n="rbac.title">Roles &amp; Privileges</div>
  <div class="doc-section-sub" data-i18n="rbac.sub">Nexspence uses a three-layer access control model: Content Selectors → Privileges → Roles → Users.</div>

  <div class="inst-sep" data-i18n="rbac.model.sep">Access Control Model</div>
  <div class="step-list">
    <div class="step-item">
      <div class="step-num" style="background:linear-gradient(135deg,#06b6d4,#0891b2)">1</div>
      <div><div class="step-title" data-i18n="rbac.cs.title">Content Selector</div><div class="step-desc" data-i18n="rbac.cs.desc">A CEL expression that defines <em>which artifacts</em> the rule applies to. Variables available: <code>format</code>, <code>path</code>, <code>repository</code>. Example: <code>format == "maven2" &amp;&amp; path =~ "^/com/example/"</code></div></div>
    </div>
    <div class="step-item">
      <div class="step-num" style="background:linear-gradient(135deg,#f59e0b,#d97706)">2</div>
      <div><div class="step-title" data-i18n="rbac.priv.title">Privilege</div><div class="step-desc" data-i18n="rbac.priv.desc">Binds a Content Selector to a permission. In Nexspence all user-created privileges are of type <code>repository-content-selector</code>. Create in <strong>Admin → Security → Privileges</strong>.</div></div>
    </div>
    <div class="step-item">
      <div class="step-num" style="background:linear-gradient(135deg,#8b5cf6,#7c3aed)">3</div>
      <div><div class="step-title" data-i18n="rbac.role.title">Role</div><div class="step-desc" data-i18n="rbac.role.desc">A named set of privileges. Assign roles to users. The built-in <code>nx-admin</code> role grants full access to everything.</div></div>
    </div>
  </div>

  <div class="inst-sep" data-i18n="rbac.cel.sep">CEL Expression Examples</div>
  <table class="doc-table">
    <thead><tr><th data-i18n="rbac.cel.th.desc">What it matches</th><th data-i18n="rbac.cel.th.expr">CEL expression</th></tr></thead>
    <tbody>
      <tr><td data-i18n="rbac.cel.ex1.desc">All Maven artifacts</td><td><code>format == "maven2"</code></td></tr>
      <tr><td data-i18n="rbac.cel.ex2.desc">Specific Maven group</td><td><code>format == "maven2" &amp;&amp; path =~ "^/com/example/"</code></td></tr>
      <tr><td data-i18n="rbac.cel.ex3.desc">All Docker images</td><td><code>format == "docker"</code></td></tr>
      <tr><td data-i18n="rbac.cel.ex4.desc">Specific repository</td><td><code>repository == "npm-releases"</code></td></tr>
      <tr><td data-i18n="rbac.cel.ex5.desc">All npm packages</td><td><code>format == "npm"</code></td></tr>
      <tr><td data-i18n="rbac.cel.ex6.desc">PyPI in specific repo</td><td><code>format == "pypi" &amp;&amp; repository == "pypi-releases"</code></td></tr>
      <tr><td data-i18n="rbac.cel.ex7.desc">Everything (wildcard)</td><td><code>true</code></td></tr>
    </tbody>
  </table>

  <div class="inst-sep" data-i18n="rbac.setup.sep">Setting Up Access Control</div>
  <div class="step-list">
    <div class="step-item"><div class="step-num">01</div><div><div class="step-title" data-i18n="rbac.setup.s1.title">Create a Content Selector</div><div class="step-desc" data-i18n="rbac.setup.s1.desc"><strong>Admin → Security → Content Selectors → + Create</strong>. Enter a name and a CEL expression. The preview shows matching artifacts.</div></div></div>
    <div class="step-item"><div class="step-num">02</div><div><div class="step-title" data-i18n="rbac.setup.s2.title">Create a Privilege</div><div class="step-desc" data-i18n="rbac.setup.s2.desc"><strong>Admin → Security → Privileges → + Create</strong>. Select the Content Selector you just created.</div></div></div>
    <div class="step-item"><div class="step-num">03</div><div><div class="step-title" data-i18n="rbac.setup.s3.title">Create a Role</div><div class="step-desc" data-i18n="rbac.setup.s3.desc"><strong>Admin → Security → Roles → + Create</strong>. Add the privilege to the role.</div></div></div>
    <div class="step-item"><div class="step-num">04</div><div><div class="step-title" data-i18n="rbac.setup.s4.title">Assign the Role to a User</div><div class="step-desc" data-i18n="rbac.setup.s4.desc"><strong>Admin → Security → Users</strong>. Open the user, click <strong>Assign Roles</strong>, add the role.</div></div></div>
  </div>
</template>
```

### 5b — Add translations

- [ ] Add to `TRANSLATIONS.en`:
```js
'rbac.title':'Roles & Privileges','rbac.sub':'Nexspence uses a three-layer access control model: Content Selectors → Privileges → Roles → Users.',
'rbac.model.sep':'Access Control Model','rbac.cel.sep':'CEL Expression Examples','rbac.setup.sep':'Setting Up Access Control',
'rbac.cs.title':'Content Selector','rbac.cs.desc':'A CEL expression that defines <em>which artifacts</em> the rule applies to. Variables: <code>format</code>, <code>path</code>, <code>repository</code>.',
'rbac.priv.title':'Privilege','rbac.priv.desc':'Binds a Content Selector to a permission. All user-created privileges are type <code>repository-content-selector</code>.',
'rbac.role.title':'Role','rbac.role.desc':'A named set of privileges. The built-in <code>nx-admin</code> role grants full access.',
'rbac.cel.th.desc':'What it matches','rbac.cel.th.expr':'CEL expression',
'rbac.cel.ex1.desc':'All Maven artifacts','rbac.cel.ex2.desc':'Specific Maven group','rbac.cel.ex3.desc':'All Docker images',
'rbac.cel.ex4.desc':'Specific repository','rbac.cel.ex5.desc':'All npm packages','rbac.cel.ex6.desc':'PyPI in specific repo','rbac.cel.ex7.desc':'Everything (wildcard)',
'rbac.setup.s1.title':'Create a Content Selector','rbac.setup.s1.desc':'<strong>Admin → Security → Content Selectors → + Create</strong>. Enter a CEL expression.',
'rbac.setup.s2.title':'Create a Privilege','rbac.setup.s2.desc':'<strong>Admin → Security → Privileges → + Create</strong>. Select your Content Selector.',
'rbac.setup.s3.title':'Create a Role','rbac.setup.s3.desc':'<strong>Admin → Security → Roles → + Create</strong>. Add the privilege to the role.',
'rbac.setup.s4.title':'Assign the Role to a User','rbac.setup.s4.desc':'<strong>Admin → Security → Users</strong>. Open the user, click <strong>Assign Roles</strong>.',
```

- [ ] Add to `TRANSLATIONS.ru`:
```js
'rbac.title':'Роли и привилегии','rbac.sub':'Nexspence использует трёхуровневую модель контроля доступа: Content Selectors → Privileges → Roles → Users.',
'rbac.model.sep':'Модель контроля доступа','rbac.cel.sep':'Примеры CEL-выражений','rbac.setup.sep':'Настройка контроля доступа',
'rbac.cs.title':'Content Selector','rbac.cs.desc':'CEL-выражение, определяющее <em>какие артефакты</em> покрывает правило. Переменные: <code>format</code>, <code>path</code>, <code>repository</code>.',
'rbac.priv.title':'Privilege','rbac.priv.desc':'Связывает Content Selector с разрешением. Все создаваемые пользователем привилегии имеют тип <code>repository-content-selector</code>.',
'rbac.role.title':'Role','rbac.role.desc':'Именованный набор привилегий. Встроенная роль <code>nx-admin</code> даёт полный доступ.',
'rbac.cel.th.desc':'Что совпадает','rbac.cel.th.expr':'CEL-выражение',
'rbac.cel.ex1.desc':'Все Maven-артефакты','rbac.cel.ex2.desc':'Конкретная Maven-группа','rbac.cel.ex3.desc':'Все Docker-образы',
'rbac.cel.ex4.desc':'Конкретный репозиторий','rbac.cel.ex5.desc':'Все npm-пакеты','rbac.cel.ex6.desc':'PyPI в конкретном репозитории','rbac.cel.ex7.desc':'Всё (wildcard)',
'rbac.setup.s1.title':'Создайте Content Selector','rbac.setup.s1.desc':'<strong>Admin → Security → Content Selectors → + Create</strong>. Введите CEL-выражение.',
'rbac.setup.s2.title':'Создайте Privilege','rbac.setup.s2.desc':'<strong>Admin → Security → Privileges → + Create</strong>. Выберите ваш Content Selector.',
'rbac.setup.s3.title':'Создайте Role','rbac.setup.s3.desc':'<strong>Admin → Security → Roles → + Create</strong>. Добавьте привилегию в роль.',
'rbac.setup.s4.title':'Назначьте роль пользователю','rbac.setup.s4.desc':'<strong>Admin → Security → Users</strong>. Откройте пользователя, нажмите <strong>Assign Roles</strong>.',
```

### 5c — Commit
- [ ] `git add website/docs/index.html && git commit -m "docs(website): add RBAC section"`

---

## Task 6: Cleanup Policies + Webhooks sections

**Files:** Modify `website/docs/index.html`

### 6a — Add `tpl-cleanup` template after `tpl-rbac`

```html
<template id="tpl-cleanup">
  <div class="doc-section-title" data-i18n="cl.title">Cleanup Policies</div>
  <div class="doc-section-sub" data-i18n="cl.sub">Automatically remove old or unused artifacts to reclaim storage space.</div>

  <div class="inst-sep" data-i18n="cl.criteria.sep">Cleanup Criteria</div>
  <table class="doc-table">
    <thead><tr><th data-i18n="cl.th.criterion">Criterion</th><th data-i18n="cl.th.desc">Description</th></tr></thead>
    <tbody>
      <tr><td data-i18n="cl.age.label">Published before</td><td data-i18n="cl.age.desc">Remove artifacts published more than N days ago.</td></tr>
      <tr><td data-i18n="cl.last.label">Last downloaded before</td><td data-i18n="cl.last.desc">Remove artifacts not downloaded in the last N days.</td></tr>
      <tr><td data-i18n="cl.retain.label">Retain N versions</td><td data-i18n="cl.retain.desc">Keep only the N newest versions of each component. All older versions are eligible for removal.</td></tr>
    </tbody>
  </table>
  <div class="step-list">
    <div class="step-item"><div class="step-num">01</div><div><div class="step-title" data-i18n="cl.s1.title">Create a policy</div><div class="step-desc" data-i18n="cl.s1.desc">Go to <strong>Admin → Cleanup Policies</strong>. Click <strong>+ Create</strong>. Choose format scope (<code>*</code> for all formats or a specific one), set criteria, and optionally set a cron schedule.</div></div></div>
    <div class="step-item"><div class="step-num">02</div><div><div class="step-title" data-i18n="cl.s2.title">Attach to repositories</div><div class="step-desc" data-i18n="cl.s2.desc">Open a repository settings (gear icon) and select one or more cleanup policies. Only artifacts in attached repositories are affected.</div></div></div>
    <div class="step-item"><div class="step-num">03</div><div><div class="step-title" data-i18n="cl.s3.title">Run now or wait for schedule</div><div class="step-desc" data-i18n="cl.s3.desc">On the policy card click <strong>Run Now</strong> to trigger an immediate dry-run (preview) or full run. The default global cron is <code>0 2 * * *</code> (2 AM daily).</div></div></div>
  </div>
  <div class="alert-info" data-i18n="cl.dryrun.note"><strong>Dry run:</strong> The first run shows what <em>would</em> be deleted without actually removing anything. Review the preview before enabling full deletion.</div>
</template>
```

### 6b — Add `tpl-webhooks` template after `tpl-cleanup`

Source: read `docs/webhooks.md` for full event list and payload before implementing.

```html
<template id="tpl-webhooks">
  <div class="doc-section-title" data-i18n="wh.title">Webhooks</div>
  <div class="doc-section-sub" data-i18n="wh.sub">Receive HTTP POST callbacks when repository events occur. Use for CI/CD triggers, Slack notifications, audit systems, and more.</div>

  <div class="inst-sep" data-i18n="wh.events.sep">Events</div>
  <table class="doc-table">
    <thead><tr><th data-i18n="wh.th.event">Event</th><th data-i18n="wh.th.when">When</th></tr></thead>
    <tbody>
      <tr><td><code>artifact.published</code></td><td data-i18n="wh.ev.published">Artifact pushed to hosted or cached by proxy</td></tr>
      <tr><td><code>artifact.deleted</code></td><td data-i18n="wh.ev.deleted">Artifact deleted</td></tr>
      <tr><td><code>repo.created</code></td><td data-i18n="wh.ev.repo-created">Repository created</td></tr>
      <tr><td><code>repo.updated</code></td><td data-i18n="wh.ev.repo-updated">Repository configuration updated</td></tr>
      <tr><td><code>repo.deleted</code></td><td data-i18n="wh.ev.repo-deleted">Repository deleted</td></tr>
      <tr><td><code>proxy.error</code></td><td data-i18n="wh.ev.proxy-error">Proxy failed to fetch from upstream</td></tr>
    </tbody>
  </table>

  <div class="inst-sep" data-i18n="wh.setup.sep">Setting Up a Webhook</div>
  <div class="step-list">
    <div class="step-item"><div class="step-num">01</div><div><div class="step-title" data-i18n="wh.s1.title">Create a webhook</div><div class="step-desc" data-i18n="wh.s1.desc"><strong>Admin → Security → Webhooks → + Create</strong>. Enter a URL, select events to subscribe to, and optionally set a secret for HMAC-SHA256 signature verification.</div></div></div>
    <div class="step-item"><div class="step-num">02</div><div><div class="step-title" data-i18n="wh.s2.title">Test delivery</div><div class="step-desc" data-i18n="wh.s2.desc">Click the ⚡ button on the webhook card to send a test event. The response status and latency are shown inline.</div></div></div>
    <div class="step-item"><div class="step-num">03</div><div><div class="step-title" data-i18n="wh.s3.title">Verify the signature</div><div class="step-desc" data-i18n="wh.s3.desc">If a secret is set, each request carries an <code>X-Nexspence-Signature</code> header (HMAC-SHA256 of the body). Verify it on your receiver to reject forged requests.</div></div></div>
  </div>
  <div class="cb"><div class="cb-bar"><span>Python — verify HMAC signature</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body">import hashlib, hmac

def verify(secret: str, body: bytes, header: str) -> bool:
    expected = "sha256=" + hmac.new(
        secret.encode(), body, hashlib.sha256
    ).hexdigest()
    return hmac.compare_digest(expected, header)</div></div>
  <div class="alert-info" data-i18n="wh.retry.note"><strong>Delivery:</strong> Webhooks are fired asynchronously (fire-and-forget). If your endpoint is unavailable, the event is not retried. Use a queue or durable receiver for critical integrations.</div>
</template>
```

### 6c — Add translations for both sections

- [ ] Add to `TRANSLATIONS.en`:
```js
'cl.title':'Cleanup Policies','cl.sub':'Automatically remove old or unused artifacts to reclaim storage space.',
'cl.criteria.sep':'Cleanup Criteria','cl.th.criterion':'Criterion','cl.th.desc':'Description',
'cl.age.label':'Published before','cl.age.desc':'Remove artifacts published more than N days ago.',
'cl.last.label':'Last downloaded before','cl.last.desc':'Remove artifacts not downloaded in the last N days.',
'cl.retain.label':'Retain N versions','cl.retain.desc':'Keep only the N newest versions of each component.',
'cl.s1.title':'Create a policy','cl.s1.desc':'Go to <strong>Admin → Cleanup Policies → + Create</strong>. Choose format scope, set criteria, and optionally set a cron schedule.',
'cl.s2.title':'Attach to repositories','cl.s2.desc':'Open a repository settings (gear icon) and select cleanup policies. Only attached repositories are affected.',
'cl.s3.title':'Run now or wait for schedule','cl.s3.desc':'Click <strong>Run Now</strong> on the policy card. The default global cron is <code>0 2 * * *</code> (2 AM daily).',
'cl.dryrun.note':'<strong>Dry run:</strong> The first run shows what <em>would</em> be deleted without actually removing anything.',
'wh.title':'Webhooks','wh.sub':'Receive HTTP POST callbacks when repository events occur.',
'wh.events.sep':'Events','wh.setup.sep':'Setting Up a Webhook',
'wh.th.event':'Event','wh.th.when':'When',
'wh.ev.published':'Artifact pushed to hosted or cached by proxy',
'wh.ev.deleted':'Artifact deleted',
'wh.ev.repo-created':'Repository created',
'wh.ev.repo-updated':'Repository configuration updated',
'wh.ev.repo-deleted':'Repository deleted',
'wh.ev.proxy-error':'Proxy failed to fetch from upstream',
'wh.s1.title':'Create a webhook','wh.s1.desc':'<strong>Admin → Security → Webhooks → + Create</strong>. Enter a URL, select events, and optionally set a secret.',
'wh.s2.title':'Test delivery','wh.s2.desc':'Click the ⚡ button on the webhook card to send a test event.',
'wh.s3.title':'Verify the signature','wh.s3.desc':'If a secret is set, each request carries an <code>X-Nexspence-Signature</code> header (HMAC-SHA256 of body).',
'wh.retry.note':'<strong>Delivery:</strong> Webhooks are fired asynchronously. Events are not retried if your endpoint is unavailable.',
```

- [ ] Add to `TRANSLATIONS.ru`:
```js
'cl.title':'Политики очистки','cl.sub':'Автоматически удаляйте старые или неиспользуемые артефакты для освобождения места.',
'cl.criteria.sep':'Критерии очистки','cl.th.criterion':'Критерий','cl.th.desc':'Описание',
'cl.age.label':'Опубликован до','cl.age.desc':'Удалять артефакты, опубликованные более N дней назад.',
'cl.last.label':'Последнее скачивание до','cl.last.desc':'Удалять артефакты, не скачивавшиеся последние N дней.',
'cl.retain.label':'Оставить N версий','cl.retain.desc':'Хранить только N новейших версий каждого компонента.',
'cl.s1.title':'Создайте политику','cl.s1.desc':'Перейдите в <strong>Admin → Cleanup Policies → + Create</strong>. Выберите формат, задайте критерии и при необходимости расписание cron.',
'cl.s2.title':'Привяжите к репозиториям','cl.s2.desc':'Откройте настройки репозитория (иконка шестерёнки) и выберите политики очистки.',
'cl.s3.title':'Запустите сейчас или дождитесь расписания','cl.s3.desc':'Нажмите <strong>Run Now</strong> на карточке политики. Стандартное расписание — <code>0 2 * * *</code> (2:00 ночи ежедневно).',
'cl.dryrun.note':'<strong>Dry run:</strong> Первый запуск показывает, что <em>будет</em> удалено, без фактического удаления.',
'wh.title':'Вебхуки','wh.sub':'Получайте HTTP POST-уведомления о событиях в репозиториях.',
'wh.events.sep':'События','wh.setup.sep':'Настройка вебхука',
'wh.th.event':'Событие','wh.th.when':'Когда',
'wh.ev.published':'Артефакт опубликован в hosted или закэширован proxy',
'wh.ev.deleted':'Артефакт удалён',
'wh.ev.repo-created':'Репозиторий создан',
'wh.ev.repo-updated':'Конфигурация репозитория обновлена',
'wh.ev.repo-deleted':'Репозиторий удалён',
'wh.ev.proxy-error':'Proxy не смог получить данные от upstream',
'wh.s1.title':'Создайте вебхук','wh.s1.desc':'<strong>Admin → Security → Webhooks → + Create</strong>. Укажите URL, выберите события, задайте секрет для проверки подписи.',
'wh.s2.title':'Тест доставки','wh.s2.desc':'Нажмите кнопку ⚡ на карточке вебхука для отправки тестового события.',
'wh.s3.title':'Проверка подписи','wh.s3.desc':'При наличии секрета каждый запрос содержит заголовок <code>X-Nexspence-Signature</code> (HMAC-SHA256 тела).',
'wh.retry.note':'<strong>Доставка:</strong> Вебхуки отправляются асинхронно. При недоступности endpoint событие не повторяется.',
```

### 6d — Commit
- [ ] `git add website/docs/index.html && git commit -m "docs(website): add Cleanup Policies and Webhooks sections"`

---

## Task 7: Advanced sections — Migration, Monitoring, Build Promotion, Content Replication

**Files:** Modify `website/docs/index.html`

### 7a — Add `tpl-migration` template

```html
<template id="tpl-migration">
  <div class="doc-section-title" data-i18n="mig.title">Migration from Nexus</div>
  <div class="doc-section-sub" data-i18n="mig.sub">Migrate repositories, users, roles, and artifacts from a live Nexus OSS or Pro instance without downtime.</div>

  <div class="inst-sep" data-i18n="mig.what.sep">What Gets Migrated</div>
  <table class="doc-table">
    <thead><tr><th data-i18n="mig.th.item">Item</th><th data-i18n="mig.th.detail">Detail</th></tr></thead>
    <tbody>
      <tr><td data-i18n="mig.repos.label">Repositories</td><td data-i18n="mig.repos.desc">Hosted, proxy, and group repo definitions (format, type, configuration)</td></tr>
      <tr><td data-i18n="mig.users.label">Users &amp; Roles</td><td data-i18n="mig.users.desc">Local Nexus users, roles, and privileges</td></tr>
      <tr><td data-i18n="mig.blobs.label">Artifacts</td><td data-i18n="mig.blobs.desc">All components streamed via Nexus REST API — large repos pause/resume</td></tr>
      <tr><td data-i18n="mig.cleanup.label">Cleanup policies</td><td data-i18n="mig.cleanup.desc">Imported and attached to the corresponding repositories</td></tr>
    </tbody>
  </table>
  <div class="step-list">
    <div class="step-item"><div class="step-num">01</div><div><div class="step-title" data-i18n="mig.s1.title">Open the Migration tab</div><div class="step-desc" data-i18n="mig.s1.desc">Go to <strong>Admin → System → Migration</strong>. Enter the source Nexus URL, admin username, and password.</div></div></div>
    <div class="step-item"><div class="step-num">02</div><div><div class="step-title" data-i18n="mig.s2.title">Select scope</div><div class="step-desc" data-i18n="mig.s2.desc">Choose what to migrate: Repositories, Users, Cleanup Policies, Artifacts (blobs). You can run each separately.</div></div></div>
    <div class="step-item"><div class="step-num">03</div><div><div class="step-title" data-i18n="mig.s3.title">Start and monitor</div><div class="step-desc" data-i18n="mig.s3.desc">Click <strong>Start Migration</strong>. Progress is shown in the job history table. Large artifact migrations can be paused and resumed.</div></div></div>
  </div>
  <div class="alert-info" data-i18n="mig.note"><strong>No downtime:</strong> Migration reads from a live Nexus instance via its REST API. Your existing Nexus continues serving traffic during migration. Switch DNS/clients to Nexspence when ready.</div>
</template>
```

### 7b — Add `tpl-monitoring` template

```html
<template id="tpl-monitoring">
  <div class="doc-section-title" data-i18n="mon.title">Monitoring</div>
  <div class="doc-section-sub" data-i18n="mon.sub">Nexspence exposes a Prometheus metrics endpoint and ships a pre-built Grafana dashboard with 8 panels.</div>

  <div class="inst-sep" data-i18n="mon.metrics.sep">Available Metrics</div>
  <table class="doc-table">
    <thead><tr><th data-i18n="mon.th.metric">Metric</th><th data-i18n="mon.th.desc">Description</th></tr></thead>
    <tbody>
      <tr><td><code>nexspence_requests_total</code></td><td data-i18n="mon.m.requests">Total HTTP requests (by method, path, status)</td></tr>
      <tr><td><code>nexspence_request_duration_seconds</code></td><td data-i18n="mon.m.duration">Request latency histogram (p50, p95, p99)</td></tr>
      <tr><td><code>nexspence_artifacts_total</code></td><td data-i18n="mon.m.artifacts">Total artifacts stored</td></tr>
      <tr><td><code>nexspence_bytes_stored_bytes</code></td><td data-i18n="mon.m.bytes">Total storage used in bytes</td></tr>
      <tr><td><code>nexspence_downloads_total</code></td><td data-i18n="mon.m.downloads">Total artifact downloads</td></tr>
      <tr><td><code>nexspence_artifacts_deleted_total</code></td><td data-i18n="mon.m.deleted">Total artifacts deleted</td></tr>
      <tr><td><code>nexspence_goroutines</code></td><td data-i18n="mon.m.goroutines">Active Go goroutines</td></tr>
      <tr><td><code>nexspence_memory_alloc_bytes</code></td><td data-i18n="mon.m.memory">Heap memory in use</td></tr>
    </tbody>
  </table>
  <div class="step-list">
    <div class="step-item"><div class="step-num">01</div><div><div class="step-title" data-i18n="mon.s1.title">Create an API token for Prometheus</div><div class="step-desc" data-i18n="mon.s1.desc">In the UI go to your profile → <strong>API Tokens → Generate Token</strong>. Paste the <code>nxs_*</code> token into <code>deploy/monitoring/prometheus-token</code>.</div></div></div>
    <div class="step-item"><div class="step-num">02</div><div><div class="step-title" data-i18n="mon.s2.title">Start the monitoring stack</div><div class="step-desc" data-i18n="mon.s2.desc">Run <code>docker compose --profile monitoring up -d</code>. Prometheus scrapes <code>/metrics</code> every 10 seconds. The Grafana dashboard loads automatically.</div></div></div>
    <div class="step-item"><div class="step-num">03</div><div><div class="step-title" data-i18n="mon.s3.title">Open the UI Charts tab</div><div class="step-desc" data-i18n="mon.s3.desc">In the Nexspence UI, go to <strong>Admin → Monitoring</strong>. The <strong>Charts</strong> tab shows request rate, error rate, and storage growth without needing Grafana.</div></div></div>
  </div>
  <div class="cb"><div class="cb-bar"><span>curl — metrics endpoint</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body">curl -H "Authorization: Bearer nxs_your_token" \
  http://localhost:8081/metrics</div></div>
</template>
```

### 7c — Add `tpl-promotion` template

```html
<template id="tpl-promotion">
  <div class="doc-section-title" data-i18n="promo.title">Build Promotion</div>
  <div class="doc-section-sub" data-i18n="promo.sub">Promote artifacts through environments (e.g. staging → production) with optional vulnerability scan gates and approval workflows.</div>

  <div class="inst-sep" data-i18n="promo.how.sep">How It Works</div>
  <div class="step-list">
    <div class="step-item"><div class="step-num">01</div><div><div class="step-title" data-i18n="promo.s1.title">Create a Promotion Rule</div><div class="step-desc" data-i18n="promo.s1.desc"><strong>Admin → Promotion → + Create Rule</strong>. Set source repository, target repository, an optional CEL path filter, and whether a passing vulnerability scan is required before promotion.</div></div></div>
    <div class="step-item"><div class="step-num">02</div><div><div class="step-title" data-i18n="promo.s2.title">Request promotion</div><div class="step-desc" data-i18n="promo.s2.desc">In <strong>Browse</strong>, select an artifact and click <strong>Promote</strong>. Or use the REST API. A promotion request is created in the queue.</div></div></div>
    <div class="step-item"><div class="step-num">03</div><div><div class="step-title" data-i18n="promo.s3.title">Approve or reject</div><div class="step-desc" data-i18n="promo.s3.desc">Admins review the request queue in <strong>Admin → Promotion → Requests</strong>. Approve to copy the artifact blob to the target repository (no data duplication — shared blob key).</div></div></div>
  </div>
  <div class="cb"><div class="cb-bar"><span>curl — promote via API</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div>
  <div class="cb-body">curl -u admin:admin123 -X POST \
  http://localhost:8081/api/v1/promotion/promote \
  -H "Content-Type: application/json" \
  -d '{
    "component_id": "abc-123",
    "rule_id": "rule-456",
    "note": "Approved for production"
  }'</div></div>
  <div class="alert-info" data-i18n="promo.scan.note"><strong>Scan gate:</strong> If <em>Require scan pass</em> is enabled on the rule, components with HIGH or CRITICAL vulnerabilities are blocked from promotion until the issues are resolved.</div>
</template>
```

### 7d — Add `tpl-replication` template

```html
<template id="tpl-replication">
  <div class="doc-section-title" data-i18n="repl.title">Content Replication</div>
  <div class="doc-section-sub" data-i18n="repl.sub">Automatically push artifacts from one Nexspence instance to another on a schedule — for geo-distribution, DR, or environment promotion.</div>

  <div class="step-list">
    <div class="step-item"><div class="step-num">01</div><div><div class="step-title" data-i18n="repl.s1.title">Create a Replication Rule</div><div class="step-desc" data-i18n="repl.s1.desc"><strong>Admin → Replication → + Create</strong>. Set: source repository, target Nexspence URL, target repository name, credentials for the remote instance, and a cron schedule.</div></div></div>
    <div class="step-item"><div class="step-num">02</div><div><div class="step-title" data-i18n="repl.s2.title">Test the connection</div><div class="step-desc" data-i18n="repl.s2.desc">Click <strong>Test Connection</strong> on the rule card. Nexspence sends a probe request to the remote URL and reports success or error.</div></div></div>
    <div class="step-item"><div class="step-num">03</div><div><div class="step-title" data-i18n="repl.s3.title">Monitor replication history</div><div class="step-desc" data-i18n="repl.s3.desc">The history table on each rule shows past runs: timestamp, artifacts synced, errors. Click <strong>Run Now</strong> to trigger an immediate sync.</div></div></div>
  </div>
  <div class="alert-info" data-i18n="repl.diff.note"><strong>Differential sync:</strong> Each run compares asset paths between source and target and only pushes artifacts that are missing or changed on the remote. Credentials are stored AES-256-GCM encrypted.</div>
</template>
```

### 7e — Add translations for all 4 advanced sections

- [ ] Add to `TRANSLATIONS.en`:
```js
'mig.title':'Migration from Nexus','mig.sub':'Migrate repositories, users, roles, and artifacts from a live Nexus OSS or Pro instance without downtime.',
'mig.what.sep':'What Gets Migrated','mig.th.item':'Item','mig.th.detail':'Detail',
'mig.repos.label':'Repositories','mig.repos.desc':'Hosted, proxy, and group repo definitions',
'mig.users.label':'Users & Roles','mig.users.desc':'Local Nexus users, roles, and privileges',
'mig.blobs.label':'Artifacts','mig.blobs.desc':'All components streamed via Nexus REST API — large repos pause/resume',
'mig.cleanup.label':'Cleanup policies','mig.cleanup.desc':'Imported and attached to corresponding repositories',
'mig.s1.title':'Open the Migration tab','mig.s1.desc':'Go to <strong>Admin → System → Migration</strong>. Enter the source Nexus URL, admin username, and password.',
'mig.s2.title':'Select scope','mig.s2.desc':'Choose what to migrate: Repositories, Users, Cleanup Policies, Artifacts. Run each separately.',
'mig.s3.title':'Start and monitor','mig.s3.desc':'Click <strong>Start Migration</strong>. Large artifact migrations can be paused and resumed.',
'mig.note':'<strong>No downtime:</strong> Migration reads from a live Nexus via its REST API. Switch clients to Nexspence when ready.',
'mon.title':'Monitoring','mon.sub':'Nexspence exposes a Prometheus metrics endpoint and ships a pre-built Grafana dashboard with 8 panels.',
'mon.metrics.sep':'Available Metrics','mon.th.metric':'Metric','mon.th.desc':'Description',
'mon.m.requests':'Total HTTP requests (by method, path, status)',
'mon.m.duration':'Request latency histogram (p50, p95, p99)',
'mon.m.artifacts':'Total artifacts stored',
'mon.m.bytes':'Total storage used in bytes',
'mon.m.downloads':'Total artifact downloads',
'mon.m.deleted':'Total artifacts deleted',
'mon.m.goroutines':'Active Go goroutines',
'mon.m.memory':'Heap memory in use',
'mon.s1.title':'Create an API token for Prometheus','mon.s1.desc':'Profile → <strong>API Tokens → Generate Token</strong>. Paste the token into <code>deploy/monitoring/prometheus-token</code>.',
'mon.s2.title':'Start the monitoring stack','mon.s2.desc':'Run <code>docker compose --profile monitoring up -d</code>. Prometheus scrapes <code>/metrics</code> every 10 seconds.',
'mon.s3.title':'Open the UI Charts tab','mon.s3.desc':'In the UI go to <strong>Admin → Monitoring → Charts</strong> for request rate, error rate, and storage growth.',
'promo.title':'Build Promotion','promo.sub':'Promote artifacts through environments with optional vulnerability scan gates and approval workflows.',
'promo.how.sep':'How It Works',
'promo.s1.title':'Create a Promotion Rule','promo.s1.desc':'<strong>Admin → Promotion → + Create Rule</strong>. Set source, target, optional CEL path filter, and scan requirement.',
'promo.s2.title':'Request promotion','promo.s2.desc':'In <strong>Browse</strong>, select an artifact and click <strong>Promote</strong>, or use the REST API.',
'promo.s3.title':'Approve or reject','promo.s3.desc':'Admins review the queue in <strong>Admin → Promotion → Requests</strong>. Approve to copy the artifact to the target repository.',
'promo.scan.note':'<strong>Scan gate:</strong> Components with HIGH or CRITICAL vulnerabilities are blocked from promotion until resolved.',
'repl.title':'Content Replication','repl.sub':'Automatically push artifacts from one Nexspence instance to another on a schedule.',
'repl.s1.title':'Create a Replication Rule','repl.s1.desc':'<strong>Admin → Replication → + Create</strong>. Set source repo, target URL, target repo, credentials, and a cron schedule.',
'repl.s2.title':'Test the connection','repl.s2.desc':'Click <strong>Test Connection</strong> on the rule card.',
'repl.s3.title':'Monitor replication history','repl.s3.desc':'The history table shows past runs. Click <strong>Run Now</strong> to trigger an immediate sync.',
'repl.diff.note':'<strong>Differential sync:</strong> Only missing or changed artifacts are pushed. Credentials are stored AES-256-GCM encrypted.',
```

- [ ] Add to `TRANSLATIONS.ru`:
```js
'mig.title':'Миграция из Nexus','mig.sub':'Перенесите репозитории, пользователей, роли и артефакты из работающего Nexus OSS или Pro без остановки сервиса.',
'mig.what.sep':'Что переносится','mig.th.item':'Элемент','mig.th.detail':'Описание',
'mig.repos.label':'Репозитории','mig.repos.desc':'Определения hosted, proxy и group репозиториев',
'mig.users.label':'Пользователи и роли','mig.users.desc':'Локальные пользователи, роли и привилегии Nexus',
'mig.blobs.label':'Артефакты','mig.blobs.desc':'Все компоненты через REST API Nexus — большие репозитории с паузой/продолжением',
'mig.cleanup.label':'Политики очистки','mig.cleanup.desc':'Импортируются и привязываются к соответствующим репозиториям',
'mig.s1.title':'Откройте вкладку Migration','mig.s1.desc':'Перейдите в <strong>Admin → System → Migration</strong>. Введите URL источника Nexus, логин и пароль.',
'mig.s2.title':'Выберите область','mig.s2.desc':'Выберите что переносить: Repositories, Users, Cleanup Policies, Artifacts. Можно запускать раздельно.',
'mig.s3.title':'Запустите и следите','mig.s3.desc':'Нажмите <strong>Start Migration</strong>. Миграцию больших репозиториев можно ставить на паузу и продолжать.',
'mig.note':'<strong>Без простоя:</strong> Миграция читает из работающего Nexus через его REST API. Переключите клиентов на Nexspence когда будете готовы.',
'mon.title':'Мониторинг','mon.sub':'Nexspence предоставляет endpoint Prometheus и поставляется с готовым дашбордом Grafana из 8 панелей.',
'mon.metrics.sep':'Доступные метрики','mon.th.metric':'Метрика','mon.th.desc':'Описание',
'mon.m.requests':'Всего HTTP-запросов (по методу, пути, статусу)',
'mon.m.duration':'Гистограмма задержек (p50, p95, p99)',
'mon.m.artifacts':'Всего хранимых артефактов',
'mon.m.bytes':'Занятое место в байтах',
'mon.m.downloads':'Всего скачиваний',
'mon.m.deleted':'Всего удалённых артефактов',
'mon.m.goroutines':'Активные goroutine Go',
'mon.m.memory':'Используемая heap-память',
'mon.s1.title':'Создайте API-токен для Prometheus','mon.s1.desc':'Профиль → <strong>API Tokens → Generate Token</strong>. Вставьте токен в <code>deploy/monitoring/prometheus-token</code>.',
'mon.s2.title':'Запустите стек мониторинга','mon.s2.desc':'Выполните <code>docker compose --profile monitoring up -d</code>. Prometheus опрашивает <code>/metrics</code> каждые 10 секунд.',
'mon.s3.title':'Откройте вкладку Charts','mon.s3.desc':'В UI перейдите в <strong>Admin → Monitoring → Charts</strong> для просмотра запросов, ошибок и роста хранилища.',
'promo.title':'Продвижение сборок','promo.sub':'Продвигайте артефакты через среды (staging → production) с проверкой уязвимостей и согласованием.',
'promo.how.sep':'Как это работает',
'promo.s1.title':'Создайте правило продвижения','promo.s1.desc':'<strong>Admin → Promotion → + Create Rule</strong>. Задайте источник, цель, CEL-фильтр и требование сканирования.',
'promo.s2.title':'Запросите продвижение','promo.s2.desc':'В <strong>Browse</strong> выберите артефакт и нажмите <strong>Promote</strong> или используйте REST API.',
'promo.s3.title':'Подтвердите или отклоните','promo.s3.desc':'Администраторы просматривают очередь в <strong>Admin → Promotion → Requests</strong> и подтверждают копирование.',
'promo.scan.note':'<strong>Проверка сканирования:</strong> Компоненты с HIGH или CRITICAL уязвимостями блокируются до устранения проблем.',
'repl.title':'Репликация контента','repl.sub':'Автоматически переносите артефакты с одного экземпляра Nexspence на другой по расписанию.',
'repl.s1.title':'Создайте правило репликации','repl.s1.desc':'<strong>Admin → Replication → + Create</strong>. Задайте источник, URL цели, репозиторий, учётные данные и cron-расписание.',
'repl.s2.title':'Проверьте соединение','repl.s2.desc':'Нажмите <strong>Test Connection</strong> на карточке правила.',
'repl.s3.title':'Следите за историей','repl.s3.desc':'Таблица истории показывает прошлые запуски. Нажмите <strong>Run Now</strong> для немедленной синхронизации.',
'repl.diff.note':'<strong>Дифференциальная синхронизация:</strong> Передаются только отсутствующие или изменённые артефакты. Учётные данные хранятся в шифровании AES-256-GCM.',
```

### 7f — Commit
- [ ] `git add website/docs/index.html && git commit -m "docs(website): add Migration, Monitoring, Build Promotion, Content Replication sections"`

---

## Task 8: Final verification + NEXT_RELEASE.md

**Files:** Modify `NEXT_RELEASE.md`, read `website/docs/index.html`

### 8a — Verify all 16 sections work

- [ ] Start server: `cd /Users/skensel/WORKING/AI/nexspence-core/website && python3 -m http.server 8900`
- [ ] Open `http://localhost:8900/docs/`
- [ ] Check sidebar shows 6 groups: Getting Started, Using Nexspence, Administration, Advanced, Reference, Changelog
- [ ] Click each section in the sidebar — content loads without JS errors
- [ ] Switch language EN → RU — all section titles and descriptions translate
- [ ] Switch between sections after language change — translations persist
- [ ] On mobile (resize to 375px): sidebar hides, mobile accordion works, all sections accessible
- [ ] Kill server: `lsof -ti:8900 | xargs kill -9`

### 8b — Update NEXT_RELEASE.md

- [ ] Append to `### ✨ Features` in `NEXT_RELEASE.md`:

```markdown
- **Comprehensive docs on nexspence.com** — `/docs/` expanded from 5 to 16 sections: Repositories (types, URLs, anonymous access), Format Setup Guides (Maven/npm/PyPI/Docker/Helm/Go/Cargo/NuGet/Raw with exact client config), Browse & Search (UI + REST API), Users & API Tokens, Roles & Privileges (RBAC model, CEL examples), Cleanup Policies, Webhooks (events, HMAC verification), Migration from Nexus, Monitoring (metrics table, Grafana setup), Build Promotion, Content Replication; sidebar restructured into 6 groups; tab bar removed; full EN/RU coverage for all new sections
```

### 8c — Commit and push

- [ ] `git add website/docs/index.html NEXT_RELEASE.md`
- [ ] `git commit -m "docs(website): comprehensive user documentation — 16 sections, full EN/RU"`
- [ ] `git push`
- [ ] Sync nexspence-demo README if it changed: `cp /Users/skensel/WORKING/AI/nexspence-core/README.md /Users/skensel/WORKING/AI/nexspence-demo/README.md && cd /Users/skensel/WORKING/AI/nexspence-demo && git add README.md && git diff --staged --quiet || git commit -m "docs: sync README" && git push`

---

## Self-Review

### Spec coverage

| Requirement | Task |
|------------|------|
| Remove tab bar | Task 1 |
| 16-section sidebar | Task 1 |
| Repositories section | Task 2 |
| Format Setup Guides (all 9 formats) | Task 3 |
| Browse & Search section | Task 4 |
| Users & API Tokens section | Task 4 |
| RBAC section with CEL examples | Task 5 |
| Cleanup Policies section | Task 6 |
| Webhooks section with HMAC | Task 6 |
| Migration from Nexus section | Task 7 |
| Monitoring section with metrics table | Task 7 |
| Build Promotion section | Task 7 |
| Content Replication section | Task 7 |
| EN translations for all new content | Tasks 2–7 |
| RU translations for all new content | Tasks 2–7 |
| NEXT_RELEASE.md updated | Task 8 |
| Push to both repos | Task 8 |

### Placeholder scan

No TBDs or TODOs. Every template has literal HTML. All translation keys are provided in both EN and RU. Code blocks contain real commands.

### Type consistency

`data-i18n` key naming: `{section}.{subsection}.{element}` — consistent throughout. `applyTranslations(node)` is already implemented in the file and requires no changes. `SECTIONS` array keys exactly match `SIDEBAR_GROUPS` item keys. `tpl-{key}` template IDs exactly match `SECTIONS[].templateId` values.
