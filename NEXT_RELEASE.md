### ✨ Features

- **Terraform provider `nexspence/nexspence` v0.1.0.** Manage Nexspence as code: `nexspence_repository` (hosted/proxy/group, all 14 formats), `nexspence_blobstore` (local/S3), `nexspence_user`, `nexspence_role`, `nexspence_content_selector`, `nexspence_privilege`, plus `nexspence_repository`/`nexspence_repositories` data sources. Built on `terraform-plugin-framework`, authenticates via `nxs_*` token or basic auth, and is published on the Terraform Registry. Source: [nexspence/terraform-provider-nexspence](https://github.com/nexspence/terraform-provider-nexspence).
- **Ready-to-apply Terraform example (`deploy/terraform-example/`).** A complete set of manifests exercising every provider resource and data source against a local stack on `:8080` — 14 formats × hosted/proxy/group, local + S3/MinIO blob stores, and the full content-selector → privilege → role → user RBAC chain. Ships a step-by-step README (bring up the stack, enable S3, `init`/`apply`) and documents the S3-endpoint gotcha (the endpoint is dialed by the Nexspence **server**, so it must be an in-network address). The in-app Terraform docs page gained a Blob Stores (local & S3) example.

### 🐛 Bug Fixes

_No bug fixes in this release._
