#!/usr/bin/env bash
# seed-rbac.sh — creates RBAC structure for 4 environment projects: dev, stage, test, prod.
#
# Per project creates:
#   Content Selector  — CEL: format == "maven2" && path.startsWith("/com/<project>/")
#   Privilege read    — type repository-content-selector, actions: browse, read
#   Privilege write   — type repository-content-selector, actions: browse, read, write
#   Role admins       — includes privilege-write
#   Role users        — includes privilege-read
#   User <project>-admin  — password Admin2026!, role <project>-admins
#   User <project>-user   — password User2026!,  role <project>-users
#
# Environment variables:
#   BASE_URL        — server URL            (default: http://localhost:8080)
#   ADMIN_USER      — admin login           (default: admin)
#   ADMIN_PASS      — admin password        (default: admin123)
#   ADMIN_PASS_TPL  — password for *-admin users (default: Admin2026!)
#   USER_PASS_TPL   — password for *-user users  (default: User2026!)
set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"
ADMIN_PASS_TPL="${ADMIN_PASS_TPL:-Admin2026!}"
USER_PASS_TPL="${USER_PASS_TPL:-User2026!}"

AUTH=(-u "${ADMIN_USER}:${ADMIN_PASS}")
API="${BASE_URL}/service/rest/v1"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'
ok()      { echo -e "${GREEN}[OK]${NC}    $*"; }
err()     { echo -e "${RED}[ERR]${NC}   $*" >&2; }
info()    { echo -e "${CYAN}[INFO]${NC}  $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC}  $*"; }
section() { echo -e "\n${BOLD}${CYAN}══ project: $* ══${NC}"; }

FAILED=()
TMP_OUT=/tmp/nxs_rbac_$$.out

cleanup() { rm -f "$TMP_OUT"; }
trap cleanup EXIT

# post <label> <url> <body> — creates resource, stores response in $TMP_OUT
post() {
    local label="$1" url="$2" body="$3"
    local code
    code=$(curl -s -o "$TMP_OUT" -w "%{http_code}" \
        "${AUTH[@]}" -X POST "$url" -H "Content-Type: application/json" -d "$body")
    local out; out=$(cat "$TMP_OUT")
    case "$code" in
        200|201) ok  "${label}" ;;
        409)     warn "${label} — already exists, skipping" ;;
        400|500)
            if echo "$out" | grep -q "duplicate key"; then
                warn "${label} — already exists (duplicate key), skipping"
            else
                err "${label} — HTTP ${code}: ${out}"; FAILED+=("$label"); return 1
            fi ;;
        *)       err "${label} — HTTP ${code}: ${out}"; FAILED+=("$label"); return 1 ;;
    esac
}

# get_id <list-url> <name-field> <name-value> — returns id of matching item
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

capitalize() { echo "$(echo "${1:0:1}" | tr '[:lower:]' '[:upper:]')${1:1}"; }

# ── Server check ──────────────────────────────────────────────────────────────
info "Connecting to ${BASE_URL} ..."
if ! curl -sf -o /dev/null "${AUTH[@]}" "${BASE_URL}/service/rest/v1/repositories"; then
    err "Cannot reach ${BASE_URL}"
    exit 1
fi
info "Server OK"
echo

