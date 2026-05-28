# Phase 14: Webhooks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix three bugs that prevent webhook delivery, add a synchronous test endpoint, extend the UI with a test button / edit form / templates, and ship a Python receiver script + docs.

**Architecture:** WebhookService already has `Dispatch()` and `deliver()` — bugs are a missing wire in router.go and a wrong header value. Test() is a synchronous variant of deliver() returning status+latency. UI changes are self-contained in `SecurityPage.tsx`. Scripts are standalone files.

**Tech Stack:** Go (Gin), React+TypeScript, Python 3 stdlib

---

## File Map

| File | Action | Change |
|------|--------|--------|
| `internal/service/webhook_service.go` | Modify | Fix `deliver()` header bug; add `Test()` + `TestResult` |
| `internal/service/repository_service.go` | Modify | Add `webhooks` field, `WithWebhooks()`, dispatch `repo.created` in `Create()` |
| `internal/api/handlers/webhooks.go` | Modify | Add `Test` handler method |
| `internal/api/router.go` | Modify | Wire `Webhooks` in `formatDeps`, add GET/:id + POST/:id/test routes, call `repoSvc.WithWebhooks` |
| `frontend/src/pages/SecurityPage.tsx` | Modify | Test button, inline edit form, quick-start templates |
| `scripts/webhook-receiver.py` | Create | Python stdlib receiver with HMAC verification |
| `docs/webhooks.md` | Create | Curl cheatsheet + payload reference |

---

## Task 1: Fix deliver() header bug + add Test() to WebhookService

**Files:**
- Modify: `internal/service/webhook_service.go`
- Test: `internal/service/webhook_service_test.go`

### Background
`deliver(wh, body)` sets `X-Nexspence-Event: wh.Events[0]` (first *subscribed* event) instead of the actual event being dispatched. Fix: pass event as explicit arg. `Test()` is a synchronous variant: delivers one request, returns status code + latency.

- [ ] **Step 1: Add the failing test for correct event header**

Append to `internal/service/webhook_service_test.go`:

```go
func TestWebhookService_Deliver_CorrectEventHeader(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Nexspence-Event")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc, _ := newWebhookSvc()
	ctx := context.Background()
	_ = svc.Create(ctx, &domain.Webhook{
		Name:   "hook",
		URL:    srv.URL,
		Events: []domain.WebhookEvent{domain.EventRepoCreated}, // subscribed to repo.created
	})

	// Dispatch artifact.published — header must reflect the dispatched event, not Events[0]
	svc.Dispatch(domain.WebhookPayload{Event: domain.EventArtifactPublished})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gotHeader != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Hook is not subscribed to artifact.published so it won't fire — that's correct.
	// Re-test with the matching event:
	gotHeader = ""
	_ = svc.Create(ctx, &domain.Webhook{
		Name:   "hook2",
		URL:    srv.URL,
		Events: []domain.WebhookEvent{domain.EventArtifactPublished},
	})
	svc.Dispatch(domain.WebhookPayload{Event: domain.EventArtifactPublished})
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gotHeader != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if gotHeader != "artifact.published" {
		t.Errorf("X-Nexspence-Event = %q, want %q", gotHeader, "artifact.published")
	}
}
```

- [ ] **Step 2: Add failing test for Test()**

Append to `internal/service/webhook_service_test.go`:

```go
func TestWebhookService_Test_ReturnsStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	svc, _ := newWebhookSvc()
	ctx := context.Background()
	wh := &domain.Webhook{
		Name:   "h",
		URL:    srv.URL,
		Events: []domain.WebhookEvent{domain.EventArtifactPublished},
	}
	_ = svc.Create(ctx, wh)

	res, err := svc.Test(ctx, wh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != http.StatusNoContent {
		t.Errorf("status = %d, want 204", res.Status)
	}
	if res.LatencyMs < 0 {
		t.Errorf("latency must be >= 0")
	}
}

func TestWebhookService_Test_NotFound(t *testing.T) {
	svc, _ := newWebhookSvc()
	_, err := svc.Test(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for unknown webhook id")
	}
}
```

