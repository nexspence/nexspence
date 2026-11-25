#!/usr/bin/env node
/**
 * Builds the on-site format guides.
 *
 *   docs/guides/*.md  ->  website/docs/guides/*.html
 *
 * The guides are the best SEO material the project has ("npm proxy behind
 * nginx", "pip index-url self-hosted", "maven settings.xml nexus alternative"),
 * but until now they only existed on GitHub. This script renders every guide
 * as a standalone page in the static site so that search engines can index them.
 *
 * Rules of the road:
 *   - deterministic and idempotent: running it twice produces byte-identical
 *     files (no timestamps, no random ids, sorted iteration order);
 *   - loud failures: a missing, empty or unparsable guide aborts the build
 *     with a non-zero exit code. A silent partial build is worse than a
 *     failed deploy.
 *
 * Usage:  node website/build-guides.mjs
 */

import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { Marked, Renderer } from 'marked';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HERE, '..');
const GUIDES_DIR = path.join(REPO_ROOT, 'docs', 'guides');
const OUT_DIR = path.join(HERE, 'docs', 'guides');
const SITEMAP_FILE = path.join(HERE, 'sitemap.xml');

const SITE_ORIGIN = 'https://nexspence.com';
const SITE_GUIDES_PREFIX = `${SITE_ORIGIN}/docs/guides/`;
const GITHUB_BLOB = 'https://github.com/nexspence/nexspence/blob/main';
const OG_IMAGE = `${SITE_ORIGIN}/assets/og-image.jpg`;

// The index of the guides directory is rendered as /docs/guides/index.html.
const INDEX_FILE = 'README.md';

// The guides the site promises to have. A missing one fails the build —
// the website must not silently lose a page that the docs link to.
const EXPECTED_GUIDES = [
  'alpine',
  'apt',
  'cargo',
  'conan',
  'conda',
  'cran',
  'docker',
  'github-actions',
  'go',
  'helm',
  'maven',
  'npm',
  'nuget',
  'pypi',
  'raw',
  'rubygems',
  'terraform',
];

// Used when git is unavailable (e.g. a source tarball without history).
const FALLBACK_LASTMOD = '2026-08-08';

const MAX_DESCRIPTION = 160;

/* ------------------------------------------------------------------ utils */

function escapeHtml(text) {
  return String(text)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function escapeAttr(text) {
  return escapeHtml(text);
}

function decodeEntities(text) {
  return String(text)
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, '&');
}

function stripTags(html) {
  return decodeEntities(String(html).replace(/<[^>]*>/g, ''));
}

function collapseWhitespace(text) {
  return String(text).replace(/\s+/g, ' ').trim();
}

/**
 * GitHub-style heading slug: lower case, punctuation dropped, spaces become
 * dashes. The anchors written in the markdown (`#7-the-compact-index`) have to
 * match the ids the generator emits, so this must stay in sync with GitHub.
 */
