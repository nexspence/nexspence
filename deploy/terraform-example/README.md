# Nexspence — full Terraform example

Ready-to-apply manifests that exercise **every resource and data source** of the
[`nexspence/nexspence`](https://registry.terraform.io/providers/nexspence/nexspence)
Terraform provider, against a locally deployed stack on **`http://localhost:8080`**.

Applying this creates **66 objects**: 2 blob stores, 45 repositories (14 formats ×
hosted/proxy/group + quota demos + 2 promotion targets), 3 content selectors,
3 privileges, 2 roles, 2 users, 2 cleanup policies, 2 routing rules, 2 webhooks,
2 promotion rules — plus 2 read-only data sources.

Requires provider **v0.2.0+** (the cleanup/routing/webhook/promotion resources;
`versions.tf` pins `>= 0.2.0`).

---

## 1. Bring up the stack

From the repository root. Pick one of the two compose files.

### Single-node (simplest)

```bash
docker compose up -d --build          # Nexspence behind :8080, Postgres, MinIO
```

- App: <http://localhost:8080>  (bootstrap admin `admin` / `admin123`, see `config.yaml`)
- MinIO console: <http://localhost:9001>  (`minioadmin` / `minioadmin`)
- MinIO S3 API: published on `localhost:9000`; pre-created buckets:
  `nexspence-blobs`, `nexspence-s3-1`, `nexspence-s3-2`.

### High-availability (nginx + 2 nodes + Redis + monitoring)

```bash
docker compose -f docker-compose.ha.yml up -d --build
```

Same `:8080` entrypoint and admin credentials. Here MinIO's S3 API (`:9000`) is
**not published to the host** — only the server (inside the Docker network) talks
to it. That changes the S3 endpoint you pass to Terraform — see step 3.

Verify the stack is up:

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/        # 200
curl -s -u admin:admin123 http://localhost:8080/service/rest/v1/blobstores
```

---

## 2. Install the provider

The provider is published to the Terraform Registry, so nothing special is needed —
`terraform init` downloads it. `versions.tf` already pins `nexspence/nexspence ~> 0.1`.

> Working from an unpublished local build instead? Use the dev-override fallback in
> `dev.tfrc.example` (see the end of this file).

---

## 3. Configure variables

```bash
cd deploy/terraform-example
cp terraform.tfvars.example terraform.tfvars
```

Defaults already target `http://localhost:8080` with `admin` / `admin123`, so for a
local stack you can leave most of it. To also create the **S3 blob store**, set:

```hcl
enable_s3_blobstore = true
s3_bucket           = "nexspence-s3-1"
s3_endpoint         = "http://minio:9000"   # see the endpoint rule below
s3_access_key       = "minioadmin"
s3_secret_key       = "minioadmin"
```

### ⚠️ The S3 endpoint is dialed by the SERVER, not by Terraform

Terraform only stores the endpoint string in Nexspence; the **Nexspence server**
is what actually connects to S3. So set it to an address the *server* can reach:

| Where the Nexspence server runs | `s3_endpoint` |
|---------------------------------|---------------|
| Docker Compose (default here)   | `http://minio:9000` (the compose service name) |
| On the host (`go run`) + MinIO published | `http://localhost:9000` |
| Real AWS S3                     | leave empty, set `s3_region` |

The bucket must already exist (compose creates `nexspence-s3-1` for you).

---

## 4. Apply

```bash
terraform init      # downloads nexspence/nexspence from the registry
terraform plan      # review: "Plan: 66 to add" (65 without S3)
terraform apply
```

Check the result in the UI (<http://localhost:8080> → Repositories / Browse /
Security) or via the API:

```bash
curl -s -u admin:admin123 http://localhost:8080/service/rest/v1/repositories | jq length   # 43
curl -s -u admin:admin123 http://localhost:8080/service/rest/v1/blobstores              # incl. tf-main, tf-s3
```

Tear everything down again:

```bash
terraform destroy
```

---

## What gets created

| Resource | Count | Notes |
|----------|-------|-------|
| `nexspence_blobstore` | 1 (+1 with S3) | `tf-main` local (10 GiB quota); `tf-s3` when `enable_s3_blobstore = true` (50 GiB) |
| `nexspence_repository` | 45 | 14 formats × {hosted, proxy, group} + 2 quota demos + 2 promotion targets |
| `nexspence_content_selector` | 3 | CEL expressions |
| `nexspence_privilege` | 3 | each scoped to a content selector |
| `nexspence_role` | 2 | group privileges |
| `nexspence_user` | 2 | `alice` (active), `bob` (disabled) |
| `nexspence_cleanup_policy` | 2 | one attached to a repo via `cleanup_policy_ids` |
| `nexspence_routing_rule` | 2 | ALLOW / BLOCK path rules |
| `nexspence_webhook` | 2 | HMAC-signed; one active, one disabled |
| `nexspence_promotion_rule` | 2 | scan-gate + manual-approval examples |
| `nexspence_repository` (data source) | 1 | single-repo lookup |
| `nexspence_repositories` (data source) | 1 | list all repos |

Formats covered: maven2, npm, pypi, docker, go, nuget, raw, apt, yum, helm,
cargo, conan, conda, terraform.

## Files

- `versions.tf` — Terraform + provider version constraints
- `providers.tf` — provider auth (basic auth or `nxs_*` token)
- `variables.tf` / `terraform.tfvars.example` — inputs (URL, creds, S3 toggle)
- `blobstores.tf` — local + optional S3/MinIO blob stores
- `repositories.tf` — all 14 formats as hosted + proxy + group via `for_each` + quota demos
- `rbac.tf` — content selectors → privileges → roles → users
- `cleanup.tf` — cleanup policies (one attached to a repo)
- `routing.tf` — routing rules (ALLOW / BLOCK)
- `webhooks.tf` — event webhooks
- `promotion.tf` — build-promotion rules + their target repos
- `data.tf` — both data sources
- `outputs.tf` — repo URLs, counts, lookups
- `dev.tfrc.example` — local provider override (only for unpublished local builds)

## Authentication

Defaults use basic auth with the bootstrap admin. To use an API token instead,
set `nexspence_token` in `terraform.tfvars` and uncomment the `token = ...` line
in `providers.tf` (leave username/password empty — they are mutually exclusive).

## Notes

- Proxy repos point at real public upstreams; they cache on first read.
- Group repos aggregate the hosted + proxy member and route writes to the hosted
  one (`writable_member`).
- `cleanup_policy_ids` is supported on repositories but cleanup policies are not
  managed by this provider — attach existing policy UUIDs if you have them (see
  the commented example in `repositories.tf`).

## Appendix — running against an unpublished local provider build

If you're hacking on the provider and want Terraform to use your local binary
instead of the registry:

```bash
# build it anywhere on disk
cd ~/path/to/terraform-provider-nexspence
go build -o ~/go/bin/terraform-provider-nexspence .

# point Terraform at it (no `terraform init` needed with dev_overrides)
cd deploy/terraform-example
cp dev.tfrc.example dev.tfrc            # edit the path if your GOBIN differs
export TF_CLI_CONFIG_FILE="$PWD/dev.tfrc"
terraform plan
terraform apply
```