- [ ] **Step 3: Run tests — expect FAIL**

```bash
cd /home/skensel/AI/self_nexus && go test -race -count=1 -run 'TestWebhookService_Deliver_CorrectEventHeader|TestWebhookService_Test' ./internal/service/...
```

Expected: compilation error or FAIL (Test/TestResult undefined).

- [ ] **Step 4: Implement fixes in webhook_service.go**

Replace the file content from line 74 onwards (keep the top of file unchanged — package, imports, struct, CRUD methods). The full replacement for `Dispatch`, `deliver`, and new `Test`/`TestResult`:

```go
// TestResult holds the outcome of a synchronous test delivery.
type TestResult struct {
	Status    int   `json:"status"`
	LatencyMs int64 `json:"latency_ms"`
}

// Test sends a ping payload to the webhook identified by id and returns the
// HTTP status + round-trip latency. Returns an error if the webhook is not
// found or the HTTP request cannot be made.
func (s *WebhookService) Test(ctx context.Context, id string) (*TestResult, error) {
	wh, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if wh == nil {
		return nil, fmt.Errorf("webhook %q not found", id)
	}
	payload := domain.WebhookPayload{
		Event:      "webhook.test",
		Timestamp:  time.Now().UTC(),
		Repository: "test",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	status, err := s.deliverWithStatus(*wh, body, "webhook.test")
	if err != nil {
		return nil, err
	}
	return &TestResult{Status: status, LatencyMs: time.Since(start).Milliseconds()}, nil
}

// Dispatch fires the payload to all active webhooks subscribed to payload.Event.
// Delivery is asynchronous — errors are silently dropped.
func (s *WebhookService) Dispatch(payload domain.WebhookPayload) {
	go func() {
		hooks, err := s.repo.ListByEvent(context.Background(), payload.Event)
		if err != nil || len(hooks) == 0 {
			return
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return
		}
		for _, wh := range hooks {
			if !wh.Active {
				continue
			}
			s.deliver(wh, body, payload.Event)
		}
	}()
}

func (s *WebhookService) deliver(wh domain.Webhook, body []byte, event domain.WebhookEvent) {
	_, _ = s.deliverWithStatus(wh, body, string(event))
}

func (s *WebhookService) deliverWithStatus(wh domain.Webhook, body []byte, event string) (int, error) {
	req, err := http.NewRequest(http.MethodPost, wh.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nexspence-Event", event)
	if wh.Secret != "" {
		mac := hmac.New(sha256.New, []byte(wh.Secret))
		mac.Write(body)
		req.Header.Set("X-Nexspence-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}
```

- [ ] **Step 5: Run tests — expect PASS**

```bash
cd /home/skensel/AI/self_nexus && go test -race -count=1 -run 'TestWebhookService' ./internal/service/...
```

Expected: all `TestWebhookService_*` pass.

- [ ] **Step 6: Confirm build still clean**

```bash
cd /home/skensel/AI/self_nexus && go build ./...
```

Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add internal/service/webhook_service.go internal/service/webhook_service_test.go
git commit -m "fix(webhook): correct event header in deliver; add Test() method"
```

---

## Task 2: repo.created dispatch in RepositoryService

**Files:**
- Modify: `internal/service/repository_service.go`
- Test: `internal/service/repository_service_webhook_test.go` (new)

- [ ] **Step 1: Write failing test**

Create `internal/service/repository_service_webhook_test.go`:

```go
package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

type capturingDispatcher struct {
	events []domain.WebhookPayload
}

func (c *capturingDispatcher) Dispatch(p domain.WebhookPayload) {
	c.events = append(c.events, p)
}

func newRepoSvc() *service.RepositoryService {
	return service.NewRepositoryService(
		testutil.NewRepoRepo(),
		testutil.NewBlobStoreRepo(),
		testutil.NewBlobStore(),
		testutil.NewCleanupPolicyRepo(),
	)
}

