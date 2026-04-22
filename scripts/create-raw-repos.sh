#!/usr/bin/env bash
# create-raw-repos.sh — создаёт three Raw-репозитория: hosted, proxy, group.
#
# Использование:
#   ./scripts/create-raw-repos.sh
#   BASE_URL=http://192.168.1.10:8081 HOSTED_NAME=my-raw ./scripts/create-raw-repos.sh
#
# Переменные окружения (все с дефолтами):
#   BASE_URL        — URL сервера            (default: http://localhost:8081)
#   ADMIN_USER      — admin логин            (default: admin)
#   ADMIN_PASS      — admin пароль           (default: admin123)
#   HOSTED_NAME     — имя hosted-репо        (default: raw-artifacts)
#   PROXY_NAME      — имя proxy-репо         (default: raw-proxy)
#   GROUP_NAME      — имя group-репо         (default: raw-common)
#   PROXY_REMOTE    — remote URL для proxy   (default: https://example.com)
#   ALLOW_ANON_PUB  — allow_anonymous для proxy/group (default: true)
set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:8081}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"

HOSTED_NAME="${HOSTED_NAME:-raw-artifacts}"
PROXY_NAME="${PROXY_NAME:-raw-proxy}"
GROUP_NAME="${GROUP_NAME:-raw-common}"
PROXY_REMOTE="${PROXY_REMOTE:-https://example.com}"
ALLOW_ANON_PUB="${ALLOW_ANON_PUB:-true}"

API="${BASE_URL}/service/rest/v1/repositories"
AUTH=(-u "${ADMIN_USER}:${ADMIN_PASS}")

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'

ok()   { echo -e "${GREEN}[OK]${NC}    $*"; }
err()  { echo -e "${RED}[ERR]${NC}   $*"; }
info() { echo -e "${CYAN}[INFO]${NC}  $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC}  $*"; }

# Создаёт репозиторий; пропускает если уже существует (409).
# Аргументы: format type JSON-body
create_repo() {
    local format="$1" type="$2" body="$3"
    local http_code
    http_code=$(curl -s -o /tmp/nexspence_create.out -w "%{http_code}" \
        "${AUTH[@]}" \
        -X POST "${API}/${format}/${type}" \
        -H "Content-Type: application/json" \
        -d "${body}")
    local out; out=$(cat /tmp/nexspence_create.out)
    case "$http_code" in
        201) ok  "created  ${format}/${type} — ${out}" ;;
        409) warn "exists   ${format}/${type} — already exists, skipping" ;;
        *)   err "failed   ${format}/${type} HTTP ${http_code} — ${out}"; return 1 ;;
    esac
}

# ── Проверка доступности сервера ──────────────────────────────────────────────
info "Connecting to ${BASE_URL} …"
if ! curl -sf -o /dev/null "${AUTH[@]}" "${BASE_URL}/service/rest/v1/repositories"; then
    err "Cannot reach ${BASE_URL} — проверьте, что сервер запущен и credentials верные."
    exit 1
fi
info "Server OK"
echo

# ── 1. Hosted ─────────────────────────────────────────────────────────────────
info "Creating Raw HOSTED repository: ${HOSTED_NAME}"
create_repo raw hosted "$(cat <<JSON
{
  "name": "${HOSTED_NAME}",
  "online": true,
  "allowAnonymous": false,
  "description": "Raw hosted repository"
}
JSON
)"

# ── 2. Proxy ──────────────────────────────────────────────────────────────────
info "Creating Raw PROXY repository: ${PROXY_NAME} → ${PROXY_REMOTE}"
create_repo raw proxy "$(cat <<JSON
{
  "name": "${PROXY_NAME}",
  "online": true,
  "allowAnonymous": ${ALLOW_ANON_PUB},
  "description": "Raw proxy → ${PROXY_REMOTE}",
  "proxyConfig": {
    "remote_url": "${PROXY_REMOTE}"
  }
}
JSON
)"

# ── 3. Group ──────────────────────────────────────────────────────────────────
info "Creating Raw GROUP repository: ${GROUP_NAME} (members: ${HOSTED_NAME}, ${PROXY_NAME})"
create_repo raw group "$(cat <<JSON
{
  "name": "${GROUP_NAME}",
  "online": true,
  "allowAnonymous": ${ALLOW_ANON_PUB},
  "description": "Raw group (hosted + proxy)",
  "formatConfig": {
    "member_names": ["${HOSTED_NAME}", "${PROXY_NAME}"]
  }
}
JSON
)"

echo
info "Done. Репозитории доступны по адресам:"
echo "  hosted : ${BASE_URL}/repository/${HOSTED_NAME}/"
echo "  proxy  : ${BASE_URL}/repository/${PROXY_NAME}/"
echo "  group  : ${BASE_URL}/repository/${GROUP_NAME}/"
echo
info "Пример upload файла в hosted:"
echo "  curl -u ${ADMIN_USER}:${ADMIN_PASS} -T myfile.zip ${BASE_URL}/repository/${HOSTED_NAME}/releases/myfile.zip"
echo
info "Пример download через group:"
echo "  curl -u ${ADMIN_USER}:${ADMIN_PASS} -O ${BASE_URL}/repository/${GROUP_NAME}/releases/myfile.zip"
echo
info "Пример download специфичного пути:"
echo "  curl -u ${ADMIN_USER}:${ADMIN_PASS} ${BASE_URL}/repository/${GROUP_NAME}/config/app.yml"
echo
