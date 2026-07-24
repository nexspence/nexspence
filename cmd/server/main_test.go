package main

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func testBootstrapCfg() config.BootstrapConfig {
	return config.BootstrapConfig{
		AdminUsername:  "admin",
		AdminPassword:  "admin123",
		AdminEmail:     "admin@example.com",
		AdminFirstName: "Admin",
	}
}

func testAuthSvc() *auth.Service {
	// bcrypt cost 4 (MinCost) keeps these tests fast.
	return auth.NewService("test-secret-test-secret-test-1234", 8, 4)
}

func testS3Config() config.S3Config {
	return config.S3Config{
		Bucket:          "artifacts",
		Region:          "us-east-1",
		Endpoint:        "http://minio:9000",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "miniosecret",
		ForcePathStyle:  true,
	}
}

// assertS3Store fails unless bs is an s3 store whose config uses the registry's
// expected key names (access_key/secret_key, NOT access_key_id) matching s3.
func assertS3Store(t *testing.T, bs *domain.BlobStore, s3 config.S3Config) {
	t.Helper()
	if bs == nil {
		t.Fatal("blob store is nil")
	}
	if bs.Type != "s3" {
		t.Fatalf("type = %q, want s3", bs.Type)
	}
	want := map[string]any{
		"bucket":           s3.Bucket,
		"region":           s3.Region,
		"endpoint":         s3.Endpoint,
		"access_key":       s3.AccessKeyID,
		"secret_key":       s3.SecretAccessKey,
		"force_path_style": s3.ForcePathStyle,
	}
	for k, v := range want {
		if bs.Config[k] != v {
			t.Fatalf("config[%q] = %v, want %v", k, bs.Config[k], v)
		}
	}
	if _, ok := bs.Config["access_key_id"]; ok {
		t.Fatal("config must use registry key access_key, not access_key_id")
	}
}

// Regression #81: with storage.default_type=s3 the seed migration still leaves
// the "default"/"docker" blob stores as local, so repositories with no explicit
// blobStoreId write to local disk. Reconcile must convert the seed stores to s3.
func TestReconcileS3BlobStores_ConvertsSeedLocalToS3(t *testing.T) {
	s3 := testS3Config()
	blobs := testutil.NewBlobStoreRepo(
		&domain.BlobStore{ID: "bs-default", Name: "default", Type: "local", Config: map[string]any{"path": "./data/blobs/default"}},
		&domain.BlobStore{ID: "bs-docker", Name: "docker", Type: "local", Config: map[string]any{"path": "./data/blobs/docker"}},
	)
	log := logger.New("error", "json")

	if err := reconcileS3BlobStores(context.Background(), blobs, s3, nil, log); err != nil {
		t.Fatalf("reconcileS3BlobStores: %v", err)
	}

	def, _ := blobs.Get(context.Background(), "default")
	assertS3Store(t, def, s3)
	dock, _ := blobs.Get(context.Background(), "docker")
	assertS3Store(t, dock, s3)
}

// Reconcile creates a seed store that is missing entirely.
func TestReconcileS3BlobStores_CreatesMissing(t *testing.T) {
	s3 := testS3Config()
	blobs := testutil.NewBlobStoreRepo(
		&domain.BlobStore{ID: "bs-default", Name: "default", Type: "local", Config: map[string]any{"path": "./data/blobs/default"}},
	)
	log := logger.New("error", "json")

	if err := reconcileS3BlobStores(context.Background(), blobs, s3, nil, log); err != nil {
		t.Fatalf("reconcileS3BlobStores: %v", err)
	}

	dock, err := blobs.Get(context.Background(), "docker")
	if err != nil {
		t.Fatalf("docker store not created: %v", err)
	}
	assertS3Store(t, dock, s3)
}

