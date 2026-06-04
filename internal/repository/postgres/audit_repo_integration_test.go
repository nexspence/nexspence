//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// makeAudit returns a minimal AuditEvent with distinct username/action for test isolation.
// All timestamps default to NOW() on the server so they fall in the 2026-06 partition.
func makeAudit(username, action, domain_, result string) *domain.AuditEvent {
	return &domain.AuditEvent{
		Username:   username,
		Domain:     domain_,
		Action:     action,
		EntityType: "repository",
		EntityID:   "repo-001",
		EntityName: "my-repo",
		RemoteIP:   "10.0.0.1",
		UserAgent:  "go-test/1.0",
		Context:    map[string]any{"key": "value"},
		Result:     result,
	}
}

// nowPlus returns a pointer to a time offset from now.
func nowPlus(d time.Duration) *time.Time {
	t := time.Now().Add(d)
	return &t
}

// ── Write ─────────────────────────────────────────────────────────────────────

func TestAuditRepo_Write_Succeeds(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	e := makeAudit("write_user", "CREATE", "REPOSITORY", "success")
	if err := repo.Write(ctx, e); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Confirm the row was persisted.
	items, total, err := repo.List(ctx, repository.AuditQuery{})
	if err != nil {
		t.Fatalf("List after Write: %v", err)
	}
	if total != 1 {
		t.Fatalf("total: got %d, want 1", total)
	}
	if len(items) != 1 {
		t.Fatalf("len(items): got %d, want 1", len(items))
	}
	if items[0].Username != "write_user" {
		t.Errorf("Username: got %q, want %q", items[0].Username, "write_user")
	}
	if items[0].Action != "CREATE" {
		t.Errorf("Action: got %q, want %q", items[0].Action, "CREATE")
	}
	if items[0].Result != "success" {
		t.Errorf("Result: got %q, want %q", items[0].Result, "success")
	}
}

