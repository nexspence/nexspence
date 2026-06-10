#!/usr/bin/env bash
# create-terraform-repos.sh — creates three Terraform Registry repositories: hosted, proxy, group.
#
# Usage:
#   ./scripts/create-terraform-repos.sh
#   BASE_URL=http://192.168.1.10:8080 ./scripts/create-terraform-repos.sh
#   BLOB_STORE=s3-secondary ./scripts/create-terraform-repos.sh
#
# Environment variables (all with defaults):
#   BASE_URL        — server URL                      (default: http://localhost:8080)
#   ADMIN_USER      — admin login                     (default: admin)
#   ADMIN_PASS      — admin password                  (default: admin123)
#   HOSTED_NAME     — hosted repo name                (default: terraform-hosted)
#   PROXY_NAME      — proxy repo name                 (default: terraform-proxy)
#   GROUP_NAME      — group repo name                 (default: terraform-group)
#   PROXY_REMOTE    — remote URL for proxy            (default: https://registry.terraform.io)
#   ALLOW_ANON_PUB  — allow_anonymous for proxy/group (default: true)
#   BLOB_STORE      — blob store name for hosted+proxy (default: default)
set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"

HOSTED_NAME="${HOSTED_NAME:-terraform-hosted}"
PROXY_NAME="${PROXY_NAME:-terraform-proxy}"
GROUP_NAME="${GROUP_NAME:-terraform-group}"
PROXY_REMOTE="${PROXY_REMOTE:-https://registry.terraform.io}"
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

info "Creating Terraform HOSTED repository: ${HOSTED_NAME} (blob store: ${BLOB_STORE})"
create_repo terraform hosted "$(cat <<JSON
{
  "name": "${HOSTED_NAME}",
  "online": true,
  "allowAnonymous": false,
  "description": "Terraform Registry hosted repository",
  "blobStoreId": "${BLOB_STORE_ID}"
}
JSON
)"

info "Creating Terraform PROXY repository: ${PROXY_NAME} → ${PROXY_REMOTE} (blob store: ${BLOB_STORE})"
create_repo terraform proxy "$(cat <<JSON
{
  "name": "${PROXY_NAME}",
  "online": true,
  "allowAnonymous": ${ALLOW_ANON_PUB},
  "description": "Terraform proxy → ${PROXY_REMOTE}",
  "blobStoreId": "${BLOB_STORE_ID}",
  "proxyConfig": {
    "remote_url": "${PROXY_REMOTE}"
  }
}
JSON
)"

info "Creating Terraform GROUP repository: ${GROUP_NAME} (members: ${HOSTED_NAME}, ${PROXY_NAME})"
create_repo terraform group "$(cat <<JSON
{
  "name": "${GROUP_NAME}",
  "online": true,
  "allowAnonymous": ${ALLOW_ANON_PUB},
  "description": "Terraform group (hosted + proxy)",
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
info "Service discovery: ${BASE_URL}/repository/${GROUP_NAME}/.well-known/terraform.json"
echo
info "Example: configure terraform to use group registry"
echo "  # terraform.tf"
echo "  terraform {"
echo "    required_providers {"
echo "      aws = {"
echo "        source = \"${BASE_URL#http://}/repository/${GROUP_NAME}/hashicorp/aws\""
echo "      }"
echo "    }"
echo "  }"
