#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <image> <full-commit-sha>" >&2
  exit 2
fi

image="$1"
commit_sha="$2"
if [[ ! "$commit_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "commit SHA must be 40 lowercase hexadecimal characters" >&2
  exit 2
fi

current_head="$(gh api "repos/${GITHUB_REPOSITORY}/branches/main" --jq .commit.sha)"
if [[ "$current_head" != "$commit_sha" ]]; then
  echo "Skipping main tag promotion: $commit_sha is no longer the main branch head (now $current_head)"
  exit 0
fi

short_sha="${commit_sha:0:7}"
docker buildx imagetools create --tag "${image}:main" "${image}:${short_sha}"
