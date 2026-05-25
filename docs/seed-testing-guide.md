# Nexspence — Repository Testing Guide

Manual testing guide for all 14 repository formats, RBAC, and blob store migration
after running `scripts/seed-all.sh`.

## Users

| User | Password | Role |
|---|---|---|
| `admin` | `admin123` | Full access |
| `dev-admin` | `Admin2026!` | Write to `com/dev/*` (maven2) |
| `dev-user` | `User2026!` | Read `com/dev/*` (maven2) |
| `stage-admin` | `Admin2026!` | Write to `com/stage/*` (maven2) |
| `stage-user` | `User2026!` | Read `com/stage/*` (maven2) |
| `test-admin` | `Admin2026!` | Write to `com/test/*` (maven2) |
| `test-user` | `User2026!` | Read `com/test/*` (maven2) |
| `prod-admin` | `Admin2026!` | Write to `com/prod/*` (maven2) |
| `prod-user` | `User2026!` | Read `com/prod/*` (maven2) |

**Base URL:** `http://localhost:8080`

---

## 1. Basic authentication

```bash
# Successful admin login — expect 42 repositories
curl -sf -u admin:admin123 http://localhost:8080/service/rest/v1/repositories \
  | python3 -c "import sys,json; repos=json.load(sys.stdin); print(f'Repos: {len(repos)}')"

# Project user login
curl -s -u dev-user:User2026! http://localhost:8080/service/rest/v1/status -w "%{http_code}"
# Expected: 200

# Wrong password
curl -s -u admin:wrongpass http://localhost:8080/service/rest/v1/repositories -w "\nHTTP: %{http_code}"
# Expected: HTTP: 401
```

---

## 2. Raw

```bash
# Upload a file (admin)
curl -u admin:admin123 -T /etc/hosts \
  http://localhost:8080/repository/raw-artifacts/test/hosts.txt -w "%{http_code}"
# Expected: 201

# Download (admin)
curl -u admin:admin123 \
  http://localhost:8080/repository/raw-artifacts/test/hosts.txt | head -3

# Anonymous access to group (allowAnonymous=true on group/proxy)
curl -s http://localhost:8080/repository/raw-common/seed/hello.txt
# Expected: Hello from Nexspence seed!

# Anonymous access to hosted blocked (allowAnonymous=false)
curl -s http://localhost:8080/repository/raw-artifacts/seed/hello.txt -w "\nHTTP: %{http_code}"
# Expected: HTTP: 401
```

---

## 3. Maven2

```bash
# Verify seed artifact exists
curl -s -u admin:admin123 \
  http://localhost:8080/repository/maven-hosted/com/example/seed/1.0/seed-1.0.pom \
  | grep artifactId

# Upload as dev-admin (own namespace — allowed)
echo "<project><modelVersion>4.0.0</modelVersion><groupId>com.dev</groupId><artifactId>myapp</artifactId><version>1.0</version></project>" \
  > /tmp/myapp-1.0.pom
curl -u dev-admin:Admin2026! -T /tmp/myapp-1.0.pom \
  http://localhost:8080/repository/maven-hosted/com/dev/myapp/1.0/myapp-1.0.pom \
  -H "Content-Type: application/xml" -w "%{http_code}"
# Expected: 201

# dev-admin CANNOT write to another environment's namespace
curl -u dev-admin:Admin2026! -T /tmp/myapp-1.0.pom \
  http://localhost:8080/repository/maven-hosted/com/prod/myapp/1.0/myapp-1.0.pom \
  -H "Content-Type: application/xml" -w "%{http_code}"
# Expected: 403

# dev-user CAN read own namespace
curl -u dev-user:User2026! \
  http://localhost:8080/repository/maven-hosted/com/dev/myapp/1.0/myapp-1.0.pom \
  -w "%{http_code}"
# Expected: 200

# dev-user CANNOT read another environment
curl -u dev-user:User2026! \
  http://localhost:8080/repository/maven-hosted/com/prod/myapp/1.0/myapp-1.0.pom \
  -w "%{http_code}"
# Expected: 403

# dev-user CANNOT write even to own namespace
curl -u dev-user:User2026! -T /tmp/myapp-1.0.pom \
  http://localhost:8080/repository/maven-hosted/com/dev/test/1.0/test-1.0.pom \
  -w "%{http_code}"
# Expected: 403

# Maven settings.xml for dev environment
cat <<'EOF'
<settings>
  <servers>
    <server><id>nexspence</id><username>dev-admin</username><password>Admin2026!</password></server>
  </servers>
  <mirrors>
    <mirror>
      <id>nexspence</id>
      <url>http://localhost:8080/repository/maven-group/</url>
      <mirrorOf>*</mirrorOf>
    </mirror>
  </mirrors>
</settings>
EOF
```

