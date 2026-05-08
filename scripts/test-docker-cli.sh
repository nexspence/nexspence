#!/usr/bin/env bash
# Docker repository RBAC test — docker CLI
# Tests: hosted (push/pull/tags/manifest/delete), proxy (pull/push→405), group (pull/push→405)
# Three users: admin (full), user-b (da/dev/* only), user-c (da/prod/* only)
set -uo pipefail

# ── Configuration ──────────────────────────────────────────────────────────
REGISTRY="${REGISTRY:-localhost:8081}"
REPO_HOSTED="${REPO_HOSTED:-docker}"
REPO_PROXY="${REPO_PROXY:-dockerproxy}"
REPO_GROUP="${REPO_GROUP:-dockergroup}"

ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"

USER_B="svcdevops"
USER_B_PASS="7C76QA4x5nkf"    # scope: da/dev/*

USER_C="user4tfsdr"
USER_C_PASS="Hyh6qq8esXy15PjOZz2b"    # scope: da/prod/*

SCOPE_B="${SCOPE_B:-da/dev}"
SCOPE_C="${SCOPE_C:-da/prod}"

SOURCE_IMAGE="alpine:3"
TAGS=("v1.0" "v2.0" "latest")

# ── Colours & counters ─────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

PASS=0; FAIL=0; DENY=0

pass()    { PASS=$((PASS+1));   echo -e "${GREEN}[PASS]${NC}  $*"; }
fail()    { FAIL=$((FAIL+1));   echo -e "${RED}[FAIL]${NC}  $*"; }
deny()    { DENY=$((DENY+1));   echo -e "${YELLOW}[DENY]${NC}  $*"; }
info()    { echo -e "${CYAN}[INFO]${NC}  $*"; }
section() { echo -e "\n${BOLD}━━━ $* ━━━${NC}"; }

# Run cmd; pass if exit=0
expect_pass() {
    local label="$1"; shift
    if "$@" >/dev/null 2>&1; then
        pass "$label"
    else
        fail "$label (expected success, got error)"
    fi
}

# Run cmd; pass if exit≠0 (denial expected)
expect_deny() {
    local label="$1"; shift
    if "$@" >/dev/null 2>&1; then
        fail "$label (expected denial, but succeeded)"
    else
        deny "$label — denied as expected ✓"
    fi
}

# ── Helpers ────────────────────────────────────────────────────────────────
dlogin() {
    local user="$1" pass="$2"
    echo "$pass" | docker login --username "$user" --password-stdin "$REGISTRY" >/dev/null 2>&1
}

# curl GET tags/list, returns JSON
tags_list() {
    local user="$1" pass="$2" repo="$3" image="$4"
    curl -sf -u "$user:$pass" \
        "http://$REGISTRY/v2/$repo/$image/tags/list" 2>/dev/null
}

# curl HEAD /v2/, returns HTTP status code
v2_ping() {
    local user="$1" pass="$2"
    curl -so /dev/null -w "%{http_code}" -u "$user:$pass" \
        "http://$REGISTRY/v2/" 2>/dev/null
}

# curl GET tags/list, returns HTTP status code only
tags_status() {
    local user="$1" pass="$2" repo="$3" image="$4"
    curl -so /dev/null -w "%{http_code}" -u "$user:$pass" \
        "http://$REGISTRY/v2/$repo/$image/tags/list" 2>/dev/null
}

# curl GET manifest, returns HTTP status code
manifest_status() {
    local user="$1" pass="$2" repo="$3" image="$4" ref="$5"
    curl -so /dev/null -w "%{http_code}" -u "$user:$pass" \
        -H "Accept: application/vnd.docker.distribution.manifest.v2+json" \
        "http://$REGISTRY/v2/$repo/$image/manifests/$ref" 2>/dev/null
}

# curl DELETE manifest, returns HTTP status code
delete_manifest() {
    local user="$1" pass="$2" repo="$3" image="$4" ref="$5"
    curl -so /dev/null -w "%{http_code}" -u "$user:$pass" -X DELETE \
        "http://$REGISTRY/v2/$repo/$image/manifests/$ref" 2>/dev/null
}

# curl POST blobs/uploads to proxy/group, returns HTTP status code (expect 405)
push_initiate_status() {
    local user="$1" pass="$2" repo="$3" image="$4"
    curl -so /dev/null -w "%{http_code}" -u "$user:$pass" -X POST \
        "http://$REGISTRY/v2/$repo/$image/blobs/uploads/" 2>/dev/null
}

# ── Prerequisite checks ────────────────────────────────────────────────────
section "Prerequisites"

