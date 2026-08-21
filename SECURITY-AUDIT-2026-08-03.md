# Security audit — nexspence

**Date:** 2026-08-03 · **Commit:** `v1.25.0` (main) · **Scope:** Go backend (31.6k LOC), React frontend (14k LOC), Dockerfiles, compose, CI

---

## How this was produced

| Layer | Tool / method | Result |
|---|---|---|
| Go SAST | `gosec` (154 files, 31k lines) | 23 findings — **all pre-annotated with justified `//nolint`**. No new issues. |
| Go deps | `govulncheck` | 0 reachable vulnerabilities. |
| JS deps | `npm audit` | 3 high (see M-8). |
| Secrets | `gitleaks` (192 commits, 8.15 MB) | 210 hits — **all documentation examples, test fixtures, or placeholders. No real secret ever committed.** |
| IaC / containers | `trivy config` | 1 high (`website/Dockerfile` runs as root). |
| Manual review | auth, RBAC, path handling, SSRF, crypto, archive handling, CORS, CSP, session, SQL | findings below |

Automated tooling was effectively clean. **Everything meaningful below came from manual review** — which is the expected shape for a codebase that has already been through static analysis.

---

## Findings

Severity reflects impact **in the deployment shape the project ships by default** (docker-compose quick start), not the best-case hardened config.

### HIGH

#### H-1 · `auth.anonymous_enabled` is a control that does nothing
`internal/config/config.go:405` defaults it to `true`, and `cmd/server/main.go:69` is the **only** place it is read — to print a warning. It is never consulted in any authorization path.

```
internal/service/rbac_service.go:31
    if repo.AllowAnonymous && isReadAction(action) { return true, nil }
```

Anonymous access is decided purely per-repository. An operator who sets `anonymous_enabled: false` to lock the instance down gets a config that silently changes nothing — the most dangerous class of security bug, because it converts a real control into a false sense of safety.

**Fix:** gate the `repo.AllowAnonymous` branch on the global flag, or delete the setting so it cannot mislead.

> Mitigating: the DB default for `allow_anonymous` is `FALSE` (`006_repo_allow_anonymous.sql`), so repositories are not public unless someone opts in.

---

#### H-2 · CORS defaults to `Access-Control-Allow-Origin: *`
`internal/api/middleware.go:35` — when `cors_origins` is empty (the shipped default, `config.go:385` and `config.yaml.example:6`), every response gets a wildcard origin.

No `Allow-Credentials`, so cookie-authenticated requests are safe. But this API authenticates by `Authorization` header **and** serves anonymous repositories. The combination is exploitable:

> Employee visits `evil.com` → page issues `fetch('http://nexspence.internal:8081/repository/private-maven/…')` → browser sends it from inside the corporate network → wildcard CORS lets the page **read the response**.

Any internal artifact in an anonymous-readable repository is exfiltratable from any website the user visits.

**Fix:** default to no CORS header at all. A wildcard should require explicitly writing `cors_origins: ["*"]`.

---

#### H-3 · `SetTrustedProxies` is never called — `X-Forwarded-For` is trusted from anyone
Verified absent across the whole tree. Gin's default trusts all proxies, so `c.ClientIP()` returns whatever the client puts in `X-Forwarded-For`. Two consequences:

1. **Rate limiting is trivially bypassed** — `ratelimit_middleware.go:73` keys buckets on `ip:` + ClientIP. Rotate the header per request, get unlimited buckets.
2. **The audit log is forgeable** — `audit_middleware.go:64` stores `RemoteIP: c.ClientIP()`, as do the login success/failure logs (`auth.go:80,86`) and OIDC/SAML logs. An attacker picks the IP that appears in the security record of their own actions.

For a product whose audit trail is a compliance feature, (2) is the bigger problem.

**Fix:** `r.SetTrustedProxies([...])` from config; default to trusting nothing.

---

### MEDIUM

#### M-1 · Rate limiting is off by default
`config.go:409` — `auth.rate_limit_enabled: false`. Out of the box there is no throttle on `/api/v1/login` or on Basic auth. With `bcrypt_cost: 12` (~250 ms CPU per attempt) this is both an unmetered credential-guessing surface and a cheap CPU exhaustion vector: a few hundred concurrent bad logins saturate the box.

There is also no account lockout or failed-attempt backoff anywhere.

**Fix:** default it on; add per-account failed-login backoff independent of the IP bucket (which H-3 shows cannot be trusted).

---

