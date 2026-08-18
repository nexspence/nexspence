#!/usr/bin/env python3
"""Generate the Nexspence architecture diagram as SVG, in the product's dark UI style."""
from PIL import ImageFont

HN = "/System/Library/Fonts/HelveticaNeue.ttc"
ML = "/System/Library/Fonts/Menlo.ttc"
IDX = {("hn", 400): 0, ("hn", 500): 10, ("hn", 700): 1, ("ml", 400): 0, ("ml", 700): 1}
_cache = {}


def font(fam, size, weight=400):
    key = (fam, size, weight)
    if key not in _cache:
        _cache[key] = ImageFont.truetype(HN if fam == "hn" else ML, size,
                                         index=IDX[(fam, weight)])
    return _cache[key]


def tw(s, fam, size, weight=400, ls=0.0):
    return font(fam, int(round(size)), weight).getlength(s) + ls * max(len(s) - 1, 0)


# ── palette: website/index.html :root + frontend/src/components/holo/holo.css ──
BG = "#070b14"
TEXT = "#f1f5f9"
DIM = "#94a3b8"
FAINT = "#64748b"
BLUE = "#3b82f6"
CYAN = "#06b6d4"
GREEN = "#22c55e"
AMBER = "#f59e0b"
RED = "#ef4444"
PURPLE = "#8b5cf6"
HOLO_A = "#7c5cff"
HOLO_B = "#22d3ee"

W = 1120
PAD = 24
CX = W / 2
MAIN_X, MAIN_W = 24, 772
GAP = 32
RIGHT_X, RIGHT_W = 828, 268

out, defs = [], []


def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def rect(x, y, w, h, rx, fill, fo=1.0, stroke=None, so=1.0, sw=1.0, extra=""):
    s = (f'<rect x="{x:.1f}" y="{y:.1f}" width="{w:.1f}" height="{h:.1f}" rx="{rx}" '
         f'fill="{fill}" fill-opacity="{fo}"')
    if stroke:
        s += f' stroke="{stroke}" stroke-opacity="{so}" stroke-width="{sw}"'
    return s + extra + "/>"


def text(x, y, s, fam="hn", size=12, weight=400, fill=TEXT, anchor="start", ls=0.0, op=1.0):
    fams = "Helvetica Neue, Helvetica, Arial, sans-serif" if fam == "hn" else "Menlo, monospace"
    a = f' text-anchor="{anchor}"' if anchor != "start" else ""
    l = f' letter-spacing="{ls}"' if ls else ""
    o = f' fill-opacity="{op}"' if op != 1.0 else ""
    return (f'<text x="{x:.1f}" y="{y:.1f}" font-family="{fams}" font-size="{size}" '
            f'font-weight="{weight}" fill="{fill}"{o}{a}{l}>{esc(s)}</text>')


# ── chips ──────────────────────────────────────────────────────────────────
CHIP_H, CHIP_PADX, CHIP_GAP, ROW_GAP, CHIP_FS = 25, 9, 7, 6, 12.5


def chip_w(label):
    return tw(label, "ml", CHIP_FS) + 2 * CHIP_PADX


def chip_row(x, y, maxw, items, draw=True):
    svg, cx_, cy = [], x, y
    for it in items:
        lab, st = (it, {}) if isinstance(it, str) else it
        w = chip_w(lab)
        if cx_ > x and cx_ + w > x + maxw:
            cx_, cy = x, cy + CHIP_H + ROW_GAP
        if draw:
            s = dict(fg="#cbd5e1", bg="#ffffff", bgo=0.06, bd=BLUE, bdo=0.18)
            s.update(st)
            svg.append(rect(cx_, cy, w, CHIP_H, 5, s["bg"], s["bgo"], s["bd"], s["bdo"]))
            svg.append(text(cx_ + CHIP_PADX, cy + CHIP_H / 2 + 4.4, lab, "ml",
                            CHIP_FS, 400, s["fg"]))
        cx_ += w + CHIP_GAP
    return "".join(svg), cy + CHIP_H - y


def measure_row(x, maxw, items):
    return chip_row(x, 0, maxw, items, draw=False)[1]


