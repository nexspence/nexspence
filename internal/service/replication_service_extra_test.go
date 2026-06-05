package service_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func newReplSvcExtra(t *testing.T) (*service.ReplicationService, *testutil.ReplicationRepo) {
	t.Helper()
	replRepo := testutil.NewReplicationRepo()
	svc := service.NewReplicationService(
		replRepo,
		testutil.NewAssetRepo(),
		testutil.NewBlobStore(),
		"extra-test-secret-32-bytes-long!",
		nopReplLog(),
	)
	return svc, replRepo
}

// ── EncryptPassword / DecryptPassword edge cases ──────────────────────────

func TestReplicationExtra_DecryptPassword_InvalidBase64(t *testing.T) {
	svc, _ := newReplSvcExtra(t)
	_, err := svc.DecryptPassword("not!!valid!!base64@@")
	if err == nil {
		t.Fatal("expected error for invalid base64 input")
	}
}

func TestReplicationExtra_DecryptPassword_TooShort(t *testing.T) {
	svc, _ := newReplSvcExtra(t)
	// A valid base64 string that decodes to fewer bytes than the GCM nonce size (12).
	import64 := "AAAAAAAAAA==" // 7 bytes decoded
	_, err := svc.DecryptPassword(import64)
	if err == nil {
		t.Fatal("expected error for ciphertext that is too short")
	}
}

func TestReplicationExtra_DecryptPassword_WrongKey(t *testing.T) {
	svcA, _ := newReplSvcExtra(t)
	svcB := service.NewReplicationService(
		testutil.NewReplicationRepo(),
		testutil.NewAssetRepo(),
		testutil.NewBlobStore(),
		"completely-different-secret-!!!",
		nopReplLog(),
	)

	enc, err := svcA.EncryptPassword("secret-value")
	if err != nil {
		t.Fatalf("EncryptPassword: %v", err)
	}
	_, err = svcB.DecryptPassword(enc)
	if err == nil {
		t.Fatal("expected error when decrypting with a different key")
	}
}

// ── ListRules / GetRule ───────────────────────────────────────────────────

func TestReplicationExtra_ListRules_Empty(t *testing.T) {
	svc, _ := newReplSvcExtra(t)
	rules, err := svc.ListRules(context.Background())
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("expected empty list, got %d", len(rules))
	}
}

func TestReplicationExtra_ListRules_MasksPassword(t *testing.T) {
	svc, replRepo := newReplSvcExtra(t)
	ctx := context.Background()
	enc, _ := svc.EncryptPassword("my-secret")
	rule := &domain.ReplicationRule{
		Name: "mask-test", SourceRepo: "r", TargetURL: "http://host",
		TargetRepo: "r", TargetPasswordEnc: enc, CronExpr: "0 2 * * *", Enabled: true,
	}
	_ = replRepo.CreateRule(ctx, rule)

	rules, err := svc.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].TargetPasswordEnc != "" {
		t.Fatal("ListRules should mask TargetPasswordEnc")
	}
}

func TestReplicationExtra_GetRule_Found(t *testing.T) {
	svc, replRepo := newReplSvcExtra(t)
	ctx := context.Background()
	enc, _ := svc.EncryptPassword("pass")
	rule := &domain.ReplicationRule{
		Name: "get-test", SourceRepo: "r", TargetURL: "http://host",
		TargetRepo: "r", TargetPasswordEnc: enc, CronExpr: "0 2 * * *", Enabled: true,
	}
	_ = replRepo.CreateRule(ctx, rule)

	got, err := svc.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil rule")
	}
	if got.TargetPasswordEnc != "" {
		t.Fatal("GetRule should mask TargetPasswordEnc")
	}
}

func TestReplicationExtra_GetRule_NotFound(t *testing.T) {
	svc, _ := newReplSvcExtra(t)
	got, err := svc.GetRule(context.Background(), "no-such-id")
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for unknown ID")
	}
}

// ── CreateRule / UpdateRule / DeleteRule ─────────────────────────────────

func TestReplicationExtra_CreateRule_EncryptsPassword(t *testing.T) {
	svc, replRepo := newReplSvcExtra(t)
	ctx := context.Background()

	rule := &domain.ReplicationRule{
		Name: "create-test", SourceRepo: "r", TargetURL: "http://host",
		TargetRepo: "r", CronExpr: "0 2 * * *", Enabled: true,
	}
	if err := svc.CreateRule(ctx, rule, "plain-pass"); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	// The mock stores the encrypted form; make sure it's not plaintext.
	stored, _ := replRepo.GetRule(ctx, rule.ID)
	if stored == nil {
		t.Fatal("rule not found after CreateRule")
	}
	if stored.TargetPasswordEnc == "plain-pass" {
		t.Fatal("password should be encrypted, not stored as plaintext")
	}
	if stored.TargetPasswordEnc == "" {
		t.Fatal("encrypted password should not be empty")
	}
}

func TestReplicationExtra_UpdateRule_WithPassword(t *testing.T) {
	svc, replRepo := newReplSvcExtra(t)
	ctx := context.Background()

	rule := &domain.ReplicationRule{
		Name: "update-test", SourceRepo: "r", TargetURL: "http://host",
		TargetRepo: "r", CronExpr: "0 2 * * *", Enabled: true,
	}
	_ = replRepo.CreateRule(ctx, rule)

	rule.Name = "updated-name"
	if err := svc.UpdateRule(ctx, rule, "new-pass"); err != nil {
		t.Fatalf("UpdateRule with password: %v", err)
	}

	stored, _ := replRepo.GetRule(ctx, rule.ID)
	if stored.Name != "updated-name" {
		t.Fatalf("expected Name=updated-name, got %q", stored.Name)
	}
	// Must be encrypted, not plaintext.
	if stored.TargetPasswordEnc == "new-pass" {
		t.Fatal("password should be encrypted after UpdateRule")
	}
}