func TestRepositoryService_Create_DispatchesRepoCreated(t *testing.T) {
	svc := newRepoSvc()
	d := &capturingDispatcher{}
	svc.WithWebhooks(d)

	repo := &domain.Repository{
		Name:   "my-repo",
		Format: domain.FormatRaw,
		Type:   domain.TypeHosted,
	}
	if err := svc.Create(context.Background(), repo); err != nil {
		t.Fatal(err)
	}

	// Dispatch is synchronous in this context (capturingDispatcher.Dispatch is sync)
	// but WebhookService.Dispatch is async — here we use a capturing dispatcher which is sync.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(d.events) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if len(d.events) == 0 {
		t.Fatal("expected repo.created event to be dispatched")
	}
	got := d.events[0]
	if got.Event != domain.EventRepoCreated {
		t.Errorf("event = %q, want %q", got.Event, domain.EventRepoCreated)
	}
	if got.Repository != "my-repo" {
		t.Errorf("repository = %q, want %q", got.Repository, "my-repo")
	}
}

func TestRepositoryService_Create_NoDispatch_WhenWebhooksNil(t *testing.T) {
	svc := newRepoSvc() // no WithWebhooks call
	repo := &domain.Repository{
		Name:   "safe-repo",
		Format: domain.FormatRaw,
		Type:   domain.TypeHosted,
	}
	// Must not panic
	if err := svc.Create(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Check testutil has the required constructors**

```bash
grep -n "func NewRepoRepo\|func NewBlobStoreRepo\|func NewBlobStore\|func NewCleanupPolicyRepo" /home/skensel/AI/self_nexus/internal/testutil/mocks.go
```

Expected: all four appear. If any is missing, check its actual name and update the test accordingly.

- [ ] **Step 3: Run test — expect FAIL (WithWebhooks undefined)**

```bash
cd /home/skensel/AI/self_nexus && go test -race -count=1 -run 'TestRepositoryService_Create' ./internal/service/...
```

Expected: compile error — `svc.WithWebhooks undefined`.

- [ ] **Step 4: Add webhooks field and WithWebhooks() to RepositoryService**

In `internal/service/repository_service.go`, replace the struct and constructor:

```go
type RepositoryService struct {
	repos     repository.RepositoryRepo
	blobs     repository.BlobStoreRepo
	blobStore storage.BlobStore
	policies  repository.CleanupPolicyRepo
	webhooks  domain.WebhookDispatcher
}

func NewRepositoryService(
	repos repository.RepositoryRepo,
	blobs repository.BlobStoreRepo,
	blobStore storage.BlobStore,
	policies repository.CleanupPolicyRepo,
) *RepositoryService {
	return &RepositoryService{repos: repos, blobs: blobs, blobStore: blobStore, policies: policies}
}

func (s *RepositoryService) WithWebhooks(d domain.WebhookDispatcher) *RepositoryService {
	s.webhooks = d
	return s
}
```

- [ ] **Step 5: Dispatch repo.created in Create()**

In `repository_service.go`, find the line `r.Online = true` followed by `return s.repos.Create(ctx, r)`. Replace with:

```go
	r.Online = true
	if err := s.repos.Create(ctx, r); err != nil {
		return err
	}
	if s.webhooks != nil {
		s.webhooks.Dispatch(domain.WebhookPayload{
			Event:      domain.EventRepoCreated,
			Timestamp:  time.Now().UTC(),
			Repository: r.Name,
		})
	}
	return nil
```

Also add `"time"` to the import block if not already present.

- [ ] **Step 6: Run tests — expect PASS**

```bash
cd /home/skensel/AI/self_nexus && go test -race -count=1 -run 'TestRepositoryService_Create' ./internal/service/...
```

Expected: both tests pass.

- [ ] **Step 7: Full build check**

```bash
cd /home/skensel/AI/self_nexus && go build ./...
```

Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add internal/service/repository_service.go internal/service/repository_service_webhook_test.go
git commit -m "feat(webhook): dispatch repo.created from RepositoryService"
```

---

## Task 3: Test handler in WebhookHandler

**Files:**
- Modify: `internal/api/handlers/webhooks.go`

- [ ] **Step 1: Add Test method to WebhookHandler**

Append to `internal/api/handlers/webhooks.go`:

```go
// Test handles POST /api/v1/webhooks/:id/test
// Sends a synchronous test ping and returns the remote HTTP status + latency.
func (h *WebhookHandler) Test(c *gin.Context) {
	res, err := h.svc.Test(c.Request.Context(), c.Param("id"))
	if err != nil {
		if err.Error() == "webhook \""+c.Param("id")+"\" not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "webhook not found"})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}
```

- [ ] **Step 2: Build check**

```bash
cd /home/skensel/AI/self_nexus && go build ./internal/api/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/api/handlers/webhooks.go
git commit -m "feat(webhook): add Test handler for POST /api/v1/webhooks/:id/test"
```

---

## Task 4: Wire everything in router.go

**Files:**
- Modify: `internal/api/router.go`

Three changes in one commit:
1. Set `Webhooks: webhookSvc` in `formatDeps`
2. Call `repoSvc.WithWebhooks(webhookSvc)` after both services exist
3. Add `GET /api/v1/webhooks/:id` and `POST /api/v1/webhooks/:id/test` routes

- [ ] **Step 1: Wire Webhooks in formatDeps**

In `router.go`, find the `formatDeps` struct literal and add the `Webhooks` field:

```go
	formatDeps := formats.Deps{
		Repos:      repoRepo,
		Components: componentRepo,
		Assets:     assetRepo,
		Blobs:      blobRepo,
		BlobStore:  localBlob,
		BaseURL:    cfg.HTTP.BaseURL,
		Webhooks:   webhookSvc,
	}
```

- [ ] **Step 2: Wire repoSvc with webhooks**

After the line `webhookSvc := service.NewWebhookService(webhookRepo)` (currently line 79), add:

```go
	repoSvc.WithWebhooks(webhookSvc)
```

Note: `repoSvc` is initialized on line 73 before `webhookSvc` on line 79, so this call goes after line 79.

- [ ] **Step 3: Add missing routes**

In the admin routes block for webhooks (around line 297-301), add the two new routes:

```go
		// ── Webhooks (admin) ──────────────────────────────────
		admin.GET("/api/v1/webhooks", webhookH.List)
		admin.GET("/api/v1/webhooks/:id", webhookH.Get)
		admin.POST("/api/v1/webhooks", webhookH.Create)
		admin.PUT("/api/v1/webhooks/:id", webhookH.Update)
		admin.DELETE("/api/v1/webhooks/:id", webhookH.Delete)
		admin.POST("/api/v1/webhooks/:id/test", webhookH.Test)
```

- [ ] **Step 4: Build check**

```bash
cd /home/skensel/AI/self_nexus && go build ./...
```

Expected: no errors.

- [ ] **Step 5: Run full test suite**

```bash
cd /home/skensel/AI/self_nexus && go test -race -count=1 ./...
```

Expected: all pass, no races.

- [ ] **Step 6: Commit**

```bash
git add internal/api/router.go
git commit -m "fix(webhook): wire dispatcher in formatDeps, add GET/:id and test routes"
```

---

## Task 5: Frontend — Test button

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx`

The `WebhooksTab` function currently renders a row per hook with a delete button. Add a test button that calls `POST /api/v1/webhooks/:id/test` and shows inline result for 5 seconds.

- [ ] **Step 1: Add testResults state to WebhooksTab**

Inside `function WebhooksTab()`, after the existing `const [saving, setSaving] = useState(false)` line, add:

```typescript
  const [testResults, setTestResults] = useState<Record<string, { ok: boolean; msg: string } | null>>({})

  async function testHook(id: string) {
    setTestResults(r => ({ ...r, [id]: null })) // null = loading
    try {
      const res = await apiClient.post<{ status: number; latency_ms: number }>(`/api/v1/webhooks/${id}/test`)
      const ok = res.data.status >= 200 && res.data.status < 300
      setTestResults(r => ({ ...r, [id]: { ok, msg: `${ok ? '✓' : '✗'} ${res.data.status} (${res.data.latency_ms}ms)` } }))
    } catch (e: any) {
      const msg = e?.response?.data?.error ?? e?.message ?? 'error'
      setTestResults(r => ({ ...r, [id]: { ok: false, msg: `✗ ${msg}` } }))
    }
    setTimeout(() => setTestResults(r => ({ ...r, [id]: undefined as any })), 5000)
  }
```

- [ ] **Step 2: Add Zap import for the test button icon**

At the top of the file find the lucide-react import line and add `Zap` and `Pencil` to it:

```typescript
import { Shield, RefreshCw, Webhook, AlertTriangle, CheckCircle, Loader, Trash2, Plus, Zap, Pencil, ... }
```

(Keep all existing imports, add `Zap` and `Pencil` to the list.)

- [ ] **Step 3: Add test button and result inline in each hook row**

Find the hook row rendering inside `WebhooksTab`. It currently ends with:

```tsx
              <span style={S.badge(h.active ? '#22c55e' : '#6b7280')}>{h.active ? 'active' : 'inactive'}</span>
              <button style={S.btn('danger')} onClick={() => del.mutate(h.id)}><Trash2 size={13} /></button>
```

Replace with:

```tsx
              <span style={S.badge(h.active ? '#22c55e' : '#6b7280')}>{h.active ? 'active' : 'inactive'}</span>
              {testResults[h.id] !== undefined && (
                <span style={{ fontSize: 12, fontFamily: 'monospace', color: testResults[h.id]?.ok ? '#22c55e' : '#ef4444' }}>
                  {testResults[h.id] === null ? '…' : testResults[h.id]?.msg}
                </span>
              )}
              <button style={S.btn('ghost')} onClick={() => testHook(h.id)} title="Send test event"><Zap size={13} /></button>
              <button style={S.btn('danger')} onClick={() => del.mutate(h.id)}><Trash2 size={13} /></button>
```

- [ ] **Step 4: TypeScript check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit
```

Expected: 0 errors.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx
git commit -m "feat(ui): add Test button to webhook rows"
```

---

## Task 6: Frontend — Edit webhook

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx`

- [ ] **Step 1: Add editingId state and save function**

Inside `function WebhooksTab()`, after `const [saving, setSaving] = useState(false)`, add:

```typescript
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editForm, setEditForm] = useState({ name: '', url: '', events: WEBHOOK_EVENTS, secret: '', active: true })

  function startEdit(h: WebhookDef) {
    setEditingId(h.id)
    setEditForm({ name: h.name, url: h.url, events: h.events, secret: h.secret ?? '', active: h.active })
  }

  async function saveEdit() {
    if (!editingId) return
    setSaving(true)
    try {
      await apiClient.put(`/api/v1/webhooks/${editingId}`, editForm)
      qc.invalidateQueries({ queryKey: ['webhooks'] })
      setEditingId(null)
    } finally { setSaving(false) }
  }

  const toggleEditEvent = (ev: string) => setEditForm(f => ({
    ...f, events: f.events.includes(ev) ? f.events.filter(e => e !== ev) : [...f.events, ev]
  }))
```

- [ ] **Step 2: Render edit form inline in hook row**

In the hook row rendering, wrap the row content in a conditional. Find the `hooks.map(h => (` block and replace the inner content so that when `editingId === h.id` an edit form is shown instead of the read-only row:

```tsx
          hooks.map(h => (
            <div key={h.id} style={S.card}>
              {editingId === h.id ? (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  <div style={{ fontSize: 13, fontWeight: 600, color: '#dbeafe' }}>Edit Webhook</div>
                  <input style={S.input} placeholder="Name" value={editForm.name} onChange={e => setEditForm(f => ({ ...f, name: e.target.value }))} />
                  <input style={S.input} placeholder="URL" value={editForm.url} onChange={e => setEditForm(f => ({ ...f, url: e.target.value }))} />
                  <input style={S.input} placeholder="HMAC secret (leave blank to keep)" value={editForm.secret} onChange={e => setEditForm(f => ({ ...f, secret: e.target.value }))} />
                  <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: '#dbeafe', cursor: 'pointer' }}>
                    <input type="checkbox" checked={editForm.active} onChange={e => setEditForm(f => ({ ...f, active: e.target.checked }))} style={{ accentColor: '#3b82f6' }} />
                    Active
                  </label>
                  <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' as const }}>
                    {WEBHOOK_EVENTS.map(ev => (
                      <label key={ev} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: editForm.events.includes(ev) ? '#dbeafe' : 'rgba(229,231,235,0.4)', cursor: 'pointer' }}>
                        <input type="checkbox" checked={editForm.events.includes(ev)} onChange={() => toggleEditEvent(ev)} style={{ accentColor: '#3b82f6' }} />
                        <span style={{ fontFamily: 'monospace', fontSize: 12 }}>{ev}</span>
                      </label>
                    ))}
                  </div>
                  <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                    <button style={S.btn('ghost')} onClick={() => setEditingId(null)}>Cancel</button>
                    <button style={S.btn('primary')} onClick={saveEdit} disabled={saving || !editForm.name || !editForm.url}>{saving ? 'Saving…' : 'Save'}</button>
                  </div>
                </div>
              ) : (
                <div style={S.row}>
                  <Webhook size={14} style={{ color: h.active ? '#22c55e' : '#6b7280', flexShrink: 0 }} />
                  <div style={{ flex: 1 }}>
                    <div style={{ color: '#dbeafe', fontWeight: 600 }}>{h.name}</div>
                    <div style={{ color: 'rgba(229,231,235,0.4)', ...S.mono }}>{h.url}</div>
                    <div style={{ display: 'flex', gap: 4, marginTop: 4, flexWrap: 'wrap' as const }}>
                      {h.events.map(ev => <span key={ev} style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: 'rgba(59,130,246,0.12)', color: '#93c5fd', fontFamily: 'monospace' }}>{ev}</span>)}
                    </div>
                  </div>
                  <span style={S.badge(h.active ? '#22c55e' : '#6b7280')}>{h.active ? 'active' : 'inactive'}</span>
                  {testResults[h.id] !== undefined && (
                    <span style={{ fontSize: 12, fontFamily: 'monospace', color: testResults[h.id]?.ok ? '#22c55e' : '#ef4444' }}>
                      {testResults[h.id] === null ? '…' : testResults[h.id]?.msg}
                    </span>
                  )}
                  <button style={S.btn('ghost')} onClick={() => testHook(h.id)} title="Send test event"><Zap size={13} /></button>
                  <button style={S.btn('ghost')} onClick={() => startEdit(h)} title="Edit"><Pencil size={13} /></button>
                  <button style={S.btn('danger')} onClick={() => del.mutate(h.id)}><Trash2 size={13} /></button>
                </div>
              )}
            </div>
          ))