#### M-2 · Bad credentials silently degrade to anonymous, unlogged
`internal/api/handlers/auth.go:414` — in `OptionalAuth`, a failed `users.Login` falls through to `c.Next()` with no identity and **no log line**. The `/repository/*` and `/v2/*` trees all use `OptionalAuth`.

Password guessing against `/repository/…` therefore produces no failed-login record at all — it is invisible to the audit log and to any alerting built on it, while still costing a bcrypt per try.

**Fix:** log the failure (as the `AuthMiddleware` path does) and feed it to the same backoff as M-1.

---

#### M-3 · OIDC `cookie_key` ships as a fixed value and is not covered by the insecure-default gate
`cmd/server/main.go:74-86` fails closed on the shipped JWT secret and on `admin123` — good, and clearly deliberate. But `NEXSPENCE_OIDC_COOKIE_KEY` is hard-coded in `docker-compose.yml:124`, `docker-compose.ha.yml:135`, and `config.yaml.example:176` (`YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY=` = `abcdefghijklmnopqrstuvwxyz123456`) and is **not** in that check.

That key seals the OIDC state cookie (`internal/auth/oidc_cookie.go`, AES-GCM). An attacker who knows it can forge a state cookie matching their own `state` parameter, defeating the CSRF protection on the login flow.

The rest of the OIDC implementation is solid — crypto-random state and nonce, PKCE, sealed cookie with expiry, `state` compared against the cookie (`handlers/oidc.go:116`). This one gap undercuts it.

**Fix:** extend the fail-closed check to the shipped cookie key.

---

#### M-4 · No SSRF guards on any outbound URL
No allow/deny list, no link-local blocking, no `IsPrivate`/`IsLoopback` check exists anywhere in the tree. Three distinct paths:

| Path | Who controls the URL |
|---|---|
| `proxy_config.remote_url` | admin |
| Webhook target URL | admin |
| Docker token `realm` (`docker_registry_token.go:85`) | **the upstream registry's `WWW-Authenticate` response** |

The first two are admin-only, but "Nexspence admin" and "owner of the cloud account" are not the same principal — a proxy repo pointed at `http://169.254.169.254/latest/meta-data/iam/security-credentials/` turns repo-admin into IAM credential theft.

The third is the interesting one: `realm` is taken from a *response header* of the upstream. A compromised or MITM'd upstream redirects the server's authenticated fetch to an arbitrary host. Exfiltration is limited (the response must parse as a token JSON), so this is blind SSRF.

**Fix:** resolve and reject link-local/loopback/private ranges before any outbound fetch, with an explicit opt-out for on-prem upstreams.

---

#### M-5 · SAML assertions are not bound to the AuthnRequest
`internal/auth/saml.go:109` — `s.sp.ParseResponse(r, nil)`. The `possibleRequestIDs` argument is `nil`, and no request-ID state is stored between `AuthnRequestURL` and the ACS endpoint.

In `crewjam/saml`, `InResponseTo` is only checked when present; an assertion **without** it skips the check entirely. So a validly-signed assertion that the attacker obtained (e.g. their own) can be replayed into a victim's browser to force a login as the attacker's identity — the classic SAML login-CSRF.

`RelayState` is HMAC-signed (`SignRelayState`), which protects the `return_to` value but does not bind the assertion to a session.

> **Confidence: needs confirmation.** This is read from the code and the library's documented behaviour; I did not build an assertion to prove it end to end. Worth verifying before prioritising.

**Fix:** persist issued request IDs and pass them to `ParseResponse`.

---

#### M-6 · No Content-Security-Policy, JWT in `localStorage`
`internal/api/middleware.go:53` sets `nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy` — and explicitly documents CSP as out of scope. The SPA stores its JWT in `localStorage` (`frontend/src/api/client.ts:25`, `OIDCCallbackPage.tsx:26`).

Neither is wrong on its own — `localStorage` is normal for a Bearer-token SPA, and no `dangerouslySetInnerHTML` or `innerHTML` sink exists in the frontend. But together they mean any future XSS is a full token theft with no defence in depth.

**Fix:** ship a CSP. The SPA is self-hosted and bundled, so a strict policy is achievable.

---

#### M-7 · Health probes bypass every middleware
`internal/api/router.go:278-279` registers `/healthz` and `/readyz` **before** `r.Use(...)`. Gin snapshots the middleware chain at registration time, so these two routes run with no `Recovery`, no rate limit, no body limit, no audit.

`/readyz` (`handlers.ReadinessHandler(pool, rdb)`) queries Postgres and Redis on every hit. Unauthenticated, unthrottled, one DB round-trip per request.

**Fix:** register them after `r.Use`, or exempt them explicitly inside the middlewares.

---