command -v docker >/dev/null 2>&1 || { echo "ERROR: docker not found in PATH"; exit 1; }
command -v curl   >/dev/null 2>&1 || { echo "ERROR: curl not found in PATH"; exit 1; }

status=$(v2_ping "$ADMIN_USER" "$ADMIN_PASS")
if [[ "$status" != "200" ]]; then
    echo "ERROR: registry $REGISTRY not reachable or admin credentials wrong (got HTTP $status)"
    exit 1
fi
info "Registry $REGISTRY reachable, admin auth OK"

# ── Setup ──────────────────────────────────────────────────────────────────
section "Setup — pull source image"

info "Pulling $SOURCE_IMAGE from Docker Hub (needed once locally)..."
docker pull "$SOURCE_IMAGE" >/dev/null 2>&1 || {
    echo "ERROR: cannot pull $SOURCE_IMAGE — check internet access"
    exit 1
}
info "Source image ready: $SOURCE_IMAGE"

# Pre-tag all test images
for tag in "${TAGS[@]}"; do
    docker tag "$SOURCE_IMAGE" "$REGISTRY/$REPO_HOSTED/$SCOPE_B/alpine:$tag" 2>/dev/null
    docker tag "$SOURCE_IMAGE" "$REGISTRY/$REPO_HOSTED/$SCOPE_C/alpine:$tag" 2>/dev/null
done
info "Tagged images for $SCOPE_B and $SCOPE_C"

# ── HOSTED: Admin ──────────────────────────────────────────────────────────
section "Hosted — Admin"

dlogin "$ADMIN_USER" "$ADMIN_PASS"

for tag in "${TAGS[@]}"; do
    expect_pass "admin: push $SCOPE_B/alpine:$tag" \
        docker push "$REGISTRY/$REPO_HOSTED/$SCOPE_B/alpine:$tag"
    expect_pass "admin: push $SCOPE_C/alpine:$tag" \
        docker push "$REGISTRY/$REPO_HOSTED/$SCOPE_C/alpine:$tag"
done

expect_pass "admin: pull $SCOPE_B/alpine:v1.0" \
    docker pull "$REGISTRY/$REPO_HOSTED/$SCOPE_B/alpine:v1.0"

# tags/list (curl — docker CLI has no native tags list)
result=$(tags_list "$ADMIN_USER" "$ADMIN_PASS" "$REPO_HOSTED" "$SCOPE_B/alpine")
if echo "$result" | grep -q '"tags"'; then
    pass "admin: tags/list $SCOPE_B/alpine → tags found"
else
    fail "admin: tags/list $SCOPE_B/alpine → unexpected response: $result"
fi

# manifest inspect via curl
st=$(manifest_status "$ADMIN_USER" "$ADMIN_PASS" "$REPO_HOSTED" "$SCOPE_B/alpine" "v1.0")
[[ "$st" == "200" ]] && pass "admin: manifest GET $SCOPE_B/alpine:v1.0 → 200" \
                      || fail "admin: manifest GET $SCOPE_B/alpine:v1.0 → $st (expected 200)"

# manifest inspect by digest
DIGEST=$(curl -sI -u "$ADMIN_USER:$ADMIN_PASS" \
    -H "Accept: application/vnd.docker.distribution.manifest.v2+json" \
    "http://$REGISTRY/v2/$REPO_HOSTED/$SCOPE_B/alpine/manifests/v1.0" \
    | grep -i "^docker-content-digest:" | tr -d '\r' | awk '{print $2}')
if [[ -n "$DIGEST" ]]; then
    st=$(manifest_status "$ADMIN_USER" "$ADMIN_PASS" "$REPO_HOSTED" "$SCOPE_B/alpine" "$DIGEST")
    [[ "$st" == "200" ]] && pass "admin: manifest GET by digest ($DIGEST) → 200" \
                          || fail "admin: manifest GET by digest → $st (expected 200)"
else
    fail "admin: could not obtain Docker-Content-Digest for $SCOPE_B/alpine:v1.0"
fi

# ── HOSTED: User B (da/dev allowed, da/prod denied) ─────────────────────────
section "Hosted — User B (scope: $SCOPE_B/*)"

dlogin "$USER_B" "$USER_B_PASS"

# Allowed: da/dev
expect_pass "user-b: push $SCOPE_B/alpine:v2.0" \
    docker push "$REGISTRY/$REPO_HOSTED/$SCOPE_B/alpine:v2.0"

