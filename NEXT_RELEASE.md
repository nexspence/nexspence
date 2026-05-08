### ✨ Features

- **Conda format handler** (Phase 61) — hosted channel with `repodata.json` generation from DB; `.tar.bz2` and `.conda` package upload/download/delete; proxy mode with upstream URL rewriting so `conda install` works transparently; default upstream `conda-forge`
- **Terraform Registry Mirror** (Phase 62) — service discovery (`/.well-known/terraform.json`); provider versions list, binary upload/download; module versions, upload, `X-Terraform-Get` redirect; proxy mode caches provider binaries from `registry.terraform.io`; works with `terraform init` against a private mirror
- **LDAP external role mapping** (Phase 60) — on every LDAP login, user roles are synced from all group memberships: `admin_group` → `nx-admin`, explicit `role_mappings` entries, and group-name-equals-role-name fallback; same REPLACE semantics as OIDC

### 🐛 Bug Fixes

- **DB migration 018** — added `conda` and `terraform` to `repositories_format_check` constraint; existing databases upgraded automatically on server start
- Fixed LDAP users getting roles written only in memory (not persisted to `user_roles` table) on first login
