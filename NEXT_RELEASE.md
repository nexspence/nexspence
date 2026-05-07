### ✨ Features

* **Phase 52 — SAML 2.0 SSO**: SP-initiated SSO via `github.com/crewjam/saml`; supports ADFS, Azure AD (legacy SAML), Okta SAML. Config: `saml.enabled`, `idp_metadata_url`, `sp_entity_id`, `acs_url`. Provisioning: jit / allowlist / manual. Role mapping via `admin_group` + `role_mappings` (REPLACE semantics). Ephemeral RSA-2048 keypair if no PEM configured; HMAC-SHA256 signed RelayState. Fragment-based JWT delivery (`/saml/callback#token=…`). "Sign in with SAML" button on login page. AdminPage → SAML SSO tab with SP config details, metadata download link, and Redis status card.
* Add `deploy/` directory with `nginx-ha.conf` to release package (required for HA mode)
* Add High Availability startup instructions to Quick Start guide

### 🐛 Bug Fixes

* Fix tags of images
