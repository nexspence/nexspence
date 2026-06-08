#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet nexspence 2>/dev/null; then
        systemctl stop nexspence || true
    fi
    systemctl disable nexspence >/dev/null 2>&1 || true
fi
