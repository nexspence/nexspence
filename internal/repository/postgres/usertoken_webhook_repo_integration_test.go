//go:build integration

package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// hashToken returns the hex SHA-256 of a raw token value — mirroring how the
// service layer derives token_hash before persisting (only the hash is stored).
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// makeWebhook returns a Webhook with distinct fields for test isolation.
func makeWebhook(name, url string, events []domain.WebhookEvent, active bool) *domain.Webhook {
	return &domain.Webhook{
		Name:   name,
		URL:    url,
		Secret: "hmac-secret-" + name,
		Events: events,
		Active: active,
	}
}

// createTokenParentUser creates a real user row so user_tokens.user_id (FK) is
// satisfied, returning the new user's ID.
func createTokenParentUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, username, email string) string {
	t.Helper()
	userRepo := NewUserRepo(pool)
	u := makeUser(username, email)
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create parent user: %v", err)
	}
	return u.ID
}

// ── UserTokenRepo: Create ──────────────────────────────────────────────────────

func TestUserTokenRepo_Create_StoresHashNotRaw(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "user_tokens", "users")
	ctx := context.Background()
	repo := NewUserTokenRepo(pool)

	userID := createTokenParentUser(t, ctx, pool, "tk_create_user", "tk_create@test.com")

	raw := "nxs_secret_raw_value_123"
	hash := hashToken(raw)
	tok := &domain.UserToken{
		UserID:    userID,
		Name:      "ci-token",
		TokenHash: hash,
		Scopes:    []string{"read", "write"},
	}
	if err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tok.ID == "" {
		t.Error("ID: got empty, want server-assigned UUID")
	}
	if tok.CreatedAt.IsZero() {
		t.Error("CreatedAt: got zero, want server-assigned timestamp")
	}

	// The raw token must NOT be retrievable; only the hash round-trips.
	got, err := repo.GetByHash(ctx, hash)
	if err != nil {
		t.Fatalf("GetByHash: %v", err)
	}
	if got == nil {
		t.Fatal("GetByHash: got nil, want token")
	}
	if got.TokenHash != hash {
		t.Errorf("TokenHash: got %q, want %q", got.TokenHash, hash)
	}
	if got.TokenHash == raw {
		t.Error("TokenHash must not equal the raw token value")
	}

	// Looking up by the raw value yields nothing — proves the raw is not stored.
	rawLookup, err := repo.GetByHash(ctx, raw)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetByHash(raw): want ErrNotFound, got %v", err)
	}
	if rawLookup != nil {
		t.Errorf("GetByHash(raw): got %+v, want nil (raw token must not be persisted)", rawLookup)
	}
}

func TestUserTokenRepo_Create_NilScopesDefaultsEmpty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "user_tokens", "users")
	ctx := context.Background()
	repo := NewUserTokenRepo(pool)

	userID := createTokenParentUser(t, ctx, pool, "tk_nilscope_user", "tk_nilscope@test.com")

	tok := &domain.UserToken{
		UserID:    userID,
		Name:      "no-scopes",
		TokenHash: hashToken("nxs_noscopes"),
		Scopes:    nil, // exercises the nil → []string{} branch in Create
	}
	if err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create with nil scopes: %v", err)
	}

	got, err := repo.Get(ctx, tok.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get: got nil, want token")
	}
	if len(got.Scopes) != 0 {
		t.Errorf("Scopes: got %v, want empty", got.Scopes)
	}
}

// ── UserTokenRepo: expiry handling ─────────────────────────────────────────────

func TestUserTokenRepo_Create_ExpiryNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "user_tokens", "users")
	ctx := context.Background()
	repo := NewUserTokenRepo(pool)

	userID := createTokenParentUser(t, ctx, pool, "tk_expnil_user", "tk_expnil@test.com")

	tok := &domain.UserToken{
		UserID:    userID,
		Name:      "never-expires",
		TokenHash: hashToken("nxs_neverexpire"),
		ExpiresAt: nil,
	}
	if err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, tok.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get: got nil, want token")
	}
	if got.ExpiresAt != nil {
		t.Errorf("ExpiresAt: got %v, want nil", got.ExpiresAt)
	}
}