```

Note: the outer wrapper changes from `style={S.row}` to `style={S.card}` to give the edit form proper padding. Verify `S.card` is defined in the style object in this file.

- [ ] **Step 3: TypeScript check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit
```

Expected: 0 errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx
git commit -m "feat(ui): inline edit form for webhooks"
```

---

## Task 7: Frontend — Quick-start templates

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx`

- [ ] **Step 1: Add TEMPLATES constant above WebhooksTab**

Add before the `function WebhooksTab()` declaration:

```typescript
const WEBHOOK_TEMPLATES = [
  { label: 'Slack',         events: ['artifact.published'],                                        urlHint: 'https://hooks.slack.com/services/…' },
  { label: 'CI/CD Trigger', events: ['artifact.published', 'artifact.deleted'],                    urlHint: '' },
  { label: 'Audit Logger',  events: ['artifact.published', 'artifact.deleted', 'repo.created', 'proxy.error'], urlHint: '' },
  { label: 'Proxy Monitor', events: ['proxy.error'],                                               urlHint: '' },
] as const
```

- [ ] **Step 2: Render template buttons inside the "New Webhook" form**

Inside `showForm && (...)` block, before the `<input placeholder="Name" ...>` field, add:

```tsx
            <div style={{ marginBottom: 4 }}>
              <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', marginBottom: 6 }}>Quick start:</div>
              <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' as const }}>
                {WEBHOOK_TEMPLATES.map(t => (
                  <button key={t.label} style={{ ...S.btn('ghost'), fontSize: 11, padding: '3px 10px' }}
                    onClick={() => setForm(f => ({
                      ...f,
                      events: [...t.events],
                      url: t.urlHint || f.url,
                    }))}>
                    {t.label}
                  </button>
                ))}
              </div>
            </div>
```

