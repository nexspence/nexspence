# Docs Page Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure `website/docs/index.html` into a version-dropdown → Changelog → top-tabs flow, promote Terraform Provider to its own aligned tab, and add OS service-setup docs.

**Architecture:** Single self-contained HTML file. The existing `SIDEBAR_GROUPS` become **top category tabs**; each tab renders a contextual left sub-nav (its group's items) + the existing `tpl-*` section templates. The version pill bar becomes a dropdown that drives the Changelog. Terraform is pulled out of the `advanced` group into its own `terraform` group whose items are five new sub-section templates. Feature badges ("in vX.Y") are added only to post-1.0 sections. Mobile keeps its existing all-groups drawer.

**Tech Stack:** Hand-written HTML + inline CSS + vanilla JS (no framework, no build step). EN/RU i18n via the `TRANSLATIONS` map and `t()` / `applyTranslations()`.

**Reference mockups (on disk, source of truth for markup):**
- `.superpowers/brainstorm/20027-1780972560/content/docs-ia-v2.html` — version dropdown, aligned top tabs, contextual sub-nav, badge placement.
- `.superpowers/brainstorm/20027-1780972560/content/install-os-tabs.html` — Installation method tabs + OS sub-tabs + service blocks.
- `.superpowers/brainstorm/20027-1780972560/content/terraform-tab.html` — Terraform Provider sub-sections.

**Verification note:** This file has no JS test harness. Verification = (a) grep-based structural / i18n-parity checks (automatable), and (b) a manual browser check against the mockups (`cd website && python3 -m http.server 8088`, open `http://localhost:8088/docs/`).

---

## File Structure

Everything lives in `website/docs/index.html`:
- **`<style>` block (lines ~25-214):** add new component CSS.
- **HTML body (~250-303):** swap `.ver-bar` markup for `.ver-dd` dropdown; add `#doc-tabs` strip; sub-nav stays in `#sidebar-inner`.
- **`<template>` blocks (~336-1276):** extend `tpl-native-install`; split `tpl-terraform` into five new templates.
- **JS (~1278-2025):** `SECTIONS`, `SIDEBAR_GROUPS`, replace `renderVersionBar`→`renderVersionDropdown`, split `renderSidebar`→`renderSubnav`+`renderMobileDrawer`, add `renderTabs`/`setTab`, add badge map + `installOS`, update boot sequence, extend `TRANSLATIONS`.

---

## Task 1: New component CSS

**Files:**
- Modify: `website/docs/index.html` (inside `<style>`, before the closing `</style>` at ~214)

- [ ] **Step 1: Add the CSS block**

Insert this immediately before `</style>` (line ~214):

```css
/* === redesign: version dropdown === */
.ver-dd{position:relative}
.ver-dd-btn{display:inline-flex;align-items:center;gap:9px;padding:9px 14px;border-radius:11px;border:1px solid var(--borhi);background:linear-gradient(180deg,rgba(139,92,246,.10),var(--glass));color:var(--text);font-size:.82rem;font-weight:700;cursor:pointer;font-family:'Inter',system-ui,sans-serif;transition:all 150ms;min-width:188px}
.ver-dd-btn:hover{border-color:rgba(139,92,246,.55);background:linear-gradient(180deg,rgba(139,92,246,.16),var(--glasm))}
.ver-dd-btn .vlabel{font-size:.6rem;font-weight:700;text-transform:uppercase;letter-spacing:.08em;color:var(--faint)}
.ver-dd-btn .vbadge{font-size:.56rem;padding:1px 6px;border-radius:4px;background:rgba(34,197,94,.16);color:var(--green);font-weight:800}
.ver-dd-btn .vchev{width:14px;height:14px;color:var(--dim);margin-left:auto;transition:transform 200ms}
.ver-dd.open .vchev{transform:rotate(180deg)}
.ver-menu{position:absolute;top:calc(100% + 6px);left:0;min-width:280px;background:rgba(12,19,34,.97);border:1px solid var(--borhi);border-radius:12px;backdrop-filter:blur(20px);box-shadow:0 18px 50px rgba(0,0,0,.6);padding:6px;z-index:50;display:none;max-height:340px;overflow:auto}
.ver-dd.open .ver-menu{display:block}
.ver-item{display:flex;align-items:center;gap:10px;padding:9px 11px;border-radius:9px;cursor:pointer;border:none;background:transparent;width:100%;text-align:left;font-family:'Inter',system-ui,sans-serif}
.ver-item:hover{background:var(--glasm)}
.ver-item.sel{background:rgba(139,92,246,.14)}
.ver-item .vi-num{font-size:.82rem;font-weight:800;color:var(--text)}
.ver-item .vi-date{font-size:.64rem;color:var(--faint);font-family:'JetBrains Mono',monospace;margin-left:auto}
.ver-item .vi-tag{font-size:.55rem;font-weight:800;padding:1px 6px;border-radius:4px}
.vi-tag.latest{background:rgba(34,197,94,.14);color:var(--green)}
.vi-tag.minor{background:rgba(59,130,246,.12);color:var(--blue)}
.vi-tag.patch{background:rgba(71,85,105,.18);color:var(--faint)}
.ver-menu-foot{border-top:1px solid var(--border);margin-top:4px;padding:9px 11px}
.ver-menu-foot a{font-size:.72rem;color:var(--blue);text-decoration:none}

/* === redesign: top category tabs === */
.doc-tabs{display:flex;gap:2px;border-bottom:1px solid var(--border);margin-bottom:22px;overflow-x:auto}
.doc-tabs::-webkit-scrollbar{height:0}
.doc-tab{position:relative;display:inline-flex;align-items:center;gap:7px;padding:11px 14px;background:transparent;border:none;color:var(--dim);font-size:.83rem;font-weight:600;cursor:pointer;font-family:'Inter',system-ui,sans-serif;white-space:nowrap;transition:color 150ms}
.doc-tab svg{width:15px;height:15px;opacity:.7}
.doc-tab:hover{color:var(--text)}
.doc-tab::after{content:'';position:absolute;left:8px;right:8px;bottom:-1px;height:2px;border-radius:2px;background:transparent;transition:background 150ms}
.doc-tab.active{color:#a78bfa}
.doc-tab.active svg{opacity:1}
.doc-tab.active::after{background:linear-gradient(90deg,var(--purple),var(--blue))}

/* === redesign: feature badges === */
.dsb-badge{margin-left:auto;font-size:.5rem;font-weight:800;padding:1px 5px;border-radius:4px;background:rgba(59,130,246,.14);color:var(--blue)}
.vbadge-inline{font-size:.58rem;font-weight:800;padding:2px 7px;border-radius:5px;background:rgba(59,130,246,.12);color:var(--blue);border:1px solid rgba(59,130,246,.28);vertical-align:middle;margin-left:8px}
.vbadge-inline.tf{background:rgba(139,92,246,.14);color:#a78bfa;border-color:rgba(139,92,246,.32)}
.vbadge-inline.reg{background:rgba(34,197,94,.12);color:var(--green);border-color:rgba(34,197,94,.28)}

/* === redesign: OS sub-tabs (Installation) === */
.os-row{display:flex;gap:6px;flex-wrap:wrap;margin-bottom:18px;padding:8px;background:var(--glass);border:1px solid var(--border);border-radius:12px}
.os-chip{display:inline-flex;align-items:center;gap:8px;padding:8px 16px;border-radius:8px;font-size:.8rem;font-weight:600;cursor:pointer;border:1px solid transparent;background:transparent;color:var(--dim);font-family:'Inter',system-ui,sans-serif;transition:all 150ms}
.os-chip:hover{background:var(--glasm);color:var(--text)}
.os-chip.active{background:rgba(139,92,246,.14);border-color:rgba(139,92,246,.4);color:#a78bfa}
.os-chip svg{width:16px;height:16px}
.os-pnl{display:none}.os-pnl.active{display:block}
.svc-sep{display:flex;align-items:center;gap:10px;margin:22px 0 12px;font-size:.67rem;font-weight:700;text-transform:uppercase;letter-spacing:.1em;color:var(--faint)}
.svc-sep::after{content:'';flex:1;height:1px;background:var(--border)}
.svc-sep .svc{color:var(--purple)}

@media(max-width:768px){.doc-tabs{display:none}}
```

- [ ] **Step 2: Verify the CSS is present and the file still loads**

Run: `grep -c '\.doc-tab{' website/docs/index.html && grep -c '\.ver-dd-btn{' website/docs/index.html && grep -c '\.os-chip{' website/docs/index.html`
Expected: each prints `1`.

- [ ] **Step 3: Commit**

```bash
git add website/docs/index.html
git commit -m "feat(docs): add CSS for version dropdown, top tabs, OS sub-tabs, badges"
```

---

## Task 2: Version dropdown (replace the pill bar)

**Files:**
- Modify: `website/docs/index.html` — HTML `.ver-bar` markup (~259-264), JS `renderVersionBar` (~1877-1896), boot calls (~2017, ~2020, ~2023).

- [ ] **Step 1: Replace the `.ver-bar` markup with the dropdown shell**

Replace lines ~259-264 (the `<div class="ver-bar" id="ver-bar" ...> ... </div>` including its skeletons) with:

```html
<div class="ver-dd" id="ver-dd" style="margin-bottom:0">
  <button type="button" class="ver-dd-btn" id="ver-dd-btn" aria-haspopup="listbox" aria-expanded="false">
    <span class="vlabel" data-i18n="ver.label">Version</span>
    <span class="vnum" id="ver-num">—</span>
    <span class="vbadge" id="ver-badge" style="display:none"></span>
    <svg class="vchev" fill="none" stroke="currentColor" stroke-width="2.2" viewBox="0 0 24 24"><polyline points="6 9 12 15 18 9"/></svg>
  </button>
  <div class="ver-menu" id="ver-menu" role="listbox"></div>
</div>
```

- [ ] **Step 2: Replace `renderVersionBar` with `renderVersionDropdown`**

Replace the whole `renderVersionBar` function (~1877-1896) with:

```javascript
function tagKind(r){
  const patchNum=parseInt(r.tag.split('.')[2]||'0',10);
  return r.isLatest?'latest':(patchNum===0?'minor':'patch');
}
function renderVersionDropdown(releases,activeTag){
  const btnNum=document.getElementById('ver-num');
  const btnBadge=document.getElementById('ver-badge');
  const menu=document.getElementById('ver-menu');
  const active=releases.find(r=>r.tag===activeTag)||releases[0];
  if(!active){return;}
  btnNum.textContent=active.tag;
  const k=tagKind(active);
  btnBadge.style.display='';btnBadge.textContent=k;
  btnBadge.className='vbadge';
  menu.innerHTML='';
  for(const r of releases){
    const k=tagKind(r);
    const item=document.createElement('button');
    item.type='button';item.className='ver-item'+(r.tag===activeTag?' sel':'');
    item.setAttribute('role','option');
    item.innerHTML=`<span class="vi-num">${esc(r.tag)}</span><span class="vi-tag ${k}">${k}</span><span class="vi-date">${esc(r.date)}</span>`;
    item.addEventListener('click',()=>{
      state.activeVersion=r.tag;
      renderVersionDropdown(releases,r.tag);
      document.getElementById('ver-dd').classList.remove('open');
      if(state.activeTab==='changelog')renderChangelog(r.tag);
    });
    menu.appendChild(item);
  }
  const foot=document.createElement('div');foot.className='ver-menu-foot';
  foot.innerHTML=`<a href="https://github.com/nexspence/nexspence/releases" target="_blank" rel="noopener">${t('sb.all-versions')}</a>`;
  menu.appendChild(foot);
}
```

- [ ] **Step 3: Wire the dropdown toggle + outside-click close**

Add this right after the `mob-nav-toggle` click handler (~1998, before the boot IIFE):

```javascript
document.getElementById('ver-dd-btn').addEventListener('click',function(e){
  e.stopPropagation();
  const dd=document.getElementById('ver-dd');
  const open=dd.classList.toggle('open');
  this.setAttribute('aria-expanded',String(open));
});
document.addEventListener('click',function(e){
  const dd=document.getElementById('ver-dd');
  if(dd&&!dd.contains(e.target))dd.classList.remove('open');
});
```

- [ ] **Step 4: Update boot calls that referenced `ver-bar`**

In the boot IIFE (~2013-2024): replace `renderVersionBar(state.releases,state.activeVersion);` with `renderVersionDropdown(state.releases,state.activeVersion);`, and replace both `document.getElementById('ver-bar').innerHTML='';` lines with `document.getElementById('ver-dd').style.display='none';`.

- [ ] **Step 5: Verify**

Run: `grep -c 'renderVersionBar\|getElementById(.ver-bar.)' website/docs/index.html`
Expected: `0` (no leftover references).
Then manual: serve and confirm the dropdown opens, lists versions with date + tag, and selecting one updates the Changelog (after Task 3 makes changelog reachable; for now just confirm the menu renders).

- [ ] **Step 6: Commit**

```bash
git add website/docs/index.html
git commit -m "feat(docs): replace version pill bar with a dropdown driving the changelog"
```

---

## Task 3: Top tabs + contextual sub-nav (core nav refactor)

**Files:**
- Modify: `website/docs/index.html` — `SECTIONS` (~1782-1800), `SIDEBAR_GROUPS` (~1802-1831), HTML around `.docs-layout` (~283-303), `renderSidebar` (~1898-1922), `setSection` (~1957-1965), boot IIFE (~2000-2025).

- [ ] **Step 1: Add the `#doc-tabs` strip to the HTML**

Insert immediately before `<div class="docs-layout">` (line ~283):

```html
<div class="doc-tabs" id="doc-tabs" role="tablist" aria-label="Documentation sections"></div>
```

- [ ] **Step 2: Move `terraform` out of the `advanced` group into its own group**

In `SIDEBAR_GROUPS` (~1819-1825): delete the `{key:'terraform', ...}` line from the `advanced` group. Then insert a new group object immediately after the `advanced` group's closing `]}` and before the `reference` group:

```javascript
  {id:'terraform',items:[
    {key:'tf-overview',  icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>'},
    {key:'tf-auth',      icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>'},
    {key:'tf-resources', icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>'},
    {key:'tf-data',      icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14a9 3 0 0 0 18 0V5"/></svg>'},
    {key:'tf-examples',  icon:'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>'},
  ]},
```

(The five `tf-*` section templates are created in Task 6. The `terraform` tab is usable only after Task 6; that is acceptable because tasks land sequentially and the group simply renders empty content until then.)

- [ ] **Step 3: Register the new sections in `SECTIONS`**

In `SECTIONS` (~1782-1800), replace the single `{key:'terraform', templateId:'tpl-terraform'},` line with:

```javascript
  {key:'tf-overview',  templateId:'tpl-tf-overview'},
  {key:'tf-auth',      templateId:'tpl-tf-auth'},
  {key:'tf-resources', templateId:'tpl-tf-resources'},
  {key:'tf-data',      templateId:'tpl-tf-data'},
  {key:'tf-examples',  templateId:'tpl-tf-examples'},
```

Then, immediately after the `SECTIONS` array, add an empty badge map (populated in Task 4) so the nav functions can reference it without a ReferenceError:

```javascript
let FEATURE_BADGE={};
```

- [ ] **Step 4: Add the tab model + `renderTabs` + `setTab`, and split `renderSidebar`**

Replace the whole `renderSidebar` function (~1898-1922) with the following block. It introduces `TAB_ORDER`, `renderTabs`, `setTab`, `renderSubnav` (desktop contextual sub-nav) and `renderMobileDrawer` (all groups, existing behavior):

```javascript
// Tabs are the sidebar groups, in display order. 'releases' = Changelog (full-width, no sub-nav).
const TAB_ORDER=['releases','getting-started','using','admin','advanced','terraform','reference'];
const TAB_ICON={
  'releases':'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><polyline points="12 7 12 12 15 14"/></svg>',
  'getting-started':'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg>',
  'using':'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/></svg>',
  'admin':'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg>',
  'advanced':'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>',
  'terraform':'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><line x1="16" y1="3" x2="16" y2="13"/><polygon points="3 5 9 8 9 18 3 15 3 5"/><polygon points="9 8 15 5 15 15 9 18 9 8"/><polyline points="16 13 21 11 21 6 16 8"/></svg>',
  'reference':'<svg fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>',
};
// First section key shown when a tab is opened.
function firstSectionOfTab(tabId){
  if(tabId==='releases')return 'changelog';
  const g=SIDEBAR_GROUPS.find(x=>x.id===tabId);
  return g&&g.items.length?g.items[0].key:'changelog';
}
function tabOfSection(key){
  if(key==='changelog')return 'releases';
  const g=SIDEBAR_GROUPS.find(x=>x.items.some(i=>i.key===key));
  return g?g.id:'releases';
}
function renderTabs(activeTab){
  const bar=document.getElementById('doc-tabs');
  bar.innerHTML='';
  for(const id of TAB_ORDER){
    const btn=document.createElement('button');
    btn.type='button';btn.className='doc-tab'+(id===activeTab?' active':'');
    btn.setAttribute('role','tab');
    btn.innerHTML=(TAB_ICON[id]||'')+t('tabg.'+id);
    btn.addEventListener('click',()=>setTab(id));
    bar.appendChild(btn);
  }
}
function setTab(tabId){
  state.activeTab=tabId;
  setSection(firstSectionOfTab(tabId));
}
function renderSubnav(activeTab,activeSection){
  const aside=document.querySelector('.docs-sidebar');
  const inner=document.getElementById('sidebar-inner');
  if(activeTab==='releases'){aside.style.display='none';return;}
  aside.style.display='';
  const group=SIDEBAR_GROUPS.find(g=>g.id===activeTab);
  inner.innerHTML='';
  if(!group)return;
  const cat=document.createElement('div');cat.className='dsb-label';cat.textContent=t('tabg.'+group.id);inner.appendChild(cat);
  for(const item of group.items){
    const btn=document.createElement('button');
    btn.type='button';
    btn.className='dsb-link'+(item.key===activeSection?' active':'');
    const bad=FEATURE_BADGE[item.key];
    btn.innerHTML=(item.icon||'')+t('sb.'+item.key)+(bad?`<span class="dsb-badge">${bad}</span>`:'');
    btn.addEventListener('click',()=>setSection(item.key));
    inner.appendChild(btn);
  }
}
function renderMobileDrawer(){
  const container=document.getElementById('mob-nav-drawer');
  container.innerHTML='';
  for(const group of SIDEBAR_GROUPS){
    const labelEl=document.createElement('span');labelEl.className='dsb-label';labelEl.textContent=t('tabg.'+group.id);container.appendChild(labelEl);
    if(group.id==='releases'){
      const btn=document.createElement('button');btn.type='button';
      btn.className='dsb-link'+(state.activeSection==='changelog'?' active':'');
      btn.innerHTML=(TAB_ICON.releases)+t('tab.changelog');
      btn.addEventListener('click',()=>{state.activeTab='releases';setSection('changelog');closeMobileDrawer();});
      container.appendChild(btn);
    }else{
      for(const item of group.items){
        const btn=document.createElement('button');btn.type='button';
        btn.className='dsb-link'+(state.activeSection===item.key?' active':'');
        const bad=FEATURE_BADGE[item.key];
        btn.innerHTML=(item.icon||'')+t('sb.'+item.key)+(bad?`<span class="dsb-badge">${bad}</span>`:'');
        btn.addEventListener('click',()=>{state.activeTab=group.id;setSection(item.key);closeMobileDrawer();});
        container.appendChild(btn);
      }
    }
    const div=document.createElement('div');div.className='dsb-divider';container.appendChild(div);
  }
}
function closeMobileDrawer(){
  const d=document.getElementById('mob-nav-drawer');d.classList.remove('open');
  const tgl=document.getElementById('mob-nav-toggle');tgl.classList.remove('open');tgl.setAttribute('aria-expanded','false');d.setAttribute('aria-hidden','true');
}
```

- [ ] **Step 5: Update `setSection` to drive tabs + sub-nav**

Replace `setSection` (~1957-1965) with:

```javascript
function setSection(key){
  state.activeSection=key;
  state.activeTab=tabOfSection(key);
  document.getElementById('breadcrumb-current').textContent=t('crumb.'+key);
  document.getElementById('mob-active-page').textContent=(key==='changelog')?t('tab.changelog'):t('sb.'+key);
  renderTabs(state.activeTab);
  renderSubnav(state.activeTab,key);
  renderMobileDrawer();
  if(key==='changelog')renderChangelog(state.activeVersion);
  else renderStaticSection(key);
  if(window.innerWidth<=768)window.scrollTo({top:0,behavior:'smooth'});
}
```

- [ ] **Step 6: Make Changelog the default landing and fix boot calls**

In the boot IIFE (~2000-2025):
- Replace `state.activeSection='quickstart';` and the two following `document.getElementById('breadcrumb-current')...` / `mob-active-page` lines and `renderSidebar([],...)` / `renderStaticSection('quickstart')` with:

```javascript
  state.activeTab='releases';
  state.activeSection='changelog';
  renderTabs('releases');
  renderSubnav('releases','changelog');
  renderMobileDrawer();
  document.getElementById('breadcrumb-current').textContent=t('crumb.changelog');
  document.getElementById('mob-active-page').textContent=t('tab.changelog');
  // #doc-content keeps its static intro paragraphs until releases load (SEO/offline fallback).
```

- In the `try` block, replace `renderVersionDropdown(...); renderSidebar(...);` lines with:

```javascript
      state.activeVersion=state.releases[0].tag;
      renderVersionDropdown(state.releases,state.activeVersion);
      if(state.activeSection==='changelog')renderChangelog(state.activeVersion);
```

- [ ] **Step 7: Add `state.activeTab` to the state object**

In the `state` object (~1280-1283), add `activeTab:'releases',` next to `activeSection`.

- [ ] **Step 8: Verify no stale references and structure is intact**

Run: `grep -c 'renderSidebar(' website/docs/index.html`
Expected: `0`.
Run: `grep -c "tpl-terraform\b" website/docs/index.html`
Expected: `1` (only the old `<template id="tpl-terraform">` remains; removed in Task 6).
Manual: serve, confirm — Changelog tab is default and full-width; clicking each tab shows its sub-nav + first section; tabs are one aligned row with Terraform Provider after API (no right gap). (Terraform tab content is empty until Task 6; that's expected.)

- [ ] **Step 9: Commit**

```bash
git add website/docs/index.html
git commit -m "feat(docs): top category tabs + contextual sub-nav; changelog default; terraform own tab"
```

---

## Task 4: Post-1.0 feature badges

**Files:**
- Modify: `website/docs/index.html` — add `FEATURE_BADGE` map in JS (near `SECTIONS`), and badge the section heading in `renderStaticSection`.

- [ ] **Step 1: Populate the badge map**

Replace the `let FEATURE_BADGE={};` line (added in Task 3) with the populated map:

```javascript
// "in vX.Y" badges — POST-1.0 features ONLY. v1.0.0 sections intentionally have none.
let FEATURE_BADGE={
  'native-install':'v1.13.0',
  'formats-guide':'v1.7.0',
  'replication':'v1.3.0',
  'promotion':'v1.8.1',
  'monitoring':'v1.9.0',
};
```

- [ ] **Step 2: Render the heading badge in `renderStaticSection`**

Replace `renderStaticSection` (~1945-1955) with:

```javascript
function renderStaticSection(key){
  const section=SECTIONS.find(s=>s.key===key);
  if(!section||!section.templateId)return;
  const tpl=document.getElementById(section.templateId);
  if(!tpl)return;
  const container=document.getElementById('doc-content');
  container.innerHTML='';
  const node=tpl.content.cloneNode(true);
  applyTranslations(node);
  const bad=FEATURE_BADGE[key];
  if(bad){
    const title=node.querySelector('.doc-section-title');
    if(title){const span=document.createElement('span');span.className='vbadge-inline';span.textContent='in '+bad;title.appendChild(span);}
  }
  container.appendChild(node);
}
```

- [ ] **Step 3: Verify**

Run: `grep -c "FEATURE_BADGE=" website/docs/index.html`
Expected: `1`.
Manual: serve, open Advanced → Monitoring shows "in v1.9.0" beside the heading and in the sub-nav; Administration sections show no badge; Getting Started → Native Install shows "in v1.13.0".

- [ ] **Step 4: Commit**

```bash
git add website/docs/index.html
git commit -m "feat(docs): add post-1.0 'in vX.Y' feature badges to sub-nav and section headings"
```

---

## Task 5: Installation — OS sub-tabs + service setup

**Files:**
- Modify: `website/docs/index.html` — extend `tpl-native-install` (~1115-1227) with OS sub-tabs + service blocks; add `installOS()` JS.

Markup source: `.superpowers/brainstorm/20027-1780972560/content/install-os-tabs.html` (the `#pm-native` block). Port its `.os-row` + three `.os-pnl` (`os-linux`, `os-macos`, `os-windows`) sections, keeping the existing `.cb` / `.step-item` / `.alert-info` classes, and wrapping captions in `data-i18n`.

- [ ] **Step 1: Insert the OS sub-tab UI into `tpl-native-install`**

Inside `<template id="tpl-native-install">`, after the existing intro/download content and before its closing `</template>`, add the OS row + panels. Use this exact markup (the service-config code blocks are the real, verified deliverable):

```html
<div class="os-row" role="tablist">
  <button type="button" class="os-chip active" onclick="installOS('linux',this)"><svg fill="none" stroke="currentColor" stroke-width="1.8" viewBox="0 0 24 24"><path d="M16 18a4 4 0 0 0-8 0M12 4a3 3 0 0 1 3 3v4a3 3 0 0 1-6 0V7a3 3 0 0 1 3-3z"/></svg> Linux</button>
  <button type="button" class="os-chip" onclick="installOS('macos',this)"><svg fill="currentColor" viewBox="0 0 24 24"><path d="M16.365 1.43c0 1.14-.493 2.27-1.177 3.08-.744.9-1.99 1.57-2.987 1.57-.12 0-.23-.02-.3-.03-.01-.06-.04-.22-.04-.39 0-1.15.572-2.27 1.206-2.98.804-.94 2.142-1.64 3.248-1.68.03.13.05.28.05.43zm4.565 15.71c-.03.07-.463 1.58-1.518 3.12-.945 1.34-1.94 2.71-3.43 2.71-1.517 0-1.9-.88-3.63-.88-1.698 0-2.302.91-3.67.91-1.377 0-2.332-1.26-3.428-2.8-1.287-1.82-2.323-4.63-2.323-7.28 0-4.28 2.797-6.55 5.552-6.55 1.448 0 2.675.95 3.6.95.865 0 2.222-1.01 3.902-1.01.613 0 2.886.06 4.374 2.19-.13.09-2.383 1.37-2.383 4.19 0 3.26 2.854 4.42 2.984 4.46z"/></svg> macOS</button>
  <button type="button" class="os-chip" onclick="installOS('windows',this)"><svg fill="currentColor" viewBox="0 0 24 24"><path d="M3 5.1L10.5 4v8H3V5.1zM10.5 13v7L3 18.9V13h7.5zM11.5 3.9L21 3v9h-9.5V3.9zM21 13v8l-9.5-1.3V13H21z"/></svg> Windows</button>
</div>

<div class="os-pnl active" data-os="linux">
  <div class="svc-sep"><span class="svc" data-i18n="ni.svc">⚙ Run as a service</span> — systemd</div>
  <div class="cb"><div class="cb-bar"><span>/etc/systemd/system/nexspence.service</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div><div class="cb-body">[Unit]
Description=Nexspence Artifact Repository
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=simple
User=nexspence
Group=nexspence
WorkingDirectory=/var/lib/nexspence
ExecStart=<span class="ck">/usr/bin/nexspence serve --config /etc/nexspence/config.yaml</span>
Restart=on-failure
RestartSec=5s
Environment=<span class="cm">NEXSPENCE_BOOTSTRAP_ADMIN_PASSWORD</span>=change-me

[Install]
WantedBy=multi-user.target</div></div>
  <div class="cb"><div class="cb-bar"><span>bash</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div><div class="cb-body">sudo systemctl daemon-reload
sudo systemctl enable --now nexspence
sudo systemctl status nexspence
journalctl -u nexspence -f</div></div>
  <div class="alert-info" data-i18n="ni.linux.note"><strong>Tip:</strong> The .deb / .rpm packages can ship this unit pre-installed — then you only run <code>systemctl enable --now nexspence</code>.</div>
</div>

<div class="os-pnl" data-os="macos">
  <div class="svc-sep"><span class="svc" data-i18n="ni.svc2">⚙ Run as a service</span> — launchd</div>
  <div class="cb"><div class="cb-bar"><span>/Library/LaunchDaemons/com.nexspence.server.plist</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div><div class="cb-body">&lt;?xml version="1.0" encoding="UTF-8"?&gt;
&lt;plist version="1.0"&gt;
&lt;dict&gt;
  &lt;key&gt;Label&lt;/key&gt;          &lt;string&gt;com.nexspence.server&lt;/string&gt;
  &lt;key&gt;ProgramArguments&lt;/key&gt;
  &lt;array&gt;
    &lt;string&gt;<span class="ck">/usr/local/bin/nexspence</span>&lt;/string&gt;
    &lt;string&gt;serve&lt;/string&gt;
    &lt;string&gt;--config&lt;/string&gt;
    &lt;string&gt;/usr/local/etc/nexspence/config.yaml&lt;/string&gt;
  &lt;/array&gt;
  &lt;key&gt;RunAtLoad&lt;/key&gt;      &lt;true/&gt;
  &lt;key&gt;KeepAlive&lt;/key&gt;      &lt;true/&gt;
  &lt;key&gt;StandardOutPath&lt;/key&gt;&lt;string&gt;/usr/local/var/log/nexspence.log&lt;/string&gt;
  &lt;key&gt;StandardErrorPath&lt;/key&gt;&lt;string&gt;/usr/local/var/log/nexspence.err&lt;/string&gt;
&lt;/dict&gt;
&lt;/plist&gt;</div></div>
  <div class="cb"><div class="cb-bar"><span>zsh</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div><div class="cb-body">sudo launchctl load -w /Library/LaunchDaemons/com.nexspence.server.plist
sudo launchctl list | grep nexspence
<span class="cc"># stop / unload:</span>
sudo launchctl unload -w /Library/LaunchDaemons/com.nexspence.server.plist</div></div>
</div>

<div class="os-pnl" data-os="windows">
  <div class="svc-sep"><span class="svc" data-i18n="ni.svc3">⚙ Run as a service</span> — Windows Service (NSSM)</div>
  <div class="cb"><div class="cb-bar"><span>powershell · NSSM</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div><div class="cb-body">nssm install Nexspence <span class="cs">"C:\nexspence\nexspence.exe"</span> <span class="cs">"serve --config C:\nexspence\config.yaml"</span>
nssm set Nexspence AppDirectory C:\nexspence
nssm set Nexspence AppStdout C:\nexspence\logs\out.log
nssm set Nexspence AppStderr C:\nexspence\logs\err.log
nssm set Nexspence Start SERVICE_AUTO_START
nssm start Nexspence</div></div>
  <div class="svc-sep" data-i18n="ni.scalt">Alternative — built-in sc.exe</div>
  <div class="cb"><div class="cb-bar"><span>cmd · sc.exe (Administrator)</span><button type="button" class="cb-copy" onclick="copyCode(this)">Copy</button></div><div class="cb-body">sc create Nexspence binPath= <span class="cs">"C:\nexspence\nexspence.exe serve --config C:\nexspence\config.yaml"</span> start= auto
sc start Nexspence
sc query Nexspence</div></div>
  <div class="alert-info" data-i18n="ni.win.note"><strong>Heads up:</strong> a bare console binary isn't a real Windows service — use NSSM (or <code>sc.exe</code>) so it survives logoff, auto-starts on boot, and restarts on crash.</div>
</div>
```

- [ ] **Step 2: Add the `installOS` switch function**

Add next to `installPlatform` (~1975):

```javascript
function installOS(os,btn){
  const root=btn.closest('.inst-pnl')||document.getElementById('doc-content');
  root.querySelectorAll('.os-chip').forEach(b=>b.classList.remove('active'));
  btn.classList.add('active');
  root.querySelectorAll('.os-pnl').forEach(p=>p.classList.toggle('active',p.dataset.os===os));
}
```

- [ ] **Step 3: Verify**

Run: `grep -c 'installOS(' website/docs/index.html`
Expected: `4` (definition + 3 onclick).
Run: `grep -c 'systemd/system/nexspence.service\|com.nexspence.server.plist\|nssm install Nexspence' website/docs/index.html`
Expected: `3`.
Manual: Getting Started → Native Install → switch Linux/macOS/Windows; each shows its service block; copy buttons work.

- [ ] **Step 4: Commit**

```bash
git add website/docs/index.html
git commit -m "feat(docs): Installation OS sub-tabs with systemd/launchd/Windows service setup"
```

---

## Task 6: Terraform Provider tab — five sub-section templates

**Files:**
- Modify: `website/docs/index.html` — remove `<template id="tpl-terraform">` (~820-904); add five new templates `tpl-tf-overview`, `tpl-tf-auth`, `tpl-tf-resources`, `tpl-tf-data`, `tpl-tf-examples`.

Markup source: `.superpowers/brainstorm/20027-1780972560/content/terraform-tab.html` (the five `.pane` blocks `p-overview`, `p-auth`, `p-resources`, `p-data`, `p-examples`). Port each `.pane`'s inner HTML into a `<template>`, keeping `.cb` / `.doc-table` / `.alert-info` / `.svc-sep`→use `.inst-sep` (already defined) classes. Each template must start with a `.doc-section-title` element (so the badge logic and headings work) and wrap prose in `data-i18n`.

- [ ] **Step 1: Delete the old combined template**

Remove the entire `<template id="tpl-terraform"> ... </template>` block (~820-904).

- [ ] **Step 2: Add the five new templates**

Insert after where `tpl-terraform` was. Each template's body is the corresponding `.pane` inner HTML from the mockup. Required heading elements (verbatim — the rest of each pane is ported from the mockup):

```html
<template id="tpl-tf-overview">
  <div class="doc-section-title" data-i18n="tf.ov.title">Terraform Provider <span class="vbadge-inline tf">Provider v0.2.0</span> <span class="vbadge-inline reg" data-i18n="tf.ov.reg">on Terraform Registry</span></div>
  <!-- port .pane#p-overview body: required_providers + provider block, first resource, terraform init, "Published" alert -->
</template>
<template id="tpl-tf-auth">
  <div class="doc-section-title" data-i18n="tf.auth.title">Authentication</div>
  <!-- port .pane#p-auth body: token block, username/password block, attribute/env table -->
</template>
<template id="tpl-tf-resources">
  <div class="doc-section-title" data-i18n="tf.res.title">Resources <span class="vbadge-inline tf">10</span></div>
  <!-- port .pane#p-resources body: 10-row resource table + RBAC example -->
</template>
<template id="tpl-tf-data">
  <div class="doc-section-title" data-i18n="tf.data.title">Data Sources <span class="vbadge-inline tf">2</span></div>
  <!-- port .pane#p-data body: 2-row table + output example -->
</template>
<template id="tpl-tf-examples">
  <div class="doc-section-title" data-i18n="tf.ex.title">Examples &amp; local development</div>
  <!-- port .pane#p-examples body: deploy/terraform-example pointer + ~/.terraformrc dev override -->
</template>
```

When porting, replace any mockup inline-styled `.cb-bar span` colors with the existing classes, and ensure code-block copy buttons use `onclick="copyCode(this)"`.

- [ ] **Step 3: Verify**

Run: `grep -c 'id="tpl-tf-overview"\|id="tpl-tf-auth"\|id="tpl-tf-resources"\|id="tpl-tf-data"\|id="tpl-tf-examples"' website/docs/index.html`
Expected: `5`.
Run: `grep -c 'id="tpl-terraform"' website/docs/index.html`
Expected: `0`.
Manual: Terraform Provider tab → sub-nav Overview/Authentication/Resources/Data Sources/Examples all render; Overview shows `Provider v0.2.0` + `on Terraform Registry`; Resources table has 10 rows; Data Sources has 2.

- [ ] **Step 4: Commit**

```bash
git add website/docs/index.html
git commit -m "feat(docs): split Terraform Provider into Overview/Auth/Resources/Data/Examples sub-sections"
```

---

## Task 7: i18n — EN + RU strings for all new keys

**Files:**
- Modify: `website/docs/index.html` — `TRANSLATIONS.en` (~1286+) and `TRANSLATIONS.ru` (the ru block further down).

- [ ] **Step 1: Add the new keys to BOTH `en` and `ru`**

Add these keys (EN values shown; write natural RU equivalents in the `ru` block). New keys introduced across Tasks 2-6:

```
ver.label                = Version              | RU: Версия
tabg.releases            = Changelog            | RU: История изменений
tabg.getting-started     = Getting Started      | RU: Начало работы
tabg.using               = Using                | RU: Использование
tabg.admin               = Administration       | RU: Администрирование
tabg.advanced            = Advanced             | RU: Продвинутое
tabg.terraform           = Terraform Provider   | RU: Terraform Provider
tabg.reference           = API                  | RU: API
sb.native-install        = Native Install       | RU: Нативная установка
sb.tf-overview           = Overview             | RU: Обзор
sb.tf-auth               = Authentication       | RU: Аутентификация
sb.tf-resources          = Resources            | RU: Ресурсы
sb.tf-data               = Data Sources         | RU: Источники данных
sb.tf-examples           = Examples             | RU: Примеры
crumb.tf-overview        = terraform · overview | RU: terraform · обзор
crumb.tf-auth            = terraform · auth     | RU: terraform · аутентификация
crumb.tf-resources       = terraform · resources| RU: terraform · ресурсы
crumb.tf-data            = terraform · data     | RU: terraform · источники
crumb.tf-examples        = terraform · examples | RU: terraform · примеры
ni.svc / ni.svc2 / ni.svc3 = ⚙ Run as a service | RU: ⚙ Запуск как службы
ni.scalt                 = Alternative — built-in sc.exe | RU: Альтернатива — встроенный sc.exe
ni.linux.note            = <strong>Tip:</strong> The .deb / .rpm packages can ship this unit pre-installed… | RU: <strong>Подсказка:</strong> пакеты .deb / .rpm могут поставлять unit-файл уже установленным…
ni.win.note              = <strong>Heads up:</strong> a bare console binary isn't a real Windows service… | RU: <strong>Внимание:</strong> голый консольный бинарник не является службой Windows…
tf.ov.title / tf.ov.reg / tf.auth.title / tf.res.title / tf.data.title / tf.ex.title  (+ any tf.* prose keys you add when porting the panes)
```

Plus add **RU** values for every `data-i18n` key you introduced when porting the Terraform panes and OS panels in Tasks 5-6 (e.g. table captions, alert bodies). Keep the existing `tf.*` RU keys that are still referenced.

- [ ] **Step 2: Automated EN/RU parity check**

Run this from the repo root:

```bash
node -e '
const fs=require("fs");const s=fs.readFileSync("website/docs/index.html","utf8");
const keys=new Set();
for(const m of s.matchAll(/data-i18n="([^"]+)"/g))keys.add(m[1]);
for(const m of s.matchAll(/\bt\(\x27([^\x27]+)\x27\)/g))keys.add(m[1]);
function block(name){const i=s.indexOf(name+":{");const j=s.indexOf("\n  },",i);return s.slice(i,j);}
const en=block("en"),ru=block("ru");
const miss=[...keys].filter(k=>!en.includes(`\x27${k}\x27:`)&&!en.includes(`${k}:`));
const missru=[...keys].filter(k=>!ru.includes(`\x27${k}\x27:`)&&!ru.includes(`${k}:`));
console.log("missing EN:",miss);console.log("missing RU:",missru);
process.exit(miss.length+missru.length?1:0);
'
```

Expected: `missing EN: []` and `missing RU: []` (exit 0). Fix any listed key, re-run until clean.

- [ ] **Step 3: Commit**

```bash
git add website/docs/index.html
git commit -m "feat(docs): EN/RU i18n for tabs, version dropdown, OS service docs, terraform sub-sections"
```

---

## Task 8: Final verification & cleanup

**Files:**
- Verify only: `website/docs/index.html`

- [ ] **Step 1: Structural sanity grep**

```bash
grep -c 'renderVersionBar\|renderSidebar(\|getElementById(.ver-bar.)\|id="tpl-terraform"' website/docs/index.html
```
Expected: `0`.

- [ ] **Step 2: Serve and walk the page against the mockups**

```bash
cd website && python3 -m http.server 8088
```
Open `http://localhost:8088/docs/` and confirm each item:
- Version dropdown opens, lists versions (num + tag + date) + GitHub link; selecting one updates the Changelog.
- Changelog is the default tab and renders full-width once releases load (static intro shows before load).
- Tabs are one aligned row: Changelog · Getting Started · Using · Administration · Advanced · Terraform Provider · API — no right-side gap.
- Each non-Changelog tab shows its contextual left sub-nav + first section.
- Badges only on post-1.0 sections (Monitoring v1.9.0, Promotion v1.8.1, Replication v1.3.0, Native Install v1.13.0, Format Setup Guides v1.7.0); Administration sections have none.
- Installation → Native → Linux/macOS/Windows service blocks all present and correct.
- Terraform Provider tab: 5 sub-sections, `Provider v0.2.0` + `on Terraform Registry`, 10 resources, 2 data sources.
- EN ⇄ RU toggle leaves no untranslated strings (re-run Task 7 parity check).
- Mobile (narrow the window < 768px): top tabs hide, the nav drawer lists all groups, the version dropdown still works.

- [ ] **Step 3: Final commit (if any fixes were made)**

```bash
git add website/docs/index.html
git commit -m "fix(docs): polish after redesign walkthrough"
```

---

## Self-Review Notes (author)

- **Spec coverage:** §4.2 IA → Tasks 3; §4.3 dropdown → Task 2; §4.4 OS service → Task 5; §4.5 Terraform tab → Task 6; §4.1 + §5 badges → Task 4; §6 i18n parity → Task 7; §7 verification → Task 8. CSS foundation → Task 1.
- **Ordering note:** Task 3 makes the `terraform` tab reachable before Task 6 creates its templates — content is empty in between but the page never breaks (renderStaticSection no-ops on a missing template).
- **Type consistency:** `state.activeTab` added in Task 3 Step 7 and read in Tasks 2/3. `FEATURE_BADGE` is referenced by `renderSubnav`/`renderMobileDrawer` (Task 3) but populated in Task 4 — resolved inline: Task 3 declares `let FEATURE_BADGE={};` after SECTIONS, Task 4 replaces it with the populated map. Function names match across tasks (`renderVersionDropdown`, `renderTabs`, `setTab`, `setSection`, `renderSubnav`, `renderMobileDrawer`, `installOS`, `firstSectionOfTab`, `tabOfSection`).
```