func TestUserTokenRepo_Create_ExpiryNonNilRoundTrips(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "user_tokens", "users")
	ctx := context.Background()
	repo := NewUserTokenRepo(pool)

	userID := createTokenParentUser(t, ctx, pool, "tk_expset_user", "tk_expset@test.com")

	exp := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	tok := &domain.UserToken{
		UserID:    userID,
		Name:      "expiring",
		TokenHash: hashToken("nxs_expiring"),
		ExpiresAt: &exp,
	}
	if err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, tok.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get: got nil, want token")
	}
	if got.ExpiresAt == nil {
		t.Fatal("ExpiresAt: got nil, want non-nil")
	}
	if !got.ExpiresAt.UTC().Truncate(time.Second).Equal(exp) {
		t.Errorf("ExpiresAt: got %v, want %v", got.ExpiresAt.UTC().Truncate(time.Second), exp)
	}
}

// ── UserTokenRepo: Get / GetByHash ─────────────────────────────────────────────

func TestUserTokenRepo_Get_JoinsUsername(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "user_tokens", "users")
	ctx := context.Background()
	repo := NewUserTokenRepo(pool)

	userID := createTokenParentUser(t, ctx, pool, "tk_join_user", "tk_join@test.com")

	tok := &domain.UserToken{
		UserID:    userID,
		Name:      "joined",
		TokenHash: hashToken("nxs_joined"),
	}
	if err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, tok.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get: got nil, want token")
	}
	if got.Username != "tk_join_user" {
		t.Errorf("Username (joined): got %q, want tk_join_user", got.Username)
	}
	if got.UserID != userID {
		t.Errorf("UserID: got %q, want %q", got.UserID, userID)
	}
}

func TestUserTokenRepo_Get_NotFound(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "user_tokens", "users")
	ctx := context.Background()
	repo := NewUserTokenRepo(pool)

	got, err := repo.Get(ctx, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get(missing): want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Errorf("Get(missing): got %+v, want nil", got)
	}
}

func TestUserTokenRepo_GetByHash_NotFound(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "user_tokens", "users")
	ctx := context.Background()
	repo := NewUserTokenRepo(pool)

	got, err := repo.GetByHash(ctx, hashToken("nxs_does_not_exist"))
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetByHash(missing): want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Errorf("GetByHash(missing): got %+v, want nil", got)
	}
}

// ── UserTokenRepo: ListByUser ──────────────────────────────────────────────────

func TestUserTokenRepo_ListByUser_ReturnsOnlyOwnTokens(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "user_tokens", "users")
	ctx := context.Background()
	repo := NewUserTokenRepo(pool)

	aliceID := createTokenParentUser(t, ctx, pool, "tk_alice", "tk_alice@test.com")
	bobID := createTokenParentUser(t, ctx, pool, "tk_bob", "tk_bob@test.com")

	for i, name := range []string{"alice-1", "alice-2", "alice-3"} {
		tok := &domain.UserToken{
			UserID:    aliceID,
			Name:      name,
			TokenHash: hashToken("nxs_alice_" + name),
		}
		if err := repo.Create(ctx, tok); err != nil {
			t.Fatalf("Create alice[%d]: %v", i, err)
		}
	}
	bobTok := &domain.UserToken{
		UserID:    bobID,
		Name:      "bob-1",
		TokenHash: hashToken("nxs_bob_1"),
	}
	if err := repo.Create(ctx, bobTok); err != nil {
		t.Fatalf("Create bob: %v", err)
	}

	aliceTokens, err := repo.ListByUser(ctx, aliceID)
	if err != nil {
		t.Fatalf("ListByUser(alice): %v", err)
	}
	if len(aliceTokens) != 3 {
		t.Errorf("alice tokens: got %d, want 3", len(aliceTokens))
	}
	for _, tk := range aliceTokens {
		if tk.UserID != aliceID {
			t.Errorf("ListByUser returned foreign token: userID %q", tk.UserID)
		}
		if tk.Username != "tk_alice" {
			t.Errorf("Username: got %q, want tk_alice", tk.Username)
		}
	}

	bobTokens, err := repo.ListByUser(ctx, bobID)
	if err != nil {
		t.Fatalf("ListByUser(bob): %v", err)
	}
	if len(bobTokens) != 1 {
		t.Errorf("bob tokens: got %d, want 1", len(bobTokens))
	}
}

