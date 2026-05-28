### ✨ Features

- **Website favicon + custom error pages** — added a browser-tab favicon (`/assets/favicon.png`, 64×64 from the logo) and apple-touch-icon, linked from both the main site and `/docs/`. Added styled `404` / `403` / `50x` error pages matching the dark glassmorphism theme; nginx now serves a real 404 for unknown paths (was silently falling back to the homepage).


### 🐛 Bug Fixes

- **Docs changelog now auto-refreshes** — `/docs/` cached the GitHub Releases list in `sessionStorage` with no expiry, so an open session never picked up newly published releases. Added a 5-minute TTL (`{ts, releases}` envelope) and bumped the cache key to `nx_releases_v2` so existing stale caches are ignored immediately.
