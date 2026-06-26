#!/bin/sh
# Auto-provision a unique JWT secret for zero-config deployments (docker compose).
#
# If NEXSPENCE_AUTH_JWT_SECRET is unset or still the shipped development default,
# generate a random 64-char secret once and persist it to a writable volume so it
# stays stable across restarts and is shared by every replica that mounts the same
# volume. When the operator supplies a real secret (Helm Secret, explicit env),
# this is a no-op and the provided value is used as-is.
set -eu

DEV_DEFAULT="nexspence-dev-default-secret-change-me-in-production"
SECRET_FILE="${NEXSPENCE_JWT_SECRET_FILE:-/app/secrets/jwt_secret}"

current="${NEXSPENCE_AUTH_JWT_SECRET:-}"
if [ -z "$current" ] || [ "$current" = "$DEV_DEFAULT" ]; then
    if [ -s "$SECRET_FILE" ]; then
        NEXSPENCE_AUTH_JWT_SECRET="$(cat "$SECRET_FILE")"
    else
        gen="$(LC_ALL=C tr -dc 'a-zA-Z0-9' < /dev/urandom | head -c 64)"
        # Atomic create (noclobber): if a peer replica wrote first, this fails
        # and we fall through to reading the winner's value below.
        if mkdir -p "$(dirname "$SECRET_FILE")" 2>/dev/null &&
            (set -C; printf '%s' "$gen" >"$SECRET_FILE") 2>/dev/null; then
            chmod 600 "$SECRET_FILE" 2>/dev/null || true
            echo "nexspence: generated a unique JWT secret -> $SECRET_FILE"
        fi
        if [ -s "$SECRET_FILE" ]; then
            NEXSPENCE_AUTH_JWT_SECRET="$(cat "$SECRET_FILE")"
        else
            NEXSPENCE_AUTH_JWT_SECRET="$gen"
            echo "nexspence: using an ephemeral JWT secret (could not persist to $SECRET_FILE; sessions reset on restart)"
        fi
    fi
    export NEXSPENCE_AUTH_JWT_SECRET
fi

exec /app/nexspence "$@"