func TestUserTokenRepo_ListByUser_EmptyReturnsNothing(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "user_tokens", "users")
	ctx := context.Background()
	repo := NewUserTokenRepo(pool)

	userID := createTokenParentUser(t, ctx, pool, "tk_empty_user", "tk_empty@test.com")

	tokens, err := repo.ListByUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(tokens) != 0 {
		t.Errorf("tokens: got %d, want 0", len(tokens))
	}
}

// ── UserTokenRepo: TouchLastUsed ───────────────────────────────────────────────

func TestUserTokenRepo_TouchLastUsed_SetsTimestamp(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "user_tokens", "users")
	ctx := context.Background()
	repo := NewUserTokenRepo(pool)

	userID := createTokenParentUser(t, ctx, pool, "tk_touch_user", "tk_touch@test.com")

	tok := &domain.UserToken{
		UserID:    userID,
		Name:      "touchable",
		TokenHash: hashToken("nxs_touchable"),
	}
	if err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// LastUsed starts nil.
	before, err := repo.Get(ctx, tok.ID)
	if err != nil {
		t.Fatalf("Get(before): %v", err)
	}
	if before.LastUsed != nil {
		t.Errorf("LastUsed before touch: got %v, want nil", before.LastUsed)
	}

	if err := repo.TouchLastUsed(ctx, tok.ID); err != nil {
		t.Fatalf("TouchLastUsed: %v", err)
	}

	after, err := repo.Get(ctx, tok.ID)
	if err != nil {
		t.Fatalf("Get(after): %v", err)
	}
	if after.LastUsed == nil {
		t.Fatal("LastUsed after touch: got nil, want non-nil timestamp")
	}
}

func TestUserTokenRepo_TouchLastUsed_MissingIsNoError(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "user_tokens", "users")
	ctx := context.Background()
	repo := NewUserTokenRepo(pool)

	// UPDATE affecting zero rows is not an error.
	if err := repo.TouchLastUsed(ctx, "00000000-0000-0000-0000-000000000000"); err != nil {
		t.Errorf("TouchLastUsed(missing): %v", err)
	}
}

// ── UserTokenRepo: Delete ──────────────────────────────────────────────────────

func TestUserTokenRepo_Delete_RemovesToken(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "user_tokens", "users")
	ctx := context.Background()
	repo := NewUserTokenRepo(pool)

	userID := createTokenParentUser(t, ctx, pool, "tk_del_user", "tk_del@test.com")

	tok := &domain.UserToken{
		UserID:    userID,
		Name:      "deletable",
		TokenHash: hashToken("nxs_deletable"),
	}
	if err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, tok.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.Get(ctx, tok.ID)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get after Delete: want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Errorf("Get after Delete: got %+v, want nil", got)
	}
}

func TestUserTokenRepo_Delete_MissingIsNoError(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "user_tokens", "users")
	ctx := context.Background()
	repo := NewUserTokenRepo(pool)

	if err := repo.Delete(ctx, "00000000-0000-0000-0000-000000000000"); err != nil {
		t.Errorf("Delete(missing): %v", err)
	}
}

// ── WebhookRepo: Create / Get ──────────────────────────────────────────────────

func TestWebhookRepo_Create_PopulatesIDAndTimestamps(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "webhooks")
	ctx := context.Background()
	repo := NewWebhookRepo(pool)

	wh := makeWebhook("wh-create", "https://example.com/hook", []domain.WebhookEvent{
		domain.EventArtifactPublished,
	}, true)
	if err := repo.Create(ctx, wh); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if wh.ID == "" {
		t.Error("ID: got empty, want server-assigned UUID")
	}
	if wh.CreatedAt.IsZero() {
		t.Error("CreatedAt: got zero, want server-assigned timestamp")
	}
	if wh.UpdatedAt.IsZero() {
		t.Error("UpdatedAt: got zero, want server-assigned timestamp")
	}
}

