### ✨ Features

- **Docs section on nexspence.com** — `/docs/` page with versioned changelog fetched live from GitHub Releases API, sidebar navigation (Getting Started / Formats / Reference / Changelog), Quick Start / Installation / Formats / API Reference tabs, collapsible release cards with features + bug fixes from NEXT_RELEASE.md content; fully responsive — mobile accordion nav, bottom tab bar, horizontal-scroll version chips; `landing/` directory removed
- **Comprehensive docs on nexspence.com** — `/docs/` expanded from 5 to 16 sections: Repositories (types, URLs, anonymous access), Format Setup Guides (Maven/npm/PyPI/Docker/Helm/Go/Cargo/NuGet/Raw with exact client config), Browse & Search (UI + REST API), Users & API Tokens, Roles & Privileges (RBAC model, CEL examples), Cleanup Policies, Webhooks (events, HMAC verification), Migration from Nexus, Monitoring (metrics table, Grafana setup), Build Promotion, Content Replication; sidebar restructured into 6 groups; tab bar removed; full EN/RU coverage for all new sections

### 🐛 Bug Fixes

- **seed-rbac.sh**: fix role privilege assignment — roles were created without privileges on re-runs (409/dup skipped `SetPrivileges`); add `put_privileges` helper that always calls `PUT .../roles/:id/privileges` after create-or-find, add guards for empty privilege/role IDs