func TestAuditRepo_Write_NullableFields(t *testing.T) {
	// Write with nil UserID, empty RemoteIP, empty UserAgent — must not error.
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	e := &domain.AuditEvent{
		UserID:   nil,
		Username: "anon_user_wr",
		Domain:   "SECURITY",
		Action:   "LOGIN",
		Result:   "failure",
		// RemoteIP, UserAgent, EntityType, EntityID, EntityName left empty
	}
	if err := repo.Write(ctx, e); err != nil {
		t.Fatalf("Write with nullable fields: %v", err)
	}

	items, total, err := repo.List(ctx, repository.AuditQuery{Username: "anon_user_wr"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Fatalf("total: got %d, want 1", total)
	}
	got := items[0]
	if got.UserID != nil {
		t.Errorf("UserID: got %v, want nil", got.UserID)
	}
	if got.RemoteIP != "" {
		t.Errorf("RemoteIP: got %q, want empty", got.RemoteIP)
	}
	if got.UserAgent != "" {
		t.Errorf("UserAgent: got %q, want empty", got.UserAgent)
	}
}

func TestAuditRepo_Write_ContextJSONRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	e := makeAudit("ctx_user", "DELETE", "REPOSITORY", "success")
	e.Context = map[string]any{"path": "/repo/foo.jar", "size": float64(1024)}

	if err := repo.Write(ctx, e); err != nil {
		t.Fatalf("Write: %v", err)
	}

	items, _, err := repo.List(ctx, repository.AuditQuery{Username: "ctx_user"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("no items returned")
	}
	got := items[0].Context
	if got["path"] != "/repo/foo.jar" {
		t.Errorf("context[path]: got %v, want /repo/foo.jar", got["path"])
	}
	if got["size"] != float64(1024) {
		t.Errorf("context[size]: got %v, want 1024", got["size"])
	}
}

// ── List — no filter ──────────────────────────────────────────────────────────

func TestAuditRepo_List_NoFilter_ReturnsAllAndTotal(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	for i := 0; i < 5; i++ {
		e := makeAudit("list_nofilter_user", "READ", "REPOSITORY", "success")
		if err := repo.Write(ctx, e); err != nil {
			t.Fatalf("Write[%d]: %v", i, err)
		}
	}

	items, total, err := repo.List(ctx, repository.AuditQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 5 {
		t.Errorf("total: got %d, want 5", total)
	}
	if len(items) != 5 {
		t.Errorf("len(items): got %d, want 5", len(items))
	}
}

func TestAuditRepo_List_EmptyTable_ReturnsZero(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	items, total, err := repo.List(ctx, repository.AuditQuery{})
	if err != nil {
		t.Fatalf("List on empty: %v", err)
	}
	if total != 0 {
		t.Errorf("total: got %d, want 0", total)
	}
	if len(items) != 0 {
		t.Errorf("len(items): got %d, want 0", len(items))
	}
}

// ── List — ordering ───────────────────────────────────────────────────────────

func TestAuditRepo_List_OrderedNewestFirst(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	// Insert 3 events with explicit timestamps spaced 1 second apart.
	base := time.Now().Truncate(time.Second)
	for i := 0; i < 3; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		_, err := pool.Exec(ctx, `
			INSERT INTO audit_events (event_time, username, domain, action, result)
			VALUES ($1, $2, 'REPOSITORY', 'READ', 'success')`,
			ts, "ordering_user")
		if err != nil {
			t.Fatalf("insert[%d]: %v", i, err)
		}
	}

	items, total, err := repo.List(ctx, repository.AuditQuery{Username: "ordering_user"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 {
		t.Fatalf("total: got %d, want 3", total)
	}
	// Verify descending order — each item should be before the next.
	for i := 1; i < len(items); i++ {
		if items[i].EventTime.After(items[i-1].EventTime) {
			t.Errorf("not DESC order at index %d: %v > %v", i, items[i].EventTime, items[i-1].EventTime)
		}
	}
}

// ── List — username filter ────────────────────────────────────────────────────

func TestAuditRepo_List_UsernameFilter(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	for i := 0; i < 3; i++ {
		if err := repo.Write(ctx, makeAudit("uf_alice", "CREATE", "REPOSITORY", "success")); err != nil {
			t.Fatalf("Write alice[%d]: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := repo.Write(ctx, makeAudit("uf_bob", "DELETE", "REPOSITORY", "success")); err != nil {
			t.Fatalf("Write bob[%d]: %v", i, err)
		}
	}

	aliceItems, aliceTotal, err := repo.List(ctx, repository.AuditQuery{Username: "uf_alice"})
	if err != nil {
		t.Fatalf("List(alice): %v", err)
	}
	if aliceTotal != 3 {
		t.Errorf("alice total: got %d, want 3", aliceTotal)
	}
	if len(aliceItems) != 3 {
		t.Errorf("alice items: got %d, want 3", len(aliceItems))
	}
	for _, item := range aliceItems {
		if item.Username != "uf_alice" {
			t.Errorf("unexpected username: got %q, want uf_alice", item.Username)
		}
	}

	bobItems, bobTotal, err := repo.List(ctx, repository.AuditQuery{Username: "uf_bob"})
	if err != nil {
		t.Fatalf("List(bob): %v", err)
	}
	if bobTotal != 2 {
		t.Errorf("bob total: got %d, want 2", bobTotal)
	}
	if len(bobItems) != 2 {
		t.Errorf("bob items: got %d, want 2", len(bobItems))
	}
}

// ── List — domain + action filters ───────────────────────────────────────────

func TestAuditRepo_List_DomainAndActionFilter(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	if err := repo.Write(ctx, makeAudit("da_user", "CREATE", "REPOSITORY", "success")); err != nil {
		t.Fatalf("Write REPOSITORY/CREATE: %v", err)
	}
	if err := repo.Write(ctx, makeAudit("da_user", "DELETE", "REPOSITORY", "success")); err != nil {
		t.Fatalf("Write REPOSITORY/DELETE: %v", err)
	}
	if err := repo.Write(ctx, makeAudit("da_user", "LOGIN", "SECURITY", "failure")); err != nil {
		t.Fatalf("Write SECURITY/LOGIN: %v", err)
	}

	// Filter by domain only.
	domItems, domTotal, err := repo.List(ctx, repository.AuditQuery{Domain: "SECURITY"})
	if err != nil {
		t.Fatalf("List(domain=SECURITY): %v", err)
	}
	if domTotal != 1 {
		t.Errorf("domain total: got %d, want 1", domTotal)
	}
	if len(domItems) != 1 || domItems[0].Domain != "SECURITY" {
		t.Errorf("domain filter returned wrong row: %+v", domItems)
	}

	// Filter by domain + action.
	actItems, actTotal, err := repo.List(ctx, repository.AuditQuery{Domain: "REPOSITORY", Action: "DELETE"})
	if err != nil {
		t.Fatalf("List(domain+action): %v", err)
	}
	if actTotal != 1 {
		t.Errorf("action total: got %d, want 1", actTotal)
	}
	if len(actItems) != 1 || actItems[0].Action != "DELETE" {
		t.Errorf("action filter returned wrong row: %+v", actItems)
	}
}

// ── List — from/to date filters ───────────────────────────────────────────────

func TestAuditRepo_List_FromToFilter(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	// Insert rows at t0-10s, t0, t0+10s (all within 2026-06 partition).
	base := time.Now().Truncate(time.Second)
	times := []time.Time{
		base.Add(-10 * time.Second),
		base,
		base.Add(10 * time.Second),
	}
	for i, ts := range times {
		_, err := pool.Exec(ctx, `
			INSERT INTO audit_events (event_time, username, domain, action, result)
			VALUES ($1, $2, 'REPOSITORY', 'READ', 'success')`,
			ts, "fromto_user")
		if err != nil {
			t.Fatalf("insert[%d]: %v", i, err)
		}
	}

	// from=base selects base and base+10s (2 events).
	from := base
	fromItems, fromTotal, err := repo.List(ctx, repository.AuditQuery{
		Username: "fromto_user",
		From:     &from,
	})
	if err != nil {
		t.Fatalf("List(from): %v", err)
	}
	if fromTotal != 2 {
		t.Errorf("from total: got %d, want 2", fromTotal)
	}
	if len(fromItems) != 2 {
		t.Errorf("from items: got %d, want 2", len(fromItems))
	}

	// to=base selects only base-10s (1 event; to is exclusive).
	to := base
	toItems, toTotal, err := repo.List(ctx, repository.AuditQuery{
		Username: "fromto_user",
		To:       &to,
	})
	if err != nil {
		t.Fatalf("List(to): %v", err)
	}
	if toTotal != 1 {
		t.Errorf("to total: got %d, want 1", toTotal)
	}
	if len(toItems) != 1 {
		t.Errorf("to items: got %d, want 1", len(toItems))
	}

	// from=base-5s, to=base+5s selects only base (1 event).
	fromMid := base.Add(-5 * time.Second)
	toMid := base.Add(5 * time.Second)
	midItems, midTotal, err := repo.List(ctx, repository.AuditQuery{
		Username: "fromto_user",
		From:     &fromMid,
		To:       &toMid,
	})
	if err != nil {
		t.Fatalf("List(from+to): %v", err)
	}
	if midTotal != 1 {
		t.Errorf("mid total: got %d, want 1", midTotal)
	}
	if len(midItems) != 1 {
		t.Errorf("mid items: got %d, want 1", len(midItems))
	}
}

// ── List — pagination ─────────────────────────────────────────────────────────

func TestAuditRepo_List_Pagination_TotalUnaffectedByPage(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	// Insert 10 events.
	for i := 0; i < 10; i++ {
		if err := repo.Write(ctx, makeAudit("page_user", "READ", "REPOSITORY", "success")); err != nil {
			t.Fatalf("Write[%d]: %v", i, err)
		}
	}

	// Page 1: limit=3, offset=0.
	page1, total1, err := repo.List(ctx, repository.AuditQuery{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if total1 != 10 {
		t.Errorf("page1 total: got %d, want 10 (total must reflect full count)", total1)
	}
	if len(page1) != 3 {
		t.Errorf("page1 items: got %d, want 3", len(page1))
	}

	// Page 2: limit=3, offset=3.
	page2, total2, err := repo.List(ctx, repository.AuditQuery{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if total2 != 10 {
		t.Errorf("page2 total: got %d, want 10", total2)
	}
	if len(page2) != 3 {
		t.Errorf("page2 items: got %d, want 3", len(page2))
	}

	// Last page: limit=3, offset=9 → 1 item left.
	last, totalLast, err := repo.List(ctx, repository.AuditQuery{Limit: 3, Offset: 9})
	if err != nil {
		t.Fatalf("List last: %v", err)
	}
	if totalLast != 10 {
		t.Errorf("last total: got %d, want 10", totalLast)
	}
	if len(last) != 1 {
		t.Errorf("last items: got %d, want 1", len(last))
	}
}

func TestAuditRepo_List_DefaultLimit(t *testing.T) {
	// When Limit<=0, the repo defaults to 100.
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	for i := 0; i < 5; i++ {
		if err := repo.Write(ctx, makeAudit("deflimit_user", "READ", "REPOSITORY", "success")); err != nil {
			t.Fatalf("Write[%d]: %v", i, err)
		}
	}

	items, total, err := repo.List(ctx, repository.AuditQuery{Limit: 0})
	if err != nil {
		t.Fatalf("List(Limit=0): %v", err)
	}
	if total != 5 {
		t.Errorf("total: got %d, want 5", total)
	}
	if len(items) != 5 {
		t.Errorf("items: got %d, want 5", len(items))
	}
}

// ── List — total vs items independence ───────────────────────────────────────

func TestAuditRepo_List_TotalIndependentOfLimit(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	for i := 0; i < 7; i++ {
		if err := repo.Write(ctx, makeAudit("total_ind_user", "READ", "REPOSITORY", "success")); err != nil {
			t.Fatalf("Write[%d]: %v", i, err)
		}
	}

	items, total, err := repo.List(ctx, repository.AuditQuery{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// total must be 7 (all rows) even though page size is 2.
	if total != 7 {
		t.Errorf("total: got %d, want 7 (must be full count, not page size)", total)
	}
	if len(items) != 2 {
		t.Errorf("items: got %d, want 2", len(items))
	}
}

// ── Stream ────────────────────────────────────────────────────────────────────

func TestAuditRepo_Stream_AllRows(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	for i := 0; i < 6; i++ {
		if err := repo.Write(ctx, makeAudit("stream_user", "READ", "REPOSITORY", "success")); err != nil {
			t.Fatalf("Write[%d]: %v", i, err)
		}
	}

	var collected []domain.AuditEvent
	err := repo.Stream(ctx, repository.AuditQuery{}, func(e domain.AuditEvent) error {
		collected = append(collected, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(collected) != 6 {
		t.Errorf("Stream collected %d events, want 6", len(collected))
	}
}

func TestAuditRepo_Stream_WithUsernameFilter(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	for i := 0; i < 4; i++ {
		if err := repo.Write(ctx, makeAudit("stream_alice", "CREATE", "REPOSITORY", "success")); err != nil {
			t.Fatalf("Write alice[%d]: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := repo.Write(ctx, makeAudit("stream_bob", "DELETE", "REPOSITORY", "success")); err != nil {
			t.Fatalf("Write bob[%d]: %v", i, err)
		}
	}

	var aliceEvents []domain.AuditEvent
	err := repo.Stream(ctx, repository.AuditQuery{Username: "stream_alice"}, func(e domain.AuditEvent) error {
		aliceEvents = append(aliceEvents, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream(alice): %v", err)
	}
	if len(aliceEvents) != 4 {
		t.Errorf("Stream alice: got %d, want 4", len(aliceEvents))
	}
	for _, ev := range aliceEvents {
		if ev.Username != "stream_alice" {
			t.Errorf("Stream returned wrong username: %q", ev.Username)
		}
	}
}

func TestAuditRepo_Stream_EmptyTable(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	var count int
	err := repo.Stream(ctx, repository.AuditQuery{}, func(_ domain.AuditEvent) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("Stream on empty: %v", err)
	}
	if count != 0 {
		t.Errorf("Stream on empty: got %d calls, want 0", count)
	}
}

func TestAuditRepo_Stream_CallbackErrorHalts(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	for i := 0; i < 5; i++ {
		if err := repo.Write(ctx, makeAudit("cb_halt_user", "READ", "REPOSITORY", "success")); err != nil {
			t.Fatalf("Write[%d]: %v", i, err)
		}
	}

	sentinel := errors.New("stop iteration")
	var count int
	err := repo.Stream(ctx, repository.AuditQuery{}, func(_ domain.AuditEvent) error {
		count++
		if count == 2 {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("Stream: expected sentinel error, got %v", err)
	}
	if count != 2 {
		t.Errorf("Stream halted at call %d, want 2", count)
	}
}

func TestAuditRepo_Stream_FromToFilter(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	base := time.Now().Truncate(time.Second)
	// Insert: one before, one at base, one after.
	insertTimes := []time.Time{
		base.Add(-10 * time.Second),
		base,
		base.Add(10 * time.Second),
	}
	for i, ts := range insertTimes {
		_, err := pool.Exec(ctx, `
			INSERT INTO audit_events (event_time, username, domain, action, result)
			VALUES ($1, $2, 'REPOSITORY', 'READ', 'success')`,
			ts, "stream_fromto_user")
		if err != nil {
			t.Fatalf("insert[%d]: %v", i, err)
		}
	}

	// Stream from=base: should get 2 events (base and base+10s).
	from := base
	var got []domain.AuditEvent
	err := repo.Stream(ctx, repository.AuditQuery{
		Username: "stream_fromto_user",
		From:     &from,
	}, func(e domain.AuditEvent) error {
		got = append(got, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream(from): %v", err)
	}
	if len(got) != 2 {
		t.Errorf("Stream(from): got %d events, want 2", len(got))
	}
}

func TestAuditRepo_Stream_OrderedNewestFirst(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	base := time.Now().Truncate(time.Second)
	for i := 0; i < 3; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		_, err := pool.Exec(ctx, `
			INSERT INTO audit_events (event_time, username, domain, action, result)
			VALUES ($1, $2, 'REPOSITORY', 'READ', 'success')`,
			ts, "stream_order_user")
		if err != nil {
			t.Fatalf("insert[%d]: %v", i, err)
		}
	}

	var streamItems []domain.AuditEvent
	err := repo.Stream(ctx, repository.AuditQuery{Username: "stream_order_user"}, func(e domain.AuditEvent) error {
		streamItems = append(streamItems, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(streamItems) != 3 {
		t.Fatalf("Stream: got %d, want 3", len(streamItems))
	}
	for i := 1; i < len(streamItems); i++ {
		if streamItems[i].EventTime.After(streamItems[i-1].EventTime) {
			t.Errorf("Stream not DESC at index %d: %v > %v", i, streamItems[i].EventTime, streamItems[i-1].EventTime)
		}
	}
}

// ── Write — result variants ───────────────────────────────────────────────────

func TestAuditRepo_Write_AllResultVariants(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	results := []string{"success", "failure", "denied"}
	for _, res := range results {
		e := makeAudit("result_user_"+res, "LOGIN", "SECURITY", res)
		if err := repo.Write(ctx, e); err != nil {
			t.Errorf("Write(result=%q): %v", res, err)
		}
	}

	items, total, err := repo.List(ctx, repository.AuditQuery{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 {
		t.Errorf("total: got %d, want 3", total)
	}
	if len(items) != 3 {
		t.Errorf("items: got %d, want 3", len(items))
	}
}

// ── FieldsRoundTrip ───────────────────────────────────────────────────────────

func TestAuditRepo_Write_FieldsRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events", "users")
	ctx := context.Background()
	repo := NewAuditRepo(pool)

	// Create a real user so UserID is a valid UUID foreign key.
	userRepo := NewUserRepo(pool)
	u := makeUser("rt_uid_user_src", "rt_uid_src@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user for FK: %v", err)
	}

	e := &domain.AuditEvent{
		UserID:     &u.ID,
		Username:   "rt_user",
		RemoteIP:   "192.168.1.100",
		UserAgent:  "test-agent/2.0",
		Domain:     "USER",
		Action:     "UPDATE",
		EntityType: "user",
		EntityID:   "some-uuid-123",
		EntityName: "alice",
		Context:    map[string]any{"reason": "test"},
		Result:     "success",
	}
	if err := repo.Write(ctx, e); err != nil {
		t.Fatalf("Write: %v", err)
	}

	items, total, err := repo.List(ctx, repository.AuditQuery{Username: "rt_user"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Fatalf("total: got %d, want 1", total)
	}
	got := items[0]

	if got.Username != "rt_user" {
		t.Errorf("Username: got %q, want rt_user", got.Username)
	}
	// PostgreSQL returns INET as CIDR text (e.g. "192.168.1.100/32") — this is
	// the actual behavior of remote_ip::text in the SELECT.
	if got.RemoteIP != "192.168.1.100" && got.RemoteIP != "192.168.1.100/32" {
		t.Errorf("RemoteIP: got %q, want 192.168.1.100 or 192.168.1.100/32", got.RemoteIP)
	}
	if got.UserAgent != "test-agent/2.0" {
		t.Errorf("UserAgent: got %q, want test-agent/2.0", got.UserAgent)
	}
	if got.Domain != "USER" {
		t.Errorf("Domain: got %q, want USER", got.Domain)
	}
	if got.Action != "UPDATE" {
		t.Errorf("Action: got %q, want UPDATE", got.Action)
	}
	if got.EntityType != "user" {
		t.Errorf("EntityType: got %q, want user", got.EntityType)
	}
	if got.EntityID != "some-uuid-123" {
		t.Errorf("EntityID: got %q, want some-uuid-123", got.EntityID)
	}
	if got.EntityName != "alice" {
		t.Errorf("EntityName: got %q, want alice", got.EntityName)
	}
	if got.Result != "success" {
		t.Errorf("Result: got %q, want success", got.Result)
	}
	if got.Context["reason"] != "test" {
		t.Errorf("Context[reason]: got %v, want test", got.Context["reason"])
	}
	if got.ID == 0 {
		t.Error("ID: got 0, want non-zero (server-assigned)")
	}
	if got.EventTime.IsZero() {
		t.Error("EventTime: got zero, want non-zero (server-assigned)")
	}
}

// ── Write — UserID resolves from users table ──────────────────────────────────

func TestAuditRepo_Write_WithValidUserID(t *testing.T) {
	// UserID references users(id); create a real user first.
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "audit_events", "users")
	ctx := context.Background()

	userRepo := NewUserRepo(pool)
	u := makeUser("audit_uid_user", "audit_uid@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}

	auditRepo := NewAuditRepo(pool)
	e := makeAudit("audit_uid_user", "DELETE", "REPOSITORY", "success")
	e.UserID = &u.ID

	if err := auditRepo.Write(ctx, e); err != nil {
		t.Fatalf("Write with valid UserID: %v", err)
	}

	items, total, err := auditRepo.List(ctx, repository.AuditQuery{Username: "audit_uid_user"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Fatalf("total: got %d, want 1", total)
	}
	if items[0].UserID == nil {
		t.Fatal("UserID: got nil, want non-nil")
	}
	if *items[0].UserID != u.ID {
		t.Errorf("UserID: got %q, want %q", *items[0].UserID, u.ID)
	}
}
