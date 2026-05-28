# Phase 28.1 — OIDC Single Logout (RP-initiated SLO)

**Date:** 2026-04-24
**Status:** approved
**Follows:** Phase 28 (OIDC SSO, complete)
**Precedes:** Phase 28.2 (refresh-token storage + silent renewal)

---

## Scope

RP-initiated Single Logout via `end_session_endpoint`. When an OIDC-authenticated user logs out of Nexspence, we redirect them to the IdP's logout endpoint so the IdP session is also terminated. Back-channel (IdP-initiated) logout is out of scope.

---

## Database

**Migration `007_oidc_id_token.sql`:**

```sql
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS oidc_id_token      TEXT,
  ADD COLUMN IF NOT EXISTS oidc_refresh_token TEXT;
```

- `oidc_id_token` — raw `id_token` string from the IdP. Overwritten on each `LoginOIDC`. NULL for local/LDAP users. Not encrypted (id_token is a signed-but-public JWT; it contains no secrets).
- `oidc_refresh_token` — NULL placeholder for Phase 28.2. Will be encrypted-at-rest when filled.

**New `UserRepo` methods (interface + postgres impl):**

```go
SetOIDCTokens(ctx context.Context, userID uuid.UUID, idToken, refreshToken string) error
GetOIDCIDToken(ctx context.Context, userID uuid.UUID) (string, error)
```

`SetOIDCTokens` with empty strings clears both columns (used on logout).

---

## Backend

### `UserService.LoginOIDC`

After successful token exchange, call:
```go
repo.SetOIDCTokens(ctx, user.ID, rawIDToken, "")
```
`rawIDToken` is already available as `oauth2Token.Extra("id_token").(string)`.

The JWT issued to the frontend gains an extra claim `auth_method: "oidc"` so the frontend can detect OIDC sessions without an extra API call. `GenerateToken` receives an optional `extraClaims map[string]any` parameter (nil for local/LDAP — no behaviour change for existing callers).

### New handler: `OIDCLogoutHandler`

**Route:** `GET /api/v1/auth/oidc/logout` — protected by `RequireAuth`.

**Logic:**

1. Read `userID` from gin context.
2. `GetOIDCIDToken(ctx, userID)` — if empty → `400 {"error":"not an OIDC session"}`.
3. Read `end_session_endpoint` from go-oidc provider discovery metadata.
   - If not present → return `200 {"logout_url":"<cfg.FrontendBaseURL>/login"}` (graceful fallback; not all IdPs publish this endpoint).
4. Build logout URL:
   ```
   <end_session_endpoint>
     ?id_token_hint=<idToken>
     &post_logout_redirect_uri=<cfg.FrontendBaseURL>/login
     &client_id=<cfg.ClientID>
   ```
5. `SetOIDCTokens(ctx, userID, "", "")` — clear stored token.
6. Return `200 {"logout_url":"<url>"}`.

**Why JSON not 302:** the frontend is a Vite SPA on a potentially different origin. Browsers do not follow `302` responses to `fetch()` calls in a useful way. The pattern `fetch → get URL → window.location.href = url` is already established in `OIDCCallbackPage`.

**Router registration (`router.go`):** under existing `oidcEnabled` guard, alongside `Login` and `Callback`.

---

## Frontend

### `authStore.ts`

New getter:
```ts
isOIDC(): boolean
```
Decodes the JWT from localStorage (same pattern as `isAdmin()`), returns `payload.auth_method === "oidc"`.

### `Layout.tsx` — logout branch

```ts
if (authStore.isOIDC()) {
  const { logout_url } = await api.get('/api/v1/auth/oidc/logout').then(r => r.data)
  window.location.href = logout_url
} else {
  // existing local/LDAP logout path
}
```

Clears authStore in the existing logout path; for OIDC the IdP redirects back to `/login` where `OIDCCallbackPage`-style cleanup is not needed (token was never issued by the IdP on the return trip).

---

## Tests

| Test | Asserts |
|------|---------|
| `TestOIDCLogout_NonOIDCUser` | `GetOIDCIDToken` returns `""` → handler returns `400` |
| `TestOIDCLogout_NoEndSessionEndpoint` | provider metadata has no `end_session_endpoint` → `200 {"logout_url":".../login"}` |
| `TestOIDCLogout_Success` | response `logout_url` contains `id_token_hint`, `post_logout_redirect_uri`, `client_id` |
| `TestOIDCLogout_ClearsToken` | after handler call, `GetOIDCIDToken` returns `""` |
| `TestSetOIDCTokens` | repo sets and clears both columns |
| `TestGetOIDCIDToken` | repo returns stored value |

Total: ~6 new tests. Running count: 315 → ~321.

---

## Implementation order

1. Migration `007_oidc_id_token.sql`
2. `UserRepo` interface + postgres impl (`SetOIDCTokens`, `GetOIDCIDToken`)
3. `GenerateToken` extra-claims parameter + `LoginOIDC` wires `SetOIDCTokens` + `auth_method` claim
4. `OIDCLogoutHandler` + route registration
5. `authStore.isOIDC()` + `Layout.tsx` logout branch
6. Tests

---

## Out of scope

- Back-channel (IdP-initiated) logout
- JWT blacklist / server-side token revocation
- `oidc_refresh_token` encryption (Phase 28.2)
- Multi-provider (Phase 28.3)