func TestWebhookRepo_Get_RoundTripsFields(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "webhooks")
	ctx := context.Background()
	repo := NewWebhookRepo(pool)

	events := []domain.WebhookEvent{
		domain.EventArtifactPublished,
		domain.EventRepoDeleted,
		domain.EventProxyError,
	}
	wh := makeWebhook("wh-roundtrip", "https://hooks.example/abc", events, true)
	if err := repo.Create(ctx, wh); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, wh.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get: got nil, want webhook")
	}
	if got.Name != "wh-roundtrip" {
		t.Errorf("Name: got %q, want wh-roundtrip", got.Name)
	}
	if got.URL != "https://hooks.example/abc" {
		t.Errorf("URL: got %q, want https://hooks.example/abc", got.URL)
	}
	if got.Secret != "hmac-secret-wh-roundtrip" {
		t.Errorf("Secret: got %q, want hmac-secret-wh-roundtrip", got.Secret)
	}
	if !got.Active {
		t.Error("Active: got false, want true")
	}
	// events text[] must round-trip in order with the same values.
	if len(got.Events) != len(events) {
		t.Fatalf("Events len: got %d, want %d", len(got.Events), len(events))
	}
	for i := range events {
		if got.Events[i] != events[i] {
			t.Errorf("Events[%d]: got %q, want %q", i, got.Events[i], events[i])
		}
	}
}

func TestWebhookRepo_Get_NotFound(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "webhooks")
	ctx := context.Background()
	repo := NewWebhookRepo(pool)

	got, err := repo.Get(ctx, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get(missing): want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Errorf("Get(missing): got %+v, want nil", got)
	}
}

func TestWebhookRepo_Create_EmptyEventsRoundTrips(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "webhooks")
	ctx := context.Background()
	repo := NewWebhookRepo(pool)

	wh := makeWebhook("wh-noevents", "https://example.com/none", []domain.WebhookEvent{}, false)
	if err := repo.Create(ctx, wh); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, wh.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get: got nil, want webhook")
	}
	if len(got.Events) != 0 {
		t.Errorf("Events: got %v, want empty", got.Events)
	}
	if got.Active {
		t.Error("Active: got true, want false")
	}
}

// ── WebhookRepo: List ──────────────────────────────────────────────────────────

func TestWebhookRepo_List_ReturnsAllOrderedByName(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "webhooks")
	ctx := context.Background()
	repo := NewWebhookRepo(pool)

	// Insert out of alphabetical order; List orders BY name.
	names := []string{"wh-charlie", "wh-alpha", "wh-bravo"}
	for _, n := range names {
		wh := makeWebhook(n, "https://example.com/"+n, []domain.WebhookEvent{domain.EventRepoCreated}, true)
		if err := repo.Create(ctx, wh); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List len: got %d, want 3", len(all))
	}
	want := []string{"wh-alpha", "wh-bravo", "wh-charlie"}
	for i, w := range want {
		if all[i].Name != w {
			t.Errorf("List[%d].Name: got %q, want %q", i, all[i].Name, w)
		}
	}
}

func TestWebhookRepo_List_EmptyReturnsNothing(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "webhooks")
	ctx := context.Background()
	repo := NewWebhookRepo(pool)

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("List len: got %d, want 0", len(all))
	}
}

// ── WebhookRepo: ListByEvent ───────────────────────────────────────────────────

func TestWebhookRepo_ListByEvent_FiltersActiveAndSubscribed(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "webhooks")
	ctx := context.Background()
	repo := NewWebhookRepo(pool)

	// Subscribed + active → matches.
	wh1 := makeWebhook("wh-evt-match", "https://example.com/1",
		[]domain.WebhookEvent{domain.EventArtifactPublished, domain.EventRepoCreated}, true)
	// Subscribed but INACTIVE → must NOT match (ListByEvent requires active=true).
	wh2 := makeWebhook("wh-evt-inactive", "https://example.com/2",
		[]domain.WebhookEvent{domain.EventArtifactPublished}, false)
	// Active but NOT subscribed to the event → must NOT match.
	wh3 := makeWebhook("wh-evt-other", "https://example.com/3",
		[]domain.WebhookEvent{domain.EventRepoDeleted}, true)

	for _, wh := range []*domain.Webhook{wh1, wh2, wh3} {
		if err := repo.Create(ctx, wh); err != nil {
			t.Fatalf("Create %s: %v", wh.Name, err)
		}
	}

	matches, err := repo.ListByEvent(ctx, domain.EventArtifactPublished)
	if err != nil {
		t.Fatalf("ListByEvent: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches: got %d, want 1", len(matches))
	}
	if matches[0].Name != "wh-evt-match" {
		t.Errorf("match name: got %q, want wh-evt-match", matches[0].Name)
	}
}

