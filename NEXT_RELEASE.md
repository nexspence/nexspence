### ✨ Features

- **LDAP External Role Mapping**: LDAP groups are now automatically mapped to Nexspence roles on every login using three resolution strategies: (1) exact name match (group "developers" → role "developers"), (2) explicit `role_mappings` config (like OIDC/SAML), (3) `admin_group` → `nx-admin`. Roles are applied with REPLACE semantics — roles not derived from current LDAP groups are removed. `LDAPConfig` gains a new `role_mappings` field.

### 🐛 Bug Fixes

- **LDAP login regression**: `users.Create` was incorrectly setting `Roles: lu.Groups` (LDAP group names) on the in-memory User struct instead of writing to the `user_roles` DB table. Roles are now exclusively managed by `syncLDAPRoles` via `SetUserRoles`.
