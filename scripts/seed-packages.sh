#!/usr/bin/env bash
# seed-packages.sh — загружает минимальные тестовые артефакты во все hosted репозитории.
#
# Требования: curl (обязателен), python3 (для zip/tar, опционально)
# Docker push пропущен — требует отдельного docker login.
#
# Переменные окружения:
#   BASE_URL   — URL сервера  (default: http://localhost:8080)
#   ADMIN_USER — логин         (default: admin)
#   ADMIN_PASS — пароль        (default: admin123)
set -uo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin123}"
AUTH=(-u "${ADMIN_USER}:${ADMIN_PASS}")

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'
ok()      { echo -e "${GREEN}[OK]${NC}    $*"; }
err()     { echo -e "${RED}[ERR]${NC}   $*"; }
info()    { echo -e "${CYAN}[INFO]${NC}  $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC}  $*"; }
section() { echo -e "\n${BOLD}${CYAN}══ $* ══${NC}"; }

TMPDIR_SEED=$(mktemp -d)
trap 'rm -rf "$TMPDIR_SEED"' EXIT

FAILED=()

upload() {
    local label="$1" url="$2" file="$3" method="${4:-PUT}"
    local code
    code=$(curl -s -o /tmp/nexspence_upload.out -w "%{http_code}" \
        "${AUTH[@]}" -X "$method" "$url" --upload-file "$file" \
        -H "Content-Type: application/octet-stream")
    if [[ "$code" =~ ^2 ]]; then
        ok "${label} → HTTP ${code}"
    else
        err "${label} → HTTP ${code}: $(cat /tmp/nexspence_upload.out)"
        FAILED+=("$label")
    fi
}

upload_json() {
    local label="$1" url="$2" body="$3" method="${4:-PUT}"
    local code
    code=$(curl -s -o /tmp/nexspence_upload.out -w "%{http_code}" \
        "${AUTH[@]}" -X "$method" "$url" \
        -H "Content-Type: application/json" -d "$body")
    if [[ "$code" =~ ^2 ]]; then
        ok "${label} → HTTP ${code}"
    else
        err "${label} → HTTP ${code}: $(cat /tmp/nexspence_upload.out)"
        FAILED+=("$label")
    fi
}

# ── Server check ──────────────────────────────────────────────────────────────
info "Connecting to ${BASE_URL} …"
if ! curl -sf -o /dev/null "${AUTH[@]}" "${BASE_URL}/service/rest/v1/repositories"; then
    err "Cannot reach ${BASE_URL}"
    exit 1
fi
info "Server OK"

# ── Raw ───────────────────────────────────────────────────────────────────────
section "Raw"
echo "Hello from Nexspence seed!" > "$TMPDIR_SEED/hello.txt"
echo "version=1.0.0" > "$TMPDIR_SEED/app.properties"
upload "raw/hello.txt"      "${BASE_URL}/repository/raw-artifacts/seed/hello.txt"      "$TMPDIR_SEED/hello.txt"
upload "raw/app.properties" "${BASE_URL}/repository/raw-artifacts/seed/app.properties" "$TMPDIR_SEED/app.properties"

# ── Maven2 ────────────────────────────────────────────────────────────────────
section "Maven2"
cat > "$TMPDIR_SEED/seed-1.0.pom" <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>seed</artifactId>
  <version>1.0</version>
  <packaging>jar</packaging>
</project>
XML
echo "PK" > "$TMPDIR_SEED/seed-1.0.jar"
upload "maven/pom" "${BASE_URL}/repository/maven-hosted/com/example/seed/1.0/seed-1.0.pom" "$TMPDIR_SEED/seed-1.0.pom"
upload "maven/jar" "${BASE_URL}/repository/maven-hosted/com/example/seed/1.0/seed-1.0.jar" "$TMPDIR_SEED/seed-1.0.jar"