def wrap(s, maxw, size=10.8):
    lines, cur = [], ""
    for word in s.split(" "):
        t = (cur + " " + word).strip()
        if cur and tw(t, "hn", size) > maxw:
            lines.append(cur)
            cur = word
        else:
            cur = t
    if cur:
        lines.append(cur)
    return lines


def arrow(x1, y1, x2, y2, color=BLUE, op=0.55, dash=None, sw=1.6):
    d = f' stroke-dasharray="{dash}"' if dash else ""
    return (f'<path d="M {x1:.1f} {y1:.1f} L {x2:.1f} {y2:.1f}" stroke="{color}" '
            f'stroke-opacity="{op}" stroke-width="{sw}" fill="none"{d} '
            f'marker-end="url(#ah)"/>')


PURPLE_CHIP = {"bd": PURPLE, "bdo": 0.5, "fg": "#ddd0ff", "bg": PURPLE, "bgo": 0.16}

# ───────────────────────────── CONTENT ─────────────────────────────────────
CLIENTS = [("Browser → Web UI", {"bd": CYAN, "bdo": 0.45, "fg": "#a5f3fc",
                                 "bg": CYAN, "bgo": 0.13}),
           "docker", "mvn", "gradle", "npm", "pip", "go", "helm", "cargo", "nuget",
           "apt", "yum", "terraform", "curl / CI"]

NODE_BLOCKS = [
    ("HTTP · Gin", BLUE, [
        ("chips", ["/repository/:repo/*", "/v2/*  OCI Distribution", "/api/v1/*",
                   "/healthz · /readyz", "/service/rest/v1/*  Nexus-compat",
                   "/metrics  Prometheus", "/  React SPA"]),
        ("note", "Recovery → request log → CORS → security headers → body limit → "
                 "metrics → audit → rate limit"),
    ]),
    ("Auth · RBAC", PURPLE, [
        ("chips", ["JWT", "HTTP Basic", "API token nxs_*", "LDAP", "OIDC + PKCE", "SAML"]),
        ("note", "Role → privilege → content selector (CEL path scoping) · anonymous read "
                 "per repo · audit log on every mutation"),
    ]),
    ("Formats", CYAN, [
        ("chips", ["maven2", "npm", "pypi", "go", "docker", "oci", "helm", "nuget", "cargo",
                   "conan", "conda", "apt", "yum", "terraform", "raw",
                   ("group", PURPLE_CHIP), ("proxy", PURPLE_CHIP)]),
        ("note", "group repos fan out to members and merge metadata · proxy repos cache "
                 "from an upstream registry"),
    ]),
    ("Services", GREEN, [
        ("chips", ["repository", "user", "token", "rbac", "content-selector", "routing-rule",
                   "webhook", "promotion", "backup / restore", "blob-store migration",
                   ("scan", {"bd": AMBER, "bdo": 0.5, "fg": "#fcd9a0",
                             "bg": AMBER, "bgo": 0.14})]),
        ("note", "Trivy scans docker / oci images, OSV.dev covers npm · maven · pypi · cargo — "
                 "findings export as CSV or JSON"),
    ]),
    ("Schedulers", AMBER, [
        ("chips", ["cleanup policies — cron", "blob GC — weekly", "replication — per-rule cron",
                   "vulnerability re-scan — nightly", "download counter — 10s flush"]),
        ("note", "Auto-scan is queued on upload; a Redis lock keeps cleanup, GC and blob-store "
                 "migration single-runner across nodes"),
    ]),
]

OUTBOUND = [
    ("Upstream registries", "Docker Hub · Maven Central · npmjs · PyPI · crates.io — "
                            "fetched and cached by proxy repos"),
    ("OSV.dev API", "vulnerability queries for npm, maven, pypi and cargo components"),
    ("Trivy vulnerability DB", "pulled by the bundled Trivy CLI, which scans images back "
                               "through this node"),
    ("Replication targets", "assets pushed to remote Nexspence instances on a cron"),
    ("Webhooks", "HTTP POST on artifact and promotion events"),
]

SHARED = [
    ("PostgreSQL", BLUE, "pgx pool · goose migrations",
     ["repositories · components · assets",
      "users · roles · privileges · content selectors",
      "audit log (partitioned) · scan results",
      "replication · promotion · cleanup rules",
      "full-text search (tsvector)"]),
    ("Redis", RED, "optional — this is what enables HA",
     ["distributed locks (cleanup / GC / migration)",
      "failed-login throttle",
      "readiness-probe dependency"]),
    ("Blob storage", AMBER, "the artifact bytes themselves",
     ["local filesystem",
      "S3 / MinIO / Ceph (presigned URLs)",
      "group store — round-robin or fill-first",
      "per-store quotas · orphan GC"]),
]

