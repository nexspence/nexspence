# Phase 58 — Interactive Security Access Map

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an "Access Map" admin tab to SecurityPage showing the full RBAC graph (Users → Roles → Privileges → Content Selectors) with chain highlighting and a sidebar.

**Architecture:** One new backend endpoint `GET /api/v1/security/access-graph` returns all 4 entity sets in a single response; graph edges are resolved on the frontend. The SVG graph is rendered with plain `<svg>` (no libraries). Chain highlighting is computed as a `Set<string>` of node IDs from the selected node traversed in both directions.

**Tech Stack:** Go / Gin (backend), React + TypeScript, Zustand, React Query, plain SVG (no graph lib).

---

### Task 1: Backend handler — `access_graph.go`

**Files:**
- Create: `internal/api/handlers/access_graph.go`
- Create: `internal/api/handlers/access_graph_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/api/handlers/access_graph_test.go
package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func buildAccessGraphRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	csRepo := testutil.NewContentSelectorRepo()
	cs := &domain.ContentSelector{Name: "cs-maven", Expression: `format == "maven2"`}
	_ = csRepo.Create(context.Background(), cs) // cs.ID assigned as "cs-1"

	csID := cs.ID
	userRepo := testutil.NewUserRepo(
		&domain.User{ID: "u1", Username: "alice", Email: "alice@corp.com",
			Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
			Roles: []string{"dev-read"}},
	)
	roleRepo := testutil.NewRoleRepo(
		&domain.Role{ID: "r1", Name: "dev-read", Description: "read access",
			Privileges: []string{"p1"}, Roles: []string{}},
	)
	privRepo := testutil.NewPrivilegeRepo(
		&domain.Privilege{ID: "p1", Name: "mvn-read",
			Type:              domain.PrivilegeTypeRepositoryContentSelector,
			ContentSelectorID: &csID},
	)

	h := handlers.NewAccessGraphHandler(userRepo, roleRepo, privRepo, csRepo)
	r := gin.New()
	r.GET("/access-graph", h.Get)
	return r
}

func TestAccessGraphHandler_Get_200(t *testing.T) {
	r := buildAccessGraphRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/access-graph", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}

	var resp handlers.AccessGraphResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Users) != 1 || resp.Users[0].Username != "alice" {
		t.Fatalf("want 1 user alice, got %+v", resp.Users)
	}
	if len(resp.Users[0].RoleIDs) != 1 || resp.Users[0].RoleIDs[0] != "r1" {
		t.Fatalf("want roleIds=[r1] got %v", resp.Users[0].RoleIDs)
	}
	if len(resp.Roles) != 1 || resp.Roles[0].ID != "r1" {
		t.Fatalf("want role r1 got %+v", resp.Roles)
	}
	if len(resp.Privileges) != 1 || resp.Privileges[0].ID != "p1" {
		t.Fatalf("want priv p1 got %+v", resp.Privileges)
	}
	if len(resp.Selectors) != 1 || resp.Selectors[0].Name != "cs-maven" {
		t.Fatalf("want selector cs-maven got %+v", resp.Selectors)
	}
}

func TestAccessGraphHandler_Get_EmptyGraph(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := handlers.NewAccessGraphHandler(
		testutil.NewUserRepo(),
		testutil.NewRoleRepo(),
		testutil.NewPrivilegeRepo(),
		testutil.NewContentSelectorRepo(),
	)
	r := gin.New()
	r.GET("/access-graph", h.Get)

	req := httptest.NewRequest(http.MethodGet, "/access-graph", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", w.Code)
	}
	var resp handlers.AccessGraphResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Users) != 0 || len(resp.Roles) != 0 || len(resp.Privileges) != 0 || len(resp.Selectors) != 0 {
		t.Fatalf("want empty arrays, got %+v", resp)
	}
}
```

- [ ] **Step 2: Run to confirm FAIL**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core
go test ./internal/api/handlers/... -run TestAccessGraph -v
```

Expected: `FAIL` — `handlers.AccessGraphResponse` undefined, `handlers.NewAccessGraphHandler` undefined.

- [ ] **Step 3: Write the handler**

```go
// internal/api/handlers/access_graph.go
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// AccessGraphHandler serves GET /api/v1/security/access-graph.
type AccessGraphHandler struct {
	users     repository.UserRepo
	roles     repository.RoleRepo
	privs     repository.PrivilegeRepo
	selectors repository.ContentSelectorRepo
}