---

## 4. npm

```bash
# Verify seed package exists
curl -s -u admin:admin123 \
  http://localhost:8080/repository/npm-hosted/@seed%2fhello \
  | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('name'), list(d.get('versions',{}).keys()))"

# Anonymous access to group
curl -s http://localhost:8080/repository/npm-group/@seed%2fhello \
  | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('name'))"

# Configure npm (.npmrc)
cat <<'EOF'
registry=http://localhost:8080/repository/npm-group/
//localhost:8080/repository/npm-hosted/:username=admin
//localhost:8080/repository/npm-hosted/:password=YWRtaW4xMjM=
EOF

# If npm is installed:
# npm config set registry http://localhost:8080/repository/npm-group/
# npm install lodash
```

---

## 5. PyPI

```bash
# Check simple index
curl -s -u admin:admin123 \
  http://localhost:8080/repository/pypi-hosted/simple/ | grep seed

# Anonymous access to group
curl -s http://localhost:8080/repository/pypi-group/simple/ -w "%{http_code}"

# If pip is installed:
# pip install seed-hello \
#   --index-url http://localhost:8080/repository/pypi-group/simple/ \
#   --trusted-host localhost

# Publish with twine:
# twine upload \
#   --repository-url http://localhost:8080/repository/pypi-hosted/ \
#   -u admin -p admin123 \
#   dist/seed_hello-1.0.0-py3-none-any.whl
```

---

## 6. Docker

```bash
# Check /v2/ endpoint
curl -s http://localhost:8080/v2/ -w "%{http_code}"
# Expected: 200

# List images (admin)
curl -s -u admin:admin123 http://localhost:8080/v2/_catalog

# Push an image (requires Docker)
docker pull alpine:3.19
docker tag alpine:3.19 localhost:8080/docker-dev/alpine:3.19
docker login localhost:8080 -u admin -p admin123
docker push localhost:8080/docker-dev/alpine:3.19

# Pull via group
docker pull localhost:8080/docker-common/alpine:3.19

# Check manifest via API
curl -s -u admin:admin123 \
  -H "Accept: application/vnd.docker.distribution.manifest.v2+json" \
  http://localhost:8080/v2/docker-dev/alpine/manifests/3.19
```

---

## 7. Helm

```bash
# Check index.yaml
curl -s -u admin:admin123 \
  http://localhost:8080/repository/helm-hosted/index.yaml | head -10

# Anonymous access to group (allowAnonymous=true)
curl -s http://localhost:8080/repository/helm-charts/index.yaml | grep "seed-chart"

# Download chart
curl -s -u admin:admin123 \
  http://localhost:8080/repository/helm-hosted/seed-chart-1.0.0.tgz -o /tmp/seed-chart.tgz
file /tmp/seed-chart.tgz

# If Helm is installed:
# helm repo add nexspence http://localhost:8080/repository/helm-charts \
#   --username admin --password admin123
# helm repo update
# helm search repo nexspence/seed-chart
```

---

## 8. Go modules

```bash
# Check info
curl -s -u admin:admin123 \
  "http://localhost:8080/repository/go-hosted/github.com/example/seedmod/@v/v1.0.0.info"
# Expected: {"Version":"v1.0.0","Time":"..."}

# Check mod file
curl -s -u admin:admin123 \
  "http://localhost:8080/repository/go-hosted/github.com/example/seedmod/@v/v1.0.0.mod"

# List versions
curl -s -u admin:admin123 \
  "http://localhost:8080/repository/go-hosted/github.com/example/seedmod/@v/list"

# If Go is installed:
# GOPROXY=http://admin:admin123@localhost:8080/repository/go-group \
#   GONOSUMCHECK=* \
#   go get github.com/example/seedmod@v1.0.0
```

---

## 9. NuGet

```bash
# Service index
curl -s -u admin:admin123 \
  http://localhost:8080/repository/nuget-hosted/index.json | python3 -m json.tool | head -5

# Search package (OData v2)
curl -s -u admin:admin123 \
  "http://localhost:8080/repository/nuget-hosted/FindPackagesById()?id='SeedHello'" | head -20

# Anonymous access to group
curl -s http://localhost:8080/repository/nuget-group/index.json -w "%{http_code}"

# If dotnet is installed:
# dotnet nuget add source http://localhost:8080/repository/nuget-group/index.json \
#   --name nexspence --username admin --password admin123
# dotnet add package SeedHello
```

---

## 10. Apt

