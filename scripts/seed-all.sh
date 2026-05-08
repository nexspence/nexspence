#!/usr/bin/env bash
# seed-all.sh — запускает все seed-скрипты последовательно.
#
# Порядок:
#   1. seed-repos.sh    — создаёт репозитории всех 14 форматов
#   2. seed-packages.sh — загружает тестовые пакеты в hosted репозитории
#   3. seed-rbac.sh     — создаёт RBAC-структуру для 4 проектов
#
# Переменные окружения:
#   BASE_URL       — URL сервера        (default: http://localhost:8080)
#   ADMIN_USER     — admin логин        (default: admin)
#   ADMIN_PASS     — admin пароль       (default: admin123)
#   BLOB_PRIMARY   — первый blob store  (default: s3-primary)
#   BLOB_SECONDARY — второй blob store  (default: s3-secondary)
#
# Пример запуска:
#   ./scripts/seed-all.sh
#   BASE_URL=http://192.168.1.10:8080 ADMIN_PASS=secret ./scripts/seed-all.sh
set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"
BLOB_PRIMARY="${BLOB_PRIMARY:-s3-primary}"
BLOB_SECONDARY="${BLOB_SECONDARY:-s3-secondary}"

SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'
ok()      { echo -e "${GREEN}[OK]${NC}    $*"; }
err()     { echo -e "${RED}[ERR]${NC}   $*"; }
info()    { echo -e "${CYAN}[INFO]${NC}  $*"; }
banner()  { echo -e "\n${BOLD}${CYAN}╔══════════════════════════════════════╗${NC}"; \
            echo -e "${BOLD}${CYAN}║  $*$(printf '%*s' $((38-${#1})) '')║${NC}"; \
            echo -e "${BOLD}${CYAN}╚══════════════════════════════════════╝${NC}"; }

banner "Nexspence Demo Seed"
info "Server:  ${BASE_URL}"
info "Primary blob store:   ${BLOB_PRIMARY}"
info "Secondary blob store: ${BLOB_SECONDARY}"
echo

# Step 1 — repositories
banner "Step 1/3: Repositories"
BASE_URL="$BASE_URL" ADMIN_USER="$ADMIN_USER" ADMIN_PASS="$ADMIN_PASS" \
    BLOB_PRIMARY="$BLOB_PRIMARY" BLOB_SECONDARY="$BLOB_SECONDARY" \
    bash "${SCRIPTS_DIR}/seed-repos.sh" || { err "seed-repos.sh failed"; exit 1; }

# Step 2 — packages
banner "Step 2/3: Test Packages"
BASE_URL="$BASE_URL" ADMIN_USER="$ADMIN_USER" ADMIN_PASS="$ADMIN_PASS" \
    bash "${SCRIPTS_DIR}/seed-packages.sh" || { err "seed-packages.sh failed"; exit 1; }

# Step 3 — RBAC
banner "Step 3/3: RBAC"
BASE_URL="$BASE_URL" ADMIN_USER="$ADMIN_USER" ADMIN_PASS="$ADMIN_PASS" \
    bash "${SCRIPTS_DIR}/seed-rbac.sh" || { err "seed-rbac.sh failed"; exit 1; }

echo
ok "All done! Nexspence demo instance is seeded."
info "UI: ${BASE_URL}"
info "Blob stores for migration testing:"
echo "  ${BLOB_PRIMARY}   → maven, npm, pypi, docker, helm, cargo, conda"
echo "  ${BLOB_SECONDARY} → go, nuget, raw, apt, yum, conan, terraform"
