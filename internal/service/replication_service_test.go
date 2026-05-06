package service_test

import (
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
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func nopReplLog() *zap.SugaredLogger { return zap.NewNop().Sugar() }

func newTestReplicationService(t *testing.T) *service.ReplicationService {
	t.Helper()
	return service.NewReplicationService(
		testutil.NewReplicationRepo(),
		testutil.NewAssetRepo(),
		testutil.NewBlobStore(),
		"test-jwt-secret-32-bytes-long!!!",
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
	svc := service.NewReplicationService(replRepo, assetRepo, blobStore, "test-secret-32-bytes-long!!!", nopReplLog())

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
	svc := service.NewReplicationService(replRepo, assetRepo, blobStore, "another-secret-32-bytes-long!!", nopReplLog())

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
	svc := service.NewReplicationService(replRepo, testutil.NewAssetRepo(), testutil.NewBlobStore(), "secret-key-32-bytes-long-padded!", nopReplLog())
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

var _ io.Closer = io.NopCloser(nil)