func TestWebhookRepo_ListByEvent_NoMatchReturnsEmpty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "webhooks")
	ctx := context.Background()
	repo := NewWebhookRepo(pool)

	wh := makeWebhook("wh-evt-none", "https://example.com/none",
		[]domain.WebhookEvent{domain.EventRepoCreated}, true)
	if err := repo.Create(ctx, wh); err != nil {
		t.Fatalf("Create: %v", err)
	}

	matches, err := repo.ListByEvent(ctx, domain.EventProxyError)
	if err != nil {
		t.Fatalf("ListByEvent: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("matches: got %d, want 0", len(matches))
	}
}

// ── WebhookRepo: Update ────────────────────────────────────────────────────────

func TestWebhookRepo_Update_ChangesAllFields(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "webhooks")
	ctx := context.Background()
	repo := NewWebhookRepo(pool)

	wh := makeWebhook("wh-update", "https://example.com/old",
		[]domain.WebhookEvent{domain.EventArtifactPublished}, true)
	if err := repo.Create(ctx, wh); err != nil {
		t.Fatalf("Create: %v", err)
	}
	origUpdated := wh.UpdatedAt

	// Mutate every column.
	wh.Name = "wh-update-renamed"
	wh.URL = "https://example.com/new"
	wh.Secret = "rotated-secret"
	wh.Events = []domain.WebhookEvent{domain.EventArtifactDeleted, domain.EventRepoUpdated}
	wh.Active = false

	if err := repo.Update(ctx, wh); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !wh.UpdatedAt.After(origUpdated) && !wh.UpdatedAt.Equal(origUpdated) {
		t.Errorf("UpdatedAt: got %v, want >= original %v", wh.UpdatedAt, origUpdated)
	}

	got, err := repo.Get(ctx, wh.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get: got nil, want webhook")
	}
	if got.Name != "wh-update-renamed" {
		t.Errorf("Name: got %q, want wh-update-renamed", got.Name)
	}
	if got.URL != "https://example.com/new" {
		t.Errorf("URL: got %q, want https://example.com/new", got.URL)
	}
	if got.Secret != "rotated-secret" {
		t.Errorf("Secret: got %q, want rotated-secret", got.Secret)
	}
	if got.Active {
		t.Error("Active: got true, want false")
	}
	wantEvents := []domain.WebhookEvent{domain.EventArtifactDeleted, domain.EventRepoUpdated}
	if len(got.Events) != len(wantEvents) {
		t.Fatalf("Events len: got %d, want %d", len(got.Events), len(wantEvents))
	}
	for i := range wantEvents {
		if got.Events[i] != wantEvents[i] {
			t.Errorf("Events[%d]: got %q, want %q", i, got.Events[i], wantEvents[i])
		}
	}
}

// ── WebhookRepo: Delete ────────────────────────────────────────────────────────

func TestWebhookRepo_Delete_RemovesWebhook(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "webhooks")
	ctx := context.Background()
	repo := NewWebhookRepo(pool)

	wh := makeWebhook("wh-delete", "https://example.com/del",
		[]domain.WebhookEvent{domain.EventRepoCreated}, true)
	if err := repo.Create(ctx, wh); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, wh.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.Get(ctx, wh.ID)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get after Delete: want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Errorf("Get after Delete: got %+v, want nil", got)
	}
}

func TestWebhookRepo_Delete_MissingIsNoError(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "webhooks")
	ctx := context.Background()
	repo := NewWebhookRepo(pool)

	if err := repo.Delete(ctx, "00000000-0000-0000-0000-000000000000"); err != nil {
		t.Errorf("Delete(missing): %v", err)
	}
}
