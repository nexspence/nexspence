package service_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func nopReplLog() *zap.SugaredLogger { return zap.NewNop().Sugar() }

// plainClient builds an unguarded HTTP client so tests can reach loopback
// httptest servers (the production SSRF guard blocks 127.0.0.1).
func plainClient(timeout time.Duration) *http.Client { return &http.Client{Timeout: timeout} }

func newTestReplicationService(t *testing.T) *service.ReplicationService {
	t.Helper()
	return service.NewReplicationService(
		testutil.NewReplicationRepo(),
		testutil.NewAssetRepo(),
		testutil.NewBlobStore(),
		"test-jwt-secret-32-bytes-long!!!",
		nil,
		nopReplLog(),
	)
}

func testEncryptionKey() []byte { return bytes.Repeat([]byte{0x42}, 32) }

func newKeyedReplicationService(t *testing.T, repo repository.ReplicationRepo) *service.ReplicationService {
	t.Helper()
	return service.NewReplicationService(
		repo,
		testutil.NewAssetRepo(),
		testutil.NewBlobStore(),
		"test-jwt-secret-32-bytes-long!!!",
		testEncryptionKey(),
		nopReplLog(),
	)
}

func TestReplicationService_EncryptDecrypt(t *testing.T) {
	svc := newTestReplicationService(t)

	plain := "super-secret-password"
	enc, err := svc.EncryptPassword(plain)
	if err != nil {
		t.Fatalf("EncryptPassword: %v", err)
	}
	if enc == plain {
		t.Fatal("EncryptPassword returned plaintext unchanged")
	}

	got, err := svc.DecryptPassword(enc)
	if err != nil {
		t.Fatalf("DecryptPassword: %v", err)
	}
	if got != plain {
		t.Fatalf("DecryptPassword: want %q got %q", plain, got)
	}
}

func TestReplicationService_EncryptEmpty(t *testing.T) {
	svc := newTestReplicationService(t)
	enc, err := svc.EncryptPassword("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enc != "" {
		t.Fatalf("want empty enc for empty plain, got %q", enc)
	}
}

func TestReplicationService_RunRule_PushesNewAssets(t *testing.T) {
	var pushed []string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/service/rest/v1/assets") {
			fmt.Fprint(w, `{"items":[],"continuationToken":null}`)
			return
		}
		if r.Method == http.MethodPut {
			pushed = append(pushed, r.URL.Path)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer target.Close()

	assetRepo := testutil.NewAssetRepo()
	compRepo := testutil.NewComponentRepo()
	blobStore := testutil.NewBlobStore()
	ctx := context.Background()

	comp := &domain.Component{Repository: "my-repo", Format: "raw", Name: "lib", Version: "1.0"}
	_ = compRepo.Create(ctx, comp)
	asset := &domain.Asset{
		ComponentID: comp.ID,
		Repository:  "my-repo",
		Path:        "lib/1.0/lib.jar",
		BlobKey:     "blobkey-1",
		SizeBytes:   5,
	}
	_ = assetRepo.Create(ctx, asset)
	_ = blobStore.Put(ctx, "blobkey-1", strings.NewReader("hello"), 5)

	replRepo := testutil.NewReplicationRepo()
	svc := service.NewReplicationService(replRepo, assetRepo, blobStore, "test-secret-32-bytes-long!!!", nil, nopReplLog()).WithHTTPClientFactory(plainClient)

	enc, _ := svc.EncryptPassword("pass")
	rule := &domain.ReplicationRule{
		Name:              "test-rule",
		SourceRepo:        "my-repo",
		TargetURL:         target.URL,
		TargetRepo:        "my-repo-mirror",
		TargetUsername:    "admin",
		TargetPasswordEnc: enc,
		CronExpr:          "0 2 * * *",
		Enabled:           true,
	}
	if err := replRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if err := svc.RunRule(ctx, rule.ID); err != nil {
		t.Fatalf("RunRule: %v", err)
	}

	if len(pushed) != 1 {
		t.Fatalf("expected 1 pushed asset, got %d: %v", len(pushed), pushed)
	}

	history, _ := svc.ListHistory(ctx, rule.ID, 10)
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].PushedCount != 1 {
		t.Fatalf("expected PushedCount=1, got %d", history[0].PushedCount)
	}
	if history[0].SkippedCount != 0 {
		t.Fatalf("expected SkippedCount=0, got %d", history[0].SkippedCount)
	}
}