function slugify(text) {
  return collapseWhitespace(
    String(text)
      .replace(/<[^>]*>/g, ' ')
      .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
      .replace(/[`*_~]/g, '')
      .toLowerCase(),
  )
    .replace(/[^\p{L}\p{N}\s_-]/gu, '')
    .replace(/\s+/g, '-');
}

/** A slugger that keeps ids unique inside one page, the way GitHub does. */
function makeSlugger() {
  const seen = new Map();
  return (text) => {
    const base = slugify(text) || 'section';
    if (!seen.has(base)) {
      seen.set(base, 0);
      return base;
    }
    const next = seen.get(base) + 1;
    seen.set(base, next);
    return `${base}-${next}`;
  };
}

function truncate(text, max) {
  if (text.length <= max) return text;
  const cut = text.slice(0, max - 1);
  const space = cut.lastIndexOf(' ');
  return `${(space > max * 0.6 ? cut.slice(0, space) : cut).trimEnd()}…`;
}

/** Last commit date of a repository file (YYYY-MM-DD), for the sitemap. */
function gitLastMod(relPath) {
  try {
    const out = execFileSync('git', ['log', '-1', '--format=%cs', '--', relPath], {
      cwd: REPO_ROOT,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    }).trim();
    if (out) return out;
  } catch {
    // No git history available — the fallback keeps the build working.
  }
  return FALLBACK_LASTMOD;
}

/* ----------------------------------------------------------- link rewrite */

/**
 * Rewrites a markdown link target for the generated site.
 *
 *   maven.md                     -> /docs/guides/maven.html
 *   maven.md#section             -> /docs/guides/maven.html#section
 *   ../security-rbac.md          -> https://github.com/.../blob/main/docs/security-rbac.md
 *   ../../scripts/foo.sh         -> https://github.com/.../blob/main/scripts/foo.sh
 *   #anchor                      -> #anchor (the page emits the matching id)
 *   https://example.com          -> unchanged
 */
function rewriteHref(href, sourceFile) {
  if (!href) return href;
  if (href.startsWith('#')) return href;
  if (href.startsWith('//')) return href;
  if (/^[a-z][a-z0-9+.-]*:/i.test(href)) return href; // http:, mailto:, tel:, ...
  if (href.startsWith('/')) return href; // already site-absolute

  const hashAt = href.indexOf('#');
  const pathPart = hashAt === -1 ? href : href.slice(0, hashAt);
  const fragment = hashAt === -1 ? '' : href.slice(hashAt);
  if (!pathPart) return href;

  const resolved = path.resolve(GUIDES_DIR, pathPart);
  const relToRepo = path.relative(REPO_ROOT, resolved).split(path.sep).join('/');
  if (relToRepo.startsWith('..')) {
    throw new Error(`${sourceFile}: link escapes the repository: ${href}`);
  }
  if (!existsSync(resolved)) {
    throw new Error(`${sourceFile}: link target does not exist: ${href} (${relToRepo})`);
  }

  // Another guide in the same directory -> the generated page.
  if (pathPart.endsWith('.md') && resolved.startsWith(GUIDES_DIR + path.sep)) {
    const name = path.basename(resolved, '.md');
    return `/docs/guides/${name}.html${fragment}`;
  }

  // Everything else (docs pages, scripts, specs) -> the repository on GitHub.
  return `${GITHUB_BLOB}/${relToRepo}${fragment}`;
}

/* ------------------------------------------------------------- rendering */

const defaultRenderer = new Renderer();

/** Renders one markdown file into the full HTML page. */
function renderPage(sourceFile, markdown) {
  const slug = makeSlugger();
  const md = new Marked({ gfm: true, breaks: false });

  md.use({
    renderer: {
      heading(token) {
        const inner = this.parser.parseInline(token.tokens);
        const id = slug(token.text);
        return `<h${token.depth} id="${escapeAttr(id)}">${inner}</h${token.depth}>\n`;
      },

      code(token) {
        const lang = (token.lang || '').trim().split(/\s+/)[0] || 'text';
        const body = token.escaped ? token.text : escapeHtml(token.text ?? '');
        return (
          `<div class="gcode"><div class="gcode-head"><span>${escapeHtml(lang)}</span></div>` +
          `<pre><code>${body}</code></pre></div>\n`
        );
      },

      link(token) {
        const inner =
          token.tokens && token.tokens.length
            ? this.parser.parseInline(token.tokens)
            : escapeHtml(token.text);
        const href = rewriteHref(token.href, sourceFile);
        const title = token.title ? ` title="${escapeAttr(token.title)}"` : '';
        const external = /^https?:/i.test(href);
        const rel = external ? ' target="_blank" rel="noopener noreferrer"' : '';
        return `<a href="${escapeAttr(href)}"${title}${rel}>${inner}</a>`;
      },

      image(token) {
        const href = rewriteHref(token.href, sourceFile);
        const title = token.title ? ` title="${escapeAttr(token.title)}"` : '';
        const alt = token.text ? escapeAttr(token.text) : '';
        return `<img src="${escapeAttr(href)}" alt="${alt}"${title}>`;
      },

      table(token) {
        return `<div class="gtable">${defaultRenderer.table.call(this, token)}</div>\n`;
      },
    },
  });

  let body;
  try {
    body = md.parse(markdown);
  } catch (err) {
    // marked appends a "please report this" note to renderer errors — drop it,
    // the real cause is ours.
    const message = String(err.message)
      .replace(/\n?Please report this to .*$/s, '')
      .trim();
    throw new Error(message.startsWith(`${sourceFile}: `) ? message : `${sourceFile}: ${message}`);
  }
  if (typeof body !== 'string' || body.trim() === '') {
    throw new Error(`${sourceFile}: markdown produced an empty document`);
  }

  const h1Match = body.match(/<h1\b[^>]*>([\s\S]*?)<\/h1>/);
  if (!h1Match) {
    throw new Error(`${sourceFile}: no level-1 heading found`);
  }
  const h1Count = (body.match(/<h1\b/g) || []).length;
  if (h1Count !== 1) {
    throw new Error(`${sourceFile}: expected exactly one level-1 heading, found ${h1Count}`);
  }

  const h1Text = collapseWhitespace(stripTags(h1Match[1]));
  const isIndex = sourceFile === INDEX_FILE;
  const pageTitle = isIndex ? 'Format Setup Guides' : guideName(h1Text);

  const paraMatch = body.match(/<p\b[^>]*>([\s\S]*?)<\/p>/);
  if (!paraMatch) {
    throw new Error(`${sourceFile}: no paragraph found for the meta description`);
  }
  const description = truncate(collapseWhitespace(stripTags(paraMatch[1])), MAX_DESCRIPTION);

  const pageName = isIndex ? 'index' : path.basename(sourceFile, '.md');
  // The index is served from the directory itself (/docs/guides/), the other
  // pages from their own .html file.
  const canonical = isIndex ? SITE_GUIDES_PREFIX : `${SITE_GUIDES_PREFIX}${pageName}.html`;
  const sourceUrl = `${GITHUB_BLOB}/docs/guides/${sourceFile}`;

  return renderShell({
    pageTitle: `${pageTitle} — Nexspence guide`,
    description,
    canonical,
    sourceFile,
    sourceUrl,
    pageName,
    isIndex,
    body,
  });
}

/** "Maven guide — hosted, proxy and group repositories" -> "Maven". */
function guideName(h1Text) {
  // The casing of the heading is kept as the author wrote it ("npm" stays
  // "npm", not "Npm").
  const cut = h1Text.replace(/\s+guide\b.*$/i, '').trim();
  return cut || h1Text;
}

function renderShell({
  pageTitle,
  description,
  canonical,
  sourceFile,
  sourceUrl,
  pageName,
  isIndex,
  body,
}) {
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>${escapeHtml(pageTitle)}</title>
<link rel="icon" type="image/png" href="/assets/favicon.png">
<link rel="apple-touch-icon" href="/assets/apple-touch-icon.png">
<meta name="description" content="${escapeAttr(description)}">
<link rel="canonical" href="${escapeAttr(canonical)}">
<meta name="theme-color" content="#070b14">
<meta property="og:type" content="article">
<meta property="og:site_name" content="Nexspence">
<meta property="og:url" content="${escapeAttr(canonical)}">
<meta property="og:title" content="${escapeAttr(pageTitle)}">
<meta property="og:description" content="${escapeAttr(description)}">
<meta property="og:image" content="${OG_IMAGE}">
<meta property="og:image:width" content="1200">
<meta property="og:image:height" content="630">
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:title" content="${escapeAttr(pageTitle)}">
<meta name="twitter:description" content="${escapeAttr(description)}">
<meta name="twitter:image" content="${OG_IMAGE}">
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@300;400;500;600;700;800;900&family=JetBrains+Mono:wght@400;500;600&display=swap" rel="stylesheet">
<style>
:root{
  --bg:#070b14;--bg2:#0c1322;
  --blue:#3b82f6;--cyan:#06b6d4;--green:#22c55e;--amber:#f59e0b;--red:#ef4444;--purple:#8b5cf6;
  --text:#f1f5f9;--dim:#94a3b8;--faint:#475569;
  --glass:rgba(255,255,255,.04);--glasm:rgba(255,255,255,.07);
  --border:rgba(59,130,246,.14);--borhi:rgba(59,130,246,.38);
}
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
html{scroll-behavior:smooth}
body{font-family:'Inter',system-ui,sans-serif;background:var(--bg);color:var(--text);line-height:1.7;overflow-x:hidden;min-height:100vh}
a{color:#7dd3fc;text-decoration:none}
a:hover{text-decoration:underline}
.ghead{position:sticky;top:0;z-index:50;background:rgba(7,11,20,.92);backdrop-filter:blur(16px) saturate(160%);border-bottom:1px solid var(--border)}
.ghead-inner{max-width:1100px;margin:0 auto;padding:12px 24px;display:flex;align-items:center;justify-content:space-between;gap:16px;flex-wrap:wrap}
.gbrand{display:flex;align-items:center;gap:8px;color:var(--text);text-decoration:none;font-weight:800;letter-spacing:-.01em}
.gbrand:hover{text-decoration:none}
.gbrand img{width:26px;height:26px;object-fit:contain}
.gnav{display:flex;gap:4px;flex-wrap:wrap}
.gnav a{padding:5px 12px;border-radius:100px;color:var(--dim);font-size:.76rem;font-weight:600;text-decoration:none}
.gnav a:hover{color:var(--text);background:var(--glasm);text-decoration:none}
.gmain{max-width:900px;margin:0 auto;padding:28px 24px 64px}
.gbread{display:flex;gap:6px;align-items:center;font-size:.7rem;color:var(--faint);flex-wrap:wrap;margin-bottom:18px}
.gbread a{color:var(--dim);text-decoration:none}
.gbread a:hover{color:var(--text)}
.gbread .sep{color:var(--faint)}
.gbread .cur{color:var(--text);font-weight:600}
.gsource{font-size:.72rem;color:var(--faint);margin:0 0 24px}
.gsource a{color:var(--dim);text-decoration:none;border-bottom:1px solid var(--faint)}
.gsource a:hover{color:var(--text)}
.garticle h1{font-size:clamp(1.6rem,4vw,2.3rem);font-weight:900;letter-spacing:-.03em;line-height:1.2;margin:0 0 12px}
.garticle h2{font-size:1.3rem;font-weight:800;letter-spacing:-.02em;margin:40px 0 12px;padding-bottom:8px;border-bottom:1px solid var(--border)}
.garticle h3{font-size:1.02rem;font-weight:700;margin:26px 0 10px;color:#cbd5e1}
.garticle h4{font-size:.92rem;font-weight:700;margin:22px 0 8px;color:var(--dim)}
.garticle p{margin:12px 0;font-size:.92rem;color:#cbd5e1}
.garticle ul,.garticle ol{margin:12px 0 12px 22px;font-size:.92rem;color:#cbd5e1}
.garticle li{margin:5px 0}
.garticle li>p{margin:6px 0}
.garticle strong{color:var(--text)}
.garticle hr{border:none;border-top:1px solid var(--border);margin:34px 0}
.garticle code{font-family:'JetBrains Mono',monospace;font-size:.82em;color:var(--cyan);background:rgba(6,182,212,.1);padding:1px 5px;border-radius:4px}
.garticle a code{color:#7dd3fc;background:rgba(125,211,252,.1)}
.garticle blockquote{margin:16px 0;padding:10px 16px;border-left:3px solid var(--borhi);background:var(--glass);border-radius:0 8px 8px 0;color:var(--dim);font-size:.88rem}
.garticle blockquote p{margin:4px 0}
.garticle img{max-width:100%;height:auto}
.garticle input[type=checkbox]{accent-color:var(--blue)}
.gcode{margin:16px 0;border:1px solid rgba(59,130,246,.18);border-radius:10px;overflow:hidden;background:rgba(0,0,0,.45)}
.gcode-head{display:flex;align-items:center;justify-content:space-between;padding:6px 12px;background:rgba(255,255,255,.025);border-bottom:1px solid rgba(59,130,246,.12)}
.gcode-head span{font-family:'JetBrains Mono',monospace;font-size:.64rem;font-weight:600;color:var(--faint);text-transform:uppercase;letter-spacing:.06em}
.gcode pre{margin:0;padding:14px 16px;overflow-x:auto}
.gcode pre code{font-family:'JetBrains Mono',monospace;font-size:.78rem;line-height:1.7;color:#cbd5e1;background:none;padding:0;border-radius:0;white-space:pre}
.gtable{overflow-x:auto;margin:16px 0;border:1px solid var(--border);border-radius:10px;-webkit-overflow-scrolling:touch}
.gtable table{width:100%;border-collapse:collapse;font-size:.82rem;min-width:460px}
.gtable th{padding:9px 12px;text-align:left;font-weight:700;font-size:.65rem;text-transform:uppercase;letter-spacing:.08em;color:var(--faint);border-bottom:1px solid var(--border);background:var(--glass)}
.gtable td{padding:9px 12px;border-bottom:1px solid rgba(59,130,246,.06);vertical-align:top}
.gtable tr:last-child td{border-bottom:none}
.gtable tr:hover td{background:rgba(255,255,255,.02)}
.gfoot{border-top:1px solid var(--border);background:rgba(7,11,20,.95);padding:30px 24px;text-align:center}
.gfoot-logo{display:flex;align-items:center;gap:8px;justify-content:center;margin-bottom:8px}
.gfoot-logo img{width:22px;height:22px;object-fit:contain}
.gfoot-logo span{font-weight:700;font-size:.88rem}
.gfoot-sub{font-size:.76rem;color:var(--faint);margin-bottom:14px}
.gfoot-links{display:flex;gap:14px;justify-content:center;flex-wrap:wrap}
.gfoot-links a{font-size:.76rem;color:var(--faint);text-decoration:none}
.gfoot-links a:hover{color:var(--text)}
@media(max-width:640px){
  .gmain{padding:20px 16px 48px}
  .ghead-inner{padding:10px 16px}
  .garticle p,.garticle ul,.garticle ol{font-size:.88rem}
}
</style>
</head>
<body>
<header class="ghead">
  <div class="ghead-inner">
    <a class="gbrand" href="/"><img src="/assets/mini_logo.png" alt="Nexspence" width="26" height="26"><span>Nexspence</span></a>
    <nav class="gnav" aria-label="Main navigation">
      <a href="/">Home</a>
      <a href="/docs/">Docs</a>
      <a href="/docs/guides/">Format guides</a>
      <a href="https://github.com/nexspence/nexspence" target="_blank" rel="noopener noreferrer">GitHub</a>
    </nav>
  </div>
</header>
<main class="gmain">
  <nav class="gbread" aria-label="Breadcrumb">
    <a href="/">nexspence.com</a><span class="sep" aria-hidden="true">/</span><a href="/docs/">docs</a><span class="sep" aria-hidden="true">/</span><a href="/docs/guides/">guides</a><span class="sep" aria-hidden="true">/</span><span class="cur">${escapeHtml(pageName)}</span>
  </nav>
  <article class="garticle">
${body}  </article>
  <p class="gsource">This page is generated from <code>docs/guides/${escapeHtml(sourceFile)}</code> in the repository &mdash; <a href="${escapeAttr(sourceUrl)}" target="_blank" rel="noopener noreferrer">view the source on GitHub</a>${isIndex ? ' or browse <a href="/docs/guides/">all format guides</a>' : ''}.</p>
</main>
<footer class="gfoot">
  <div class="gfoot-logo"><img src="/assets/mini_logo.png" alt="Nexspence" width="22" height="22"><span>Nexspence</span></div>
  <div class="gfoot-sub">&copy; 2026 Nexspence Contributors &middot; Licensed under <a href="https://www.gnu.org/licenses/agpl-3.0.html" target="_blank" rel="noopener noreferrer">GNU AGPL v3</a> or later</div>
  <div class="gfoot-links">
    <a href="/">Home</a>
    <a href="/docs/">Docs</a>
    <a href="/docs/guides/">All format guides</a>
    <a href="https://github.com/nexspence/nexspence" target="_blank" rel="noopener noreferrer">GitHub</a>
  </div>
</footer>
</body>
</html>
`;
}

/* --------------------------------------------------------------- sitemap */

/**
 * Rewrites sitemap.xml: the hand-maintained entries are kept as they are, the
 * guide entries are (re)generated from the guides that exist. Running the
 * generator twice therefore produces the same file.
 */
async function writeSitemap(pages) {
  let xml;
  try {
    xml = await readFile(SITEMAP_FILE, 'utf8');
  } catch {
    xml = '<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n</urlset>\n';
  }

  const firstUrl = xml.indexOf('<url>');
  const head = (firstUrl === -1 ? xml : xml.slice(0, firstUrl)).replace(/\s+$/, '') + '\n';
  const blocks = xml.match(/<url>[\s\S]*?<\/url>/g) || [];
  const kept = blocks.filter(
    (block) => !/<loc>\s*https:\/\/nexspence\.com\/docs\/guides\//.test(block),
  );

  const guideBlocks = pages.map((page) =>
    [
      '  <url>',
      `    <loc>${page.canonical}</loc>`,
      `    <lastmod>${page.lastmod}</lastmod>`,
      `    <changefreq>${page.changefreq}</changefreq>`,
      `    <priority>${page.priority}</priority>`,
      '  </url>',
    ].join('\n'),
  );

  // Normalise the indentation so the file looks the same no matter how the
  // hand-maintained entries were formatted.
  const format = (block) => {
    const inner = block
      .replace(/^\s*<url>/, '')
      .replace(/<\/url>\s*$/, '')
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line !== '');
    return ['  <url>', ...inner.map((line) => `    ${line}`), '  </url>'].join('\n');
  };

  await writeFile(
    SITEMAP_FILE,
    `${head}${[...kept, ...guideBlocks].map(format).join('\n')}\n</urlset>\n`,
    'utf8',
  );
}

/* ------------------------------------------------------------------ main */

async function main() {
  if (!existsSync(GUIDES_DIR)) {
    throw new Error(`guides directory not found: ${GUIDES_DIR}`);
  }

  const files = (await readdir(GUIDES_DIR)).filter((f) => f.endsWith('.md')).sort();
  if (files.length === 0) {
    throw new Error(`no markdown guides found in ${GUIDES_DIR}`);
  }

  const missing = EXPECTED_GUIDES.filter((name) => !files.includes(`${name}.md`));
  if (missing.length > 0) {
    throw new Error(`missing guide(s): ${missing.map((m) => `${m}.md`).join(', ')}`);
  }
  const unexpected = files.filter(
    (f) => f !== INDEX_FILE && !EXPECTED_GUIDES.includes(path.basename(f, '.md')),
  );
  if (unexpected.length > 0) {
    console.warn(
      `note: ${unexpected.join(', ')} not listed in EXPECTED_GUIDES — rendering them anyway, ` +
        'please add them to the list and to website/docs/index.html',
    );
  }

  await rm(OUT_DIR, { recursive: true, force: true });
  await mkdir(OUT_DIR, { recursive: true });

  const pages = [];
  const written = [];

  for (const file of files) {
    const source = await readFile(path.join(GUIDES_DIR, file), 'utf8');
    if (!source.trim()) {
      throw new Error(`${file}: guide is empty`);
    }
    const html = renderPage(file, source);
    const isIndex = file === INDEX_FILE;
    const pageName = isIndex ? 'index' : path.basename(file, '.md');
    const outName = `${pageName}.html`;
    await writeFile(path.join(OUT_DIR, outName), html, 'utf8');
    written.push(outName);
    pages.push({
      canonical: isIndex ? SITE_GUIDES_PREFIX : `${SITE_GUIDES_PREFIX}${outName}`,
      lastmod: gitLastMod(path.join('docs', 'guides', file)),
      changefreq: isIndex ? 'weekly' : 'monthly',
      priority: isIndex ? '0.8' : '0.6',
    });
  }

  await writeSitemap(pages);
  verifyLinks(written);

  console.log(`Rendered ${written.length} guide pages from ${path.relative(REPO_ROOT, GUIDES_DIR)}:`);
  for (const name of written) console.log(`  - ${name}`);
  console.log(`Output: ${path.relative(REPO_ROOT, OUT_DIR)}`);
  console.log(`Sitemap: ${path.relative(REPO_ROOT, SITEMAP_FILE)} (${pages.length} guide URLs)`);
}

/**
 * Every anchor written in the markdown has to match a heading id the page
 * actually emits — a broken anchor is a silent SEO loss. Checked here so the
 * build fails instead of shipping dead links.
 */
function verifyLinks(written) {
  const idsByPage = new Map();
  for (const name of written) {
    const html = readFileSync(path.join(OUT_DIR, name), 'utf8');
    const ids = new Set();
    for (const m of html.matchAll(/<h[1-6]\b[^>]*\sid="([^"]+)"/g)) ids.add(m[1]);
    idsByPage.set(name, ids);
  }

  const problems = [];
  for (const name of written) {
    const html = readFileSync(path.join(OUT_DIR, name), 'utf8');
    for (const m of html.matchAll(/href="([^"]+)"/g)) {
      const href = m[1];
      let targetPage = name;
      let fragment = '';
      if (href.startsWith('/docs/guides/')) {
        const rest = href.slice('/docs/guides/'.length);
        if (rest === '') continue; // the guide index directory
        const hashAt = rest.indexOf('#');
        targetPage = hashAt === -1 ? rest : rest.slice(0, hashAt);
        fragment = hashAt === -1 ? '' : rest.slice(hashAt + 1);
      } else if (href.startsWith('#')) {
        fragment = href.slice(1);
      } else {
        continue;
      }
      if (!fragment) continue;
      const ids = idsByPage.get(targetPage);
      if (!ids) {
        problems.push(`${name}: link to unknown page ${href}`);
      } else if (!ids.has(fragment)) {
        problems.push(`${name}: anchor ${href} has no matching heading id`);
      }
    }
  }

  if (problems.length > 0) {
    throw new Error(`broken internal links:\n  ${problems.join('\n  ')}`);
  }
}

main().catch((err) => {
  console.error(`build-guides: ${err.message}`);
  process.exitCode = 1;
});
