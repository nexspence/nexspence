/**
 * Tests for the static documentation page (website/docs/index.html).
 *
 * It lives outside the app bundle but is exercised here because this is the
 * only JS test runner in the repo. Issue #114: every documentation section used
 * to live inside a <template>, so a browser that does not run the inline script
 * — and every crawler — saw an empty shell instead of the manual.
 */
import { readFileSync } from 'fs'
import { resolve } from 'path'
import { JSDOM } from 'jsdom'
import { describe, expect, it, beforeAll } from 'vitest'

const DOCS_HTML = resolve(__dirname, '../../../website/docs/index.html')

/** Section keys the page navigates between, changelog excluded (it is fetched). */
const SECTION_KEYS = [
  'quickstart',
  'install',
  'native-install',
  'upgrade',
  'repositories',
  'formats-guide',
  'browse-search',
  'users',
  'rbac',
  'cleanup',
  'webhooks',
  'migration',
  'monitoring',
  'promotion',
  'replication',
  'tf-overview',
  'tf-auth',
  'tf-resources',
  'tf-data',
  'tf-examples',
  'security',
  'formats',
  'api',
]

describe('docs page without JavaScript', () => {
  let doc: Document

  beforeAll(() => {
    const html = readFileSync(DOCS_HTML, 'utf8')
    doc = new DOMParser().parseFromString(html, 'text/html')
  })

  it('puts every documentation section in the document, not in a <template>', () => {
    expect(doc.querySelectorAll('template').length).toBe(0)
    for (const key of SECTION_KEYS) {
      expect(doc.getElementById(`sec-${key}`), `section ${key} is missing`).not.toBeNull()
    }
  })

  it('keeps the section text readable by crawlers and JS-less browsers', () => {
    // Asserted per section: the intro paragraph and the script's translation
    // table mention most of these words, so a page-wide text search would pass
    // even with the whole manual missing.
    for (const key of SECTION_KEYS) {
      const section = doc.getElementById(`sec-${key}`)
      const title = section?.querySelector('.doc-section-title')?.textContent?.trim() ?? ''
      expect(title.length, `section ${key} has no title`).toBeGreaterThan(0)
      expect((section?.textContent ?? '').trim().length, `section ${key} has no body`)
        .toBeGreaterThan(200)
    }
  })

  it('reveals the hidden sections when the script never runs', () => {
    expect(doc.documentElement.className).toContain('no-js')
    const css = Array.from(doc.querySelectorAll('style'))
      .map((s) => s.textContent ?? '')
      .join('\n')
    expect(css).toMatch(/\.no-js\s+\.doc-section\[hidden\]\s*\{[^}]*display:\s*block/)
  })

  it('offers a table of contents that works with plain anchors', () => {
    const links = Array.from(doc.querySelectorAll<HTMLAnchorElement>('#nojs-nav a[href^="#sec-"]'))
    expect(links.length).toBeGreaterThanOrEqual(SECTION_KEYS.length)
    for (const link of links) {
      const id = link.getAttribute('href')!.slice(1)
      expect(doc.getElementById(id), `${id} is linked but does not exist`).not.toBeNull()
    }
  })
})

describe('docs page with JavaScript', () => {
  let dom: JSDOM

  beforeAll(() => {
    const html = readFileSync(DOCS_HTML, 'utf8')
    dom = new JSDOM(html, {
      runScripts: 'dangerously',
      url: 'https://nexspence.com/docs/',
      beforeParse(window) {
        // The releases fetch is optional decoration; keep the test offline.
        ;(window as unknown as { fetch: () => Promise<never> }).fetch = () =>
          Promise.reject(new Error('offline'))
      },
    })
  })

  it('drops the no-js marker so only the selected section shows', () => {
    expect(dom.window.document.documentElement.className).not.toContain('no-js')
  })

  it('shows exactly the section the navigation selects', () => {
    const win = dom.window as unknown as { setSection: (key: string) => void }
    win.setSection('quickstart')

    const doc = dom.window.document
    expect(doc.getElementById('sec-quickstart')!.hasAttribute('hidden')).toBe(false)
    expect(doc.getElementById('sec-install')!.hasAttribute('hidden')).toBe(true)

    win.setSection('install')
    expect(doc.getElementById('sec-quickstart')!.hasAttribute('hidden')).toBe(true)
    expect(doc.getElementById('sec-install')!.hasAttribute('hidden')).toBe(false)
  })
})