func TestReplicationService_RunRule_SkipsExistingAssets(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/service/rest/v1/assets") {
			fmt.Fprint(w, `{"items":[{"path":"lib/1.0/lib.jar"}],"continuationToken":null}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()

	assetRepo := testutil.NewAssetRepo()
	compRepo := testutil.NewComponentRepo()
	blobStore := testutil.NewBlobStore()
	ctx := context.Background()

	comp := &domain.Component{Repository: "repo-a", Format: "raw", Name: "lib", Version: "1.0"}
	_ = compRepo.Create(ctx, comp)
	asset := &domain.Asset{
		ComponentID: comp.ID,
		Repository:  "repo-a",
		Path:        "lib/1.0/lib.jar",
		BlobKey:     "blobkey-2",
		SizeBytes:   3,
	}
	_ = assetRepo.Create(ctx, asset)

	replRepo := testutil.NewReplicationRepo()
	svc := service.NewReplicationService(replRepo, assetRepo, blobStore, "another-secret-32-bytes-long!!", nil, nopReplLog()).WithHTTPClientFactory(plainClient)

	rule := &domain.ReplicationRule{
		Name:       "skip-rule",
		SourceRepo: "repo-a",
		TargetURL:  target.URL,
		TargetRepo: "repo-a-mirror",
		CronExpr:   "0 3 * * *",
		Enabled:    true,
	}
	_ = replRepo.CreateRule(ctx, rule)

	if err := svc.RunRule(ctx, rule.ID); err != nil {
		t.Fatalf("RunRule: %v", err)
	}

	history, _ := svc.ListHistory(ctx, rule.ID, 10)
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry")
	}
	if history[0].SkippedCount != 1 {
		t.Fatalf("expected SkippedCount=1, got %d", history[0].SkippedCount)
	}
	if history[0].PushedCount != 0 {
		t.Fatalf("expected PushedCount=0, got %d", history[0].PushedCount)
	}
}

func TestReplicationService_TestConnection_OK(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/service/rest/v1/status" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer target.Close()

	replRepo := testutil.NewReplicationRepo()
	svc := service.NewReplicationService(replRepo, testutil.NewAssetRepo(), testutil.NewBlobStore(), "secret-key-32-bytes-long-padded!", nil, nopReplLog()).WithHTTPClientFactory(plainClient)
	ctx := context.Background()

	rule := &domain.ReplicationRule{
		Name: "conn-test", SourceRepo: "r", TargetURL: target.URL,
		TargetRepo: "r", CronExpr: "0 2 * * *", Enabled: true,
	}
	_ = replRepo.CreateRule(ctx, rule)

	if err := svc.TestConnection(ctx, rule.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplicationService_RunRule_NotFound(t *testing.T) {
	svc := newTestReplicationService(t)
	err := svc.RunRule(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent rule")
	}
}

func TestReplicationService_ListHistory_Limit(t *testing.T) {
	replRepo := testutil.NewReplicationRepo()
	ctx := context.Background()

	rule := &domain.ReplicationRule{
		Name: "hist-rule", SourceRepo: "r", TargetURL: "http://localhost",
		TargetRepo: "r", CronExpr: "0 2 * * *", Enabled: true,
	}
	_ = replRepo.CreateRule(ctx, rule)

	now := time.Now()
	fin := now.Add(time.Second)
	for i := 0; i < 5; i++ {
		_ = replRepo.AddHistory(ctx, &domain.ReplicationHistory{
			RuleID: rule.ID, StartedAt: now, FinishedAt: &fin,
		})
	}
	hist, err := replRepo.ListHistory(ctx, rule.ID, 3)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(hist) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(hist))
	}
}

func TestReplicationService_EncryptDecrypt_DedicatedKey(t *testing.T) {
	svc := newKeyedReplicationService(t, testutil.NewReplicationRepo())
	enc, err := svc.EncryptPassword("p@ss")
	if err != nil {
		t.Fatalf("EncryptPassword: %v", err)
	}
	got, err := svc.DecryptPassword(enc)
	if err != nil || got != "p@ss" {
		t.Fatalf("DecryptPassword: got %q err %v", got, err)
	}
}

func TestReplicationService_Decrypt_LegacyFallback(t *testing.T) {
	// Sealed under the legacy jwt-derived key…
	legacy := newTestReplicationService(t)
	enc, err := legacy.EncryptPassword("p@ss")
	if err != nil {
		t.Fatalf("legacy EncryptPassword: %v", err)
	}
	// …opens on a service configured with a dedicated key + same jwt secret.
	keyed := newKeyedReplicationService(t, testutil.NewReplicationRepo())
	got, err := keyed.DecryptPassword(enc)
	if err != nil || got != "p@ss" {
		t.Fatalf("fallback decrypt: got %q err %v", got, err)
	}
}

func TestReplicationService_Decrypt_NoFallbackWithoutKey(t *testing.T) {
	keyed := newKeyedReplicationService(t, testutil.NewReplicationRepo())
	enc, err := keyed.EncryptPassword("p@ss")
	if err != nil {
		t.Fatalf("EncryptPassword: %v", err)
	}
	// Legacy-only service must NOT decrypt dedicated-key ciphertext.
	legacy := newTestReplicationService(t)
	if _, err := legacy.DecryptPassword(enc); err == nil {
		t.Fatal("legacy service decrypted dedicated-key ciphertext")
	}
}

func TestReplicationService_ReEncryptCredentials_MigratesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := testutil.NewReplicationRepo()

	legacy := newTestReplicationService(t)
	enc, err := legacy.EncryptPassword("p@ss")
	if err != nil {
		t.Fatalf("EncryptPassword: %v", err)
	}
	rule := &domain.ReplicationRule{Name: "r1", SourceRepo: "raw-hosted",
		TargetURL: "http://example", TargetRepo: "raw", TargetUsername: "u",
		TargetPasswordEnc: enc}
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	keyed := newKeyedReplicationService(t, repo)
	if migrated := keyed.ReEncryptCredentials(ctx); migrated != 1 {
		t.Fatalf("first sweep migrated: got %d want 1", migrated)
	}
	updated, err := repo.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if updated.TargetPasswordEnc == enc {
		t.Fatal("ciphertext was not re-encrypted")
	}
	if got, err := keyed.DecryptPassword(updated.TargetPasswordEnc); err != nil || got != "p@ss" {
		t.Fatalf("decrypt after sweep: got %q err %v", got, err)
	}
	if migrated := keyed.ReEncryptCredentials(ctx); migrated != 0 {
		t.Fatalf("second sweep must be a no-op: got %d", migrated)
	}
}

func TestReplicationService_ReEncryptCredentials_SkipsUndecryptableRows(t *testing.T) {
	ctx := context.Background()
	repo := testutil.NewReplicationRepo()

	// Sealed under a jwt secret no key in this test can derive.
	foreign := service.NewReplicationService(testutil.NewReplicationRepo(), testutil.NewAssetRepo(),
		testutil.NewBlobStore(), "a-completely-different-jwt-secret!!", nil, nopReplLog())
	badEnc, err := foreign.EncryptPassword("other")
	if err != nil {
		t.Fatalf("foreign EncryptPassword: %v", err)
	}
	bad := &domain.ReplicationRule{Name: "bad", SourceRepo: "raw-hosted",
		TargetURL: "http://example", TargetRepo: "raw", TargetUsername: "u",
		TargetPasswordEnc: badEnc}
	if err := repo.CreateRule(ctx, bad); err != nil {
		t.Fatalf("CreateRule(bad): %v", err)
	}

	// Sealed under the legacy key the keyed service falls back to.
	legacy := newTestReplicationService(t)
	goodEnc, err := legacy.EncryptPassword("p@ss")
	if err != nil {
		t.Fatalf("legacy EncryptPassword: %v", err)
	}
	good := &domain.ReplicationRule{Name: "good", SourceRepo: "raw-hosted",
		TargetURL: "http://example", TargetRepo: "raw", TargetUsername: "u",
		TargetPasswordEnc: goodEnc}
	if err := repo.CreateRule(ctx, good); err != nil {
		t.Fatalf("CreateRule(good): %v", err)
	}

	keyed := newKeyedReplicationService(t, repo)
	if migrated := keyed.ReEncryptCredentials(ctx); migrated != 1 {
		t.Fatalf("sweep migrated: got %d want 1", migrated)
	}

	badAfter, err := repo.GetRule(ctx, bad.ID)
	if err != nil {
		t.Fatalf("GetRule(bad): %v", err)
	}
	if badAfter.TargetPasswordEnc != badEnc {
		t.Fatal("undecryptable ciphertext must be left untouched by the sweep")
	}

	goodAfter, err := repo.GetRule(ctx, good.ID)
	if err != nil {
		t.Fatalf("GetRule(good): %v", err)
	}
	if goodAfter.TargetPasswordEnc == goodEnc {
		t.Fatal("legacy ciphertext was not re-encrypted")
	}
	if got, err := keyed.DecryptPassword(goodAfter.TargetPasswordEnc); err != nil || got != "p@ss" {
		t.Fatalf("decrypt after sweep: got %q err %v", got, err)
	}
}

// goldenLegacyCiphertext is EncryptPassword("golden-vector-password") under jwt
// secret "test-jwt-secret-32-bytes-long!!!" with no dedicated key — i.e. it is
// sealed by the pre-change implementation layout:
// base64url(nonce || AES-256-GCM(sha256(jwtSecret))).
// Generated with the v1.16 layout (sealWithKey is byte-identical to the
// pre-change code). It pins the on-disk format: any layout drift in both
// seal+open (e.g. URLEncoding → StdEncoding) fails this test even though
// round-trip tests would still pass.
const goldenLegacyCiphertext = "uKUigYphWUHkp1uYGfBb1FLdRKY_cKbJsTqobrY_ckrTQ8F6iBsXe2mzSDN_2kWkmQ4="

func TestReplicationService_Decrypt_GoldenVector(t *testing.T) {
	const want = "golden-vector-password"

	legacy := newTestReplicationService(t)
	if got, err := legacy.DecryptPassword(goldenLegacyCiphertext); err != nil || got != want {
		t.Fatalf("legacy decrypt: got %q err %v", got, err)
	}

	// A keyed service with the same jwt secret must open it via the legacy fallback.
	keyed := newKeyedReplicationService(t, testutil.NewReplicationRepo())
	if got, err := keyed.DecryptPassword(goldenLegacyCiphertext); err != nil || got != want {
		t.Fatalf("keyed fallback decrypt: got %q err %v", got, err)
	}
}

var _ io.Closer = io.NopCloser(nil)
