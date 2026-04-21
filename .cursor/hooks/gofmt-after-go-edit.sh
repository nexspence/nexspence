#!/usr/bin/env bash
# Runs gofmt when the edited file is a Go source file (project hook).
set -euo pipefail
input=$(cat)
file=$(echo "$input" | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
    print(d.get('file_path') or d.get('path') or d.get('file') or '')
except Exception:
    print('')
" 2>/dev/null || echo "")
if [[ -n "$file" && "$file" == *.go && -f "$file" ]]; then
  command -v gofmt >/dev/null 2>&1 && gofmt -w "$file" || true
fi
exit 0
