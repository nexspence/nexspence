#!/usr/bin/env bash
# create-huggingface-repos.sh — creates three Hugging Face repositories: hosted, proxy, group.
#
# Usage:
#   ./scripts/create-huggingface-repos.sh
#   BASE_URL=http://192.168.1.10:8081 ./scripts/create-huggingface-repos.sh
#   BLOB_STORE=s3-secondary ./scripts/create-huggingface-repos.sh
#
# Environment variables (all with defaults):
#   BASE_URL        — server URL                      (default: http://localhost:8081)
#   ADMIN_USER      — admin login                     (default: admin)
#   ADMIN_PASS      — admin password                  (default: admin123)
#   HOSTED_NAME     — hosted repo name                (default: huggingface-hosted)
#   PROXY_NAME      — proxy repo name                 (default: huggingface-proxy)
#   GROUP_NAME      — group repo name                 (default: huggingface-group)
#   PROXY_REMOTE    — remote URL for proxy            (default: https://huggingface.co)
#   ALLOW_ANON_PUB  — allow_anonymous for proxy/group (default: true)
#   BLOB_STORE      — blob store name for hosted+proxy (default: default)
set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:8081}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"

HOSTED_NAME="${HOSTED_NAME:-huggingface-hosted}"
PROXY_NAME="${PROXY_NAME:-huggingface-proxy}"
GROUP_NAME="${GROUP_NAME:-huggingface-group}"
PROXY_REMOTE="${PROXY_REMOTE:-https://huggingface.co}"
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

resolve_blob_store_id() {
    local name="$1"
    local http_code body
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
        id=$(echo "${body}" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p' | head -1)
    fi
    if [[ -z "${id}" ]]; then
        err "could not parse id from blob store response: ${body}" >&2
        return 1
    fi
    echo "${id}"
}

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
        201) ok  "created  ${format}/${type} — $(echo "$out" | sed 's/.*"name":"\([^"]*\)".*/\1/')" ;;
        409) warn "exists   ${format}/${type} — already exists, skipping" ;;
        *)   err "failed   ${format}/${type} HTTP ${http_code} — ${out}"; return 1 ;;
    esac
}

info "Connecting to ${BASE_URL} …"
if ! curl -sf -o /dev/null "${AUTH[@]}" "${BASE_URL}/service/rest/v1/repositories"; then
    err "Cannot reach ${BASE_URL} — verify that the server is running and credentials are correct."
    exit 1
fi
info "Server OK"

info "Resolving blob store '${BLOB_STORE}' …"
if ! BLOB_STORE_ID=$(resolve_blob_store_id "${BLOB_STORE}"); then
    err "Aborting — blob store '${BLOB_STORE}' must exist before repos can reference it."
    exit 1
fi
info "blob store ${BLOB_STORE} → ${BLOB_STORE_ID}"
echo

info "Creating Hugging Face HOSTED repository: ${HOSTED_NAME} (blob store: ${BLOB_STORE})"
create_repo huggingface hosted "$(cat <<JSON
{
  "name": "${HOSTED_NAME}",
  "online": true,
  "allowAnonymous": false,
  "description": "Hugging Face hosted repository (models, datasets, spaces)",
  "blobStoreId": "${BLOB_STORE_ID}"
}
JSON
)"

info "Creating Hugging Face PROXY repository: ${PROXY_NAME} → ${PROXY_REMOTE} (blob store: ${BLOB_STORE})"
create_repo huggingface proxy "$(cat <<JSON
{
  "name": "${PROXY_NAME}",
  "online": true,
  "allowAnonymous": ${ALLOW_ANON_PUB},
  "description": "Hugging Face proxy → ${PROXY_REMOTE}",
  "blobStoreId": "${BLOB_STORE_ID}",
  "proxyConfig": {
    "remote_url": "${PROXY_REMOTE}"
  }
}
JSON
)"

info "Creating Hugging Face GROUP repository: ${GROUP_NAME} (members: ${HOSTED_NAME}, ${PROXY_NAME})"
create_repo huggingface group "$(cat <<JSON
{
  "name": "${GROUP_NAME}",
  "online": true,
  "allowAnonymous": ${ALLOW_ANON_PUB},
  "description": "Hugging Face group (hosted + proxy)",
  "formatConfig": {
    "member_names": ["${HOSTED_NAME}", "${PROXY_NAME}"]
  }
}
JSON
)"

echo
info "Done. Repositories available at:"
echo "  hosted : ${BASE_URL}/repository/${HOSTED_NAME}/"
echo "  proxy  : ${BASE_URL}/repository/${PROXY_NAME}/"
echo "  group  : ${BASE_URL}/repository/${GROUP_NAME}/"
echo
info "Example: point a client at the hosted repository"
echo "  export HF_ENDPOINT=${BASE_URL}/repository/${HOSTED_NAME}"
echo
info "Example: publish a model file to hosted (a per-file PUT at the download URL)"
echo "  curl -u ${ADMIN_USER}:${ADMIN_PASS} -X PUT ${BASE_URL}/repository/${HOSTED_NAME}/my-model/resolve/main/config.json --upload-file config.json"