for PROJECT in dev stage test prod; do
    section "${PROJECT}"

    CS_NAME="${PROJECT}-cs"
    PRIV_READ="${PROJECT}-read"
    PRIV_WRITE="${PROJECT}-write"
    ROLE_ADMIN="${PROJECT}-admins"
    ROLE_USER="${PROJECT}-users"
    USER_ADMIN="${PROJECT}-admin"
    USER_MEMBER="${PROJECT}-user"
    PROJECT_CAP=$(capitalize "$PROJECT")

    # 1. Content Selector
    post "content-selector/${CS_NAME}" \
        "${API}/security/content-selectors" \
        "{\"name\":\"${CS_NAME}\",\"description\":\"Access to com.${PROJECT} Maven artifacts\",\"expression\":\"format == \\\"maven2\\\" && path.startsWith(\\\"\/com\/${PROJECT}\/\\\")\"}"

    CS_ID=$(get_id "${API}/security/content-selectors" "name" "${CS_NAME}")
    info "  content-selector id: ${CS_ID:-<unknown>}"

    if [[ -z "${CS_ID}" ]]; then
        err "Cannot find content selector '${CS_NAME}' — skipping ${PROJECT}"
        FAILED+=("cs/${PROJECT}")
        continue
    fi

    # 2. Privilege read
    post "privilege/${PRIV_READ}" \
        "${API}/security/privileges" \
        "{\"name\":\"${PRIV_READ}\",\"description\":\"Read access for ${PROJECT} environment\",\"type\":\"repository-content-selector\",\"contentSelectorId\":\"${CS_ID}\",\"attrs\":{\"actions\":[\"browse\",\"read\"]}}"

    # 3. Privilege write
    post "privilege/${PRIV_WRITE}" \
        "${API}/security/privileges" \
        "{\"name\":\"${PRIV_WRITE}\",\"description\":\"Write access for ${PROJECT} environment\",\"type\":\"repository-content-selector\",\"contentSelectorId\":\"${CS_ID}\",\"attrs\":{\"actions\":[\"browse\",\"read\",\"write\"]}}"

    PRIV_READ_ID=$(get_id "${API}/security/privileges" "name" "${PRIV_READ}")
    PRIV_WRITE_ID=$(get_id "${API}/security/privileges" "name" "${PRIV_WRITE}")
    info "  privilege-read id:  ${PRIV_READ_ID:-<unknown>}"
    info "  privilege-write id: ${PRIV_WRITE_ID:-<unknown>}"

    # 4. Role admins (write privilege)
    post "role/${ROLE_ADMIN}" \
        "${API}/security/roles" \
        "{\"name\":\"${ROLE_ADMIN}\",\"description\":\"Admins for ${PROJECT} environment\",\"privileges\":[\"${PRIV_WRITE_ID}\"]}"

    # 5. Role users (read privilege)
    post "role/${ROLE_USER}" \
        "${API}/security/roles" \
        "{\"name\":\"${ROLE_USER}\",\"description\":\"Users for ${PROJECT} environment\",\"privileges\":[\"${PRIV_READ_ID}\"]}"

    ROLE_ADMIN_ID=$(get_id "${API}/security/roles" "name" "${ROLE_ADMIN}")
    ROLE_USER_ID=$(get_id "${API}/security/roles" "name" "${ROLE_USER}")
    info "  role-admins id: ${ROLE_ADMIN_ID:-<unknown>}"
    info "  role-users id:  ${ROLE_USER_ID:-<unknown>}"

    # 6. User admin
    post "user/${USER_ADMIN}" \
        "${API}/security/users" \
        "{\"userId\":\"${USER_ADMIN}\",\"firstName\":\"${PROJECT_CAP}\",\"lastName\":\"Admin\",\"emailAddress\":\"${USER_ADMIN}@example.com\",\"password\":\"${ADMIN_PASS_TPL}\",\"status\":\"active\",\"roles\":[\"${ROLE_ADMIN_ID}\"]}"

    # 7. User member
    post "user/${USER_MEMBER}" \
        "${API}/security/users" \
        "{\"userId\":\"${USER_MEMBER}\",\"firstName\":\"${PROJECT_CAP}\",\"lastName\":\"User\",\"emailAddress\":\"${USER_MEMBER}@example.com\",\"password\":\"${USER_PASS_TPL}\",\"status\":\"active\",\"roles\":[\"${ROLE_USER_ID}\"]}"

    ok "Project ${PROJECT} done: CS + 2 privileges + 2 roles + 2 users"
done

# ── Summary ───────────────────────────────────────────────────────────────────
echo
if [[ ${#FAILED[@]} -eq 0 ]]; then
    ok "RBAC seeded for all 4 environments: dev, stage, test, prod"
    info "Users created:"
    for p in dev stage test prod; do
        printf "  %-16s / %-12s  (role: %s)\n" "${p}-admin" "${ADMIN_PASS_TPL}" "${p}-admins"
        printf "  %-16s / %-12s  (role: %s)\n" "${p}-user"  "${USER_PASS_TPL}"  "${p}-users"
    done
    echo
    info "To clean up all RBAC objects: ./scripts/seed-cleanup.sh --rbac"
else
    err "Failed items: ${FAILED[*]}"
    exit 1
fi
