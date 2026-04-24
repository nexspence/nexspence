#!/usr/bin/env bash
# create-docker-repos.sh — создаёт three Docker-репозитория: hosted, proxy, group.
#
# Использование:
#   ./scripts/create-docker-repos.sh
#   BASE_URL=http://192.168.1.10:8081 HOSTED_NAME=my-docker ./scripts/create-docker-repos.sh
#   BLOB_STORE=s3-prod ./scripts/create-docker-repos.sh     # положить всё в стор "s3-prod"
#
# Переменные окружения (все с дефолтами):
#   BASE_URL        — URL сервера                    (default: http://localhost:8081)
#   ADMIN_USER      — admin логин                    (default: admin)
#   ADMIN_PASS      — admin пароль                   (default: admin123)
#   HOSTED_NAME     — имя hosted-репо                (default: docker-hosted)
#   PROXY_NAME      — имя proxy-репо                 (default: docker-proxy)
#   GROUP_NAME      — имя group-репо                 (default: docker-group)
#   PROXY_REMOTE    — remote URL для proxy           (default: https://registry-1.docker.io)
#   ALLOW_ANON_PUB  — allow_anonymous для proxy/group (default: true)
#   BLOB_STORE      — имя blob store для hosted+proxy (default: default)
#                     группа не использует storage, ей blob store не задаётся.
set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:8081}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"

HOSTED_NAME="${HOSTED_NAME:-docker-dev}"
PROXY_NAME="${PROXY_NAME:-docker-proxy}"
GROUP_NAME="${GROUP_NAME:-docker-common}"
PROXY_REMOTE="${PROXY_REMOTE:-https://registry-1.docker.io}"
ALLOW_ANON_PUB="${ALLOW_ANON_PUB:-true}"
BLOB_STORE="${BLOB_STORE:-default}"

API="${BASE_URL}/service/rest/v1/repositories"
BLOBS_API="${BASE_URL}/service/rest/v1/blobstores"
AUTH=(-u "${ADMIN_USER}:${ADMIN_PASS}")

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'

ok()   { echo -e "${GREEN}[OK]${NC}    $*"; }
err()  { echo -e "${RED}[ERR]${NC}   $*"; }
info() { echo -e "${CYAN}[INFO]${NC}  $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC}  $*"; }

# Резолвит имя blob store в UUID через GET /service/rest/v1/blobstores/:name.
# Использует jq если доступен, иначе — портативное извлечение первого "id":"…"
# поля из JSON. Печатает UUID в stdout, возвращает 1 если стор не найден.
resolve_blob_store_id() {
    local name="$1"
    local http_code body
    # Caller captures our stdout for the UUID — direct any error output to stderr.
    http_code=$(curl -s -o /tmp/nexspence_bs.out -w "%{http_code}" \
        "${AUTH[@]}" "${BLOBS_API}/${name}")
    body=$(cat /tmp/nexspence_bs.out)
    if [[ "$http_code" != "200" ]]; then
        err "blob store '${name}' not found (HTTP ${http_code}): ${body}" >&2
        return 1
    fi
    local id
    if command -v jq >/dev/null 2>&1; then
        id=$(echo "${body}" | jq -r '.id // empty')
    else
        # portable fallback — first "id":"<uuid>" in the object
        id=$(echo "${body}" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p' | head -1)
    fi
    if [[ -z "${id}" ]]; then
        err "could not parse id from blob store response: ${body}" >&2
        return 1
    fi
    echo "${id}"
}

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

# ── Blob store resolution ─────────────────────────────────────────────────────
info "Resolving blob store '${BLOB_STORE}' …"
if ! BLOB_STORE_ID=$(resolve_blob_store_id "${BLOB_STORE}"); then
    err "Aborting — blob store '${BLOB_STORE}' must exist before repos can reference it."
    err "Create it via UI (Admin → Blob Stores) or POST /service/rest/v1/blobstores/<type>."
    exit 1
fi
info "blob store ${BLOB_STORE} → ${BLOB_STORE_ID}"
echo

# ── 1. Hosted ─────────────────────────────────────────────────────────────────
info "Creating Docker HOSTED repository: ${HOSTED_NAME} (blob store: ${BLOB_STORE})"
create_repo docker hosted "$(cat <<JSON
{
  "name": "${HOSTED_NAME}",
  "online": true,
  "allowAnonymous": false,
  "description": "Docker hosted repository",
  "blobStoreId": "${BLOB_STORE_ID}"
}
JSON
)"

# ── 2. Proxy ──────────────────────────────────────────────────────────────────
info "Creating Docker PROXY repository: ${PROXY_NAME} → ${PROXY_REMOTE} (blob store: ${BLOB_STORE})"
create_repo docker proxy "$(cat <<JSON
{
  "name": "${PROXY_NAME}",
  "online": true,
  "allowAnonymous": ${ALLOW_ANON_PUB},
  "description": "Docker proxy → ${PROXY_REMOTE}",
  "blobStoreId": "${BLOB_STORE_ID}",
  "proxyConfig": {
    "remote_url": "${PROXY_REMOTE}"
  }
}
JSON
)"

# ── 3. Group ──────────────────────────────────────────────────────────────────
info "Creating Docker GROUP repository: ${GROUP_NAME} (members: ${HOSTED_NAME}, ${PROXY_NAME})"
create_repo docker group "$(cat <<JSON
{
  "name": "${GROUP_NAME}",
  "online": true,
  "allowAnonymous": ${ALLOW_ANON_PUB},
  "description": "Docker group (hosted + proxy)",
  "formatConfig": {
    "member_names": ["${HOSTED_NAME}", "${PROXY_NAME}"]
  }
}
JSON
)"

echo
info "Done. Репозитории доступны по адресам:"
echo "  hosted : ${BASE_URL}/v2/${HOSTED_NAME}/"
echo "  proxy  : ${BASE_URL}/v2/${PROXY_NAME}/"
echo "  group  : ${BASE_URL}/v2/${GROUP_NAME}/"
echo
info "Пример push в hosted:"
echo "  docker tag alpine:3 ${BASE_URL#http://}/${HOSTED_NAME}/alpine:3"
echo "  docker push ${BASE_URL#http://}/${HOSTED_NAME}/alpine:3"
echo
info "Пример pull через group:"
echo "  docker pull ${BASE_URL#http://}/${GROUP_NAME}/alpine:3"
