### ✨ Features

- **Docs section on nexspence.com** — `/docs/` page with versioned changelog fetched live from GitHub Releases API, sidebar navigation (Getting Started / Formats / Reference / Changelog), Quick Start / Installation / Formats / API Reference tabs, collapsible release cards with features + bug fixes from NEXT_RELEASE.md content; fully responsive — mobile accordion nav, bottom tab bar, horizontal-scroll version chips; `landing/` directory removed

### 🐛 Bug Fixes

- **seed-rbac.sh**: fix role privilege assignment — roles were created without privileges on re-runs (409/dup skipped `SetPrivileges`); add `put_privileges` helper that always calls `PUT .../roles/:id/privileges` after create-or-find, add guards for empty privilege/role IDs
