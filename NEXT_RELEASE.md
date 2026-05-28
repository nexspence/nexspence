### ✨ Features


### 🐛 Bug Fixes

- **Docs changelog now auto-refreshes** — `/docs/` cached the GitHub Releases list in `sessionStorage` with no expiry, so an open session never picked up newly published releases. Added a 5-minute TTL (`{ts, releases}` envelope) and bumped the cache key to `nx_releases_v2` so existing stale caches are ignored immediately.