expect_pass "user-b: pull $SCOPE_B/alpine:v1.0" \
    docker pull "$REGISTRY/$REPO_HOSTED/$SCOPE_B/alpine:v1.0"

st=$(tags_status "$USER_B" "$USER_B_PASS" "$REPO_HOSTED" "$SCOPE_B/alpine")
[[ "$st" == "200" ]] && pass "user-b: tags/list $SCOPE_B/alpine → 200" \
                      || fail "user-b: tags/list $SCOPE_B/alpine → $st (expected 200)"

st=$(manifest_status "$USER_B" "$USER_B_PASS" "$REPO_HOSTED" "$SCOPE_B/alpine" "v1.0")
[[ "$st" == "200" ]] && pass "user-b: manifest GET $SCOPE_B/alpine:v1.0 → 200" \
                      || fail "user-b: manifest GET $SCOPE_B/alpine:v1.0 → $st (expected 200)"

# Denied: da/prod
expect_deny "user-b: push $SCOPE_C/alpine:v2.0 (should be denied)" \
    docker push "$REGISTRY/$REPO_HOSTED/$SCOPE_C/alpine:v2.0"

expect_deny "user-b: pull $SCOPE_C/alpine:v1.0 (should be denied)" \
    docker pull "$REGISTRY/$REPO_HOSTED/$SCOPE_C/alpine:v1.0"

st=$(tags_status "$USER_B" "$USER_B_PASS" "$REPO_HOSTED" "$SCOPE_C/alpine")
[[ "$st" == "401" || "$st" == "403" ]] \
    && deny "user-b: tags/list $SCOPE_C/alpine → $st (denied as expected ✓)" \
    || fail "user-b: tags/list $SCOPE_C/alpine → $st (expected 401/403)"

st=$(manifest_status "$USER_B" "$USER_B_PASS" "$REPO_HOSTED" "$SCOPE_C/alpine" "v1.0")
[[ "$st" == "401" || "$st" == "403" ]] \
    && deny "user-b: manifest GET $SCOPE_C/alpine:v1.0 → $st (denied as expected ✓)" \
    || fail "user-b: manifest GET $SCOPE_C/alpine:v1.0 → $st (expected 401/403)"

# ── HOSTED: User C (da/prod allowed, da/dev denied) ─────────────────────────
section "Hosted — User C (scope: $SCOPE_C/*)"

dlogin "$USER_C" "$USER_C_PASS"

# Allowed: da/prod
expect_pass "user-c: push $SCOPE_C/alpine:v2.0" \
    docker push "$REGISTRY/$REPO_HOSTED/$SCOPE_C/alpine:v2.0"

expect_pass "user-c: pull $SCOPE_C/alpine:v1.0" \
    docker pull "$REGISTRY/$REPO_HOSTED/$SCOPE_C/alpine:v1.0"

st=$(tags_status "$USER_C" "$USER_C_PASS" "$REPO_HOSTED" "$SCOPE_C/alpine")
[[ "$st" == "200" ]] && pass "user-c: tags/list $SCOPE_C/alpine → 200" \
                      || fail "user-c: tags/list $SCOPE_C/alpine → $st (expected 200)"

st=$(manifest_status "$USER_C" "$USER_C_PASS" "$REPO_HOSTED" "$SCOPE_C/alpine" "v1.0")
[[ "$st" == "200" ]] && pass "user-c: manifest GET $SCOPE_C/alpine:v1.0 → 200" \
                      || fail "user-c: manifest GET $SCOPE_C/alpine:v1.0 → $st (expected 200)"

# Denied: da/dev
expect_deny "user-c: push $SCOPE_B/alpine:v2.0 (should be denied)" \
    docker push "$REGISTRY/$REPO_HOSTED/$SCOPE_B/alpine:v2.0"

expect_deny "user-c: pull $SCOPE_B/alpine:v1.0 (should be denied)" \
    docker pull "$REGISTRY/$REPO_HOSTED/$SCOPE_B/alpine:v1.0"

st=$(tags_status "$USER_C" "$USER_C_PASS" "$REPO_HOSTED" "$SCOPE_B/alpine")
[[ "$st" == "401" || "$st" == "403" ]] \
    && deny "user-c: tags/list $SCOPE_B/alpine → $st (denied as expected ✓)" \
    || fail "user-c: tags/list $SCOPE_B/alpine → $st (expected 401/403)"

st=$(manifest_status "$USER_C" "$USER_C_PASS" "$REPO_HOSTED" "$SCOPE_B/alpine" "v1.0")
[[ "$st" == "401" || "$st" == "403" ]] \
    && deny "user-c: manifest GET $SCOPE_B/alpine:v1.0 → $st (denied as expected ✓)" \
    || fail "user-c: manifest GET $SCOPE_B/alpine:v1.0 → $st (expected 401/403)"

