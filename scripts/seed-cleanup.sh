#!/usr/bin/env bash
# seed-cleanup.sh — removes everything created by seed-all.sh.
#
# Modes (can be combined):
#   --rbac      Delete users, roles, privileges, content selectors (dev/stage/test/prod)
#   --repos     Delete all 42 repositories created by seed-repos.sh
#   --packages  Delete all seed components/assets from hosted repos
#   --all       All of the above
#
# Usage:
#   ./scripts/seed-cleanup.sh --rbac
#   ./scripts/seed-cleanup.sh --repos
#   ./scripts/seed-cleanup.sh --all
#   ./scripts/seed-cleanup.sh --rbac --repos
#   BASE_URL=http://192.168.1.10:8080 ./scripts/seed-cleanup.sh --all
#
# Environment variables:
#   BASE_URL   — server URL     (default: http://localhost:8080)
#   ADMIN_USER — admin login    (default: admin)
#   ADMIN_PASS — admin password (default: admin123)
set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"

AUTH=(-u "${ADMIN_USER}:${ADMIN_PASS}")
API="${BASE_URL}/service/rest/v1"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'
ok()      { echo -e "${GREEN}[OK]${NC}    $*"; }
err()     { echo -e "${RED}[ERR]${NC}   $*" >&2; }
info()    { echo -e "${CYAN}[INFO]${NC}  $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC}  $*"; }
section() { echo -e "\n${BOLD}${CYAN}══ $* ══${NC}"; }

DO_RBAC=false
DO_REPOS=false
DO_PACKAGES=false

[[ $# -eq 0 ]] && { echo "Usage: $0 --rbac | --repos | --packages | --all"; exit 1; }

for arg in "$@"; do
    case "$arg" in
        --rbac)     DO_RBAC=true ;;
        --repos)    DO_REPOS=true ;;
        --packages) DO_PACKAGES=true ;;
        --all)      DO_RBAC=true; DO_REPOS=true; DO_PACKAGES=true ;;
        *) echo "Unknown option: $arg"; exit 1 ;;
    esac
done

# ── Server check ──────────────────────────────────────────────────────────────
info "Connecting to ${BASE_URL} ..."
if ! curl -sf -o /dev/null "${AUTH[@]}" "${BASE_URL}/service/rest/v1/repositories"; then
    err "Cannot reach ${BASE_URL}"
    exit 1
fi
info "Server OK"

del() {
    local label="$1" url="$2"
    local code
    code=$(curl -s -o /dev/null -w "%{http_code}" "${AUTH[@]}" -X DELETE "$url")
    case "$code" in
        204|200) ok  "deleted  ${label}" ;;
        404)     warn "not found ${label} — skipping" ;;
        *)       err "failed   ${label} — HTTP ${code}" ;;
    esac
}

get_id() {
    local url="$1" field="$2" value="$3"
    curl -s "${AUTH[@]}" "$url" | python3 -c "
import sys, json
items = json.load(sys.stdin)
if not isinstance(items, list): items = [items]
for item in items:
    if item.get('$field') == '$value':
        print(item.get('id', ''))
        break
" 2>/dev/null
}

# ════════════════════════════════════════════════════════════════════════════
# RBAC cleanup: dev, stage, test, prod
# ════════════════════════════════════════════════════════════════════════════
if $DO_RBAC; then
    section "RBAC cleanup"

    PROJECTS=(dev stage test prod)

    # Users first (they reference roles)
    info "Deleting users ..."
    for p in "${PROJECTS[@]}"; do
        del "user/${p}-admin" "${API}/security/users/${p}-admin"
        del "user/${p}-user"  "${API}/security/users/${p}-user"
    done

    # Roles
    info "Deleting roles ..."
    for p in "${PROJECTS[@]}"; do
        ROLE_ADMIN_ID=$(get_id "${API}/security/roles" "name" "${p}-admins")
        ROLE_USER_ID=$(get_id "${API}/security/roles"  "name" "${p}-users")
        [[ -n "${ROLE_ADMIN_ID}" ]] && del "role/${p}-admins" "${API}/security/roles/${ROLE_ADMIN_ID}"
        [[ -n "${ROLE_USER_ID}"  ]] && del "role/${p}-users"  "${API}/security/roles/${ROLE_USER_ID}"
    done

    # Privileges
    info "Deleting privileges ..."
    for p in "${PROJECTS[@]}"; do
        PRIV_READ_ID=$(get_id "${API}/security/privileges" "name" "${p}-read")
        PRIV_WRITE_ID=$(get_id "${API}/security/privileges" "name" "${p}-write")
        [[ -n "${PRIV_READ_ID}"  ]] && del "privilege/${p}-read"  "${API}/security/privileges/${PRIV_READ_ID}"
        [[ -n "${PRIV_WRITE_ID}" ]] && del "privilege/${p}-write" "${API}/security/privileges/${PRIV_WRITE_ID}"
    done

    # Content selectors
    info "Deleting content selectors ..."
    for p in "${PROJECTS[@]}"; do
        CS_ID=$(get_id "${API}/security/content-selectors" "name" "${p}-cs")
        [[ -n "${CS_ID}" ]] && del "content-selector/${p}-cs" "${API}/security/content-selectors/${CS_ID}"
    done

    ok "RBAC cleanup done"
