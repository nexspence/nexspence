### ✨ Features

- **Website favicon + custom error pages** — added a browser-tab favicon (`/assets/favicon.png`, 64×64 from the logo) and apple-touch-icon, linked from both the main site and `/docs/`. Added styled `404` / `403` / `50x` error pages matching the dark glassmorphism theme; nginx now serves a real 404 for unknown paths (was silently falling back to the homepage).


### 🔒 Security

- **Dependency CVE sweep (Trivy + Grype verified)** — patched all vulnerabilities found in the container image's application layer. Upgraded `russellhaering/goxmldsig` v1.4.0 → v1.6.0 (CVE-2026-33487, HIGH — XML Digital Signature integrity bypass affecting SAML/SSO validation), `Azure/go-ntlmssp` v0.1.0 → v0.1.1 (CVE-2026-32952, MEDIUM — NTLM challenge DoS), and `jackc/pgx/v5` v5.9.1 → v5.9.2 (CVE-2026-41889, LOW — SQL injection under specific query conditions). Pinned the Go toolchain to **1.26.3** (go directive + `golang:1.26-alpine` build image) to pull in the patched standard library (closes a batch of HIGH/MEDIUM stdlib CVEs: net/http2 SETTINGS DoS, mail-parsing, ReverseProxy, LookupCNAME). A fresh `trivy rootfs` scan of the 1.26.3-built binary reports **0 vulnerabilities**.

### 🔧 Maintenance

- **Dependency refresh** — updated all remaining Go modules to latest within-major (AWS SDK v2, cel-go, prometheus/client_golang, redis/go-redis, zap, goose, golang.org/x/{crypto,net,sys,text}, …) and bumped frontend packages (react 19.2.6, vite 8.0.14, react-router-dom 7.16, @tanstack/react-query 5.100, lucide-react 1.17, axios 1.16, eslint 10.4, plus date-fns 4 / pretty-bytes 7). Build image bumped to Node 24 (Active LTS). 474 Go tests pass; frontend `tsc` + Vite build clean.

### 🐛 Bug Fixes

- **Docs changelog now auto-refreshes** — `/docs/` cached the GitHub Releases list in `sessionStorage` with no expiry, so an open session never picked up newly published releases. Added a 5-minute TTL (`{ts, releases}` envelope) and bumped the cache key to `nx_releases_v2` so existing stale caches are ignored immediately.