# ── HOSTED: Delete ─────────────────────────────────────────────────────────
section "Hosted — Delete (RBAC)"

dlogin "$ADMIN_USER" "$ADMIN_PASS"

# Get digest for delete tests
DEL_DIGEST=$(curl -sI -u "$ADMIN_USER:$ADMIN_PASS" \
    -H "Accept: application/vnd.docker.distribution.manifest.v2+json" \
    "http://$REGISTRY/v2/$REPO_HOSTED/$SCOPE_B/alpine/manifests/v2.0" \
    | grep -i "^docker-content-digest:" | tr -d '\r' | awk '{print $2}')

if [[ -n "$DEL_DIGEST" ]]; then
    # user-b denied to delete in da/prod
    st=$(delete_manifest "$USER_B" "$USER_B_PASS" "$REPO_HOSTED" "$SCOPE_C/alpine" "$DEL_DIGEST")
    [[ "$st" == "401" || "$st" == "403" ]] \
        && deny "user-b: DELETE $SCOPE_C manifest → $st (denied as expected ✓)" \
        || fail "user-b: DELETE $SCOPE_C manifest → $st (expected 401/403)"

    # admin can delete
    st=$(delete_manifest "$ADMIN_USER" "$ADMIN_PASS" "$REPO_HOSTED" "$SCOPE_B/alpine" "$DEL_DIGEST")
    [[ "$st" == "202" ]] && pass "admin: DELETE $SCOPE_B/alpine:v2.0 digest → 202" \
                          || fail "admin: DELETE $SCOPE_B/alpine:v2.0 digest → $st (expected 202)"

    # verify gone
    st=$(manifest_status "$ADMIN_USER" "$ADMIN_PASS" "$REPO_HOSTED" "$SCOPE_B/alpine" "$DEL_DIGEST")
    [[ "$st" == "404" ]] && pass "admin: deleted manifest GET → 404 (gone ✓)" \
                          || fail "admin: deleted manifest still returns $st (expected 404)"
else
    fail "delete tests: could not obtain digest for $SCOPE_B/alpine:v2.0"
fi

# ── PROXY ──────────────────────────────────────────────────────────────────
section "Proxy — $REPO_PROXY"

dlogin "$ADMIN_USER" "$ADMIN_PASS"

# Pull via proxy (cache miss → upstream fetch)
info "Pulling via proxy (may be slow on first request — fetches from upstream)..."
expect_pass "admin: pull $REPO_PROXY/library/alpine:latest (cache miss)" \
    docker pull "$REGISTRY/$REPO_PROXY/library/alpine:latest"

# Pull again — should be cache hit (faster)
expect_pass "admin: pull $REPO_PROXY/library/alpine:latest (cache hit)" \
    docker pull "$REGISTRY/$REPO_PROXY/library/alpine:latest"

# Push to proxy must fail with 405
docker tag "$SOURCE_IMAGE" "$REGISTRY/$REPO_PROXY/library/alpine:test-push" 2>/dev/null || true
expect_deny "admin: push to proxy (must return 405)" \
    docker push "$REGISTRY/$REPO_PROXY/library/alpine:test-push"

# User B and C have no matching content selector for proxy paths → denied
dlogin "$USER_B" "$USER_B_PASS"
expect_deny "user-b: pull $REPO_PROXY/library/alpine (no matching selector)" \
    docker pull "$REGISTRY/$REPO_PROXY/library/alpine:latest"

dlogin "$USER_C" "$USER_C_PASS"
expect_deny "user-c: pull $REPO_PROXY/library/alpine (no matching selector)" \
    docker pull "$REGISTRY/$REPO_PROXY/library/alpine:latest"

# ── GROUP ───────────────────────────────────────────────────────────────────
section "Group — $REPO_GROUP"

dlogin "$ADMIN_USER" "$ADMIN_PASS"

# Pull image from hosted member (admin pushed it above)
expect_pass "admin: pull $REPO_GROUP/$SCOPE_B/alpine:v1.0 (from hosted member)" \
    docker pull "$REGISTRY/$REPO_GROUP/$SCOPE_B/alpine:v1.0"

# Pull image from proxy member
expect_pass "admin: pull $REPO_GROUP/library/alpine:latest (from proxy member)" \
    docker pull "$REGISTRY/$REPO_GROUP/library/alpine:latest"