# ───────────────────────────── LAYOUT ──────────────────────────────────────
y = 30

# Title
out.append(text(PAD + 2, y + 27, "Nexspence Architecture", "hn", 31, 700, "url(#holo)", ls=-0.4))
out.append(text(PAD + 3, y + 51,
                "One Go binary — Gin HTTP layer, embedded React UI, pluggable artifact "
                "formats. Every node is stateless; all state lives outside the process.",
                "hn", 13, 400, DIM))
# No version badge: the architecture changes far more slowly than releases do,
# and a version baked into a static image is a claim that goes stale on the next
# tag with nothing to catch it.
y += 76

# Clients band
cl_x, cl_w = PAD, W - 2 * PAD
cl_ix, cl_iw = cl_x + 108, cl_w - 108 - 16
band_h = measure_row(cl_ix, cl_iw, CLIENTS) + 26
out.append(rect(cl_x, y, cl_w, band_h, 12, "#ffffff", 0.035, BLUE, 0.16))
out.append(text(cl_x + 18, y + band_h / 2 + 4, "CLIENTS", "hn", 11, 700, DIM, ls=1.1))
out.append(chip_row(cl_ix, y + 13, cl_iw, CLIENTS)[0])
y += band_h

out.append(arrow(CX, y + 5, CX, y + 25))
y += 31

# Load balancer
lb_w, lb_h = 400, 47
lb_x = CX - lb_w / 2
out.append(rect(lb_x, y, lb_w, lb_h, 10, "#ffffff", 0.05, BLUE, 0.3))
out.append(text(CX, y + 21, "Load balancer / Ingress", "hn", 14, 700, TEXT, anchor="middle"))
out.append(text(CX, y + 36, "TLS termination · nginx · k8s Ingress · ALB", "hn", 11.5, 400,
                DIM, anchor="middle"))
lb_bot = y + lb_h
y = lb_bot + 32

# ── node panel ──
node_y = y
node_ix = MAIN_X + 16
LAB_COL = 118
blk_cx = node_ix + LAB_COL
blk_cw = MAIN_W - 32 - LAB_COL - 8

HDR = 44
blocks = []
for name, accent, rows in NODE_BLOCKS:
    h = 13
    for kind, payload in rows:
        h += (measure_row(blk_cx, blk_cw, payload) + 6) if kind == "chips" else 18
    blocks.append((name, accent, rows, h + 8))
node_h = HDR + sum(b[3] for b in blocks) + 9 * (len(blocks) - 1) + 16

out.append(rect(MAIN_X, node_y, MAIN_W, node_h, 14, "#0c1424", 0.92, BLUE, 0.3, 1.2))
out.append(text(node_ix, node_y + 26, "Nexspence node", "hn", 16, 700, TEXT))
out.append(text(node_ix + tw("Nexspence node", "hn", 16, 700) + 11, node_y + 26,
                "one process · stateless · horizontally scalable", "hn", 11.5, 400, FAINT))
out.append(f'<path d="M {MAIN_X + 14} {node_y + HDR - 8} H {MAIN_X + MAIN_W - 14}" '
           f'stroke="{BLUE}" stroke-opacity="0.16" stroke-width="1"/>')

by = node_y + HDR
for name, accent, rows, h in blocks:
    out.append(rect(node_ix, by, MAIN_W - 32, h, 9, "#ffffff", 0.035, accent, 0.14))
    out.append(f'<path d="M {node_ix + 1.5} {by + 9} V {by + h - 9}" stroke="{accent}" '
               f'stroke-opacity="0.8" stroke-width="3" stroke-linecap="round"/>')
    out.append(text(node_ix + 14, by + 21, name.upper(), "hn", 10.5, 700, accent, ls=0.9))
    ry = by + 13
    for kind, payload in rows:
        if kind == "chips":
            s, hh = chip_row(blk_cx, ry, blk_cw, payload)
            out.append(s)
            ry += hh + 6
        else:
            out.append(text(blk_cx + 1, ry + 12, payload, "hn", 11.5, 400, DIM))
            ry += 18
    by += h + 9