```bash
# Download .deb directly
curl -s -u admin:admin123 \
  "http://localhost:8080/repository/apt-hosted/pool/main/s/seed-hello/seed-hello_1.0.0_amd64.deb" \
  -o /tmp/seed.deb -w "%{http_code}"
# Expected: 200

# Anonymous access to group
curl -s "http://localhost:8080/repository/apt-group/" -w "%{http_code}"

# Configure apt (requires root on Debian/Ubuntu):
# echo "deb http://localhost:8080/repository/apt-group/ bookworm main" \
#   > /etc/apt/sources.list.d/nexspence.list
# apt-get update --allow-unauthenticated
```

---

## 11. Yum/RPM

```bash
# Download .rpm
curl -s -u admin:admin123 \
  "http://localhost:8080/repository/yum-hosted/Packages/s/seed-hello-1.0.0-1.x86_64.rpm" \
  -o /tmp/seed.rpm -w "%{http_code}"
# Expected: 200

# repomd.xml
curl -s -u admin:admin123 \
  "http://localhost:8080/repository/yum-hosted/repodata/repomd.xml" | head -5

# Configure yum/dnf:
# cat > /etc/yum.repos.d/nexspence.repo <<EOF
# [nexspence]
# name=Nexspence
# baseurl=http://localhost:8080/repository/yum-group/
# enabled=1
# gpgcheck=0
# EOF
```

---

## 12. Cargo

```bash
# Download crate
curl -s -u admin:admin123 \
  "http://localhost:8080/repository/cargo-hosted/api/v1/crates/seed-hello/1.0.0/download" \
  -o /tmp/seed-hello.crate -w "%{http_code}"
# Expected: 200

# ~/.cargo/config.toml
cat <<'EOF'
[source.nexspence]
registry = "http://localhost:8080/repository/cargo-group/"

[source.crates-io]
replace-with = "nexspence"

[net]
git-fetch-with-cli = true
EOF
```

---

## 13. Conda

```bash
# Check repodata.json (noarch)
curl -s -u admin:admin123 \
  "http://localhost:8080/repository/conda-hosted/noarch/repodata.json" \
  | python3 -c "import sys,json; d=json.load(sys.stdin); print('packages:', list(d.get('packages',{}).keys())[:3])"

# Download package
curl -s -u admin:admin123 \
  "http://localhost:8080/repository/conda-hosted/noarch/seed-hello-1.0.0-py_0.tar.bz2" \
  -o /tmp/seed.tar.bz2 -w "%{http_code}"
# Expected: 200

# If conda is installed:
# conda config --add channels http://localhost:8080/repository/conda-group/
# conda search seed-hello
```

---

## 14. Terraform

```bash
# Service discovery
curl -s -u admin:admin123 \
  "http://localhost:8080/repository/terraform-hosted/.well-known/terraform.json"
# Expected: {"modules.v1":"...","providers.v1":"..."}

# List provider versions
curl -s -u admin:admin123 \
  "http://localhost:8080/repository/terraform-hosted/v1/providers/example/seed/versions"

# Download provider binary
curl -s -u admin:admin123 \
  "http://localhost:8080/repository/terraform-hosted/v1/providers/example/seed/1.0.0/download/linux/amd64" \
  -o /tmp/provider.zip -w "%{http_code}"
# Expected: 200

# terraform.tf (if Terraform is installed):
cat <<'EOF'
terraform {
  required_providers {
    seed = {
      source  = "localhost:8080/repository/terraform-group/example/seed"
      version = "1.0.0"
    }
  }
}
EOF
```

---

## 15. RBAC access matrix

