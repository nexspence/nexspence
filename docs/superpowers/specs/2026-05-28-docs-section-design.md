---
name: docs-section-design
description: Design for versioned documentation section on nexspence.com website — changelog from GitHub Releases API, static guide pages, responsive layout
metadata:
  type: project
---

# Docs Section — nexspence.com

## Goal

Add a `/docs` section to the nexspence.com static site (`website/`) that shows:
- Versioned changelog pulled live from GitHub Releases API
- Static documentation pages (Getting Started, Installation, Formats, API reference)
- Full responsive support (desktop sidebar + mobile accordion/bottom-nav)

## Data Source

**GitHub Releases API** — `https://api.github.com/repos/skensell201/nexspence/releases`

- Public API, no auth, 60 req/hour unauthenticated (sufficient for a docs site)
- Each release has `tag_name`, `name`, `published_at`, `body` (Markdown from NEXT_RELEASE.md)
- CI workflow (`release.yml`) already creates releases from NEXT_RELEASE.md on every tag push — no extra work needed
- Body format: `### ✨ Features\n...\n### 🐛 Bug Fixes\n...`
- Cached in sessionStorage to avoid redundant fetches

## File Structure

```
website/
├── index.html            # existing main site — add Docs nav pill + CTA button
├── docs/
│   ├── index.html        # docs hub — version switcher, sidebar, content area
│   └── data/
│       └── (no files — releases fetched live from GitHub API)
├── assets/               # existing
└── Dockerfile            # unchanged
```

URL: `nexspence.com/docs/` → `docs/index.html`

## Architecture

Single HTML file (`docs/index.html`) with vanilla JS, no build step, same CSS variables as `index.html`:

1. **On load**: fetch GitHub Releases API → parse → render version list and first release changelog
2. **Version switch**: click → render that release's body
3. **Section switch**: click sidebar link → render static content (embedded in HTML as `<template>` tags)
4. **No frameworks** — vanilla JS, same approach as existing index.html

## Layout (Desktop)

```
[Floating Nav — same as main site, Docs pill added]

┌─────────────────────────────────────────────┐
│  Version: [v1.9 ▾current] [v1.8] [v1.7]... │
├──────────────┬──────────────────────────────┤
│  Sidebar     │  Breadcrumb: Docs / v1.9 /…  │
│  ──────────  │  Tabs: [Quick Start] [Changelog*] [API] │
│  Getting     │                               │
│  Started     │  [Changelog content]          │
│  Install     │  v1.9.1 ▾ latest             │
│  Config      │  v1.9.0 ▾ minor              │
│  ──────────  │  v1.8.2 ▾ patch              │
│  Changelog ✓ │                               │
│  ──────────  │                               │
│  API Ref     │                               │
└──────────────┴──────────────────────────────┘
```

## Layout (Mobile, ≤768px)

- Top bar: logo + "Docs" badge + hamburger
- Version: horizontal scroll chips
- Navigation: collapsed accordion showing current page name; tap to expand full menu
- Tabs: horizontal scroll, no wrap
- Content: full width, cards full width
- Bottom tab bar: Overview / Install / Docs (active) / GitHub

## Changelog Rendering

Release body is Markdown. Parser: lightweight custom (no library):
- `### ✨ Features` → group header with blue accent
- `### 🐛 Bug Fixes` → group header with green accent  
- `- **Bold:** text` → list items with strong
- `\`code\`` → `<code>` inline
- Long Docker section at end of release body is hidden (collapsed "Show Docker install info")

## Sections (static content, embedded as templates)

1. **Quick Start** — docker compose snippet, 3 steps, links to full install
2. **Installation** — Docker Compose, Helm, From Source (links to docs/deployment.md)
3. **Configuration** — config.yaml reference table
4. **Formats** — table of 14 formats with hosted/proxy/group support
5. **Changelog** — live from GitHub API (default section)
6. **API Reference** — link to docs/api-spec.yaml + key endpoints table

## Responsive Specifics

| Element | Desktop | Mobile |
|---------|---------|--------|
| Sidebar | Sticky 220px column | Collapsed accordion |
| Version switcher | Pill buttons row | Horizontal scroll chips |
| Tabs | Fixed row with border-bottom | Horizontal scroll |
| Nav | Floating pill bar (adds Docs pill) | Top bar + bottom nav bar |

## Integration with index.html

- Add `Docs` nav pill to floating nav (purple colour, `href="/docs/"`)
- Add "Read the Docs" button to hero actions
- No other changes to index.html

## Caching & Performance

- GitHub API response cached in `sessionStorage` with key `nx_releases_cache`
- TTL: session-only (fresh on each page load, same session reuses)
- Skeleton loader shown while fetching (3 placeholder cards)
- On API error: show cached data if available, otherwise show "View on GitHub" fallback link

## Nginx

No changes to Dockerfile or nginx config — nginx:alpine already serves directory indexes correctly for `docs/index.html` at path `/docs/`.
