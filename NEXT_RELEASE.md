### ✨ Features

* **Phase 57 — Role Privilege Detail View**: Role cards in SecurityPage → Roles tab now show a clickable privilege-count badge (e.g. "3 privileges ▼"). Expanding a card reveals an inline panel per privilege: type badge, name, linked Content Selector name, and action chips (read/browse/write/delete). Privilege details are fetched lazily on first expand and cached client-side; cache is invalidated when the role is saved.
* **Phase 52 — SAML 2.0 SSO**: SP-initiated SSO via `github.com/crewjam/saml`; supports ADFS, Azure AD (legacy SAML), Okta SAML. Config: `saml.enabled`, `idp_metadata_url`, `sp_entity_id`, `acs_url`. Provisioning: jit / allowlist / manual. Role mapping via `admin_group` + `role_mappings` (REPLACE semantics). Ephemeral RSA-2048 keypair if no PEM configured; HMAC-SHA256 signed RelayState. Fragment-based JWT delivery (`/saml/callback#token=…`). "Sign in with SAML" button on login page. AdminPage → SAML SSO tab with SP config details, metadata download link, and Redis status card.
* Add `deploy/` directory with `nginx-ha.conf` to release package (required for HA mode)
* Add High Availability startup instructions to Quick Start guide

### 🐛 Bug Fixes

* **Roles API returned empty privileges list**: `GET /service/rest/v1/security/roles` always returned `privileges: []` — the DB query did not join `role_privileges`. Fixed with `LEFT JOIN … array_agg` so the roles list now includes privilege IDs; the expand button in the UI now appears correctly.
* **OIDC panic on startup when Keycloak is not yet ready**: `NewRouter` panicked with a stack trace if the IdP was unreachable at boot time. Replaced with a 60-second retry loop (every 3 s) that logs `WARN oidc discovery not ready, retrying in 3s` and only calls `os.Exit(1)` with a clean message after the timeout — handles Keycloak's ~30 s cold-start in Docker Compose.
* **HA Compose `depends_on` invalid project error**: Removed erroneous `depends_on: keycloak` — Docker Compose rejects dependencies on profile-gated services when the profile is not active. The retry loop above handles the race instead.
* Fix tags of images