# ── npm ───────────────────────────────────────────────────────────────────────
section "npm"
# npm publish uses PUT /:name with a JSON body containing _attachments
PKG_JSON='{"name":"@seed/hello","version":"1.0.0","description":"Nexspence seed package","main":"index.js","license":"MIT"}'
INDEX_JS='module.exports = function() { return "hello from nexspence"; };'
# Create minimal tgz via base64-encoded stub (real npm tarballs need proper structure)
# Using raw upload as a workaround — real publish requires the npm client
if command -v python3 >/dev/null 2>&1; then
    python3 - "$TMPDIR_SEED" <<'PYEOF'
import tarfile, io, json, os, sys
tmpdir = sys.argv[1]
buf = io.BytesIO()
with tarfile.open(fileobj=buf, mode='w:gz') as tf:
    for name, content in [
        ('package/package.json', '{"name":"@seed/hello","version":"1.0.0","main":"index.js","license":"MIT"}'),
        ('package/index.js', 'module.exports = function() { return "hello"; };'),
    ]:
        b = content.encode()
        info = tarfile.TarInfo(name=name)
        info.size = len(b)
        tf.addfile(info, io.BytesIO(b))
buf.seek(0)
with open(os.path.join(tmpdir, 'seed-hello-1.0.0.tgz'), 'wb') as f:
    f.write(buf.read())
