# Phase 52: SAML 2.0 Authentication — Design Spec

**Date:** 2026-05-07  
**Status:** Approved  
**Library:** `github.com/crewjam/saml`

---

## Goal

Add SAML 2.0 SSO alongside the existing OIDC implementation (Phase 28) to support organizations using ADFS, Azure AD (legacy SAML), and Okta SAML.

---

## Authentication Flow

```
User → "Sign in with SAML" → GET /api/v1/auth/saml/login
     → SAMLHandler.Login: builds AuthnRequest, redirects to IdP

IdP authenticates → POST /api/v1/auth/saml/acs (ACS endpoint)
     → SAMLHandler.ACS: validates XML assertion (signature + expiry)
     → UserService.LoginSAML(): jit-provisioning, role mapping
     → redirect to /saml/callback#token=<jwt>

Frontend /saml/callback: reads #token from hash, stores in localStorage, redirects to /

SP Metadata: GET /api/v1/auth/saml/metadata → XML (public, no auth required)
```

**RelayState**: HMAC-SHA256 signed JSON `{"return_to": "/..."}`. Protects against open redirects without requiring a cookie sealer (simpler than OIDC state).

**SP key pair**: If `saml.sp_cert_pem` / `saml.sp_key_pem` are not set → ephemeral RSA-2048 generated at startup (logged as warning). For production, admin sets explicit PEM strings in config.

---

## Config

New `SAMLConfig` struct in `internal/config/config.go`, new block in `config.yaml`:

```yaml
saml:
  enabled: false
  display_name: "SAML SSO"
  show_login_button: true
  idp_metadata_url: ""          # URL to fetch IdP metadata XML
  idp_metadata_xml: ""          # alternative: paste XML directly
  sp_entity_id: ""              # e.g. "https://nexspence.example.com/saml"
  acs_url: ""                   # e.g. "https://nexspence.example.com/api/v1/auth/saml/acs"
  sp_cert_pem: ""               # PEM X.509 cert (ephemeral if empty)
  sp_key_pem: ""                # PEM RSA private key
  provisioning: "jit"           # jit | allowlist | manual
  email_allowlist: []           # glob patterns, active when provisioning=allowlist
  groups_attribute: "groups"    # SAML attribute name for groups
  email_attribute: "email"
  username_attribute: "uid"     # falls back to NameID if attribute absent
  name_attribute: "displayName"
  admin_group: ""               # members get nx-admin role
  role_mappings: {}             # {"saml-group-value": "nexspence-role-name"}
  hmac_key: ""                  # base64 32 bytes for RelayState signing (auto-gen if empty)
```

`ValidateSAML(cfg SAMLConfig) error` — mirrors `ValidateOIDC`: requires `sp_entity_id` and `acs_url` when `enabled=true`.

Defaults registered in `setDefaults()`:
- `saml.enabled` → false
- `saml.display_name` → "SAML SSO"
- `saml.provisioning` → "jit"
- `saml.groups_attribute` → "groups"
- `saml.show_login_button` → true

---

## Backend

### `internal/auth/saml.go`

```go
type SAMLClaims struct {
    Subject  string
    Email    string
    Username string
    Name     string
    Groups   []string
    RawAttrs map[string][]string
}

type SAMLAuthenticator interface {
    MetadataXML() ([]byte, error)
    MakeAuthnRequest(returnTo string) (redirectURL string, relayState string, err error)
    // Returns claims, returnTo, error
    ParseResponse(r *http.Request, relayState string) (*SAMLClaims, string, error)
}

type SAMLService struct {
    sp  crewjam.ServiceProvider
    cfg config.SAMLConfig
}
```

`NewSAMLService(cfg SAMLConfig) (*SAMLService, error)`:
1. Load or generate RSA key pair
2. Fetch IdP metadata from `idp_metadata_url` or parse `idp_metadata_xml`
3. Construct `crewjam.ServiceProvider`

### `internal/service/user_service.go`

Two new methods (mirrors OIDC pattern exactly):

```go
func (s *UserService) WithSAML(a auth.SAMLAuthenticator, cfg config.SAMLConfig)
func (s *UserService) LoginSAML(ctx context.Context, claims *auth.SAMLClaims) (*domain.User, string, error)
```

`LoginSAML` flow:
1. Provisioning check (jit / allowlist / manual)
2. Upsert user (normalize username to lowercase)
3. `syncSAMLRoles`: apply `admin_group` → nx-admin, `role_mappings` claim → role name (REPLACE semantics — IdP is source of truth)
4. Reload roles from DB (same critical pattern as LDAP/OIDC)
5. `GenerateToken` → return JWT

### `internal/api/handlers/saml.go`

`SAMLHandler` with three endpoints:

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/api/v1/auth/saml/metadata` | public | SP metadata XML |
| GET | `/api/v1/auth/saml/login` | public | Redirect to IdP |
| POST | `/api/v1/auth/saml/acs` | public | ACS — parse assertion, issue JWT |

ACS success: `302` to `/saml/callback#token=<jwt>`  
ACS error: `302` to `/login?saml_error=<message>`

### `internal/api/handlers/auth.go`

`GetAuthConfig` response extended with:
```json
{
  "samlEnabled": true,
  "samlLoginUrl": "/api/v1/auth/saml/login",
  "samlDisplayName": "SAML SSO"
}
```