#### M-8 · Frontend dependencies: 3 high
- `react-router` / `react-router-dom` — RSC-mode CSRF bypass. The app does not use RSC mode, so **not exploitable here**, but it should still be bumped.
- `brace-expansion` — OOM DoS; dev-dependency only.

---

### LOW

| ID | Finding |
|---|---|
| L-1 | **Backup import buffers up to 8 GiB in memory.** `backup_import.go:26` caps decompression (good — the gzip-bomb guard is deliberate), but every entry lands in an in-memory map. Admin-only, so it is a self-inflicted DoS at worst. Stream to disk instead. |
| L-2 | **Archive parsers cap entry size, not entry count.** npm/conda/nuget limit each metadata read (`4 MiB`, `maxManifestBytes`) but iterate unbounded tar entries. A pathological upload from a user with write access burns CPU. |
| L-3 | **`website/Dockerfile` runs as root** (trivy DS-0002). The main `Dockerfile` correctly uses `USER 1000`. |
| L-4 | **Presign handlers exist but are not mounted.** `PresignGet`/`PresignPut`/`ConfigureLifecycle` (`blobstores.go:277-361`) take an arbitrary blob key and are unreachable via `router.go` today. If they are ever wired up outside the `admin` group they become a straight RBAC bypass — any blob, any repository. Add the admin gate now, before someone mounts them. |
| L-5 | **Subdomain not character-validated.** `subdomain_rewriter.go:57` rejects dots and empties but not other characters before splicing the value into a URL path. RBAC still runs on the resulting repo name so there is no bypass — worth tightening to `[a-z0-9-]+` regardless. |
| L-6 | **`gitleaks` noise.** 210 hits, all `curl -u admin:admin123` examples in docs and test fixtures. Consider a `.gitleaksignore` so a real leak is not lost in the noise. |

---

## What is solid

Worth stating explicitly, because it is the majority of the code:

- **SQL injection: none.** Every value goes through `$N` placeholders; `fmt.Sprintf` is used only to assemble static column lists and JOIN clauses. No dynamic `ORDER BY`.
- **Path traversal: correctly defended.** `storage/local.go:27` cleans and then verifies containment under the blob root, with the reasoning in a comment.
- **JWT: correctly verified.** `auth/auth.go:83` asserts `*jwt.SigningMethodHMAC` — `alg: none` and algorithm-confusion both rejected.
- **Token storage:** SHA-256 over 32 bytes of `crypto/rand`, with a comment explaining why bcrypt is unnecessary here. Correct reasoning.
- **Passwords:** bcrypt cost 12.
- **MD5/SHA-1 usage** is confined to package-format checksums (Maven, npm, conda) where the protocols mandate them — never for security decisions.
- **Gzip-bomb guard** on backup import is explicit and tested.
- **RBAC** has a coherent shape: admin bypass, anonymous read-only, then per-privilege matching with path selectors.
- **No secret was ever committed** across 192 commits.
- **`/metrics` requires authentication** — a very common miss, handled here.

---

## Suggested order of work

1. **H-1** — a security control that does nothing is worse than an absent one. Smallest fix, largest correctness gain.
2. **H-3** — one line, and it restores the integrity of the audit log.
3. **H-2** — change the default; keep wildcard available but opt-in.
4. **M-1 + M-2** — brute-force surface; they are the same fix.
5. **M-3** — extend the existing fail-closed check by one condition.
6. **M-4**, **M-5** — larger; M-5 needs confirmation first.

---

## Tooling recommendation

Of the security plugins available in the official marketplace, the ones that fit this repo (self-contained, no SaaS account, no data leaving the machine):

| Plugin | Why |
|---|---|
| **`claude-security`** | Anthropic's own deep vulnerability scanner. Runs entirely inside the session at a chosen effort tier and adversarially challenges each finding before reporting — the closest thing to a repeat of this audit, on demand. Best single pick. |
| **`semgrep`** | Rule-based SAST, catches issues as code is written rather than after. Complements the above rather than duplicating it. |
| **`security-guidance`** | Preventive: warns on edits and reviews diffs at commit time. |

The SaaS-backed options (Aikido, Endor Labs, SonarQube, StackHawk, 42Crunch, NightVision) are all credible but each needs an account and sends code or findings off-machine — a deliberate trade-off worth making only if you want continuous monitoring rather than point-in-time review.

Already in CI and worth keeping: `govulncheck` (`.github/workflows/govulncheck.yml`), `golangci-lint`, `trivy`.

---

*No finding in this report was exploited — all are derived from reading the code and from tool output. M-5 in particular is marked as needing confirmation.*
