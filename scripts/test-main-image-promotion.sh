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