```bash
# Helper function
check_access() {
  local user="$1" pass="$2" method="$3" url="$4" expected="$5"
  local auth_args=()
  [[ -n "$user" ]] && auth_args=(-u "$user:$pass")
  code=$(curl -s -o /dev/null -w "%{http_code}" "${auth_args[@]}" -X "$method" "$url")
  if [[ "$code" == "$expected" ]]; then
    printf "[OK]   %-14s %s %-50s → %s\n" "$user" "$method" "$(basename $url)" "$code"
  else
    printf "[FAIL] %-14s %s %-50s → got %s, expected %s\n" "$user" "$method" "$(basename $url)" "$code" "$expected"
  fi
}

BASE="http://localhost:8080"
echo "test" > /tmp/test.txt

echo "--- Write access ---"
# dev-admin can write to own namespace
check_access dev-admin Admin2026! PUT "$BASE/repository/maven-hosted/com/dev/test/1.0/test.txt"  201
# dev-admin cannot write to prod namespace
check_access dev-admin Admin2026! PUT "$BASE/repository/maven-hosted/com/prod/test/1.0/test.txt" 403
# prod-admin can write to prod namespace
check_access prod-admin Admin2026! PUT "$BASE/repository/maven-hosted/com/prod/test/1.0/test.txt" 201

echo ""
echo "--- Read access ---"
# dev-user can read own namespace
check_access dev-user  User2026!  GET "$BASE/repository/maven-hosted/com/dev/test/1.0/test.txt"  200
# dev-user cannot read prod namespace
check_access dev-user  User2026!  GET "$BASE/repository/maven-hosted/com/prod/test/1.0/test.txt" 403
# dev-user cannot write even to own namespace
check_access dev-user  User2026!  PUT "$BASE/repository/maven-hosted/com/dev/test/1.0/test2.txt" 403

echo ""
echo "--- Anonymous access ---"
# Group/proxy repos allow anonymous (allowAnonymous=true)
check_access "" "" GET "$BASE/repository/raw-common/seed/hello.txt" 200
check_access "" "" GET "$BASE/repository/npm-group/@seed%2fhello"   200
check_access "" "" GET "$BASE/repository/helm-charts/index.yaml"    200
# Hosted repos block anonymous (allowAnonymous=false)
check_access "" "" GET "$BASE/repository/raw-artifacts/seed/hello.txt" 401
check_access "" "" GET "$BASE/repository/npm-hosted/@seed%2fhello"     401
```

---

## 16. Blob store migration test

Repositories are split across two S3 blob stores for migration testing:

| Blob store | Formats |
|---|---|
| `s3-primary` | maven, npm, pypi, docker, helm, cargo, conda |
| `s3-secondary` | go, nuget, raw, apt, yum, conan, terraform |

```bash
# Check usage of all blob stores
for bs in s3-primary s3-secondary default docker; do
  curl -s -u admin:admin123 "http://localhost:8080/service/rest/v1/blobstores/$bs" \
    | python3 -c "
import sys, json
b = json.load(sys.stdin)
print(f'  {b[\"name\"]:16} type={b[\"type\"]:6}  used={b[\"usedBytes\"]} bytes')
" 2>/dev/null
done

# Trigger blob store migration via API (example: move raw-artifacts to s3-primary):
# POST http://localhost:8080/api/v1/repositories/raw-artifacts/blob-store-migration
# Body: {"targetBlobStoreName": "s3-primary"}

# Verify artifact is still accessible after migration:
curl -s -u admin:admin123 \
  http://localhost:8080/repository/raw-artifacts/seed/hello.txt
# Expected: Hello from Nexspence seed!
```

---

## 17. Quick health check

```bash
echo "=== Repository availability ==="
for repo in maven-hosted npm-hosted pypi-hosted helm-hosted go-hosted \
            nuget-hosted raw-artifacts apt-hosted yum-hosted cargo-hosted \
            conda-hosted terraform-hosted docker-dev; do
  code=$(curl -s -u admin:admin123 -o /dev/null -w "%{http_code}" \
    "http://localhost:8080/repository/$repo/")
  printf "  %-22s → %s\n" "$repo" "$code"
done

echo ""
echo "=== Blob stores ==="
curl -s -u admin:admin123 "http://localhost:8080/service/rest/v1/blobstores" \
  | python3 -c "
import sys, json
for b in json.load(sys.stdin):
    print(f'  {b[\"type\"]:8} {b[\"name\"]:16} used={b[\"usedBytes\"]} bytes')
"

echo ""
echo "=== RBAC users ==="
curl -s -u admin:admin123 "http://localhost:8080/service/rest/v1/security/users" \
  | python3 -c "
import sys, json
for u in sorted(json.load(sys.stdin), key=lambda x: x['userId']):
    roles = [r.get('name', r) if isinstance(r, dict) else r for r in u.get('roles', [])]
    print(f'  {u[\"userId\"]:20} roles={roles}')
"
```

---

## 18. Cleanup

Remove everything created by `seed-all.sh`:

```bash
# Remove only RBAC (users, roles, privileges, content selectors)
./scripts/seed-cleanup.sh --rbac

# Remove only repositories (all 42)
./scripts/seed-cleanup.sh --repos

# Remove only seed packages/components from hosted repos
./scripts/seed-cleanup.sh --packages

# Remove everything
./scripts/seed-cleanup.sh --all
```

> **Note:** Blob stores (`s3-primary`, `s3-secondary`) are not deleted automatically.
> Remove them via UI: **Admin → Blob Stores → Delete**, or via API:
> `DELETE /service/rest/v1/blobstores/{name}` (only works if no repos reference them).