- [ ] **Step 3: TypeScript check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit
```

Expected: 0 errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx
git commit -m "feat(ui): quick-start templates for webhook creation"
```

---

## Task 8: scripts/webhook-receiver.py

**Files:**
- Create: `scripts/webhook-receiver.py`

- [ ] **Step 1: Create the script**

Create `scripts/webhook-receiver.py` with the following content:

```python
#!/usr/bin/env python3
"""
Nexspence webhook receiver — development / integration testing helper.

Usage:
  python scripts/webhook-receiver.py [--port PORT] [--secret SECRET] [--once]

Options:
  --port PORT      Listen port (default: 8888)
  --secret SECRET  HMAC-SHA256 signing secret; if set, rejects invalid signatures
  --once           Exit after receiving the first valid event
"""
import argparse
import hashlib
import hmac
import http.server
import json
import sys
from datetime import datetime, timezone


def verify_signature(secret: str, body: bytes, header: str) -> bool:
    expected = "sha256=" + hmac.new(
        secret.encode(), body, hashlib.sha256
    ).hexdigest()
    return hmac.compare_digest(expected, header)


def make_handler(secret: str | None, once: bool, stop: list[bool]):
    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, fmt, *args):
            pass  # suppress default request log

        def do_POST(self):
            length = int(self.headers.get("Content-Length", 0))
            body = self.rfile.read(length)

            if secret:
                sig = self.headers.get("X-Nexspence-Signature", "")
                if not verify_signature(secret, body, sig):
                    self.send_response(403)
                    self.end_headers()
                    self.wfile.write(b"invalid signature\n")
                    print(f"[{now()}] REJECTED — bad signature")
                    return

            event = self.headers.get("X-Nexspence-Event", "unknown")
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            self.wfile.write(b"ok\n")

            try:
                payload = json.loads(body)
            except json.JSONDecodeError:
                payload = {"raw": body.decode(errors="replace")}

            color = {
                "artifact.published": "\033[32m",  # green
                "artifact.deleted":   "\033[31m",  # red
                "repo.created":       "\033[34m",  # blue
                "proxy.error":        "\033[33m",  # yellow
                "webhook.test":       "\033[36m",  # cyan
            }.get(event, "\033[0m")
            reset = "\033[0m" if sys.stdout.isatty() else ""
            c = color if sys.stdout.isatty() else ""

            print(f"\n{c}▶ {event}{reset}  [{now()}]")
            print(json.dumps(payload, indent=2, default=str))

            if once:
                stop.append(True)

    return Handler


def now() -> str:
    return datetime.now(timezone.utc).strftime("%H:%M:%S")


def main():
    p = argparse.ArgumentParser(description="Nexspence webhook receiver")
    p.add_argument("--port", type=int, default=8888)
    p.add_argument("--secret", default=None)
    p.add_argument("--once", action="store_true")
    args = p.parse_args()

    stop: list[bool] = []
    handler = make_handler(args.secret, args.once, stop)
    server = http.server.HTTPServer(("", args.port), handler)

    print(f"Listening on :{args.port}" + (" (HMAC verification ON)" if args.secret else ""))
    print("Waiting for webhook events… (Ctrl-C to quit)\n")

    try:
        while not stop:
            server.handle_request()
    except KeyboardInterrupt:
        print("\nBye.")


if __name__ == "__main__":
    main()
```

