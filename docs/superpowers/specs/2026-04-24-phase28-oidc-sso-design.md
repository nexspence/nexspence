# Phase 28 — OIDC / OAuth2 SSO

**Status:** design approved 2026-04-24, ready for implementation plan
**Goal:** First-class OIDC / OAuth2 Single Sign-On in Nexspence. One IdP per deployment (Keycloak, Google Workspace, Microsoft Entra ID, Okta, or any OIDC-compliant provider). Coexists with existing local + LDAP auth.

## 1. Design decisions (answered)

| # | Decision | Chosen |
|---|---|---|
| 1 | Provider support model | **Single provider** — one `oidc:` block in `config.yaml`. Multi-provider deferred (Phase 28.3). |
| 2 | User provisioning | **Hybrid** — `oidc.provisioning: jit \| allowlist \| manual`, default `jit` (mirrors LDAP's implicit JIT). |
| 3 | Role / group mapping | **A+C hybrid** — `admin_group` (one-shot nx-admin grant, LDAP-parallel) + optional `role_mappings` table mapping claim values to Nexspence role names. Claim name configurable (`groups_claim`, default `"groups"`). |
| 4 | Coexistence with local / LDAP | **All three parallel**, routed by `user.Source`. Username conflict (user exists with different Source) → 409. Bootstrap-admin (`local`) always available for DR. |
| 5 | UI login flow | **Button under form** — "Sign in with {DisplayName}". Local form stays. Backend exposes `GET /api/v1/auth/config` for feature detection. |
| 6 | Session / token handling | **Issue our own JWT**, ignore OIDC refresh_token. Logout = clear localStorage (as today). Single Logout deferred (Phase 28.1). |

## 2. Architecture

### 2.1 File layout

Mirrors existing LDAP code path. New files in **bold**, edits in *italic*:

```
internal/
├── auth/
│   ├── ldap.go
│   └── **oidc.go**                   — OIDCAuthenticator interface + OIDCService impl
├── config/
│   └── *config.go*                   — add OIDCConfig struct + validation
├── domain/
│   └── *types.go*                    — add UserSourceOIDC constant
├── service/
│   ├── *user_service.go*             — WithOIDC() builder, LoginOIDC(), syncOIDCRoles()
│   ├── *errors.go*                   — ErrProvisioningRejected, ErrProvisioningConflict
│   └── **user_service_oidc_test.go**
├── api/
│   ├── handlers/
│   │   ├── **oidc.go**               — OIDCHandler.Login, Callback
│   │   ├── **oidc_test.go**
│   │   └── *auth.go*                 — new Config() endpoint
│   └── *router.go*                   — wire /api/v1/auth/oidc/{login,callback,config}
└── cmd/server/*main.go*              — bootstrap OIDCService if enabled

frontend/src/
├── pages/
│   ├── *LoginPage.tsx*               — "Sign in with X" button, oidc_error banner
│   └── **OIDCCallbackPage.tsx**      — reads #token=… fragment, hydrates authStore
├── api/*client.ts*                   — getAuthConfig() helper
└── *App.tsx*                         — add /oidc/callback route

config.yaml                           — add oidc: block (disabled by default)
go.mod                                — add github.com/coreos/go-oidc/v3, golang.org/x/oauth2
docs/**oidc-setup.md**                — Keycloak + Google + Entra + Okta presets
```

### 2.2 Data flow (authorization code + PKCE)

```
User → Frontend: click "Sign in with Keycloak"
Frontend → Backend: GET /api/v1/auth/oidc/login?return_to=/repos
Backend: generate state + nonce + code_verifier
       + seal {state, nonce, code_verifier, return_to, iat} into "oidc_state" cookie
         (AES-256-GCM, httpOnly, Secure, SameSite=Lax, maxAge=600s)
       → 302 {issuer}/authorize?client_id=…&redirect_uri=…&state=…
                                  &code_challenge=base64url(sha256(verifier))&nonce=…

User authenticates at IdP ↓

IdP → Backend: GET /api/v1/auth/oidc/callback?code=…&state=…
Backend: 
  1. Read + delete oidc_state cookie (one-shot)
  2. CSRF check: state(query) == state(cookie)
  3. Handle ?error= from IdP
  4. OIDCService.ExchangeAndVerify(code, code_verifier, nonce)
       → validates id_token sig / iss / aud / exp / nonce
       → returns normalized OIDCClaims
  5. UserService.LoginOIDC(claims)
       → provisioning check (jit/allowlist/manual)
       → user upsert (source=oidc)
       → syncOIDCRoles — REPLACE user's roles with resolved set (IdP = source of truth)
       → generate Nexspence JWT
  6. Audit hook: c.Set("username", user.Username); c.Set("audit_source", "oidc")
     → AuditMiddleware writes LOGIN event with source in Context
  7. 302 {frontend_base_url}/oidc/callback#token=<JWT>&return_to=<sanitized>

Frontend /oidc/callback:
  1. Parse fragment (#token=…&return_to=…)
  2. localStorage.setItem('nexspence_token', token)
  3. history.replaceState(null, '', return_to)   — scrub token from URL
  4. authStore.init()                             — fetches /api/v1/me
  5. navigate(return_to, { replace: true })
```

Error path: any step 1-5 failure on backend → `302 {frontend_base_url}/login?oidc_error=<reason>`. Reasons are human-readable but non-leaky (no "wrong signature"; just "verification failed"). Precise cause is logged server-side.

### 2.3 Key security properties

- **Fragment-based token delivery** — token in `#…` part of URL, never sent to server, never in Referer, never in access logs. Frontend reads and immediately scrubs via `history.replaceState`.
- **httpOnly + Secure + SameSite=Lax** state cookie — Lax (not Strict) is required because callback arrives as cross-site top-level navigation from IdP. One-shot: read + clear in the same handler.
- **AEAD-sealed cookie payload** — AES-256-GCM (stdlib `crypto/cipher`). Prevents tampering (swap state, extend TTL) even though data itself isn't secret.
- **PKCE S256** — mandatory for all flows, prevents auth code interception in the redirect chain.
- **10-minute state TTL** — balance between "user reads the IdP consent screen" and attack window.
- **Return-path sanitization** — `isSafeReturnPath`: must be absolute path (`/repos`), not URL with host, not `//evil`, not `javascript:`, `data:`, length ≤ 200. Rejected value falls back to `/`.
- **No leaky error messages** — backend logs precise cause; frontend sees only "verification failed" / "provisioning rejected" / "state mismatch".

## 3. Interfaces + types

```go
// internal/auth/oidc.go

type OIDCClaims struct {
    Subject   string // sub — stable IdP user identifier
    Username  string // from config.UsernameClaim (default "preferred_username")
    Email     string
    Name      string
    FirstName string
    LastName  string
    Groups    []string // from config.GroupsClaim — empty if claim missing
    Raw       map[string]any
}

type OIDCAuthenticator interface {
    AuthCodeURL(state, nonce, codeChallenge string) string
    ExchangeAndVerify(ctx context.Context, code, codeVerifier, expectedNonce string) (*OIDCClaims, error)
    TestConnection(ctx context.Context) error
}

type OIDCService struct {
    cfg      config.OIDCConfig
    provider *oidc.Provider        // go-oidc discovery result
    verifier *oidc.IDTokenVerifier // JWKS-based id_token verifier
    oauth    *oauth2.Config
}

func NewOIDCService(ctx context.Context, cfg config.OIDCConfig) (*OIDCService, error)
```

**Sentinel errors** (in `internal/service/errors.go`):

```go
var (
    ErrProvisioningRejected = errors.New("oidc provisioning rejected") // → 403
    ErrProvisioningConflict = errors.New("user source conflict")       // → 409
)
```

## 4. Config schema

```yaml
oidc:
  enabled: false
  display_name: "Keycloak"
  issuer: "https://kc.example.com/realms/nexspence"   # discovery URL
  client_id: "nexspence"
  client_secret: "${OIDC_CLIENT_SECRET}"              # Viper env substitution
  redirect_url: "https://nexspence.example.com/api/v1/auth/oidc/callback"
  frontend_base_url: "https://nexspence.example.com"  # post-callback redirect target
  scopes: ["openid", "profile", "email", "groups"]

  # Provisioning (Q2)
  provisioning: "jit"               # jit | allowlist | manual
  email_allowlist: []               # required if provisioning=allowlist; glob (path.Match semantics)

  # Role resolution (Q3)
  groups_claim: "groups"            # OIDC claim holding user's groups/roles
  admin_group: ""                   # optional; if set and present in claim → nx-admin
  role_mappings: {}                 # optional; claim value → Nexspence role name

  # Claim name overrides (provider-specific tweaks)
  username_claim: "preferred_username"  # Google: use "email"; Entra: "preferred_username" works
  email_claim: "email"
  name_claim: "name"

  # Security
  show_login_button: true           # false hides UI only; callback endpoint remains
  cookie_secure: true               # set false for local HTTP dev only
  cookie_key: "${OIDC_COOKIE_KEY}"  # base64-encoded 32 bytes; `make oidc-secret` generates
  allowed_skew_seconds: 60
```

### 4.1 Validation (on config load)

- `enabled=true` → `issuer`, `client_id`, `client_secret`, `redirect_url`, `frontend_base_url`, `cookie_key` required.
- `provisioning="allowlist"` → `email_allowlist` non-empty.
- `cookie_key` must be valid base64 decoding to 32 bytes.
- `cookie_secure=false` + `redirect_url` starts with `https://` → warn (mis-config).
- Role-mapping value validity — **runtime**, not startup, because roles may not exist at boot.

## 5. Login / Callback handlers

Full pseudocode was reviewed; key rules:

**`Login`:**
- Generate cryptographic state/nonce/code_verifier (256+ bits each, `crypto/rand`).
- Seal into `oidc_state` cookie with AEAD.
- Redirect to `provider.AuthCodeURL(...)` with PKCE S256 challenge.

**`Callback`:**
- Read + immediately clear `oidc_state` cookie (one-shot).
- Reject on: missing cookie, un-openable payload, age > 10m, state mismatch, IdP `?error=`, verification failure, provisioning rejection, source conflict.
- On success: redirect to `{frontend_base_url}/oidc/callback#token=<JWT>&return_to=<path>`.
- On failure: redirect to `{frontend_base_url}/login?oidc_error=<reason>`. Never 5xx (user doesn't need to see stack traces).

**`GET /api/v1/auth/config`** (public, unauthenticated):

```json
{
  "oidcEnabled": true,
  "oidcDisplayName": "Keycloak",
  "oidcLoginUrl": "/api/v1/auth/oidc/login",
  "ldapEnabled": false
}
```

## 6. UserService.LoginOIDC contract

```go
func (s *UserService) LoginOIDC(ctx context.Context, claims *auth.OIDCClaims) (string, *domain.User, error)
```

1. Validate claims have username + email → else `ErrInvalidInput`.
2. Lowercase-normalize username (mirror LDAP).
3. `users.Get(username)`:
   - Exists, `Source != oidc` → `ErrProvisioningConflict`.
   - Exists, `Source == oidc` → update profile fields (email / name) from claims (IdP is source of truth).
   - Does not exist → check `checkProvisioning(email)`. On reject → `ErrProvisioningRejected`. On pass → `Create(source=oidc, status=active)`.
4. `syncOIDCRoles(userID, claims.Groups)`:
   - Collect target role names: `{admin_group match → nx-admin} ∪ {role_mappings[g] for g in groups}`.
   - Resolve names → IDs via `RoleRepo.List`. Missing role in DB → warn-log, skip.
   - `RoleRepo.SetUserRoles(userID, targetIDs)` — **REPLACE**, not merge. Zero-target → user ends with no roles.
5. Reload roles from DB (same pattern as `loginLDAP` — `SetUserRoles` doesn't update the in-memory user).
6. If `user.Status != active` → `ErrInvalidInput`.
7. `auth.GenerateToken(user.ID, user.Username, user.Roles)` → return JWT.
8. `UpdateLastLogin(username)`.

### 6.1 Provisioning switch

```go
switch cfg.Provisioning {
case "jit", "":   return nil
case "allowlist": return matchAny(cfg.EmailAllowlist, email) else ErrProvisioningRejected
case "manual":    return ErrProvisioningRejected                 // existing user check done earlier
default:          return ErrInvalidInput
}
```

`matchAny` uses `path.Match` for glob semantics (`*@company.com`, `alice@*.io`). No regex.

### 6.2 Group/CN matching

Reuses existing `ldapGroupMatch(claim, configured)`:
- Case-insensitive.
- Strips DN/CN prefix: `CN=nexspence-admins,OU=Groups,DC=example,DC=com` → `nexspence-admins`.
- Direct string equality after normalization.

Entra and AD often emit DN-style group claims; this keeps admins from surprise mismatches.

## 7. Frontend integration

### 7.1 `LoginPage.tsx` (edit)

- `useEffect` fetches `nexspenceApi.getAuthConfig()` on mount.
- If `authConfig.oidcEnabled`: render divider + "Sign in with {displayName}" button below the password form. Clicking → `window.location.href = oidcLoginUrl + '?return_to=' + encodeURIComponent(...)`.
- Reads `?oidc_error=...` from URL on mount → shows red banner.

### 7.2 `OIDCCallbackPage.tsx` (new)

- `useEffect`:
  1. Parse `window.location.hash.slice(1)` as URLSearchParams.
  2. If no token → `navigate('/login?oidc_error=missing+token', { replace: true })`.
  3. `localStorage.setItem('nexspence_token', token)`.
  4. `history.replaceState(null, '', returnTo || '/')` — scrub URL.
  5. `authStore.init()` → navigate to `returnTo`.
- Renders `<div>Finishing sign-in…</div>` while waiting.

### 7.3 `App.tsx`

```tsx
<Route path="/oidc/callback" element={<OIDCCallbackPage />} />  {/* public, no PrivateRoute */}
```

### 7.4 `client.ts`

```ts
export interface AuthConfig {
  oidcEnabled: boolean
  oidcDisplayName: string
  oidcLoginUrl: string
  ldapEnabled: boolean
}
// Added: nexspenceApi.getAuthConfig() → /api/v1/auth/config
```

## 8. Testing strategy

### 8.1 Unit — `internal/auth/oidc_test.go`

Fake IdP on `httptest.Server` exposing `/.well-known/openid-configuration`, `/token`, `/jwks`. RS256 key-pair generated per test; fake issues id_tokens signed with it.

- `TestOIDCService_HappyPath`
- `TestOIDCService_NonceMismatch_ReturnsError`
- `TestOIDCService_ExpiredToken_ReturnsError`
- `TestOIDCService_WrongAudience_ReturnsError`
- `TestOIDCService_WrongSignature_ReturnsError`
- `TestOIDCService_ClaimCustomization` — `username_claim: "email"` → email becomes username.

### 8.2 Unit — `internal/service/user_service_oidc_test.go`

Mock `OIDCAuthenticator` via table-driven tests.

- `TestLoginOIDC_NewUser_JIT_AutoCreates_ResolvesRoles`
- `TestLoginOIDC_NewUser_Allowlist_EmailMatch_Created`
- `TestLoginOIDC_NewUser_Allowlist_EmailMiss_Rejected` → `ErrProvisioningRejected`
- `TestLoginOIDC_NewUser_Manual_AlwaysRejected`
- `TestLoginOIDC_ExistingUser_SourceMismatch_Rejected` → `ErrProvisioningConflict`
- `TestLoginOIDC_ExistingUser_SyncRoles_Replaces` — admin claim absent now → role removed.
- `TestLoginOIDC_MissingRoleInDB_Warns_NoFail`
- `TestLoginOIDC_InactiveUser_Rejected`
- `TestLoginOIDC_EmptyClaims_Rejected`
- `TestResolveOIDCRoles_AdminGroupDNFormat` — `CN=admins,OU=...` matches `admins`.

### 8.3 Handler — `internal/api/handlers/oidc_test.go`

- `TestOIDCHandler_Login_SetsStateCookie_And_Redirects` — verifies Location, cookie attrs (httpOnly, Secure, SameSite=Lax, maxAge).
- `TestOIDCHandler_Callback_StateMismatch_Redirects_WithError`
- `TestOIDCHandler_Callback_ExpiredState_Redirects`
- `TestOIDCHandler_Callback_IdPError_Redirects` — `?error=access_denied`.
- `TestOIDCHandler_Callback_HappyPath_Redirects_WithToken` — mock authenticator → Location contains `#token=`.
- `TestOIDCHandler_Callback_ProvisioningRejected_Redirects_WithForbiddenErrorMsg`
- `TestIsSafeReturnPath` — table: `/`, `/foo`, `//evil.com`, `http://evil.com`, `javascript:…`, long-string → expected verdicts.

### 8.4 Config — `internal/config/config_test.go` (edit)

- `TestConfig_OIDC_MissingIssuer_Fails_WhenEnabled`
- `TestConfig_OIDC_Allowlist_EmptyList_Fails`
- `TestConfig_OIDC_Secret_EnvSubstitution_Works`
- `TestConfig_OIDC_CookieKey_InvalidBase64_Fails`

### 8.5 Frontend

Skipped in Phase 28 — no Vitest infrastructure in project today. Manual verification below. Follow-up phase will set up Vitest.

### 8.6 Manual verification checklist

- [ ] Keycloak (docker-compose override in `docs/oidc-setup.md`):
  - [ ] Fresh user → JIT create → `nx-admin` via `admin_group`.
  - [ ] User without `admin_group` → login succeeds, no roles, anon-repos only.
  - [ ] `role_mappings: { "developers": "release-manager" }` → role granted.
  - [ ] Remove `developers` claim → next login removes role (REPLACE semantics).
  - [ ] `provisioning: manual` + no pre-created user → redirect with `?oidc_error=...`.
  - [ ] Existing `source=local` user conflict → 409 / error banner.
  - [ ] `cookie_secure=true` over HTTP in dev → cookie not set → graceful failure.
- [ ] Google Workspace — `username_claim: "email"`, allowlist works.
- [ ] Audit log — LOGIN entries have `source: oidc` in `AuditEvent.Context`. Implementation: in `OIDCHandler.Callback`, after successful `LoginOIDC`, call `c.Set("audit_source", "oidc")` alongside `c.Set("username", ...)`. `AuditMiddleware` then merges the `audit_source` gin-context key into `Context["source"]` (fallback "local" for the existing local/LDAP `AuthHandler.Login` path for backward compat — LDAP sets "ldap" via the same key).

## 9. Deliverables

| # | Artifact | LOC estimate |
|---|---|---|
| 1 | `internal/auth/oidc.go` + test | 200 + 250 |
| 2 | `internal/service/user_service.go` (edit) + new test file | 150 + 300 |
| 3 | `internal/api/handlers/oidc.go` + test | 180 + 250 |
| 4 | `internal/api/handlers/auth.go` (`Config` endpoint) | +30 |
| 5 | `internal/config/config.go` (`OIDCConfig` + validation) | +80 |
| 6 | `internal/domain/types.go` (`UserSourceOIDC`) | +3 |
| 7 | `internal/api/router.go` (routes + wiring; `AuditMiddleware` must wrap `/api/v1/auth/oidc/login` + `/callback` so LOGIN events are recorded) | +15 |
| 8 | `cmd/server/main.go` (bootstrap OIDCService) | +20 |
| 9 | `config.yaml` (default `oidc:` block, disabled) | +25 |
| 10 | `frontend/src/pages/LoginPage.tsx` (edit) | +40 |
| 11 | `frontend/src/pages/OIDCCallbackPage.tsx` (new) | ~50 |
| 12 | `frontend/src/App.tsx` + `client.ts` (edits) | +15 |
| 13 | `docs/oidc-setup.md` (Keycloak + Google + Entra + Okta presets) | ~300 lines MD |
| 14 | `go.mod` — `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2` | dep add |
| 15 | `Makefile` — `oidc-secret` target (32-byte base64 cookie key) | +5 |

**Backend:** ~1 300 prod + ~800 test LOC. **Frontend:** ~105 LOC. **Docs:** 1 page.

## 10. Out of scope (follow-up phases)

| Deferred item | Phase | Reason |
|---|---|---|
| Single Logout (SLO) via `end_session_endpoint` | 28.1 | Incremental; ~50 LOC. Safe to ship Phase 28 without — 24h JWT TTL bounds orphan-session risk. |
| Refresh-token storage + silent session renewal | 28.2 | Requires encryption-at-rest, rotation, reuse detection. Symmetric to LDAP's "just a JWT" is fine first. |
| Multi-provider (N IdPs) | 28.3 | Rarely-needed; interface `OIDCAuthenticator` future-proof. |
| SAML 2.0 | 29 | Different flow model; fresh phase. |
| Frontend Vitest setup | separate tooling phase | Orthogonal to OIDC feature. |

## 11. Open questions

None at spec-approval time. Reviewer should flag anything ambiguous below.

## 12. Risks / unknowns

- **Google groups not in id_token by default** — documented in `oidc-setup.md`. Users on Google Workspace without Admin-SDK integration should set `groups_claim: ""` and rely on `provisioning: allowlist` + manual role assignment.
- **Entra app-roles vs groups** — Entra emits app-registration app-roles under `roles` claim by default; group membership requires extra config. `oidc-setup.md` documents both paths.
- **Clock skew on id_token validation** — `allowed_skew_seconds: 60` default. Infrastructure with broken NTP will fail silently-on-login; check logs.
- **Username collisions on JIT** — IdP guarantees `sub` uniqueness but not `preferred_username`. Two IdPs (if we ever go multi-provider) could emit same `preferred_username` for different humans. Not relevant for Phase 28 (single provider), but noted.