PYEOF
    TARBALL_B64=$(python3 -c "import base64,sys; print(base64.b64encode(open(sys.argv[1],'rb').read()).decode())" "$TMPDIR_SEED/seed-hello-1.0.0.tgz")
    NPM_BODY=$(python3 -c "
import json,sys
b64=sys.argv[1]
pkg={'name':'@seed/hello','versions':{'1.0.0':{'name':'@seed/hello','version':'1.0.0','dist':{'tarball':'','shasum':'0'*40}}},'dist-tags':{'latest':'1.0.0'},'_attachments':{'@seed/hello-1.0.0.tgz':{'content_type':'application/octet-stream','data':b64,'length':1}}}
print(json.dumps(pkg))
" "$TARBALL_B64")
    upload_json "npm/@seed/hello" "${BASE_URL}/repository/npm-hosted/@seed%2fhello" "$NPM_BODY"
else
    warn "python3 not found — skipping npm (need it for tarball creation)"
fi

# ── PyPI ──────────────────────────────────────────────────────────────────────
section "PyPI"
if command -v python3 >/dev/null 2>&1; then
    python3 - "$TMPDIR_SEED" <<'PYEOF'
import zipfile, io, os, sys
tmpdir = sys.argv[1]
dist_info = "seed_hello-1.0.0.dist-info"
buf = io.BytesIO()
with zipfile.ZipFile(buf, 'w') as zf:
    zf.writestr("seed_hello/__init__.py", "def hello(): return 'hello from nexspence'\n")
    zf.writestr(f"{dist_info}/METADATA", "Metadata-Version: 2.1\nName: seed-hello\nVersion: 1.0.0\n")
    zf.writestr(f"{dist_info}/WHEEL", "Wheel-Version: 1.0\nGenerator: seed\nRoot-Is-Purelib: true\nTag: py3-none-any\n")
    zf.writestr(f"{dist_info}/RECORD", "")
buf.seek(0)
with open(os.path.join(tmpdir, "seed_hello-1.0.0-py3-none-any.whl"), "wb") as f:
    f.write(buf.read())
PYEOF
    WHL="$TMPDIR_SEED/seed_hello-1.0.0-py3-none-any.whl"
    CONTENT_B64=$(python3 -c "import base64,sys; print(base64.b64encode(open(sys.argv[1],'rb').read()).decode())" "$WHL")
    SHA256=$(python3 -c "import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],'rb').read()).hexdigest())" "$WHL")
    code=$(curl -s -o /tmp/nexspence_upload.out -w "%{http_code}" \
        "${AUTH[@]}" -X POST "${BASE_URL}/repository/pypi-hosted/" \
        -F ":action=file_upload" \
        -F "name=seed-hello" \
        -F "version=1.0.0" \
        -F "filetype=bdist_wheel" \
        -F "pyversion=py3" \
        -F "md5_digest=" \
        -F "content=@${WHL};type=application/zip")
    if [[ "$code" =~ ^2 ]]; then
        ok "pypi/seed-hello-1.0.0 → HTTP ${code}"
    else
        err "pypi/seed-hello-1.0.0 → HTTP ${code}: $(cat /tmp/nexspence_upload.out)"
        FAILED+=("pypi")
    fi
else
    warn "python3 not found — skipping pypi"
fi

# ── Helm ──────────────────────────────────────────────────────────────────────
section "Helm"
if command -v python3 >/dev/null 2>&1; then
    python3 - "$TMPDIR_SEED" <<'PYEOF'
import tarfile, io, os, sys
tmpdir = sys.argv[1]
buf = io.BytesIO()
with tarfile.open(fileobj=buf, mode='w:gz') as tf:
    for name, content in [
        ('seed-chart/Chart.yaml',
         'apiVersion: v2\nname: seed-chart\nversion: 1.0.0\ndescription: Nexspence seed Helm chart\n'),
        ('seed-chart/values.yaml', 'replicaCount: 1\n'),
        ('seed-chart/templates/deployment.yaml',
         'apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: seed\n'),
    ]:
        b = content.encode()
        info = tarfile.TarInfo(name=name)
        info.size = len(b)
        tf.addfile(info, io.BytesIO(b))
buf.seek(0)
with open(os.path.join(tmpdir, 'seed-chart-1.0.0.tgz'), 'wb') as f:
    f.write(buf.read())
PYEOF
    code=$(curl -s -o /tmp/nexspence_upload.out -w "%{http_code}" \
        "${AUTH[@]}" -X POST "${BASE_URL}/repository/helm-hosted/api/charts" \
        -F "chart=@$TMPDIR_SEED/seed-chart-1.0.0.tgz;type=application/gzip")
    if [[ "$code" =~ ^2 ]]; then
        ok "helm/seed-chart-1.0.0 → HTTP ${code}"
    else
        err "helm/seed-chart-1.0.0 → HTTP ${code}: $(cat /tmp/nexspence_upload.out)"
        FAILED+=("helm")
    fi
else
    warn "python3 not found — skipping helm"
fi

# ── Go modules ────────────────────────────────────────────────────────────────
section "Go modules"
if command -v python3 >/dev/null 2>&1; then
    python3 - "$TMPDIR_SEED" <<'PYEOF'
import zipfile, io, os, sys
tmpdir = sys.argv[1]
mod_path = "github.com/example/seedmod"
version = "v1.0.0"
prefix = f"{mod_path}@{version}"
buf = io.BytesIO()
with zipfile.ZipFile(buf, 'w') as zf:
    zf.writestr(f"{prefix}/go.mod", f"module {mod_path}\n\ngo 1.21\n")
    zf.writestr(f"{prefix}/hello.go",
        f'package seedmod\n\nfunc Hello() string {{ return "hello from nexspence" }}\n')
buf.seek(0)
with open(os.path.join(tmpdir, "seedmod-v1.0.0.zip"), "wb") as f:
    f.write(buf.read())
with open(os.path.join(tmpdir, "seedmod.info"), "w") as f:
    f.write('{"Version":"v1.0.0","Time":"2026-01-01T00:00:00Z"}')
with open(os.path.join(tmpdir, "seedmod.mod"), "w") as f:
    f.write(f"module {mod_path}\n\ngo 1.21\n")
PYEOF
    GOMOD_BASE="${BASE_URL}/repository/go-hosted/github.com/example/seedmod/@v"
    upload "go/v1.0.0.info" "${GOMOD_BASE}/v1.0.0.info" "$TMPDIR_SEED/seedmod.info"
    upload "go/v1.0.0.mod"  "${GOMOD_BASE}/v1.0.0.mod"  "$TMPDIR_SEED/seedmod.mod"
    upload "go/v1.0.0.zip"  "${GOMOD_BASE}/v1.0.0.zip"  "$TMPDIR_SEED/seedmod-v1.0.0.zip"
else
    warn "python3 not found — skipping go modules"
fi

# ── Cargo ─────────────────────────────────────────────────────────────────────
section "Cargo"
if command -v python3 >/dev/null 2>&1; then
    python3 - "$TMPDIR_SEED" <<'PYEOF'
import tarfile, io, os, sys, json
tmpdir = sys.argv[1]
buf = io.BytesIO()
with tarfile.open(fileobj=buf, mode='w:gz') as tf:
    for name, content in [
        ('seed-hello-1.0.0/Cargo.toml',
         '[package]\nname = "seed-hello"\nversion = "1.0.0"\nedition = "2021"\n'),
        ('seed-hello-1.0.0/src/lib.rs',
         'pub fn hello() -> &\'static str { "hello from nexspence" }\n'),
    ]:
        b = content.encode()
        info = tarfile.TarInfo(name=name)
        info.size = len(b)
        tf.addfile(info, io.BytesIO(b))
buf.seek(0)
crate_data = buf.read()
meta = json.dumps({"name":"seed-hello","vers":"1.0.0","deps":[],"features":{},"authors":[],"description":"seed","homepage":None,"documentation":None,"readme":None,"keywords":[],"categories":[],"license":"MIT","license_file":None,"repository":None,"links":None}).encode()
# Cargo new API: first 4 bytes = meta len (LE), then meta JSON, then 4 bytes crate len, then crate data
import struct
payload = struct.pack('<I', len(meta)) + meta + struct.pack('<I', len(crate_data)) + crate_data
with open(os.path.join(tmpdir, 'seed-hello-1.0.0.crate'), 'wb') as f:
    f.write(payload)
PYEOF
    code=$(curl -s -o /tmp/nexspence_upload.out -w "%{http_code}" \
        "${AUTH[@]}" -X PUT "${BASE_URL}/repository/cargo-hosted/api/v1/crates/new" \
        -H "Content-Type: application/octet-stream" \
        --data-binary "@$TMPDIR_SEED/seed-hello-1.0.0.crate")
    if [[ "$code" =~ ^2 ]]; then
        ok "cargo/seed-hello-1.0.0 → HTTP ${code}"
    else
        err "cargo/seed-hello-1.0.0 → HTTP ${code}: $(cat /tmp/nexspence_upload.out)"
        FAILED+=("cargo")
    fi
else
    warn "python3 not found — skipping cargo"
fi

# ── NuGet ─────────────────────────────────────────────────────────────────────
section "NuGet"
if command -v python3 >/dev/null 2>&1; then
    python3 - "$TMPDIR_SEED" <<'PYEOF'
import zipfile, io, os, sys
tmpdir = sys.argv[1]
buf = io.BytesIO()
with zipfile.ZipFile(buf, 'w') as zf:
    zf.writestr("SeedHello.nuspec", '''<?xml version="1.0"?>
<package><metadata>
  <id>SeedHello</id><version>1.0.0</version>
  <authors>Nexspence</authors><description>Seed package</description>
</metadata></package>''')
    zf.writestr("[Content_Types].xml",
        '<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="nuspec" ContentType="application/xml"/></Types>')
buf.seek(0)
with open(os.path.join(tmpdir, "SeedHello.1.0.0.nupkg"), "wb") as f:
    f.write(buf.read())
PYEOF
    code=$(curl -s -o /tmp/nexspence_upload.out -w "%{http_code}" \
        "${AUTH[@]}" -X PUT "${BASE_URL}/repository/nuget-hosted/v2/package" \
        -F "package=@$TMPDIR_SEED/SeedHello.1.0.0.nupkg;type=application/octet-stream")
    if [[ "$code" =~ ^2 ]]; then
        ok "nuget/SeedHello.1.0.0 → HTTP ${code}"
    else
        err "nuget/SeedHello.1.0.0 → HTTP ${code}: $(cat /tmp/nexspence_upload.out)"
        FAILED+=("nuget")
    fi
else
    warn "python3 not found — skipping nuget"
fi

# ── Apt (minimal .deb) ────────────────────────────────────────────────────────
section "Apt"
if command -v python3 >/dev/null 2>&1; then
    python3 - "$TMPDIR_SEED" <<'PYEOF'
import tarfile, io, os, sys, struct
tmpdir = sys.argv[1]

def make_tar_gz(files):
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode='w:gz') as tf:
        for name, content in files:
            b = content if isinstance(content, bytes) else content.encode()
            info = tarfile.TarInfo(name=name)
            info.size = len(b)
            tf.addfile(info, io.BytesIO(b))
    buf.seek(0)
    return buf.read()