# ── right column: HA ghosts + outbound ──
gh_h, g_gap = 96, 10
g_w = (RIGHT_W - g_gap) / 2
for i, lab in enumerate(["node 2", "node N"]):
    gx = RIGHT_X + i * (g_w + g_gap)
    out.append(rect(gx, node_y, g_w, gh_h, 12, "#0c1424", 0.55, BLUE, 0.22,
                    extra=' stroke-dasharray="5 4"'))
    out.append(text(gx + g_w / 2, node_y + 28, lab, "hn", 13.5, 700, "#cbd5e1", anchor="middle"))
    out.append(text(gx + g_w / 2, node_y + 47, "identical", "hn", 11, 400, FAINT, anchor="middle"))
    out.append(text(gx + g_w / 2, node_y + 62, "stateless", "hn", 11, 400, FAINT, anchor="middle"))
    out.append(text(gx + g_w / 2, node_y + 82, "shares the state below", "hn", 10, 400,
                    FAINT, anchor="middle", op=0.85))
out.append(text(RIGHT_X + RIGHT_W / 2, node_y + gh_h + 23, "HIGH AVAILABILITY", "hn", 10.5,
                700, DIM, anchor="middle", ls=1.0))
out.append(text(RIGHT_X + RIGHT_W / 2, node_y + gh_h + 38, "run N nodes behind the LB",
                "hn", 10.5, 400, FAINT, anchor="middle"))

# fan-out arrows LB → node + ghosts
fan_y = lb_bot + 13
out.append(f'<path d="M {CX} {lb_bot} V {fan_y}" stroke="{BLUE}" stroke-opacity="0.55" '
           f'stroke-width="1.6"/>')
for tx in (MAIN_X + MAIN_W / 2, RIGHT_X + g_w / 2, RIGHT_X + g_w + g_gap + g_w / 2):
    out.append(f'<path d="M {CX} {fan_y} H {tx}" stroke="{BLUE}" stroke-opacity="0.4" '
               f'stroke-width="1.4"/>')
    out.append(arrow(tx, fan_y, tx, node_y - 5))

# outbound panel, sized to content
ob_tw_ = RIGHT_W - 32
ob_items = [(n, wrap(d, ob_tw_)) for n, d in OUTBOUND]
ITEM_GAP = 13
ob_h = 40 + sum(24 + len(ls) * 13 for _, ls in ob_items) + ITEM_GAP * (len(ob_items) - 1) + 14
ob_y = node_y + gh_h + 54
out.append(rect(RIGHT_X, ob_y, RIGHT_W, ob_h, 12, "#0c1424", 0.7, CYAN, 0.24, 1.0,
                extra=' stroke-dasharray="6 4"'))
out.append(text(RIGHT_X + 16, ob_y + 25, "OUTBOUND", "hn", 11, 700, CYAN, ls=1.1))
oy = ob_y + 40
for name, lines in ob_items:
    out.append(f'<circle cx="{RIGHT_X + 19:.1f}" cy="{oy + 6:.1f}" r="2.5" fill="{CYAN}" '
               f'fill-opacity="0.8"/>')
    out.append(text(RIGHT_X + 28, oy + 10, name, "hn", 12.2, 700, "#e2e8f0"))
    for j, ln in enumerate(lines):
        out.append(text(RIGHT_X + 28, oy + 25 + j * 13, ln, "hn", 10.8, 400, FAINT))
    oy += 24 + len(lines) * 13 + ITEM_GAP

out.append(arrow(MAIN_X + MAIN_W + 5, ob_y + ob_h / 2, RIGHT_X - 5, ob_y + ob_h / 2,
                 color=CYAN, op=0.6, dash="5 4"))

y = node_y + node_h
for tx in (MAIN_X + MAIN_W * 0.18, MAIN_X + MAIN_W * 0.5, MAIN_X + MAIN_W * 0.82):
    out.append(arrow(tx, y + 5, tx, y + 27))
y += 33

