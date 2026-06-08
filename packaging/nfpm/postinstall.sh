#!/bin/sh
set -e

# Create dedicated system group + user if missing
if ! getent group nexspence >/dev/null 2>&1; then
    groupadd --system nexspence
fi
if ! getent passwd nexspence >/dev/null 2>&1; then
    useradd --system --gid nexspence --home-dir /var/lib/nexspence \
        --shell /usr/sbin/nologin --comment "Nexspence service" nexspence
fi

# Data directory (matches LocalBlobStore 0750 convention)
mkdir -p /var/lib/nexspence
chown nexspence:nexspence /var/lib/nexspence
chmod 0750 /var/lib/nexspence

# Config holds secrets — readable only by root + service group
if [ -f /etc/nexspence/config.yaml ]; then
    chown root:nexspence /etc/nexspence/config.yaml
    chmod 0640 /etc/nexspence/config.yaml
fi

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
fi

cat <<'BANNER'
────────────────────────────────────────────────────────────
 Nexspence installed.

 Next steps:
   1. Edit /etc/nexspence/config.yaml:
        - database.dsn          (point at your PostgreSQL)
        - auth.jwt_secret       (>= 32 random chars)
        - bootstrap.admin_password
   2. Enable + start the service:
        sudo systemctl enable --now nexspence
   3. Browse to http://localhost:8081

 Docs: https://nexspence.com  →  Native install
────────────────────────────────────────────────────────────
BANNER