control_data = make_tar_gz([
    ('./control', 'Package: seed-hello\nVersion: 1.0.0\nArchitecture: amd64\nMaintainer: nexspence\nDescription: seed package\n'),
])
data_data = make_tar_gz([('./usr/share/doc/seed-hello/copyright', 'MIT\n')])

# ar format: magic + file entries
def ar_entry(name, data):
    n = name.encode().ljust(16)[:16]
    ts = b'0           '[:12]
    uid = b'0     '[:6]
    gid = b'0     '[:6]
    mode = b'100644  '[:8]
    size = str(len(data)).encode().ljust(10)[:10]
    end = b'`\n'
    entry = n + ts + uid + gid + mode + size + end + data
    if len(data) % 2 == 1:
        entry += b'\n'
    return entry

deb = b'!<arch>\n'
deb += ar_entry('debian-binary', b'2.0\n')
deb += ar_entry('control.tar.gz', control_data)
deb += ar_entry('data.tar.gz', data_data)

with open(os.path.join(tmpdir, 'seed-hello_1.0.0_amd64.deb'), 'wb') as f:
    f.write(deb)
PYEOF
    upload "apt/seed-hello_1.0.0_amd64.deb" \
        "${BASE_URL}/repository/apt-hosted/pool/main/s/seed-hello/seed-hello_1.0.0_amd64.deb" \
        "$TMPDIR_SEED/seed-hello_1.0.0_amd64.deb"