func NewAccessGraphHandler(
	users repository.UserRepo,
	roles repository.RoleRepo,
	privs repository.PrivilegeRepo,
	selectors repository.ContentSelectorRepo,
) *AccessGraphHandler {
	return &AccessGraphHandler{users: users, roles: roles, privs: privs, selectors: selectors}
}

// Response types — exported so tests can decode directly.

type GraphUser struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Status   string   `json:"status"`
	Source   string   `json:"source"`
	RoleIDs  []string `json:"roleIds"`
}

type GraphRole struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	PrivilegeIDs []string `json:"privilegeIds"`
	RoleIDs      []string `json:"roleIds"`
}

type GraphPrivilege struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Type              string         `json:"type"`
	Attrs             map[string]any `json:"attrs,omitempty"`
	ContentSelectorID *string        `json:"contentSelectorId,omitempty"`
}

type GraphSelector struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Expression string `json:"expression"`
}

type AccessGraphResponse struct {
	Users      []GraphUser      `json:"users"`
	Roles      []GraphRole      `json:"roles"`
	Privileges []GraphPrivilege `json:"privileges"`
	Selectors  []GraphSelector  `json:"selectors"`
}

// Get handles GET /api/v1/security/access-graph (admin-only, wired in router).
func (h *AccessGraphHandler) Get(c *gin.Context) {
	ctx := c.Request.Context()

	users, err := h.users.List(ctx, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	roles, err := h.roles.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	privs, err := h.privs.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	selectors, err := h.selectors.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// user.Roles contains role *names*; build name→id map to resolve edges.
	roleByName := make(map[string]string, len(roles))
	for _, r := range roles {
		roleByName[r.Name] = r.ID
	}

	gUsers := make([]GraphUser, 0, len(users))
	for _, u := range users {
		roleIDs := make([]string, 0, len(u.Roles))
		for _, name := range u.Roles {
			if id, ok := roleByName[name]; ok {
				roleIDs = append(roleIDs, id)
			}
		}
		gUsers = append(gUsers, GraphUser{
			ID:       u.ID,
			Username: u.Username,
			Email:    u.Email,
			Status:   string(u.Status),
			Source:   string(u.Source),
			RoleIDs:  roleIDs,
		})
	}

	gRoles := make([]GraphRole, 0, len(roles))
	for _, r := range roles {
		privIDs := r.Privileges
		if privIDs == nil {
			privIDs = []string{}
		}
		nestedRoleIDs := r.Roles
		if nestedRoleIDs == nil {
			nestedRoleIDs = []string{}
		}
		gRoles = append(gRoles, GraphRole{
			ID:           r.ID,
			Name:         r.Name,
			Description:  r.Description,
			PrivilegeIDs: privIDs,
			RoleIDs:      nestedRoleIDs,
		})
	}

	gPrivs := make([]GraphPrivilege, 0, len(privs))
	for _, p := range privs {
		gPrivs = append(gPrivs, GraphPrivilege{
			ID:                p.ID,
			Name:              p.Name,
			Type:              string(p.Type),
			Attrs:             p.Attrs,
			ContentSelectorID: p.ContentSelectorID,
		})
	}

	gSels := make([]GraphSelector, 0, len(selectors))
	for _, s := range selectors {
		gSels = append(gSels, GraphSelector{
			ID:         s.ID,
			Name:       s.Name,
			Expression: s.Expression,
		})
	}

	c.JSON(http.StatusOK, AccessGraphResponse{
		Users:      gUsers,
		Roles:      gRoles,
		Privileges: gPrivs,
		Selectors:  gSels,
	})
}
```

- [ ] **Step 4: Run tests to confirm PASS**

```bash
go test ./internal/api/handlers/... -run TestAccessGraph -v
```

Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/handlers/access_graph.go internal/api/handlers/access_graph_test.go
git commit -m "feat(security): add GET /api/v1/security/access-graph handler"
```

---

### Task 2: Wire route in `router.go`

**Files:**
- Modify: `internal/api/router.go`

- [ ] **Step 1: Add handler instantiation**

In `router.go` after line 201 (`csH := handlers.NewContentSelectorHandler(selectorSvc)`), add:

```go
accessGraphH := handlers.NewAccessGraphHandler(userRepo, roleRepo, privilegeRepo, selectorRepo)
```

- [ ] **Step 2: Add route to admin group**

In `router.go` after the Content Selectors write block (around line 431), add:

```go
// ── Security access graph (admin) ────────────────────
admin.GET("/api/v1/security/access-graph", accessGraphH.Get)
```

- [ ] **Step 3: Verify build and all tests pass**

```bash
go build ./... && go test ./... 2>&1 | tail -5
```

Expected: `ok` for all packages, no compilation errors.

- [ ] **Step 4: Commit**

```bash
git add internal/api/router.go
git commit -m "feat(security): wire GET /api/v1/security/access-graph route"
```

---

### Task 3: Frontend — `AccessMapTab` in `SecurityPage.tsx`

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx`

The component uses `apiClient` (already imported), `useQuery` (already imported), `useState` and `useMemo` (already imported).

- [ ] **Step 1: Add types and constants before `RoleModal`**

Find the line `interface WebhookDef` in `SecurityPage.tsx` and add the following block **before** it:

```typescript
/* ─── Access Map types ───────────────────────────────── */
interface GraphUser     { id:string; username:string; email:string; status:string; source:string; roleIds:string[] }
interface GraphRole     { id:string; name:string; description:string; privilegeIds:string[]; roleIds:string[] }
interface GraphPrivilege{ id:string; name:string; type:string; attrs?:Record<string,unknown>; contentSelectorId?:string }
interface GraphSelector { id:string; name:string; expression:string }
interface AccessGraph   { users:GraphUser[]; roles:GraphRole[]; privileges:GraphPrivilege[]; selectors:GraphSelector[] }
type GNodeType = 'user' | 'role' | 'privilege' | 'selector'

const NODE_W = 120, NODE_H = 26, ROW_GAP = 40, ROW_Y0 = 36, SVG_W = 680
const COL_CX: Record<GNodeType, number> = { user: 70, role: 230, privilege: 390, selector: 560 }
const NODE_COLORS: Record<GNodeType, {stroke:string;fill:string;text:string}> = {
  user:      { stroke:'#7c5cff', fill:'#1e1049', text:'#c4b5fd' },
  role:      { stroke:'#3b82f6', fill:'#0c2340', text:'#93c5fd' },
  privilege: { stroke:'#f59e0b', fill:'#1c1200', text:'#fde68a' },
  selector:  { stroke:'#22c55e', fill:'#001c0c', text:'#86efac' },
}
```

- [ ] **Step 2: Add `AccessMapTab` component**

Add the following function immediately before the `export default function SecurityPage()` line (line ~1539):

```typescript
function AccessMapTab() {
  const [selType, setSelType] = useState<GNodeType|null>(null)
  const [selId,   setSelId]   = useState<string|null>(null)
  const [search,  setSearch]  = useState('')
  const [open,    setOpen]    = useState(false)

  const { data: graph, isLoading } = useQuery<AccessGraph>({
    queryKey: ['access-graph'],
    queryFn: () => apiClient.get<AccessGraph>('/api/v1/security/access-graph').then(r => r.data),
  })

  // Combobox options filtered by search.
  const options = useMemo(() => {
    if (!graph || !selType) return [] as {id:string;label:string;hint:string}[]
    const q = search.toLowerCase()
    const src =
      selType === 'user'      ? graph.users.map(u => ({ id:u.id, label:u.username, hint:u.email })) :
      selType === 'role'      ? graph.roles.map(r => ({ id:r.id, label:r.name, hint:`${r.privilegeIds.length} privileges` })) :
      selType === 'privilege' ? graph.privileges.map(p => ({ id:p.id, label:p.name, hint:p.type })) :
                                graph.selectors.map(s => ({ id:s.id, label:s.name, hint:'' }))
    return src.filter(o => o.label.toLowerCase().includes(q))
  }, [graph, selType, search])

  // Traverse graph in both directions from selected node; returns set of node IDs in chain.
  const chainIds = useMemo((): Set<string> => {
    if (!graph || !selId || !selType) return new Set()
    const set = new Set<string>([selId])

    function roleDown(rid: string) {
      const role = graph.roles.find(r => r.id === rid)
      if (!role) return
      role.privilegeIds.forEach(pid => {
        set.add(pid)
        const priv = graph.privileges.find(p => p.id === pid)
        if (priv?.contentSelectorId) set.add(priv.contentSelectorId)
      })
      role.roleIds.forEach(nid => { if (!set.has(nid)) { set.add(nid); roleDown(nid) } })
    }

    if (selType === 'user') {
      const user = graph.users.find(u => u.id === selId)
      user?.roleIds.forEach(rid => { set.add(rid); roleDown(rid) })
    } else if (selType === 'role') {
      roleDown(selId)
      graph.users.filter(u => u.roleIds.includes(selId)).forEach(u => set.add(u.id))
      graph.roles.filter(r => r.roleIds.includes(selId)).forEach(r => set.add(r.id))
    } else if (selType === 'privilege') {
      const priv = graph.privileges.find(p => p.id === selId)
      if (priv?.contentSelectorId) set.add(priv.contentSelectorId)
      graph.roles.filter(r => r.privilegeIds.includes(selId)).forEach(r => {
        set.add(r.id)
        roleDown(r.id)
        graph.users.filter(u => u.roleIds.includes(r.id)).forEach(u => set.add(u.id))
      })
    } else {
      graph.privileges.filter(p => p.contentSelectorId === selId).forEach(p => {
        set.add(p.id)
        graph.roles.filter(r => r.privilegeIds.includes(p.id)).forEach(r => {
          set.add(r.id)
          graph.users.filter(u => u.roleIds.includes(r.id)).forEach(u => set.add(u.id))
        })
      })
    }
    return set
  }, [graph, selId, selType])

  // Node center positions: id → {cx, cy}.
  const posMap = useMemo(() => {
    const m = new Map<string, {cx:number; cy:number}>()
    if (!graph) return m
    const place = (items:{id:string}[], cx:number) =>
      items.forEach((it, i) => m.set(it.id, { cx, cy: ROW_Y0 + i * ROW_GAP + NODE_H / 2 }))
    place(graph.users,      COL_CX.user)
    place(graph.roles,      COL_CX.role)
    place(graph.privileges, COL_CX.privilege)
    place(graph.selectors,  COL_CX.selector)
    return m
  }, [graph])

  const svgH = useMemo(() => {
    if (!graph) return 200
    const max = Math.max(graph.users.length, graph.roles.length, graph.privileges.length, graph.selectors.length, 1)
    return ROW_Y0 + max * ROW_GAP + 20
  }, [graph])

  function pick(type: GNodeType, id: string) {
    setSelType(type); setSelId(id); setSearch(''); setOpen(false)
  }
  function reset() { setSelType(null); setSelId(null); setSearch('') }

  // Pill labels/keys per type.
  const pills: {key:GNodeType; label:string}[] = [
    {key:'user', label:'User'}, {key:'role', label:'Role'},
    {key:'privilege', label:'Privilege'}, {key:'selector', label:'Content Selector'},
  ]

  // Render a single node <rect>+<text>.
  function GNode({ id, label, type }: {id:string; label:string; type:GNodeType}) {
    const pos = posMap.get(id)
    if (!pos) return null
    const { stroke, fill, text } = NODE_COLORS[type]
    const active = id === selId
    const inChain = chainIds.size === 0 || chainIds.has(id)
    const opacity = inChain ? 1 : 0.12
    const x = pos.cx - NODE_W / 2
    const y = pos.cy - NODE_H / 2
    return (
      <g style={{ cursor: 'pointer', opacity }} onClick={() => pick(type, id)}>
        <rect x={x} y={y} width={NODE_W} height={NODE_H} rx={6}
          fill={fill} stroke={stroke} strokeWidth={active ? 2 : 1} />
        <text x={pos.cx} y={pos.cy + 4} textAnchor="middle"
          fill={text} fontSize={10} fontWeight={active ? 700 : 400}
          style={{ pointerEvents:'none', userSelect:'none' }}>
          {label.length > 14 ? label.slice(0, 13) + '…' : label}
        </text>
      </g>
    )
  }

  // Render an edge line between two nodes.
  function Edge({ fromId, toId }: {fromId:string; toId:string}) {
    const a = posMap.get(fromId), b = posMap.get(toId)
    if (!a || !b) return null
    const inChain = chainIds.size === 0 || (chainIds.has(fromId) && chainIds.has(toId))
    // from right edge of source, to left edge of target
    const x1 = a.cx + NODE_W / 2, x2 = b.cx - NODE_W / 2
    return <line x1={x1} y1={a.cy} x2={x2} y2={b.cy}
      stroke="#334155" strokeWidth={inChain ? 1.5 : 0.4}
      markerEnd="url(#arr)" opacity={inChain ? 0.7 : 0.15} />
  }

  // Sidebar detail panel content.
  function SidebarDetail() {
    if (!selId || !selType || !graph) return null
    const s = { fontSize:11, color:'var(--holo-text-faint)' }
    const label = (t:string) => <div style={{fontSize:9, color:'#475569', textTransform:'uppercase', letterSpacing:1, marginBottom:4, marginTop:10}}>{t}</div>

    if (selType === 'user') {
      const u = graph.users.find(x => x.id === selId)
      if (!u) return null
      const roleNames = u.roleIds.map(rid => graph.roles.find(r => r.id === rid)?.name ?? rid)
      return <>
        <div style={{color:NODE_COLORS.user.text, fontSize:9, textTransform:'uppercase', letterSpacing:1, marginBottom:6}}>User</div>
        <div style={{color:'#e2e8f0', fontWeight:600, marginBottom:2}}>{u.username}</div>
        <div style={s}>{u.email}</div>
        {label('Roles')}
        {roleNames.length ? roleNames.map(n => <div key={n} style={{background:'#0c2340', borderRadius:4, padding:'2px 6px', color:'#60a5fa', fontSize:10, marginBottom:2}}>{n}</div>) : <div style={s}>none</div>}
        {label('Status')} <div style={{color: u.status==='active'?'#22c55e':'#ef4444', fontSize:10}}>● {u.status}</div>
        {label('Source')} <div style={s}>{u.source}</div>
      </>
    }
    if (selType === 'role') {
      const r = graph.roles.find(x => x.id === selId)
      if (!r) return null
      return <>
        <div style={{color:NODE_COLORS.role.text, fontSize:9, textTransform:'uppercase', letterSpacing:1, marginBottom:6}}>Role</div>
        <div style={{color:'#e2e8f0', fontWeight:600, marginBottom:2}}>{r.name}</div>
        {r.description && <div style={s}>{r.description}</div>}
        {label('Privileges')} <div style={s}>{r.privilegeIds.length} privilege{r.privilegeIds.length!==1?'s':''}</div>
        {r.roleIds.length > 0 && <>{label('Nested Roles')}<div style={s}>{r.roleIds.length} role{r.roleIds.length!==1?'s':''}</div></>}
      </>
    }
    if (selType === 'privilege') {
      const p = graph.privileges.find(x => x.id === selId)
      if (!p) return null
      const cs = p.contentSelectorId ? graph.selectors.find(s => s.id === p.contentSelectorId) : null
      return <>
        <div style={{color:NODE_COLORS.privilege.text, fontSize:9, textTransform:'uppercase', letterSpacing:1, marginBottom:6}}>Privilege</div>
        <div style={{color:'#e2e8f0', fontWeight:600, marginBottom:2}}>{p.name}</div>
        {label('Type')} <div style={s}>{p.type}</div>
        {cs && <>{label('Content Selector')}<div style={{color:'#4ade80', fontSize:10}}>{cs.name}</div></>}
      </>
    }
    // selector
    const sel = graph.selectors.find(x => x.id === selId)
    if (!sel) return null
    return <>
      <div style={{color:NODE_COLORS.selector.text, fontSize:9, textTransform:'uppercase', letterSpacing:1, marginBottom:6}}>Content Selector</div>
      <div style={{color:'#e2e8f0', fontWeight:600, marginBottom:2}}>{sel.name}</div>
      {label('Expression')}
      <div style={{fontFamily:'monospace', fontSize:10, color:'#94a3b8', background:'#0a1120', borderRadius:4, padding:'4px 6px', wordBreak:'break-all'}}>{sel.expression}</div>
    </>
  }

  const hasGraph = graph && (graph.users.length > 0 || graph.roles.length > 0 || graph.privileges.length > 0 || graph.selectors.length > 0)

  return (
    <div style={{display:'flex', flexDirection:'column', gap:12}}>
      {/* Type pills + combobox */}
      <div style={{display:'flex', gap:8, alignItems:'center', flexWrap:'wrap'}}>
        <span style={{fontSize:10, color:'#475569'}}>Type:</span>
        {pills.map(({key, label}) => (
          <div key={key} onClick={() => { setSelType(key); setSelId(null); setSearch(''); setOpen(false) }}
            style={{
              background: selType===key ? '#1e1049' : '#0a0f1a',
              border: `1px solid ${selType===key ? '#7c5cff' : '#1e3a5f'}`,
              borderRadius:5, padding:'4px 10px',
              color: selType===key ? '#a78bfa' : '#475569',
              fontSize:11, cursor:'pointer', fontWeight: selType===key ? 600 : 400,
            }}>{label}</div>
        ))}
        {selId && (
          <div onClick={reset} style={{background:'transparent', border:'1px solid #334155', borderRadius:5, padding:'4px 10px', color:'#64748b', fontSize:11, cursor:'pointer'}}>× Reset</div>
        )}
      </div>

      {selType && (
        <div style={{position:'relative', maxWidth:360}}>
          <input
            value={search}
            onChange={e => { setSearch(e.target.value); setOpen(true) }}
            onFocus={() => setOpen(true)}
            placeholder={`Search ${selType}s…`}
            style={{width:'100%', background:'#0d1829', border:`1px solid ${selType?NODE_COLORS[selType].stroke:'#1e3a5f'}`, borderRadius: open && options.length ? '6px 6px 0 0' : 6, color:'#e2e8f0', fontSize:12, padding:'7px 10px', outline:'none', boxSizing:'border-box'}}
          />
          {open && options.length > 0 && (
            <div style={{position:'absolute', zIndex:10, width:'100%', background:'#0d1829', border:`1px solid ${NODE_COLORS[selType].stroke}`, borderTop:'none', borderRadius:'0 0 6px 6px', maxHeight:200, overflowY:'auto'}}>
              {options.slice(0,20).map(o => (
                <div key={o.id} onClick={() => pick(selType!, o.id)}
                  style={{padding:'7px 12px', display:'flex', alignItems:'center', gap:8, cursor:'pointer', borderTop:'1px solid #0d1829'}}
                  onMouseEnter={e => (e.currentTarget.style.background='#1e1049')}
                  onMouseLeave={e => (e.currentTarget.style.background='transparent')}>
                  <span style={{color:'#e2e8f0', fontSize:11}}>{o.label}</span>
                  {o.hint && <span style={{color:'#475569', fontSize:10, marginLeft:'auto'}}>{o.hint}</span>}
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Graph area */}
      {isLoading && <div style={{color:'#475569', fontSize:12}}>Loading graph…</div>}

      {!isLoading && !hasGraph && (
        <div style={{height:200, border:'1px dashed #1e3a5f', borderRadius:8, display:'flex', flexDirection:'column', alignItems:'center', justifyContent:'center', gap:8}}>
          <div style={{color:'#1e3a5f', fontSize:24}}>⬡</div>
          <div style={{color:'#334155', fontSize:12}}>Select a node to explore the access graph</div>
          <div style={{color:'#1e3a5f', fontSize:10}}>User → Roles → Privileges → Content Selectors</div>
        </div>
      )}

      {!isLoading && hasGraph && graph && (
        <div style={{display:'flex', gap:12, alignItems:'flex-start'}}>
          {/* SVG graph */}
          <div style={{flex:1, overflowX:'auto', background:'#070b14', borderRadius:8, border:'1px solid #1e3a5f'}}>
            <svg width={SVG_W} height={svgH} viewBox={`0 0 ${SVG_W} ${svgH}`} style={{fontFamily:'system-ui', display:'block'}}>
              <defs>
                <marker id="arr" markerWidth={6} markerHeight={6} refX={3} refY={3} orient="auto">
                  <path d="M0,0 L0,6 L6,3 z" fill="#334155"/>
                </marker>
              </defs>
              {/* Column labels */}
              {(['user','role','privilege','selector'] as GNodeType[]).map(type => (
                <text key={type} x={COL_CX[type]} y={16} textAnchor="middle"
                  fill={NODE_COLORS[type].stroke} fontSize={8} fontWeight={600} letterSpacing={1}
                  style={{textTransform:'uppercase'}}>
                  {type === 'selector' ? 'SELECTORS' : type.toUpperCase() + 'S'}
                </text>
              ))}
              {/* Edges */}
              {graph.users.flatMap(u => u.roleIds.map(rid => <Edge key={`u${u.id}-r${rid}`} fromId={u.id} toId={rid}/>))}
              {graph.roles.flatMap(r => [
                ...r.privilegeIds.map(pid => <Edge key={`r${r.id}-p${pid}`} fromId={r.id} toId={pid}/>),
                ...r.roleIds.map(nid => <Edge key={`r${r.id}-nr${nid}`} fromId={r.id} toId={nid}/>),
              ])}
              {graph.privileges.filter(p => p.contentSelectorId).map(p =>
                <Edge key={`p${p.id}-cs`} fromId={p.id} toId={p.contentSelectorId!}/>
              )}
              {/* Nodes */}
              {graph.users.map(u      => <GNode key={u.id}   id={u.id}   label={u.username} type="user"/>)}
              {graph.roles.map(r      => <GNode key={r.id}   id={r.id}   label={r.name}     type="role"/>)}
              {graph.privileges.map(p => <GNode key={p.id}   id={p.id}   label={p.name}     type="privilege"/>)}
              {graph.selectors.map(s  => <GNode key={s.id}   id={s.id}   label={s.name}     type="selector"/>)}
            </svg>
          </div>
          {/* Sidebar */}
          {selId && (
            <div style={{width:200, background:'#0d1829', border:'1px solid #1e3a5f', borderRadius:8, padding:14, flexShrink:0, fontSize:11}}>
              <SidebarDetail/>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 3: Wire the tab into `SecurityPage`**

In `SecurityPage.tsx`, find:

```typescript
type Tab = 'roles' | 'privileges' | 'selectors' | 'users' | 'scan' | 'vulndash' | 'webhooks'
```

Replace with:

```typescript
type Tab = 'roles' | 'privileges' | 'selectors' | 'users' | 'scan' | 'vulndash' | 'webhooks' | 'accessmap'
```

Find the `allTabs` array and add `accessmap` after `webhooks` inside the admin-only spread:

```typescript
const allTabs: HoloTabItem[] = [
  { value: 'roles',      label: 'Roles' },
  { value: 'privileges', label: 'Privileges' },
  { value: 'selectors',  label: 'Content Selectors' },
  ...(admin ? [
    { value: 'users',     label: 'Users' },
    { value: 'scan',      label: 'CVE Scan' },
    { value: 'vulndash',  label: 'Vulnerability Dashboard' },
    { value: 'webhooks',  label: 'Webhooks' },
    { value: 'accessmap', label: 'Access Map' },
  ] : []),
]
```

Find the tab render block and add:

```typescript
{tab === 'accessmap'  && admin && <AccessMapTab />}
```

After the line `{tab === 'webhooks' && admin && <WebhooksTab />}`.

- [ ] **Step 4: TypeScript check**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core/frontend
npm run build 2>&1 | tail -20
```

Expected: build succeeds, no TypeScript errors. If there are unused variable warnings in `AccessMapTab` (e.g. from functions defined inside render), move them outside or suppress with a comment — but the build must pass.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx
git commit -m "feat(security): add Access Map tab — SVG RBAC graph with chain highlighting"
```

---

### Task 4: Update task_plan.md

**Files:**
- Modify: `task_plan.md`

- [ ] **Step 1: Mark Phase 58 complete**

In `task_plan.md`, find:

```
## Phase 58: Interactive Security Access Map
**Status:** backlog
```

Replace with:

```
## Phase 58: Interactive Security Access Map
**Status:** complete (2026-05-07)
```

Mark all task checkboxes in Phase 58 as done (`[x]`).

- [ ] **Step 2: Commit**

```bash
git add task_plan.md
git commit -m "chore: mark Phase 58 complete"
```