# ── shared state band ──
sh_x, sh_w = PAD, W - 2 * PAD
gap = 16
card_w = (sh_w - 2 * gap) / 3
maxlines = max(len(b[3]) for b in SHARED)
card_h = 62 + maxlines * 16 + 12
band_h = card_h + 48
out.append(rect(sh_x, y, sh_w, band_h, 14, "#ffffff", 0.03, BLUE, 0.18))
out.append(text(sh_x + 18, y + 25, "SHARED STATE", "hn", 11, 700, DIM, ls=1.1))
out.append(text(sh_x + 18 + tw("SHARED STATE", "hn", 11, 700, ls=1.1) + 12, y + 25,
                "— every node reads and writes the same three backends", "hn", 11.5, 400, FAINT))
cy0 = y + 36
for i, (name, accent, tag, items) in enumerate(SHARED):
    cx0 = sh_x + i * (card_w + gap)
    out.append(rect(cx0, cy0, card_w, card_h, 10, "#ffffff", 0.04, accent, 0.24))
    out.append(f'<path d="M {cx0 + 1.5} {cy0 + 10} V {cy0 + card_h - 10}" stroke="{accent}" '
               f'stroke-opacity="0.8" stroke-width="3" stroke-linecap="round"/>')
    out.append(text(cx0 + 16, cy0 + 25, name, "hn", 14.5, 700, TEXT))
    out.append(text(cx0 + 16, cy0 + 42, tag, "hn", 11, 400, accent, op=0.92))
    for j, it in enumerate(items):
        ly = cy0 + 62 + j * 16
        out.append(f'<circle cx="{cx0 + 19:.1f}" cy="{ly - 3.5:.1f}" r="2" fill="{accent}" '
                   f'fill-opacity="0.7"/>')
        out.append(text(cx0 + 27, ly, it, "hn", 11.2, 400, DIM))
y += band_h + 22

out.append(text(PAD + 2, y,
                "Solid arrows: the request path.   Dashed: optional or outbound.   "
                "Without Redis a single node runs fine; with it, N nodes coordinate.",
                "hn", 11, 400, FAINT))
H = y + 26

# ───────────────────────────── ASSEMBLE ────────────────────────────────────
defs.append(f'<linearGradient id="holo" x1="0" y1="0" x2="1" y2="0.36">'
            f'<stop offset="0" stop-color="{HOLO_A}"/>'
            f'<stop offset="0.6" stop-color="{HOLO_B}"/>'
            f'<stop offset="1" stop-color="{HOLO_B}"/></linearGradient>')
defs.append(f'<radialGradient id="gl" cx="0.05" cy="0.13" r="0.55">'
            f'<stop offset="0" stop-color="{BLUE}" stop-opacity="0.17"/>'
            f'<stop offset="1" stop-color="{BLUE}" stop-opacity="0"/></radialGradient>')
defs.append(f'<radialGradient id="gr" cx="0.97" cy="0.45" r="0.5">'
            f'<stop offset="0" stop-color="{PURPLE}" stop-opacity="0.15"/>'
            f'<stop offset="1" stop-color="{PURPLE}" stop-opacity="0"/></radialGradient>')
defs.append(f'<radialGradient id="gb" cx="0.45" cy="1.0" r="0.55">'
            f'<stop offset="0" stop-color="{HOLO_B}" stop-opacity="0.10"/>'
            f'<stop offset="1" stop-color="{HOLO_B}" stop-opacity="0"/></radialGradient>')
defs.append(f'<marker id="ah" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="5.5" '
            f'markerHeight="5.5" orient="auto-start-reverse">'
            f'<path d="M 0 1 L 9 5 L 0 9 z" fill="{BLUE}" fill-opacity="0.8"/></marker>')

svg = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H:.0f}" '
       f'viewBox="0 0 {W} {H:.0f}">', "<defs>" + "".join(defs) + "</defs>",
       f'<rect width="{W}" height="{H:.0f}" fill="{BG}"/>',
       f'<rect width="{W}" height="{H:.0f}" fill="url(#gl)"/>',
       f'<rect width="{W}" height="{H:.0f}" fill="url(#gr)"/>',
       f'<rect width="{W}" height="{H:.0f}" fill="url(#gb)"/>'] + out + ["</svg>"]

with open("nexspence-architecture.svg", "w") as f:
    f.write("\n".join(svg))
print(f"wrote nexspence-architecture.svg  {W}x{H:.0f}")