func TestReplicationExtra_UpdateRule_KeepsExistingPassword(t *testing.T) {
	svc, replRepo := newReplSvcExtra(t)
	ctx := context.Background()

	enc, _ := svc.EncryptPassword("original-pass")
	rule := &domain.ReplicationRule{
		Name: "keep-pass", SourceRepo: "r", TargetURL: "http://host",
		TargetRepo: "r", TargetPasswordEnc: enc, CronExpr: "0 2 * * *", Enabled: true,
	}
	_ = replRepo.CreateRule(ctx, rule)

	// Update with empty password → should preserve the existing encrypted one.
	rule.Name = "keep-pass-v2"
	if err := svc.UpdateRule(ctx, rule, ""); err != nil {
		t.Fatalf("UpdateRule with empty password: %v", err)
	}

	stored, _ := replRepo.GetRule(ctx, rule.ID)
	if stored.TargetPasswordEnc != enc {
		t.Fatalf("expected existing enc kept, got %q", stored.TargetPasswordEnc)
	}
}

func TestReplicationExtra_DeleteRule(t *testing.T) {
	svc, replRepo := newReplSvcExtra(t)
	ctx := context.Background()

	rule := &domain.ReplicationRule{
		Name: "del-rule", SourceRepo: "r", TargetURL: "http://host",
		TargetRepo: "r", CronExpr: "0 2 * * *", Enabled: true,
	}
	_ = replRepo.CreateRule(ctx, rule)

	if err := svc.DeleteRule(ctx, rule.ID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}

	got, _ := replRepo.GetRule(ctx, rule.ID)
	if got != nil {
		t.Fatal("expected nil after DeleteRule")
	}
}

// ── TestConnection error branches ─────────────────────────────────────────

func TestReplicationExtra_TestConnection_NotFound(t *testing.T) {
	svc, _ := newReplSvcExtra(t)
	err := svc.TestConnection(context.Background(), "no-such-rule")
	if err == nil {
		t.Fatal("expected error for non-existent rule")
	}
}

func TestReplicationExtra_TestConnection_TargetError(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer target.Close()

	svc, replRepo := newReplSvcExtra(t)
	ctx := context.Background()
	rule := &domain.ReplicationRule{
		Name: "tc-err", SourceRepo: "r", TargetURL: target.URL,
		TargetRepo: "r", CronExpr: "0 2 * * *", Enabled: true,
	}
	_ = replRepo.CreateRule(ctx, rule)

	err := svc.TestConnection(ctx, rule.ID)
	if err == nil {
		t.Fatal("expected error when target returns 401")
	}
}

// ── ListHistory ─────────────────────────────────────────────────────────

func TestReplicationExtra_ListHistory_Empty(t *testing.T) {
	svc, replRepo := newReplSvcExtra(t)
	ctx := context.Background()
	rule := &domain.ReplicationRule{
		Name: "hist-empty", SourceRepo: "r", TargetURL: "http://host",
		TargetRepo: "r", CronExpr: "0 2 * * *", Enabled: true,
	}
	_ = replRepo.CreateRule(ctx, rule)

	hist, err := svc.ListHistory(ctx, rule.ID, 10)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(hist) != 0 {
		t.Fatalf("expected empty history, got %d", len(hist))
	}
}

func TestReplicationExtra_ListHistory_LimitClamped(t *testing.T) {
	// limit <= 0 should be clamped to 20.
	svc, _ := newReplSvcExtra(t)
	hist, err := svc.ListHistory(context.Background(), "any-id", 0)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	// No entries exist, so just confirming no panic / no error.
	_ = hist
}

// ── ReloadRule — cron scheduler nil guard ────────────────────────────────

func TestReplicationExtra_ReloadRule_NoScheduler(t *testing.T) {
	// ReloadRule should be a no-op when the cron scheduler has not been started.
	svc, replRepo := newReplSvcExtra(t)
	ctx := context.Background()

	rule := &domain.ReplicationRule{
		Name: "reload-test", SourceRepo: "r", TargetURL: "http://host",
		TargetRepo: "r", CronExpr: "0 2 * * *", Enabled: true,
	}
	_ = replRepo.CreateRule(ctx, rule)

	// Should not panic or error even without a running scheduler.
	svc.ReloadRule(ctx, rule.ID)
	svc.ReloadRule(ctx, "no-such-rule") // also safe for unknown IDs
}

// ── RunRule error branches ────────────────────────────────────────────────

func TestReplicationExtra_RunRule_TargetHTTPError(t *testing.T) {
	// Target /service/rest/v1/assets returns a non-200 status; RunRule should return an error.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()

	replRepo := testutil.NewReplicationRepo()
	svc := service.NewReplicationService(
		replRepo,
		testutil.NewAssetRepo(),
		testutil.NewBlobStore(),
		"run-err-secret-32-bytes-long!!!",
		nopReplLog(),
	)
	ctx := context.Background()

	rule := &domain.ReplicationRule{
		Name: "run-err-rule", SourceRepo: "no-assets", TargetURL: target.URL,
		TargetRepo: "r", CronExpr: "0 2 * * *", Enabled: true,
	}
	_ = replRepo.CreateRule(ctx, rule)

	err := svc.RunRule(ctx, rule.ID)
	if err == nil {
		t.Fatal("expected error when target list-assets returns 500")
	}
}