- [ ] **Step 2: Verify it starts without error**

```bash
python3 /home/skensel/AI/self_nexus/scripts/webhook-receiver.py --help
```

Expected: prints usage and exits 0.

- [ ] **Step 3: Commit**

```bash
git add scripts/webhook-receiver.py
git commit -m "feat: add webhook-receiver.py for local event testing"
```

---

## Task 9: docs/webhooks.md

**Files:**
- Create: `docs/webhooks.md`

- [ ] **Step 1: Create the doc**

Create `docs/webhooks.md`:

```markdown
# Webhooks

Nexspence fires HTTP POST callbacks to registered URLs when repository events occur.

## Events

| Event | When |
|-------|------|
| `artifact.published` | A new artifact is pushed to a hosted or proxy-cached repo |
| `artifact.deleted` | An artifact is deleted |
| `repo.created` | A repository is created |
| `proxy.error` | A proxy repo fails to fetch from upstream |

## Payload

All events share this JSON structure (unused fields are omitted):

```json
{
  "event": "artifact.published",
  "timestamp": "2026-04-23T10:00:00Z",
  "repository": "my-maven-repo",
  "component": {
    "group": "com.example",
    "name": "my-lib",
    "version": "1.2.3",
    "format": "maven2"
  },
  "asset": {
    "path": "/com/example/my-lib/1.2.3/my-lib-1.2.3.jar",
    "contentType": "application/java-archive",
    "size": 204800
  }
}
```

`repo.created` payload only contains `event`, `timestamp`, `repository`.

## API — CRUD

```bash
BASE=http://localhost:8081
AUTH="-u admin:admin123"