### `internal/api/router.go`

New block mirrors OIDC block (lines ~106–124):
```go
var samlSvc auth.SAMLAuthenticator
if cfg.SAML.Enabled {
    svc, err := auth.NewSAMLService(cfg.SAML)
    if err != nil { panic("saml init: " + err.Error()) }
    samlSvc = svc
    userSvc.WithSAML(samlSvc, cfg.SAML)
}
// ...
if samlSvc != nil {
    samlH := handlers.NewSAMLHandler(samlSvc, userSvc)
    r.GET("/api/v1/auth/saml/metadata", samlH.Metadata)
    r.GET("/api/v1/auth/saml/login",    samlH.Login)
    r.POST("/api/v1/auth/saml/acs",     samlH.ACS)
}
```

### `internal/api/handlers/system.go`

`GET /api/v1/system/services` — add Redis entry when `redis.enabled=true`:
```json
{
  "name": "Redis",
  "address": "redis:6379",
  "status": "connected" | "not_configured"
}
```

---

## Frontend

### `frontend/src/pages/LoginPage.tsx`

Add SAML button alongside OIDC button:
```tsx
{authConfig?.samlEnabled && (
  <button onClick={handleSAML}>
    Sign in with {authConfig.samlDisplayName}
  </button>
)}
```
`handleSAML` → `window.location.href = authConfig.samlLoginUrl`

`saml_error` URL param handling (mirrors `oidc_error`).

### `frontend/src/pages/SAMLCallbackPage.tsx`

New file, structurally identical to `OIDCCallbackPage.tsx`:
- Reads `location.hash` for `#token=`
- Stores in `authStore`
- Redirects to `/`
- On error → `/login?saml_error=...`

Registered in router as `/saml/callback`.

### `frontend/src/pages/AdminPage.tsx`

New "SAML" tab in System section (HoloCard style, read-only):

```
● SAML SSO: Enabled / Disabled
  Display Name:  SAML SSO
  SP Entity ID:  https://nexspence.example.com/saml
  ACS URL:       https://nexspence.example.com/api/v1/auth/saml/acs
  IdP Metadata:  https://idp.example.com/metadata
  Provisioning:  jit
  [Download SP Metadata XML]   ← direct link to /api/v1/auth/saml/metadata

● Redis: Enabled / Disabled
  Address: redis:6379
  Status:  ✓ Connected / ✗ Not configured
```

Redis info pulled from `GET /api/v1/system/services`.

---

## Testing

### `internal/auth/saml_test.go`
- `TestSAMLService_MetadataXML` — valid XML, contains EntityID and ACS URL
- `TestSAMLService_MakeAuthnRequest` — redirect URL contains SAMLRequest param, RelayState is signed
- `TestSAMLService_ParseResponse_InvalidSignature` — returns error

### `internal/service/user_service_saml_test.go`
- `TestLoginSAML_JIT_NewUser` — creates user on first login
- `TestLoginSAML_JIT_ExistingUser` — updates roles on subsequent login
- `TestLoginSAML_Allowlist_Blocked` — rejects email not matching allowlist
- `TestLoginSAML_AdminGroup_AssignsRole` — admin_group membership → nx-admin
- `TestLoginSAML_RoleMappings` — saml group → nexspence role via role_mappings

### `internal/api/handlers/saml_test.go`
- `TestSAMLHandler_Metadata_ReturnsXML`
- `TestSAMLHandler_Login_RedirectsToIdP`
- `TestSAMLHandler_ACS_ValidAssertion_ReturnsToken` (mock SAMLAuthenticator)
- `TestSAMLHandler_ACS_InvalidAssertion_Returns400`
- `TestSAMLHandler_ACS_AllowlistBlocked_RedirectsWithError`

### `testutil/mocks.go`
Add `MockSAMLAuthenticator` implementing `SAMLAuthenticator` interface.

---

## Files Changed

| File | Change |
|------|--------|
| `go.mod` / `go.sum` | add `github.com/crewjam/saml` |
| `config.yaml` | add `saml:` block |
| `internal/config/config.go` | `SAMLConfig`, `ValidateSAML`, defaults |
| `internal/auth/saml.go` | new — `SAMLAuthenticator` + `SAMLService` |
| `internal/auth/saml_test.go` | new — unit tests |
| `internal/service/user_service.go` | `WithSAML`, `LoginSAML`, `syncSAMLRoles` |
| `internal/service/user_service_saml_test.go` | new — service tests |
| `internal/api/handlers/saml.go` | new — `SAMLHandler` |
| `internal/api/handlers/saml_test.go` | new — handler tests |
| `internal/api/handlers/auth.go` | extend `GetAuthConfig` response |
| `internal/api/handlers/system.go` | add Redis to services response |
| `internal/api/router.go` | wire SAML routes |
| `testutil/mocks.go` | `MockSAMLAuthenticator` |
| `frontend/src/pages/LoginPage.tsx` | SAML button + error handling |
| `frontend/src/pages/SAMLCallbackPage.tsx` | new |
| `frontend/src/App.tsx` | register `/saml/callback` route |
| `frontend/src/pages/AdminPage.tsx` | SAML tab + Redis card |
| `frontend/src/api/client.ts` | `samlEnabled`/`samlLoginUrl`/`samlDisplayName` in auth config type |
