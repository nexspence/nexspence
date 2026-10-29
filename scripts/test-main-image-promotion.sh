#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
mkdir -p "$TMP_DIR/bin"
cat > "$TMP_DIR/bin/gh" <<'GH'
#!/usr/bin/env bash
printf '%s' "$GH_HEAD_SHA"
GH
cat > "$TMP_DIR/bin/docker" <<'DOCKER'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$DOCKER_LOG"
DOCKER
chmod +x "$TMP_DIR/bin/gh" "$TMP_DIR/bin/docker"
export PATH="$TMP_DIR/bin:$PATH"
export DOCKER_LOG="$TMP_DIR/docker.log"
export GITHUB_REPOSITORY="nexspence/nexspence"
IMAGE="ghcr.io/nexspence/nexspence"
CURRENT_SHA="0123456789abcdef0123456789abcdef01234567"

export GH_HEAD_SHA="$CURRENT_SHA"
bash "$ROOT/scripts/promote-main-image.sh" "$IMAGE" "$CURRENT_SHA"
grep -Fx "buildx imagetools create --tag $IMAGE:main $IMAGE:0123456" "$DOCKER_LOG"

export GH_HEAD_SHA="89abcdef0123456789abcdef0123456789abcdef"
bash "$ROOT/scripts/promote-main-image.sh" "$IMAGE" "$CURRENT_SHA"
[[ "$(wc -l < "$DOCKER_LOG" | tr -d ' ')" == 1 ]]
printf 'main promotion succeeds only for the current main commit\n'
WORKFLOW="$ROOT/.github/workflows/main-image.yml"
release_token_count="$(grep -Fc 'password: ${{ secrets.RELEASE_PAT }}' "$WORKFLOW" || true)"
if [[ "$release_token_count" != 2 ]]; then
  echo "expected both GHCR login steps to use RELEASE_PAT; found $release_token_count" >&2
  exit 1
fi
if grep -Fq 'password: ${{ secrets.GITHUB_TOKEN }}' "$WORKFLOW"; then
  echo "GITHUB_TOKEN cannot write the nexspence GHCR package" >&2
  exit 1
fi
printf 'main image jobs use the package-authorized release token\n'
api_token_count="$(grep -Fc 'GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}' "$WORKFLOW" || true)"
if [[ "$api_token_count" != 1 ]]; then
  echo "expected the promotion step to provide GH_TOKEN for the main-head API check; found $api_token_count" >&2
  exit 1
fi
printf 'main promotion receives a read-only GitHub API token\n'
