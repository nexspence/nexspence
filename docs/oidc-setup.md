# OIDC / OAuth2 SSO setup

Nexspence supports any OIDC-compliant Identity Provider. This guide covers
the four most common: Keycloak, Google Workspace, Microsoft Entra ID, Okta.

## Common steps

1. Register Nexspence as an **application** / **client** on your IdP.
2. Set the **redirect URI** to `https://<nexspence-host>/api/v1/auth/oidc/callback`.
3. Generate a client secret.
4. Generate a 32-byte cookie key: `make oidc-secret`.
5. Export the secrets as env vars (recommended over config-file in cleartext):
   ```bash
   export NEXSPENCE_OIDC_CLIENT_SECRET="<your-client-secret>"
   export NEXSPENCE_OIDC_COOKIE_KEY="<output-of-make-oidc-secret>"
   ```
6. Edit `config.yaml` `oidc:` block and set `enabled: true` plus provider-
   specific fields (see presets below).
7. Restart Nexspence. Startup log should show `oidc discovery OK`.

## Keycloak

```yaml
oidc:
  enabled: true
  display_name: "Keycloak"
  issuer: "https://kc.example.com/realms/nexspence"
  client_id: "nexspence"
  client_secret: "${OIDC_CLIENT_SECRET}"
  redirect_url: "https://nexspence.example.com/api/v1/auth/oidc/callback"
  frontend_base_url: "https://nexspence.example.com"
  scopes: ["openid", "profile", "email", "groups"]
  groups_claim: "groups"
  admin_group: "nexspence-admins"
  cookie_key: "${OIDC_COOKIE_KEY}"
```

**Keycloak realm config:**

- Clients → **Create Client** → OpenID Connect, client ID `nexspence`,
  **Client authentication** ON (confidential).
- Valid Redirect URIs: `https://nexspence.example.com/api/v1/auth/oidc/callback`.
- Client Scopes → `groups` → **Create mapper** → type: Group Membership,
  Token Claim Name: `groups`, **Full group path: OFF**. Assign to the
  `nexspence` client's default scopes.
- Groups → create `nexspence-admins` → add admin users.

## Google Workspace

Google **does not emit group membership in the id_token** by default. Use
one of:

- **Allowlist mode** (simplest) — gate access by email domain, assign roles
  manually in Nexspence Security → Roles.
- **Admin SDK integration** — out of Phase 28 scope.

```yaml
oidc:
  enabled: true
  display_name: "Google"
  issuer: "https://accounts.google.com"
  client_id: "<app>.apps.googleusercontent.com"
  client_secret: "${OIDC_CLIENT_SECRET}"
  redirect_url: "https://nexspence.example.com/api/v1/auth/oidc/callback"
  frontend_base_url: "https://nexspence.example.com"
  scopes: ["openid", "profile", "email"]

  provisioning: "allowlist"
  email_allowlist: ["*@company.com"]

  username_claim: "email"   # Google has no preferred_username
  groups_claim: ""          # no group claim without Admin SDK
  cookie_key: "${OIDC_COOKIE_KEY}"
```

**Google Cloud Console:** APIs & Services → Credentials → Create OAuth
Client ID → Web application → Authorized redirect URIs.

## Microsoft Entra ID (Azure AD)

Entra emits app-role claims under `roles`, not `groups`. Configure **App
Registration** → **Expose an API** → **App roles**, assign users.

```yaml
oidc:
  enabled: true
  display_name: "Microsoft"
  issuer: "https://login.microsoftonline.com/<tenant-id>/v2.0"
  client_id: "<app-registration-client-id>"
  client_secret: "${OIDC_CLIENT_SECRET}"
  redirect_url: "https://nexspence.example.com/api/v1/auth/oidc/callback"
  frontend_base_url: "https://nexspence.example.com"
  scopes: ["openid", "profile", "email"]

  groups_claim: "roles"     # Entra app-roles → claim
  role_mappings:
    "NexspenceAdmin": "nx-admin"
    "NexspenceDev":   "release-manager"
  cookie_key: "${OIDC_COOKIE_KEY}"
```

