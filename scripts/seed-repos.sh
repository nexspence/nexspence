#!/usr/bin/env bash
# seed-repos.sh — creates hosted+proxy+group repositories for all 14 formats.
#
# Repositories are split across two S3 blob stores for migration testing:
#   s3-primary   → maven, npm, pypi, docker, helm, cargo, conda   (7 formats)
#   s3-secondary → go, nuget, raw, apt, yum, conan, terraform    (7 formats)
#
# The script auto-creates s3-primary and s3-secondary blob stores if they
# don't exist yet. S3 config defaults match the docker-compose.ha.yml setup.
#
# Environment variables:
#   BASE_URL        — server URL                   (default: http://localhost:8080)
#   ADMIN_USER      — admin login                  (default: admin)
#   ADMIN_PASS      — admin password               (default: admin123)
#   BLOB_PRIMARY    — primary blob store name      (default: s3-primary)
#   BLOB_SECONDARY  — secondary blob store name    (default: s3-secondary)
#   S3_ENDPOINT     — MinIO/S3 endpoint (internal) (default: http://minio:9000)
#   S3_ACCESS_KEY   — S3 access key                (default: minioadmin)
#   S3_SECRET_KEY   — S3 secret key                (default: minioadmin)
#   S3_REGION       — S3 region                    (default: us-east-1)
set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"
BLOB_PRIMARY="${BLOB_PRIMARY:-s3-primary}"
BLOB_SECONDARY="${BLOB_SECONDARY:-s3-secondary}"
S3_ENDPOINT="${S3_ENDPOINT:-http://minio:9000}"
S3_ACCESS_KEY="${S3_ACCESS_KEY:-minioadmin}"
S3_SECRET_KEY="${S3_SECRET_KEY:-minioadmin}"
S3_REGION="${S3_REGION:-us-east-1}"

SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'
ok()      { echo -e "${GREEN}[OK]${NC}    $*"; }
err()     { echo -e "${RED}[ERR]${NC}   $*"; }
info()    { echo -e "${CYAN}[INFO]${NC}  $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC}  $*"; }
section() { echo -e "\n${BOLD}${CYAN}══ $* ══${NC}"; }

FAILED=()

run_script() {
    local script="$1"
    local blob="$2"
    local name
    name=$(basename "$script" .sh | sed 's/create-//;s/-repos//')
    section "${name} → ${blob}"
    if BASE_URL="$BASE_URL" ADMIN_USER="$ADMIN_USER" ADMIN_PASS="$ADMIN_PASS" \
       BLOB_STORE="$blob" bash "$script"; then
        ok "${name} done"
    else
        err "${name} FAILED"
        FAILED+=("${name}")
    fi
}

# Creates an S3 blob store if it doesn't already exist.
# Arguments: name bucket
ensure_s3_blob_store() {
    local name="$1" bucket="$2"
    local code
    code=$(curl -s -o /dev/null -w "%{http_code}" \
        -u "${ADMIN_USER}:${ADMIN_PASS}" \
        "${BASE_URL}/service/rest/v1/blobstores/${name}")

    if [[ "$code" == "200" ]]; then
        info "Blob store '${name}' already exists — skipping"
        return 0
    fi

    info "Creating S3 blob store '${name}' (bucket: ${bucket}) …"
    local resp http_code
    http_code=$(curl -s -o /tmp/nxs_bs_create.out -w "%{http_code}" \
        -u "${ADMIN_USER}:${ADMIN_PASS}" \
        -X POST "${BASE_URL}/service/rest/v1/blobstores/s3" \
        -H "Content-Type: application/json" \
        -d "{
              \"name\": \"${name}\",
              \"config\": {
                \"bucket\":           \"${bucket}\",
                \"region\":           \"${S3_REGION}\",
                \"endpoint\":         \"${S3_ENDPOINT}\",
                \"access_key\":       \"${S3_ACCESS_KEY}\",
                \"secret_key\":       \"${S3_SECRET_KEY}\",
                \"force_path_style\": true
              }
            }")
    resp=$(cat /tmp/nxs_bs_create.out)
    if [[ "$http_code" =~ ^2 ]]; then
        ok "Blob store '${name}' created"
    else
        err "Failed to create blob store '${name}' (HTTP ${http_code}): ${resp}"
        return 1
    fi
}

# ── Server reachability ───────────────────────────────────────────────────────
info "Connecting to ${BASE_URL} …"
if ! curl -sf -o /dev/null -u "${ADMIN_USER}:${ADMIN_PASS}" "${BASE_URL}/service/rest/v1/repositories"; then
    err "Cannot reach ${BASE_URL}"
    exit 1
fi
info "Server OK"

# ── Ensure blob stores exist (create if missing) ──────────────────────────────
section "Blob stores"
ensure_s3_blob_store "$BLOB_PRIMARY"   "nexspence-s3-1" || exit 1
ensure_s3_blob_store "$BLOB_SECONDARY" "nexspence-s3-2" || exit 1

# ── s3-primary: maven, npm, pypi, docker, helm, cargo, conda ─────────────────
run_script "${SCRIPTS_DIR}/create-maven-repos.sh"   "$BLOB_PRIMARY"
run_script "${SCRIPTS_DIR}/create-npm-repos.sh"     "$BLOB_PRIMARY"
run_script "${SCRIPTS_DIR}/create-pypi-repos.sh"    "$BLOB_PRIMARY"
run_script "${SCRIPTS_DIR}/create-docker-repos.sh"  "$BLOB_PRIMARY"
run_script "${SCRIPTS_DIR}/create-helm-repos.sh"    "$BLOB_PRIMARY"
run_script "${SCRIPTS_DIR}/create-cargo-repos.sh"   "$BLOB_PRIMARY"
run_script "${SCRIPTS_DIR}/create-conda-repos.sh"   "$BLOB_PRIMARY"

# ── s3-secondary: go, nuget, raw, apt, yum, conan, terraform ─────────────────
run_script "${SCRIPTS_DIR}/create-go-repos.sh"        "$BLOB_SECONDARY"
run_script "${SCRIPTS_DIR}/create-nuget-repos.sh"     "$BLOB_SECONDARY"
run_script "${SCRIPTS_DIR}/create-raw-repos.sh"       "$BLOB_SECONDARY"
run_script "${SCRIPTS_DIR}/create-apt-repos.sh"       "$BLOB_SECONDARY"
run_script "${SCRIPTS_DIR}/create-yum-repos.sh"       "$BLOB_SECONDARY"
run_script "${SCRIPTS_DIR}/create-conan-repos.sh"     "$BLOB_SECONDARY"
run_script "${SCRIPTS_DIR}/create-terraform-repos.sh" "$BLOB_SECONDARY"

# ── Summary ───────────────────────────────────────────────────────────────────
echo
if [[ ${#FAILED[@]} -eq 0 ]]; then
    ok "All 14 formats seeded successfully."
    info "Blob store split for migration testing:"
    echo "  ${BLOB_PRIMARY}   → maven, npm, pypi, docker, helm, cargo, conda"
    echo "  ${BLOB_SECONDARY} → go, nuget, raw, apt, yum, conan, terraform"
    echo
    info "To test blob store migration: Admin → Blob Stores → select a repo → Change Blob Store"
else
    err "Failed formats: ${FAILED[*]}"
    exit 1
fi