# List all webhooks
curl $AUTH $BASE/api/v1/webhooks

# Create
curl $AUTH -X POST $BASE/api/v1/webhooks \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "my-hook",
    "url": "https://example.com/hook",
    "events": ["artifact.published", "repo.created"],
    "secret": "mysecret"
  }'

# Update (replace id)
curl $AUTH -X PUT $BASE/api/v1/webhooks/<id> \
  -H 'Content-Type: application/json' \
  -d '{"name":"my-hook","url":"https://example.com/hook","events":["artifact.published"],"active":false}'

# Delete
curl $AUTH -X DELETE $BASE/api/v1/webhooks/<id>

# Send test ping (synchronous — shows HTTP status from target URL)
curl $AUTH -X POST $BASE/api/v1/webhooks/<id>/test
# → {"status":200,"latency_ms":42}
```

## Headers on each delivery

| Header | Value |
|--------|-------|
| `Content-Type` | `application/json` |
| `X-Nexspence-Event` | Event name (e.g. `artifact.published`) |
| `X-Nexspence-Signature` | `sha256=<hex>` — only if secret is set |

## Verifying HMAC signatures (Python)

```python
import hashlib, hmac

def verify(secret: str, body: bytes, header: str) -> bool:
    expected = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, header)
```

## Local testing

```bash
# 1. Start the receiver
python scripts/webhook-receiver.py --port 8888 --secret mysecret