else
    warn "python3 not found — skipping apt"
fi

# ── Yum (minimal .rpm stub) ───────────────────────────────────────────────────
section "Yum"
# Minimal RPM: magic header only (not a valid installable RPM, but accepted by the blob store)
python3 - "$TMPDIR_SEED" <<'PYEOF' 2>/dev/null || warn "python3 not found — skipping yum"
import os, sys, struct
tmpdir = sys.argv[1]
# RPM magic + lead (96 bytes total stub)
magic = b'\xed\xab\xee\xdb'  # RPM magic
lead = magic + b'\x03\x00' + b'\x00\x01' + b'seed-hello' + b'\x00' * 54 + b'\x00\x01\x00\x05\x00\x00'
with open(os.path.join(tmpdir, 'seed-hello-1.0.0-1.x86_64.rpm'), 'wb') as f:
    f.write(lead[:96])
PYEOF
if [[ -f "$TMPDIR_SEED/seed-hello-1.0.0-1.x86_64.rpm" ]]; then
    upload "yum/seed-hello-1.0.0-1.x86_64.rpm" \
        "${BASE_URL}/repository/yum-hosted/Packages/s/seed-hello-1.0.0-1.x86_64.rpm" \
        "$TMPDIR_SEED/seed-hello-1.0.0-1.x86_64.rpm"
fi

# ── Conda ─────────────────────────────────────────────────────────────────────
section "Conda"
if command -v python3 >/dev/null 2>&1; then
    python3 - "$TMPDIR_SEED" <<'PYEOF'