If you prefer group-based mapping: edit the app manifest to emit
`groupMembershipClaims: "SecurityGroup"`, keep `groups_claim: "groups"`,
and populate `role_mappings` with group object-IDs (not names — Entra
emits groups by ID by default unless you enable the cloud-only groups
preview feature).

## Okta

```yaml
oidc:
  enabled: true
  display_name: "Okta"
  issuer: "https://<org>.okta.com/oauth2/default"
  client_id: "<okta-app-client-id>"
  client_secret: "${OIDC_CLIENT_SECRET}"
  redirect_url: "https://nexspence.example.com/api/v1/auth/oidc/callback"
  frontend_base_url: "https://nexspence.example.com"
  scopes: ["openid", "profile", "email", "groups"]
  groups_claim: "groups"
  admin_group: "nexspence-admins"
  cookie_key: "${OIDC_COOKIE_KEY}"
```

**Okta setup:** Authorization Servers → default → Claims → Add Claim
`groups`, type: Groups, filter: `Matches regex: .*` (or a narrower filter).

## Local testing with Keycloak + docker-compose

Create `docker-compose.oidc.yml` (dev only):

```yaml
services:
  keycloak:
    image: quay.io/keycloak/keycloak:24.0
    command: start-dev
    environment:
      KEYCLOAK_ADMIN: admin
      KEYCLOAK_ADMIN_PASSWORD: admin
    ports: ["8180:8080"]
```

Run:

```bash
docker compose -f docker-compose.yml -f docker-compose.oidc.yml up
```

Configure the realm at `http://localhost:8180`:

1. Realm: `nexspence`.
2. Client: `nexspence` (confidential, redirect `http://localhost:8081/api/v1/auth/oidc/callback`).
3. Create user with password + group `nexspence-admins`.
4. Add `groups` client scope + mapper as above.

Nexspence `config.yaml`:

```yaml
oidc:
  enabled: true
  issuer: "http://keycloak:8080/realms/nexspence"  # inside compose network
  client_id: "nexspence"
  client_secret: "<secret-from-keycloak-ui>"
  redirect_url: "http://localhost:8081/api/v1/auth/oidc/callback"
  frontend_base_url: "http://localhost:8081"
  cookie_secure: false        # dev only — HTTP
  cookie_key: "<make oidc-secret output>"
```

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `oidc discovery failed` on startup | `issuer` URL unreachable or missing `.well-known/openid-configuration` | Verify URL from the Nexspence container: `curl {issuer}/.well-known/openid-configuration`. |
| Redirect loop / `state mismatch` | Cookie not being sent back | Ensure `cookie_secure` matches your scheme (false for plain HTTP). Check SameSite isn't blocking (Lax is used — top-level redirect from IdP must preserve it). |
| `oidc_error=provisioning rejected` banner | `provisioning: allowlist` + no email match, or `provisioning: manual` + user not pre-created | Adjust mode, add email pattern, or pre-create user in Security → Users. |
| No roles assigned after login | Claim name wrong or claim empty | Decode id_token at jwt.io. Verify `groups_claim` matches actual claim name. For Entra: use `roles`. For Google: set `groups_claim: ""` and assign roles manually. |
| `username conflict` error (409 banner) | Local or LDAP user already exists with same username | Rename the local user or delete them before migrating to OIDC. |

## Security notes

- **State cookie** is AES-256-GCM sealed (`cookie_key` must be 32 bytes).
  Rotate the key to invalidate all in-flight login attempts.
- **Fragment-based JWT delivery** — tokens never appear in access logs or
  Referer headers. Do not change the redirect scheme to query-string.
- **Role sync is REPLACE, not merge** — an OIDC user's Nexspence roles are
  rewritten from claims on every login. If an admin is removed from
  `nexspence-admins` in your IdP, they lose `nx-admin` on their next
  login. Manual role grants in the UI are NOT preserved for OIDC users.
- **Session lifetime** — Phase 28 issues a Nexspence JWT (24h default).
  Single Logout (SLO) is deferred to Phase 28.1; if you need immediate
  session termination on IdP side, consider reducing `auth.jwt_expiry_hours`.
