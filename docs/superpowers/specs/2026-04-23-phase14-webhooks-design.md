# Phase 14: Webhooks — Design Spec

**Date:** 2026-04-23  
**Status:** approved

## Overview

Make the existing webhook infrastructure actually deliver events (currently broken due to nil dispatcher), fix two bugs, add a test endpoint, improve the UI with templates + edit + test button, and ship a Python receiver script with docs.

---

## 1. Backend Bug Fixes

### 1.1 Wire dispatcher in router.go

`formatDeps` in `NewRouter` has a `Webhooks` field that is never set → no artifact events are ever delivered.

**Fix:** add `Webhooks: webhookSvc` to the `formatDeps` struct literal (one line).

### 1.2 Fix X-Nexspence-Event header

`deliver(wh, body)` sets `X-Nexspence-Event: wh.Events[0]` (first subscribed event) instead of the actual dispatched event.

**Fix:** change signature to `deliver(wh, body, event)` — pass `payload.Event` from `Dispatch()`, set header to `string(event)`.

### 1.3 Add GET /api/v1/webhooks/:id route

The handler method `Get` exists but the route is not registered.

**Fix:** add `admin.GET("/api/v1/webhooks/:id", webhookH.Get)` in router.go.

### 1.4 Dispatch repo.created event

`RepositoryService.Create` never fires `EventRepoCreated`.

**Fix:**
- Add `webhooks domain.WebhookDispatcher` field to `RepositoryService`
- Add `func (s *RepositoryService) WithWebhooks(d domain.WebhookDispatcher) *RepositoryService` method
- In `Create`, after successful DB insert, call `s.webhooks.Dispatch(...)` if non-nil
- In router.go: call `repoSvc.WithWebhooks(webhookSvc)` after both are constructed

---

## 2. Test Endpoint

**`POST /api/v1/webhooks/:id/test`** (admin-only)

- Load webhook by ID; 404 if not found
- Build test payload: `domain.WebhookPayload{Event: "webhook.test", Timestamp: now(), Repository: "test"}`
- Call deliver synchronously (10s timeout already on `http.Client`)
- Return `{"status": <http_status>, "latency_ms": <ms>}` on success
- Return `{"error": "<message>"}` with 502 if HTTP call fails

New handler method `WebhookHandler.Test`. New `WebhookService.Test(ctx, id) (*TestResult, error)` — synchronous variant of deliver that returns status + latency.

`TestResult` struct: `Status int`, `LatencyMs int64`.

---

## 3. Frontend — WebhooksTab

### 3.1 Test button

Each webhook row gets `⚡ Test` button next to the delete button.

- On click: `POST /api/v1/webhooks/:id/test`
- Show inline result for 5s: `✓ 200 (42ms)` in green or `✗ connection refused` in red
- Loading spinner while request is in flight

State: `testResult: Record<string, {ok: boolean; msg: string} | null>`, cleared after 5s via `setTimeout`.

### 3.2 Edit

Pencil icon next to delete. Sets `editingId` state, renders same form fields inline within the row card pre-filled with current values.

On save: `PUT /api/v1/webhooks/:id`, invalidate query, clear `editingId`.

### 3.3 Quick-start templates

Above the "New Webhook" form (when open), a row of 4 template buttons that pre-fill the events checkboxes (and URL placeholder for Slack):

| Button | Events pre-selected | URL placeholder |
|--------|-------------------|-----------------|
| Slack | `artifact.published` | `https://hooks.slack.com/services/…` |
| CI/CD Trigger | `artifact.published`, `artifact.deleted` | — |
| Audit Logger | all 4 events | — |
| Proxy Monitor | `proxy.error` | — |

Clicking a template only sets the form state — user still enters URL and saves manually.

---

## 4. scripts/webhook-receiver.py

Standalone Python 3 script (stdlib only, no pip deps):

```
python scripts/webhook-receiver.py [--port PORT] [--secret SECRET] [--once]
```

- `--port` default `8888`
- `--secret` optional HMAC secret; if provided, rejects requests with invalid/missing signature (returns 403)
- `--once` exit after receiving first valid event
- Pretty-prints each payload to stdout with timestamp, event type, and colored output (ANSI if terminal)
- Returns 200 OK for all valid requests

HMAC verification: compute `sha256=<hex(hmac-sha256(secret, body))>`, compare with `X-Nexspence-Signature` header using `hmac.compare_digest`.

---

## 5. docs/webhooks.md

Short reference doc covering:
- What events exist and when they fire (table)
- How to register a webhook (curl example for each CRUD operation)
- Example payload for each event type (JSON blocks)
- How to verify HMAC signatures (Python snippet)
- How to run the local receiver for testing

---

## Payload Schema

```json
{
  "event": "artifact.published",
  "timestamp": "2026-04-23T10:00:00Z",
  "repository": "my-repo",
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

`component` and `asset` are omitted for events that don't involve a specific artifact (e.g. `repo.created`).

---

## Files Changed

| File | Change |
|------|--------|
| `internal/api/router.go` | Wire `Webhooks`, add GET/:id route, call `repoSvc.WithWebhooks` |
| `internal/service/webhook_service.go` | Fix `deliver()` signature, add `Test()` method |
| `internal/service/repository_service.go` | Add `webhooks` field + `WithWebhooks()` + dispatch in `Create` |
| `internal/api/handlers/webhooks.go` | Add `Test` handler method |
| `frontend/src/pages/SecurityPage.tsx` | Test button, edit form, templates |
| `scripts/webhook-receiver.py` | New file |
| `docs/webhooks.md` | New file |

---

## Out of Scope

- Retry logic / delivery log (fire-and-forget is sufficient for now)
- SSE broker wiring (MultiDispatcher) — separate concern
- Routing rules (separate Phase 14 sub-task, not requested here)