fi

# ════════════════════════════════════════════════════════════════════════════
# Repository cleanup: all 42 repos created by seed-repos.sh
# ════════════════════════════════════════════════════════════════════════════
if $DO_REPOS; then
    section "Repository cleanup"

    REPOS=(
        # maven2
        maven-hosted maven-proxy maven-group
        # npm
        npm-hosted npm-proxy npm-group
        # pypi
        pypi-hosted pypi-proxy pypi-group
        # docker
        docker-dev docker-proxy docker-common
        # helm
        helm-hosted helm-proxy helm-charts
        # cargo
        cargo-hosted cargo-proxy cargo-group
        # conda
        conda-hosted conda-proxy conda-group
        # go
        go-hosted go-proxy go-group
        # nuget
        nuget-hosted nuget-proxy nuget-group
        # raw
        raw-artifacts raw-proxy raw-common
        # apt
        apt-hosted apt-proxy apt-group
        # yum
        yum-hosted yum-proxy yum-group
        # conan
        conan-hosted conan-proxy conan-group
        # terraform
        terraform-hosted terraform-proxy terraform-group
    )

    # Delete groups first (they reference members)
    info "Deleting group repositories first ..."
    for repo in "${REPOS[@]}"; do
        [[ "$repo" == *-group || "$repo" == *-common || "$repo" == *-charts ]] && \
            del "repo/${repo}" "${API}/repositories/${repo}"
    done

    # Then proxy and hosted
    info "Deleting proxy and hosted repositories ..."
    for repo in "${REPOS[@]}"; do
        [[ "$repo" != *-group && "$repo" != *-common && "$repo" != *-charts ]] && \
            del "repo/${repo}" "${API}/repositories/${repo}"
    done

    ok "Repository cleanup done"
fi

# ════════════════════════════════════════════════════════════════════════════
# Package cleanup: delete seed components from hosted repos
# ════════════════════════════════════════════════════════════════════════════
if $DO_PACKAGES; then
    section "Package cleanup (seed components)"

    info "Searching for seed components ..."

    # Get all components, filter those with 'seed' in name or group
    COMPONENT_IDS=$(curl -s "${AUTH[@]}" \
        "${API}/components?repository=raw-artifacts" \
        | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    items = d.get('items', d) if isinstance(d, dict) else d
    for c in items:
        if 'seed' in c.get('name','').lower() or 'seed' in c.get('group','').lower():
            print(c['id'])
except: pass
" 2>/dev/null)

    if [[ -n "${COMPONENT_IDS}" ]]; then
        while IFS= read -r cid; do
            [[ -z "$cid" ]] && continue
            del "component/${cid}" "${API}/components/${cid}"
        done <<< "${COMPONENT_IDS}"
    else
        warn "No seed components found in raw-artifacts (may already be cleaned up)"
    fi

    # Delete by known paths for other formats
    HOSTED_REPOS=(maven-hosted npm-hosted pypi-hosted helm-hosted go-hosted
                  nuget-hosted cargo-hosted apt-hosted yum-hosted
                  conda-hosted terraform-hosted)

    for repo in "${HOSTED_REPOS[@]}"; do
        IDS=$(curl -s "${AUTH[@]}" "${API}/components?repository=${repo}" \
            | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    items = d.get('items', d) if isinstance(d, dict) else d
    for c in items:
        if any(k in str(c).lower() for k in ['seed', 'example']):
            print(c['id'])
except: pass
" 2>/dev/null)
        if [[ -n "${IDS}" ]]; then
            while IFS= read -r cid; do
                [[ -z "$cid" ]] && continue
                del "component/${cid} (${repo})" "${API}/components/${cid}"
            done <<< "${IDS}"
        fi
    done

    ok "Package cleanup done"
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo
ok "Cleanup complete."
info "Blob stores (s3-primary, s3-secondary, default, docker) were NOT deleted."
info "To delete blob stores: Admin UI → Blob Stores → Delete"