import tarfile, io, os, sys, json
tmpdir = sys.argv[1]
meta = {
    "name": "seed-hello",
    "version": "1.0.0",
    "build": "py_0",
    "build_number": 0,
    "depends": [],
    "license": "MIT",
    "platform": None,
    "subdir": "noarch",
    "timestamp": 1700000000000
}
buf = io.BytesIO()
with tarfile.open(fileobj=buf, mode='w:bz2') as tf:
    for name, content in [
        ('info/index.json', json.dumps(meta)),
        ('info/about.json', '{"summary":"Nexspence seed conda package"}'),
        ('lib/python3.11/site-packages/seed_hello/__init__.py', 'def hello(): return "hello"\n'),
    ]:
        b = content.encode()
        info = tarfile.TarInfo(name=name)
        info.size = len(b)
        tf.addfile(info, io.BytesIO(b))
buf.seek(0)
with open(os.path.join(tmpdir, 'seed-hello-1.0.0-py_0.tar.bz2'), 'wb') as f:
    f.write(buf.read())
PYEOF
    upload "conda/seed-hello-1.0.0-py_0.tar.bz2" \
        "${BASE_URL}/repository/conda-hosted/noarch/seed-hello-1.0.0-py_0.tar.bz2" \
        "$TMPDIR_SEED/seed-hello-1.0.0-py_0.tar.bz2"
else
    warn "python3 not found — skipping conda"
fi

# ── Terraform ─────────────────────────────────────────────────────────────────
section "Terraform"
if command -v python3 >/dev/null 2>&1; then
    python3 - "$TMPDIR_SEED" <<'PYEOF'
import zipfile, io, os, sys
tmpdir = sys.argv[1]
buf = io.BytesIO()
with zipfile.ZipFile(buf, 'w') as zf:
    zf.writestr("terraform-provider-seed_v1.0.0_linux_amd64/terraform-provider-seed",
                "#!/bin/sh\necho 'seed provider'\n")
buf.seek(0)
with open(os.path.join(tmpdir, "terraform-provider-seed_1.0.0_linux_amd64.zip"), "wb") as f:
    f.write(buf.read())
PYEOF
    code=$(curl -s -o /tmp/nexspence_upload.out -w "%{http_code}" \
        "${AUTH[@]}" -X PUT \
        "${BASE_URL}/repository/terraform-hosted/v1/providers/example/seed/1.0.0/upload/linux/amd64" \
        -H "Content-Type: application/octet-stream" \
        --data-binary "@$TMPDIR_SEED/terraform-provider-seed_1.0.0_linux_amd64.zip")
    if [[ "$code" =~ ^2 ]]; then
        ok "terraform/provider-seed-1.0.0 → HTTP ${code}"
    else
        err "terraform/provider-seed-1.0.0 → HTTP ${code}: $(cat /tmp/nexspence_upload.out)"
        FAILED+=("terraform")
    fi
else
    warn "python3 not found — skipping terraform"
fi

# ── Docker — skip ─────────────────────────────────────────────────────────────
section "Docker"
warn "Docker push requires docker CLI — skipping automated seed."
info "Manual push example:"
echo "  docker pull alpine:3.19"
echo "  docker tag alpine:3.19 localhost:8080/docker-dev/alpine:3.19"
echo "  docker push localhost:8080/docker-dev/alpine:3.19"

# ── Conan — skip ──────────────────────────────────────────────────────────────
section "Conan"
warn "Conan publish requires conan CLI — skipping automated seed."
info "Manual example:"
echo "  conan remote add nexspence ${BASE_URL}/repository/conan-hosted/"
echo "  conan upload seed-hello/1.0.0 --remote nexspence"

# ── Summary ───────────────────────────────────────────────────────────────────
echo
if [[ ${#FAILED[@]} -eq 0 ]]; then
    ok "All automated package uploads completed."
else
    err "Failed uploads: ${FAILED[*]}"
    exit 1
fi