# 2. Register a webhook pointing at it
curl -u admin:admin123 -X POST http://localhost:8081/api/v1/webhooks \
  -H 'Content-Type: application/json' \
  -d '{"name":"local-test","url":"http://localhost:8888","events":["artifact.published"],"secret":"mysecret"}'

# 3. Send a test ping via UI ⚡ button, or curl:
curl -u admin:admin123 -X POST http://localhost:8081/api/v1/webhooks/<id>/test

# 4. Push any artifact — the receiver prints the full payload
```
```

- [ ] **Step 2: Commit**

```bash
git add docs/webhooks.md
git commit -m "docs: webhook reference — events, payloads, curl examples, HMAC verification"
```

---

## Final verification

- [ ] **Run full backend test suite**

```bash
cd /home/skensel/AI/self_nexus && go test -race -count=1 ./...
```

Expected: all pass.

- [ ] **Run frontend type check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit
```

Expected: 0 errors.

- [ ] **Update task_plan.md**

Mark Phase 14 tasks as complete:

```markdown
### Phase 14: Webhooks & Routing Rules (Infrastructure)
**Status:** complete (webhooks)

**Tasks:**
- [x] Webhook management API (`WebhookRepo` CRUD)
- [x] Webhook delivery + retry logic
- [ ] Routing rules API (`RoutingRuleRepo` CRUD)  ← separate task
- [x] Frontend Webhooks tab in SecurityPage
```

- [ ] **Final commit**

```bash
git add task_plan.md
git commit -m "docs: mark Phase 14 webhooks tasks complete"
```
