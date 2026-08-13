# Screenshots

UI screenshots served from `nexspence.com/assets/screenshots/` and referenced by
the repository README, the website and the organization profile page.

Captured at 1920×950 (2× device pixel ratio, downscaled to 1920 wide) against a
demo instance seeded with `scripts/seed-all.sh` plus container images, OCI Helm
charts, cleanup policies and a vulnerability scan.

| Filename | What it shows |
|----------|---------------|
| `repositories.PNG` | Repositories list — 44 repos, format badges, size and per-repo actions |
| `browse.PNG` | Browse — Docker image tree (blobs, manifests, tags) with asset metadata |
| `browse_oci.PNG` | Browse — OCI repository holding Helm charts pushed with `helm push oci://` |
| `search.PNG` | Search — cross-repository component search with version and tag columns |
| `security_roles.PNG` | Security → Roles — built-in and per-environment roles with privilege chips |
| `security_vulnerabilities.PNG` | Security → Vulnerability Dashboard — severity counters and per-component findings |
| `users.PNG` | Security → Users — local accounts and their roles |
| `admin_blobstores.PNG` | System Admin → Blob Stores — local and S3 stores with usage and quotas |
| `monitoring_charts.PNG` | System Admin → Monitoring → Charts — requests/sec, error rate, storage |
| `cleanup.PNG` | Cleanup Policies — policy list with criteria, schedule and scope chips |
| `cleanup_preview.PNG` | Cleanup Policies — dry-run preview listing the assets a policy would delete |
| `audit.PNG` | Audit Log — repository, user and security events with filters |

The repository README references a subset of these files; the rest are used by
the website and the `nexspence/.github` profile page. A missing file renders as a
broken image placeholder on GitHub.