// Reconcile is idempotent: a second run leaves the already-reconciled stores
// unchanged and returns no error.
func TestReconcileS3BlobStores_Idempotent(t *testing.T) {
	s3 := testS3Config()
	blobs := testutil.NewBlobStoreRepo(
		&domain.BlobStore{ID: "bs-default", Name: "default", Type: "local", Config: map[string]any{"path": "./data/blobs/default"}},
	)
	log := logger.New("error", "json")

	for i := 0; i < 2; i++ {
		if err := reconcileS3BlobStores(context.Background(), blobs, s3, nil, log); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	def, _ := blobs.Get(context.Background(), "default")
	assertS3Store(t, def, s3)
}

// Regression: a fresh deploy seeds the admin row with a placeholder hash (NOT
// admin123). Bootstrap must apply the configured password so admin/admin123 can
// log in. Previously bootstrap saw "admin already exists" and left the broken
// placeholder in place.
func TestEnsureBootstrapAdmin_SeedPlaceholder_PasswordApplied(t *testing.T) {
	authSvc := testAuthSvc()
	users := testutil.NewUserRepo(&domain.User{
		ID:           "u-admin",
		Username:     "admin",
		PasswordHash: seedPlaceholderAdminHash,
		Status:       domain.UserStatusActive,
		Source:       domain.UserSourceLocal,
	})
	log := logger.New("error", "json")

	if err := ensureBootstrapAdmin(context.Background(), users, testutil.NewRoleRepo(), authSvc, testBootstrapCfg(), log); err != nil {
		t.Fatalf("ensureBootstrapAdmin: %v", err)
	}

	got, _ := users.Get(context.Background(), "admin")
	if got.PasswordHash == seedPlaceholderAdminHash {
		t.Fatal("password hash still the seed placeholder; configured password was not applied")
	}
	if err := authSvc.CheckPassword(got.PasswordHash, "admin123"); err != nil {
		t.Fatalf("admin123 does not verify against the stored hash: %v", err)
	}
}

// A genuinely changed admin password must never be clobbered by bootstrap
// (preserves the #36 hardening intent: rotate via API, not config+restart).
func TestEnsureBootstrapAdmin_ChangedPassword_NotModified(t *testing.T) {
	authSvc := testAuthSvc()
	custom, err := authSvc.HashPassword("operator-secret")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := testutil.NewUserRepo(&domain.User{
		ID:           "u-admin",
		Username:     "admin",
		PasswordHash: custom,
		Status:       domain.UserStatusActive,
	})
	log := logger.New("error", "json")

	if err := ensureBootstrapAdmin(context.Background(), users, testutil.NewRoleRepo(), authSvc, testBootstrapCfg(), log); err != nil {
		t.Fatalf("ensureBootstrapAdmin: %v", err)
	}

	got, _ := users.Get(context.Background(), "admin")
	if got.PasswordHash != custom {
		t.Fatal("changed admin password was overwritten by bootstrap")
	}
	if err := authSvc.CheckPassword(got.PasswordHash, "admin123"); err == nil {
		t.Fatal("admin123 must NOT verify against a changed password")
	}
}

// No admin row at all: bootstrap creates one with the configured password and
// the nx-admin role. (mock Get returns ErrNotFound, which must be treated as
// "not found", not a hard error.)
func TestEnsureBootstrapAdmin_Missing_Created(t *testing.T) {
	authSvc := testAuthSvc()
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo(&domain.Role{ID: "r-admin", Name: "nx-admin"})
	log := logger.New("error", "json")

	if err := ensureBootstrapAdmin(context.Background(), users, roles, authSvc, testBootstrapCfg(), log); err != nil {
		t.Fatalf("ensureBootstrapAdmin: %v", err)
	}

	got, err := users.Get(context.Background(), "admin")
	if err != nil || got == nil {
		t.Fatalf("admin not created: err=%v", err)
	}
	if err := authSvc.CheckPassword(got.PasswordHash, "admin123"); err != nil {
		t.Fatalf("created admin password does not verify: %v", err)
	}
}