# tags/list on group (union of members)
result=$(tags_list "$ADMIN_USER" "$ADMIN_PASS" "$REPO_GROUP" "$SCOPE_B/alpine")
if echo "$result" | grep -q '"tags"'; then
    pass "admin: tags/list $REPO_GROUP/$SCOPE_B/alpine → tags found"
else
    fail "admin: tags/list $REPO_GROUP/$SCOPE_B/alpine → unexpected: $result"
fi

# Push to group must fail with 405
docker tag "$SOURCE_IMAGE" "$REGISTRY/$REPO_GROUP/$SCOPE_B/alpine:test-push" 2>/dev/null || true
expect_deny "admin: push to group (must return 405)" \
    docker push "$REGISTRY/$REPO_GROUP/$SCOPE_B/alpine:test-push"

# User B: pull from group within scope
dlogin "$USER_B" "$USER_B_PASS"
expect_pass "user-b: pull $REPO_GROUP/$SCOPE_B/alpine:v1.0 (allowed scope)" \
    docker pull "$REGISTRY/$REPO_GROUP/$SCOPE_B/alpine:v1.0"

expect_deny "user-b: pull $REPO_GROUP/$SCOPE_C/alpine:v1.0 (denied scope)" \
    docker pull "$REGISTRY/$REPO_GROUP/$SCOPE_C/alpine:v1.0"

# User C: pull from group within scope
dlogin "$USER_C" "$USER_C_PASS"
expect_pass "user-c: pull $REPO_GROUP/$SCOPE_C/alpine:v1.0 (allowed scope)" \
    docker pull "$REGISTRY/$REPO_GROUP/$SCOPE_C/alpine:v1.0"

expect_deny "user-c: pull $REPO_GROUP/$SCOPE_B/alpine:v1.0 (denied scope)" \
    docker pull "$REGISTRY/$REPO_GROUP/$SCOPE_B/alpine:v1.0"

# ── Cleanup ────────────────────────────────────────────────────────────────
section "Cleanup"

dlogin "$ADMIN_USER" "$ADMIN_PASS"

info "Removing local test tags..."
for tag in "${TAGS[@]}"; do
    docker rmi "$REGISTRY/$REPO_HOSTED/$SCOPE_B/alpine:$tag" 2>/dev/null || true
    docker rmi "$REGISTRY/$REPO_HOSTED/$SCOPE_C/alpine:$tag" 2>/dev/null || true
done
docker rmi "$REGISTRY/$REPO_PROXY/library/alpine:latest" 2>/dev/null || true
docker rmi "$REGISTRY/$REPO_GROUP/$SCOPE_B/alpine:v1.0"  2>/dev/null || true
docker rmi "$REGISTRY/$REPO_GROUP/$SCOPE_C/alpine:v1.0"  2>/dev/null || true
docker rmi "$REGISTRY/$REPO_PROXY/library/alpine:test-push"  2>/dev/null || true
docker rmi "$REGISTRY/$REPO_GROUP/$SCOPE_B/alpine:test-push" 2>/dev/null || true

# Delete remaining registry manifests (v1.0, latest) via API
dlogin "$ADMIN_USER" "$ADMIN_PASS"
for tag in v1.0 latest; do
    for scope in "$SCOPE_B" "$SCOPE_C"; do
        digest=$(curl -sI -u "$ADMIN_USER:$ADMIN_PASS" \
            -H "Accept: application/vnd.docker.distribution.manifest.v2+json" \
            "http://$REGISTRY/v2/$REPO_HOSTED/$scope/alpine/manifests/$tag" \
            | grep -i "^docker-content-digest:" | tr -d '\r' | awk '{print $2}')
        [[ -n "$digest" ]] && curl -sf -u "$ADMIN_USER:$ADMIN_PASS" -X DELETE \
            "http://$REGISTRY/v2/$REPO_HOSTED/$scope/alpine/manifests/$digest" >/dev/null 2>&1 || true
    done
done

info "Cleanup complete"

# ── Summary ────────────────────────────────────────────────────────────────
TOTAL=$((PASS + FAIL + DENY))
echo ""
echo -e "${BOLD}────────────────────────────────────${NC}"
printf " ${GREEN}%-6s${NC} passed   ${YELLOW}%-6s${NC} denied (expected)   ${RED}%-6s${NC} failed\n" \
    "$PASS" "$DENY" "$FAIL"
echo -e " Total: $TOTAL checks"
echo -e "${BOLD}────────────────────────────────────${NC}"

[[ "$FAIL" -eq 0 ]] && exit 0 || exit 1
