### ✨ Features

- **Terraform provider `nexspence/nexspence` v0.1.0.** Manage Nexspence as code: `nexspence_repository` (hosted/proxy/group, all 14 formats), `nexspence_blobstore` (local/S3), `nexspence_user`, `nexspence_role`, `nexspence_content_selector`, `nexspence_privilege`, plus `nexspence_repository`/`nexspence_repositories` data sources. Built on `terraform-plugin-framework`, authenticates via `nxs_*` token or basic auth, and is published on the Terraform Registry. Source: [nexspence/terraform-provider-nexspence](https://github.com/nexspence/terraform-provider-nexspence).
- **Ready-to-apply Terraform example (`deploy/terraform-example/`).** A complete set of manifests exercising every provider resource and data source against a local stack on `:8080` — 14 formats × hosted/proxy/group, local + S3/MinIO blob stores, and the full content-selector → privilege → role → user RBAC chain. Ships a step-by-step README (bring up the stack, enable S3, `init`/`apply`) and documents the S3-endpoint gotcha (the endpoint is dialed by the Nexspence **server**, so it must be an in-network address). The in-app Terraform docs page gained a Blob Stores (local & S3) example.
- **Terraform provider v0.2.0 — four new resources.** `nexspence_cleanup_policy` (scheduled cleanup: age/last-downloaded/retain-N, attach to repos via `cleanup_policy_ids`), `nexspence_routing_rule` (ALLOW/BLOCK path rules), `nexspence_webhook` (HMAC-signed outbound event webhooks), and `nexspence_promotion_rule` (build promotion with scan-pass and manual-approval gates). The example manifests, in-app docs, and website now cover all of them, and the website landing page advertises the provider with a live registry version.

### 🐛 Bug Fixes

_No bug fixes in this release._

### 🔧 Maintenance

- Dependency bumps: `runc` 1.2.3 → 1.2.8, `github.com/docker/cli`, and dev-deps `vitest` / `@vitest/coverage-v8`.
