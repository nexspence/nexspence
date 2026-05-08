#!/usr/bin/env bash
# seed-repos.sh — creates hosted+proxy+group repositories for all 14 formats.
#
# Repositories are split across two S3 blob stores for migration testing:
#   s3-primary   → maven, npm, pypi, docker, helm, cargo, conda   (7 formats)
#   s3-secondary → go, nuget, raw, apt, yum, conan, terraform    (7 formats)
#
# Both blob stores must exist before running (create via UI or API).
# Defaults to s3-primary and s3-secondary; override with:
#   BLOB_PRIMARY=my-s3 BLOB_SECONDARY=other-s3 ./scripts/seed-repos.sh
#
# Environment variables:
#   BASE_URL       — server URL             (default: http://localhost:8080)
#   ADMIN_USER     — admin login            (default: admin)
#   ADMIN_PASS     — admin password         (default: admin123)
#   BLOB_PRIMARY   — primary blob store     (default: s3-primary)
#   BLOB_SECONDARY — secondary blob store   (default: s3-secondary)
set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"
BLOB_PRIMARY="${BLOB_PRIMARY:-s3-primary}"
BLOB_SECONDARY="${BLOB_SECONDARY:-s3-secondary}"

SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'
ok()      { echo -e "${GREEN}[OK]${NC}    $*"; }
err()     { echo -e "${RED}[ERR]${NC}   $*"; }
info()    { echo -e "${CYAN}[INFO]${NC}  $*"; }
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

# ── Server reachability ───────────────────────────────────────────────────────
info "Connecting to ${BASE_URL} …"
if ! curl -sf -o /dev/null -u "${ADMIN_USER}:${ADMIN_PASS}" "${BASE_URL}/service/rest/v1/repositories"; then
    err "Cannot reach ${BASE_URL}"
    exit 1
fi
info "Server OK"

# ── Verify blob stores exist ──────────────────────────────────────────────────
for bs in "$BLOB_PRIMARY" "$BLOB_SECONDARY"; do
    code=$(curl -s -o /dev/null -w "%{http_code}" -u "${ADMIN_USER}:${ADMIN_PASS}" \
        "${BASE_URL}/service/rest/v1/blobstores/${bs}")
    if [[ "$code" != "200" ]]; then
        err "Blob store '${bs}' not found (HTTP ${code}). Create it first."
        exit 1
    fi
    info "Blob store verified: ${bs}"
done

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
    echo "  s3-primary   (${BLOB_PRIMARY}):   maven, npm, pypi, docker, helm, cargo, conda"
    echo "  s3-secondary (${BLOB_SECONDARY}): go, nuget, raw, apt, yum, conan, terraform"
    echo
    info "To test blob store migration: Admin → Blob Stores → select a repo → Change Blob Store"
else
    err "Failed formats: ${FAILED[*]}"
    exit 1
fi
