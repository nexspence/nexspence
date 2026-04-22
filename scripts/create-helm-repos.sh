#!/usr/bin/env bash
# create-helm-repos.sh — creates three Helm chart repositories: hosted, proxy, group.
#
# Usage:
#   ./scripts/create-helm-repos.sh
#   BASE_URL=http://192.168.1.10:8081 HOSTED_NAME=my-helm ./scripts/create-helm-repos.sh
#
# Environment variables (all with defaults):
#   BASE_URL        — server URL                (default: http://localhost:8081)
#   ADMIN_USER      — admin login               (default: admin)
#   ADMIN_PASS      — admin password            (default: admin123)
#   HOSTED_NAME     — hosted repo name          (default: helm-hosted)
#   PROXY_NAME      — proxy repo name           (default: helm-proxy)
#   GROUP_NAME      — group repo name           (default: helm-charts)
#   PROXY_REMOTE    — remote URL for proxy      (default: https://charts.helm.sh/stable)
#   ALLOW_ANON_PUB  — allow_anonymous for proxy/group (default: true)
set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:8081}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"

HOSTED_NAME="${HOSTED_NAME:-helm-hosted}"
PROXY_NAME="${PROXY_NAME:-helm-proxy}"
GROUP_NAME="${GROUP_NAME:-helm-charts}"
PROXY_REMOTE="${PROXY_REMOTE:-https://charts.helm.sh/stable}"
ALLOW_ANON_PUB="${ALLOW_ANON_PUB:-true}"

API="${BASE_URL}/service/rest/v1/repositories"
AUTH=(-u "${ADMIN_USER}:${ADMIN_PASS}")

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'

ok()   { echo -e "${GREEN}[OK]${NC}    $*"; }
err()  { echo -e "${RED}[ERR]${NC}   $*"; }
info() { echo -e "${CYAN}[INFO]${NC}  $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC}  $*"; }

# Creates a repository; skips if already exists (409).
# Arguments: format type JSON-body
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

# ── Server reachability check ─────────────────────────────────────────────────────
info "Connecting to ${BASE_URL} …"
if ! curl -sf -o /dev/null "${AUTH[@]}" "${BASE_URL}/service/rest/v1/repositories"; then
    err "Cannot reach ${BASE_URL} — verify that the server is running and credentials are correct."
    exit 1
fi
info "Server OK"
echo

# ── 1. Hosted ─────────────────────────────────────────────────────────────────────
info "Creating Helm HOSTED repository: ${HOSTED_NAME}"
create_repo helm hosted "$(cat <<JSON
{
  "name": "${HOSTED_NAME}",
  "online": true,
  "allowAnonymous": false,
  "description": "Helm hosted repository"
}
JSON
)"

# ── 2. Proxy ──────────────────────────────────────────────────────────────────────
info "Creating Helm PROXY repository: ${PROXY_NAME} → ${PROXY_REMOTE}"
create_repo helm proxy "$(cat <<JSON
{
  "name": "${PROXY_NAME}",
  "online": true,
  "allowAnonymous": ${ALLOW_ANON_PUB},
  "description": "Helm proxy → ${PROXY_REMOTE}",
  "proxyConfig": {
    "remote_url": "${PROXY_REMOTE}"
  }
}
JSON
)"

# ── 3. Group ──────────────────────────────────────────────────────────────────────
info "Creating Helm GROUP repository: ${GROUP_NAME} (members: ${HOSTED_NAME}, ${PROXY_NAME})"
create_repo helm group "$(cat <<JSON
{
  "name": "${GROUP_NAME}",
  "online": true,
  "allowAnonymous": ${ALLOW_ANON_PUB},
  "description": "Helm group (hosted + proxy)",
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
info "Add Nexspense repo to Helm:"
echo "  helm repo add nexspense ${BASE_URL}/repository/${GROUP_NAME}"
echo "  helm repo update"
echo
info "Search for charts:"
echo "  helm search repo nexspense/"
echo
info "Install a chart from group:"
echo "  helm install my-ingress nexspense/nginx-ingress --version 4.11.3"
echo "  helm install cert-manager nexspense/cert-manager --version v1.14.4"
echo "  helm install redis nexspense/redis --version 19.0.1"
echo
info "Upload a chart to hosted (curl):"
echo "  curl -u ${ADMIN_USER}:${ADMIN_PASS} -F \"chart=@mychart-0.1.0.tgz\" ${BASE_URL}/repository/${HOSTED_NAME}/"
echo
info "Download a chart manually:"
echo "  curl -u ${ADMIN_USER}:${ADMIN_PASS} -O ${BASE_URL}/repository/${GROUP_NAME}/mychart-0.1.0.tgz"
echo
