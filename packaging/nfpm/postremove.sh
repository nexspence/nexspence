#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
fi
# NOTE: /var/lib/nexspence (data) and /etc/nexspence/config.yaml are
# intentionally left in place. Remove them manually to fully purge.
