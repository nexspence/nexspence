//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// makeReplRule builds a fully-populated ReplicationRule with a unique name.
func makeReplRule(name string) *domain.ReplicationRule {
	return &domain.ReplicationRule{
		Name:              name,
		SourceRepo:        "src-" + name,
		TargetURL:         "https://remote.example.com",
		TargetRepo:        "tgt-" + name,
		TargetUsername:    "svc-" + name,
		TargetPasswordEnc: "ENC(" + name + ")", // stand-in for AES-256-GCM ciphertext
		CronExpr:          "0 3 * * *",
		Enabled:           true,
	}
}

// makeScanComponent creates blob_store → repository → component rows and
// returns the component ID plus the repo name/format, for scan_results FK use.
func makeScanComponent(t *testing.T, ctx context.Context, suffix string) (compID, repoName, format string) {
	t.Helper()
	pool := pgtest.Pool(t)

	p := makeCompParent(t, ctx, suffix)

	compRepo := NewComponentRepo(pool)
	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Group:        "com.example",
		Name:         "scan-comp-" + suffix,
		Version:      "1.0.0",
	}
	if err := compRepo.Create(ctx, c); err != nil {
		t.Fatalf("makeScanComponent: create component: %v", err)
	}
	return c.ID, p.RepoName, "raw"
}

// ── ReplicationRepo — rule CRUD ───────────────────────────────────────────────

func TestReplicationRepo_CreateRule_PopulatesIDAndTimestamp(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	r := makeReplRule("repl_create")
	if err := repo.CreateRule(ctx, r); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if r.ID == "" {
		t.Error("ID: got empty, want server-assigned UUID")
	}
	if r.CreatedAt.IsZero() {
		t.Error("CreatedAt: got zero, want server-assigned")
	}
}

func TestReplicationRepo_GetRule_RoundTripsAllFields(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	r := makeReplRule("repl_roundtrip")
	r.Enabled = false
	if err := repo.CreateRule(ctx, r); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	got, err := repo.GetRule(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got == nil {
		t.Fatal("GetRule: got nil, want rule")
	}
	if got.Name != r.Name {
		t.Errorf("Name: got %q, want %q", got.Name, r.Name)
	}
	if got.SourceRepo != r.SourceRepo {
		t.Errorf("SourceRepo: got %q, want %q", got.SourceRepo, r.SourceRepo)
	}
	if got.TargetURL != r.TargetURL {
		t.Errorf("TargetURL: got %q, want %q", got.TargetURL, r.TargetURL)
	}
	if got.TargetRepo != r.TargetRepo {
		t.Errorf("TargetRepo: got %q, want %q", got.TargetRepo, r.TargetRepo)
	}
	if got.TargetUsername != r.TargetUsername {
		t.Errorf("TargetUsername: got %q, want %q", got.TargetUsername, r.TargetUsername)
	}
	// Encrypted credential must round-trip verbatim.
	if got.TargetPasswordEnc != r.TargetPasswordEnc {
		t.Errorf("TargetPasswordEnc: got %q, want %q", got.TargetPasswordEnc, r.TargetPasswordEnc)
	}
	if got.CronExpr != r.CronExpr {
		t.Errorf("CronExpr: got %q, want %q", got.CronExpr, r.CronExpr)
	}
	if got.Enabled != false {
		t.Errorf("Enabled: got %v, want false", got.Enabled)
	}
	// last_run_at is nullable and was never set.
	if got.LastRunAt != nil {
		t.Errorf("LastRunAt: got %v, want nil", got.LastRunAt)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt: got zero, want non-zero")
	}
}

func TestReplicationRepo_GetRule_NotFound_ReturnsNilNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	// Random non-existent UUID.
	got, err := repo.GetRule(ctx, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("GetRule(missing): unexpected error %v", err)
	}
	if got != nil {
		t.Errorf("GetRule(missing): got %+v, want nil", got)
	}
}

func TestReplicationRepo_ListRules_OrderedByName(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	// Insert out of alphabetical order.
	for _, name := range []string{"repl_zebra", "repl_alpha", "repl_mike"} {
		if err := repo.CreateRule(ctx, makeReplRule(name)); err != nil {
			t.Fatalf("CreateRule(%s): %v", name, err)
		}
	}

	rules, err := repo.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("ListRules: got %d, want 3", len(rules))
	}
	want := []string{"repl_alpha", "repl_mike", "repl_zebra"}
	for i, n := range want {
		if rules[i].Name != n {
			t.Errorf("rules[%d].Name: got %q, want %q", i, rules[i].Name, n)
		}
	}
}

func TestReplicationRepo_ListRules_EmptyTable(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	rules, err := repo.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules on empty: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("ListRules on empty: got %d, want 0", len(rules))
	}
}

func TestReplicationRepo_UpdateRule_PersistsChanges(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	r := makeReplRule("repl_update")
	if err := repo.CreateRule(ctx, r); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	r.Name = "repl_update_renamed"
	r.SourceRepo = "src-changed"
	r.TargetURL = "https://other.example.com"
	r.TargetRepo = "tgt-changed"
	r.TargetUsername = "newuser"
	r.TargetPasswordEnc = "ENC(rotated)"
	r.CronExpr = "30 1 * * *"
	r.Enabled = false
	if err := repo.UpdateRule(ctx, r); err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}

	got, err := repo.GetRule(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRule after update: %v", err)
	}
	if got.Name != "repl_update_renamed" {
		t.Errorf("Name: got %q, want repl_update_renamed", got.Name)
	}
	if got.SourceRepo != "src-changed" {
		t.Errorf("SourceRepo: got %q, want src-changed", got.SourceRepo)
	}
	if got.TargetURL != "https://other.example.com" {
		t.Errorf("TargetURL: got %q, want https://other.example.com", got.TargetURL)
	}
	if got.TargetRepo != "tgt-changed" {
		t.Errorf("TargetRepo: got %q, want tgt-changed", got.TargetRepo)
	}
	if got.TargetUsername != "newuser" {
		t.Errorf("TargetUsername: got %q, want newuser", got.TargetUsername)
	}
	if got.TargetPasswordEnc != "ENC(rotated)" {
		t.Errorf("TargetPasswordEnc: got %q, want ENC(rotated)", got.TargetPasswordEnc)
	}
	if got.CronExpr != "30 1 * * *" {
		t.Errorf("CronExpr: got %q, want 30 1 * * *", got.CronExpr)
	}
	if got.Enabled != false {
		t.Errorf("Enabled: got %v, want false", got.Enabled)
	}
}

func TestReplicationRepo_UpdateRuleStatus_SetsStatusAndTime(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	r := makeReplRule("repl_status")
	if err := repo.CreateRule(ctx, r); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	at := time.Now().Truncate(time.Second)
	if err := repo.UpdateRuleStatus(ctx, r.ID, "ok", at); err != nil {
		t.Fatalf("UpdateRuleStatus: %v", err)
	}

	got, err := repo.GetRule(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got.LastRunStatus != "ok" {
		t.Errorf("LastRunStatus: got %q, want ok", got.LastRunStatus)
	}
	if got.LastRunAt == nil {
		t.Fatal("LastRunAt: got nil, want non-nil")
	}
	if !got.LastRunAt.Equal(at) {
		t.Errorf("LastRunAt: got %v, want %v", got.LastRunAt, at)
	}
}

func TestReplicationRepo_DeleteRule_RemovesRow(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	r := makeReplRule("repl_delete")
	if err := repo.CreateRule(ctx, r); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if err := repo.DeleteRule(ctx, r.ID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}

	got, err := repo.GetRule(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRule after delete: %v", err)
	}
	if got != nil {
		t.Errorf("GetRule after delete: got %+v, want nil", got)
	}
}

func TestReplicationRepo_DeleteRule_NonExistent_NoError(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	// Deleting a missing row is a no-op, not an error.
	if err := repo.DeleteRule(ctx, "00000000-0000-0000-0000-000000000000"); err != nil {
		t.Errorf("DeleteRule(missing): unexpected error %v", err)
	}
}

// ── ReplicationRepo — history ─────────────────────────────────────────────────

func TestReplicationRepo_AddHistory_PopulatesID(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	rule := makeReplRule("repl_hist_add")
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	finished := time.Now()
	h := &domain.ReplicationHistory{
		RuleID:           rule.ID,
		StartedAt:        time.Now().Add(-time.Minute),
		FinishedAt:       &finished,
		DurationMs:       1234,
		PushedCount:      10,
		SkippedCount:     2,
		FailedCount:      1,
		TransferredBytes: 4096,
		Error:            "",
	}
	if err := repo.AddHistory(ctx, h); err != nil {
		t.Fatalf("AddHistory: %v", err)
	}
	if h.ID == "" {
		t.Error("ID: got empty, want server-assigned UUID")
	}
}

func TestReplicationRepo_ListHistory_RoundTripsAndOrders(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	rule := makeReplRule("repl_hist_list")
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	base := time.Now().Truncate(time.Second)
	// Insert 3 runs at increasing started_at — newest must come first.
	for i := 0; i < 3; i++ {
		finished := base.Add(time.Duration(i)*time.Second + 500*time.Millisecond)
		h := &domain.ReplicationHistory{
			RuleID:           rule.ID,
			StartedAt:        base.Add(time.Duration(i) * time.Second),
			FinishedAt:       &finished,
			DurationMs:       int64(100 * (i + 1)),
			PushedCount:      i + 1,
			SkippedCount:     i,
			FailedCount:      0,
			TransferredBytes: int64(1000 * (i + 1)),
		}
		if err := repo.AddHistory(ctx, h); err != nil {
			t.Fatalf("AddHistory[%d]: %v", i, err)
		}
	}

	hist, err := repo.ListHistory(ctx, rule.ID, 10)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(hist) != 3 {
		t.Fatalf("ListHistory: got %d, want 3", len(hist))
	}
	// DESC by started_at — first entry is the latest (i==2).
	for i := 1; i < len(hist); i++ {
		if hist[i].StartedAt.After(hist[i-1].StartedAt) {
			t.Errorf("not DESC order at index %d: %v > %v", i, hist[i].StartedAt, hist[i-1].StartedAt)
		}
	}
	newest := hist[0]
	if newest.PushedCount != 3 {
		t.Errorf("newest PushedCount: got %d, want 3", newest.PushedCount)
	}
	if newest.SkippedCount != 2 {
		t.Errorf("newest SkippedCount: got %d, want 2", newest.SkippedCount)
	}
	if newest.DurationMs != 300 {
		t.Errorf("newest DurationMs: got %d, want 300", newest.DurationMs)
	}
	if newest.TransferredBytes != 3000 {
		t.Errorf("newest TransferredBytes: got %d, want 3000", newest.TransferredBytes)
	}
	if newest.FinishedAt == nil {
		t.Error("newest FinishedAt: got nil, want non-nil")
	}
	if newest.RuleID != rule.ID {
		t.Errorf("newest RuleID: got %q, want %q", newest.RuleID, rule.ID)
	}
}

func TestReplicationRepo_ListHistory_RespectsLimit(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	rule := makeReplRule("repl_hist_limit")
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	base := time.Now().Truncate(time.Second)
	for i := 0; i < 5; i++ {
		h := &domain.ReplicationHistory{
			RuleID:    rule.ID,
			StartedAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := repo.AddHistory(ctx, h); err != nil {
			t.Fatalf("AddHistory[%d]: %v", i, err)
		}
	}

	hist, err := repo.ListHistory(ctx, rule.ID, 2)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(hist) != 2 {
		t.Errorf("ListHistory(limit=2): got %d, want 2", len(hist))
	}
}

func TestReplicationRepo_ListHistory_FiltersByRule(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	ruleA := makeReplRule("repl_hist_ruleA")
	ruleB := makeReplRule("repl_hist_ruleB")
	if err := repo.CreateRule(ctx, ruleA); err != nil {
		t.Fatalf("CreateRule A: %v", err)
	}
	if err := repo.CreateRule(ctx, ruleB); err != nil {
		t.Fatalf("CreateRule B: %v", err)
	}

	if err := repo.AddHistory(ctx, &domain.ReplicationHistory{RuleID: ruleA.ID, StartedAt: time.Now()}); err != nil {
		t.Fatalf("AddHistory A: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := repo.AddHistory(ctx, &domain.ReplicationHistory{RuleID: ruleB.ID, StartedAt: time.Now()}); err != nil {
			t.Fatalf("AddHistory B[%d]: %v", i, err)
		}
	}

	histA, err := repo.ListHistory(ctx, ruleA.ID, 10)
	if err != nil {
		t.Fatalf("ListHistory A: %v", err)
	}
	if len(histA) != 1 {
		t.Errorf("ListHistory A: got %d, want 1", len(histA))
	}
	histB, err := repo.ListHistory(ctx, ruleB.ID, 10)
	if err != nil {
		t.Fatalf("ListHistory B: %v", err)
	}
	if len(histB) != 2 {
		t.Errorf("ListHistory B: got %d, want 2", len(histB))
	}
}

func TestReplicationRepo_ListHistory_EmptyForUnknownRule(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	hist, err := repo.ListHistory(ctx, "00000000-0000-0000-0000-000000000000", 10)
	if err != nil {
		t.Fatalf("ListHistory(unknown): %v", err)
	}
	if len(hist) != 0 {
		t.Errorf("ListHistory(unknown): got %d, want 0", len(hist))
	}
}

func TestReplicationRepo_DeleteRule_CascadesHistory(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "replication_history", "replication_rules")
	ctx := context.Background()
	repo := NewReplicationRepo(pool)

	rule := makeReplRule("repl_cascade")
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if err := repo.AddHistory(ctx, &domain.ReplicationHistory{RuleID: rule.ID, StartedAt: time.Now()}); err != nil {
		t.Fatalf("AddHistory: %v", err)
	}

	if err := repo.DeleteRule(ctx, rule.ID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}

	// FK is ON DELETE CASCADE — history rows for the rule must be gone.
	hist, err := repo.ListHistory(ctx, rule.ID, 10)
	if err != nil {
		t.Fatalf("ListHistory after cascade: %v", err)
	}
	if len(hist) != 0 {
		t.Errorf("history after rule delete: got %d, want 0", len(hist))
	}
}

// ── ScanResultRepo — Insert + GetLatestByComponent ────────────────────────────

func TestScanResultRepo_Insert_And_GetLatest(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	compID, _, _ := makeScanComponent(t, ctx, "scan_insert")

	scannedAt := time.Now().Truncate(time.Second)
	row := &domain.ScanResultRow{
		ComponentID: compID,
		Scanner:     "trivy",
		Status:      domain.ScanStatusOK,
		Critical:    1,
		High:        2,
		Medium:      3,
		Low:         4,
		Unknown:     5,
		Total:       15,
		ScannedAt:   scannedAt,
		Raw:         map[string]any{"vulns": "list", "count": float64(15)},
		Error:       "",
	}
	if err := repo.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.GetLatestByComponent(ctx, compID)
	if err != nil {
		t.Fatalf("GetLatestByComponent: %v", err)
	}
	if got == nil {
		t.Fatal("GetLatestByComponent: got nil, want row")
	}
	if got.ID == "" {
		t.Error("ID: got empty, want server-assigned")
	}
	if got.ComponentID != compID {
		t.Errorf("ComponentID: got %q, want %q", got.ComponentID, compID)
	}
	if got.Scanner != "trivy" {
		t.Errorf("Scanner: got %q, want trivy", got.Scanner)
	}
	if got.Status != domain.ScanStatusOK {
		t.Errorf("Status: got %q, want %q", got.Status, domain.ScanStatusOK)
	}
	if got.Critical != 1 || got.High != 2 || got.Medium != 3 || got.Low != 4 || got.Unknown != 5 {
		t.Errorf("severity counts: got C%d H%d M%d L%d U%d, want 1/2/3/4/5",
			got.Critical, got.High, got.Medium, got.Low, got.Unknown)
	}
	if got.Total != 15 {
		t.Errorf("Total: got %d, want 15", got.Total)
	}
	if got.Raw["vulns"] != "list" {
		t.Errorf("Raw[vulns]: got %v, want list", got.Raw["vulns"])
	}
	if got.Raw["count"] != float64(15) {
		t.Errorf("Raw[count]: got %v, want 15", got.Raw["count"])
	}
}

func TestScanResultRepo_GetLatest_ReturnsNewestScan(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	compID, _, _ := makeScanComponent(t, ctx, "scan_newest")

	base := time.Now().Truncate(time.Second)
	// Older scan.
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: compID, Scanner: "trivy", Status: domain.ScanStatusOK,
		Critical: 9, Total: 9, ScannedAt: base.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Insert old: %v", err)
	}
	// Newer scan with different counts.
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: compID, Scanner: "osv", Status: domain.ScanStatusOK,
		Critical: 1, Total: 1, ScannedAt: base,
	}); err != nil {
		t.Fatalf("Insert new: %v", err)
	}

	got, err := repo.GetLatestByComponent(ctx, compID)
	if err != nil {
		t.Fatalf("GetLatestByComponent: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want newest row")
	}
	// Newest is the osv scan with Critical=1.
	if got.Scanner != "osv" || got.Critical != 1 {
		t.Errorf("latest: got Scanner=%q Critical=%d, want osv/1", got.Scanner, got.Critical)
	}
}

func TestScanResultRepo_GetLatest_NotFound_ReturnsNilNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	got, err := repo.GetLatestByComponent(ctx, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("GetLatestByComponent(missing): unexpected error %v", err)
	}
	if got != nil {
		t.Errorf("GetLatestByComponent(missing): got %+v, want nil", got)
	}
}

func TestScanResultRepo_Insert_NullableRawAndError(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	compID, _, _ := makeScanComponent(t, ctx, "scan_nullraw")

	// nil Raw map and a non-empty Error.
	row := &domain.ScanResultRow{
		ComponentID: compID,
		Scanner:     "trivy",
		Status:      domain.ScanStatusFailed,
		ScannedAt:   time.Now(),
		Raw:         nil,
		Error:       "scanner timed out",
	}
	if err := repo.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.GetLatestByComponent(ctx, compID)
	if err != nil {
		t.Fatalf("GetLatestByComponent: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want row")
	}
	if got.Status != domain.ScanStatusFailed {
		t.Errorf("Status: got %q, want %q", got.Status, domain.ScanStatusFailed)
	}
	if got.Error != "scanner timed out" {
		t.Errorf("Error: got %q, want 'scanner timed out'", got.Error)
	}
}

// ── ScanResultRepo — Aggregate ────────────────────────────────────────────────

func TestScanResultRepo_Aggregate_SumsLatestPerComponent(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	comp1, _, _ := makeScanComponent(t, ctx, "scan_agg1")
	comp2, _, _ := makeScanComponent(t, ctx, "scan_agg2")

	base := time.Now().Truncate(time.Second)
	// comp1: two scans — only the latest must be counted.
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: comp1, Scanner: "trivy", Status: domain.ScanStatusOK,
		Critical: 100, High: 100, ScannedAt: base.Add(-time.Hour), // stale, ignored
	}); err != nil {
		t.Fatalf("Insert comp1 old: %v", err)
	}
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: comp1, Scanner: "trivy", Status: domain.ScanStatusOK,
		Critical: 1, High: 2, Medium: 3, Low: 4, Unknown: 5, ScannedAt: base,
	}); err != nil {
		t.Fatalf("Insert comp1 new: %v", err)
	}
	// comp2: single scan.
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: comp2, Scanner: "osv", Status: domain.ScanStatusOK,
		Critical: 10, High: 20, Medium: 0, Low: 0, Unknown: 0, ScannedAt: base,
	}); err != nil {
		t.Fatalf("Insert comp2: %v", err)
	}

	sum, err := repo.Aggregate(ctx)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	// Latest comp1 (1/2/3/4/5) + comp2 (10/20/0/0/0).
	if sum.Critical != 11 {
		t.Errorf("Critical: got %d, want 11", sum.Critical)
	}
	if sum.High != 22 {
		t.Errorf("High: got %d, want 22", sum.High)
	}
	if sum.Medium != 3 {
		t.Errorf("Medium: got %d, want 3", sum.Medium)
	}
	if sum.Low != 4 {
		t.Errorf("Low: got %d, want 4", sum.Low)
	}
	if sum.Unknown != 5 {
		t.Errorf("Unknown: got %d, want 5", sum.Unknown)
	}
	// Distinct components with at least one scan.
	if sum.ScannedTotal != 2 {
		t.Errorf("ScannedTotal: got %d, want 2", sum.ScannedTotal)
	}
}

func TestScanResultRepo_Aggregate_EmptyTable_ReturnsZeros(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	sum, err := repo.Aggregate(ctx)
	if err != nil {
		t.Fatalf("Aggregate on empty: %v", err)
	}
	if sum.Critical != 0 || sum.High != 0 || sum.Medium != 0 || sum.Low != 0 || sum.Unknown != 0 {
		t.Errorf("severity sums: got %+v, want all 0", sum)
	}
	if sum.ScannedTotal != 0 {
		t.Errorf("ScannedTotal: got %d, want 0", sum.ScannedTotal)
	}
}

// ── ScanResultRepo — List (vuln dashboard) ────────────────────────────────────

func TestScanResultRepo_List_ReturnsRowsWithJoinedRepo(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	compID, repoName, format := makeScanComponent(t, ctx, "scan_list")
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: compID, Scanner: "trivy", Status: domain.ScanStatusOK,
		Critical: 1, High: 1, ScannedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rows, total, err := repo.List(ctx, domain.VulnFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Errorf("total: got %d, want 1", total)
	}
	if len(rows) != 1 {
		t.Fatalf("rows: got %d, want 1", len(rows))
	}
	vr := rows[0]
	if vr.RepoName != repoName {
		t.Errorf("RepoName: got %q, want %q", vr.RepoName, repoName)
	}
	if vr.Format != format {
		t.Errorf("Format: got %q, want %q", vr.Format, format)
	}
	if vr.ComponentID != compID {
		t.Errorf("ComponentID: got %q, want %q", vr.ComponentID, compID)
	}
	if vr.Critical != 1 || vr.High != 1 {
		t.Errorf("counts: got C%d H%d, want 1/1", vr.Critical, vr.High)
	}
}

func TestScanResultRepo_List_EmptyTable(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	rows, total, err := repo.List(ctx, domain.VulnFilter{})
	if err != nil {
		t.Fatalf("List on empty: %v", err)
	}
	if total != 0 {
		t.Errorf("total: got %d, want 0", total)
	}
	if len(rows) != 0 {
		t.Errorf("rows: got %d, want 0", len(rows))
	}
}

func TestScanResultRepo_List_SeverityFilter(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	// One component with only LOW vulns, one with a CRITICAL.
	lowComp, _, _ := makeScanComponent(t, ctx, "scan_sev_low")
	critComp, _, _ := makeScanComponent(t, ctx, "scan_sev_crit")

	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: lowComp, Scanner: "trivy", Status: domain.ScanStatusOK,
		Low: 5, ScannedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Insert low: %v", err)
	}
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: critComp, Scanner: "trivy", Status: domain.ScanStatusOK,
		Critical: 1, ScannedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Insert crit: %v", err)
	}

	// Severity=CRITICAL → only the component with critical>0.
	rows, total, err := repo.List(ctx, domain.VulnFilter{Severity: "CRITICAL"})
	if err != nil {
		t.Fatalf("List(CRITICAL): %v", err)
	}
	if total != 1 {
		t.Errorf("CRITICAL total: got %d, want 1", total)
	}
	if len(rows) != 1 || rows[0].ComponentID != critComp {
		t.Errorf("CRITICAL filter returned wrong rows: %+v", rows)
	}

	// Severity=LOW → both components (critical>0 OR high>0 OR medium>0 OR low>0).
	rowsLow, totalLow, err := repo.List(ctx, domain.VulnFilter{Severity: "LOW"})
	if err != nil {
		t.Fatalf("List(LOW): %v", err)
	}
	if totalLow != 2 {
		t.Errorf("LOW total: got %d, want 2", totalLow)
	}
	if len(rowsLow) != 2 {
		t.Errorf("LOW rows: got %d, want 2", len(rowsLow))
	}
}

func TestScanResultRepo_List_RepoAndFormatFilter(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	compA, repoA, _ := makeScanComponent(t, ctx, "scan_repoA")
	compB, _, _ := makeScanComponent(t, ctx, "scan_repoB")

	for _, c := range []string{compA, compB} {
		if err := repo.Insert(ctx, &domain.ScanResultRow{
			ComponentID: c, Scanner: "trivy", Status: domain.ScanStatusOK,
			Critical: 1, ScannedAt: time.Now(),
		}); err != nil {
			t.Fatalf("Insert %s: %v", c, err)
		}
	}

	// Filter by repoA name → only its component.
	rows, total, err := repo.List(ctx, domain.VulnFilter{Repo: repoA})
	if err != nil {
		t.Fatalf("List(repo=%s): %v", repoA, err)
	}
	if total != 1 {
		t.Errorf("repo filter total: got %d, want 1", total)
	}
	if len(rows) != 1 || rows[0].ComponentID != compA {
		t.Errorf("repo filter returned wrong rows: %+v", rows)
	}

	// Filter by format=raw → both components (both raw).
	rowsFmt, totalFmt, err := repo.List(ctx, domain.VulnFilter{Format: "raw"})
	if err != nil {
		t.Fatalf("List(format=raw): %v", err)
	}
	if totalFmt != 2 {
		t.Errorf("format filter total: got %d, want 2", totalFmt)
	}
	if len(rowsFmt) != 2 {
		t.Errorf("format filter rows: got %d, want 2", len(rowsFmt))
	}

	// Filter by a format with no rows → empty.
	rowsNone, totalNone, err := repo.List(ctx, domain.VulnFilter{Format: "docker"})
	if err != nil {
		t.Fatalf("List(format=docker): %v", err)
	}
	if totalNone != 0 || len(rowsNone) != 0 {
		t.Errorf("format=docker: got total=%d rows=%d, want 0/0", totalNone, len(rowsNone))
	}
}

func TestScanResultRepo_List_PaginationAndTotal(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	// 3 components, each with one scan.
	for i := 0; i < 3; i++ {
		c, _, _ := makeScanComponent(t, ctx, "scan_page"+string(rune('a'+i)))
		if err := repo.Insert(ctx, &domain.ScanResultRow{
			ComponentID: c, Scanner: "trivy", Status: domain.ScanStatusOK,
			Critical: i + 1, ScannedAt: time.Now(),
		}); err != nil {
			t.Fatalf("Insert[%d]: %v", i, err)
		}
	}

	// Limit=2, Offset=0 → 2 rows but total reflects all 3.
	rows, total, err := repo.List(ctx, domain.VulnFilter{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("List(limit=2): %v", err)
	}
	if total != 3 {
		t.Errorf("total: got %d, want 3 (full count, not page size)", total)
	}
	if len(rows) != 2 {
		t.Errorf("rows: got %d, want 2", len(rows))
	}

	// Offset=2 → 1 remaining row.
	rows2, total2, err := repo.List(ctx, domain.VulnFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("List(offset=2): %v", err)
	}
	if total2 != 3 {
		t.Errorf("total (offset page): got %d, want 3", total2)
	}
	if len(rows2) != 1 {
		t.Errorf("rows (offset page): got %d, want 1", len(rows2))
	}
}

func TestScanResultRepo_List_DefaultLimit(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	compID, _, _ := makeScanComponent(t, ctx, "scan_deflimit")
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: compID, Scanner: "trivy", Status: domain.ScanStatusOK,
		Critical: 1, ScannedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Limit<=0 defaults to 50 internally; the single row must still come back.
	rows, total, err := repo.List(ctx, domain.VulnFilter{Limit: 0})
	if err != nil {
		t.Fatalf("List(limit=0): %v", err)
	}
	if total != 1 {
		t.Errorf("total: got %d, want 1", total)
	}
	if len(rows) != 1 {
		t.Errorf("rows: got %d, want 1", len(rows))
	}
}

func TestScanResultRepo_List_DedupLatestPerComponent(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "scan_results", "components", "repositories", "blob_stores")
	ctx := context.Background()
	repo := NewScanResultRepo(pool)

	compID, _, _ := makeScanComponent(t, ctx, "scan_dedup")
	base := time.Now().Truncate(time.Second)
	// Two scans for the same component — List uses DISTINCT ON (component_id).
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: compID, Scanner: "trivy", Status: domain.ScanStatusOK,
		Critical: 9, ScannedAt: base.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Insert old: %v", err)
	}
	if err := repo.Insert(ctx, &domain.ScanResultRow{
		ComponentID: compID, Scanner: "osv", Status: domain.ScanStatusOK,
		Critical: 2, ScannedAt: base,
	}); err != nil {
		t.Fatalf("Insert new: %v", err)
	}

	rows, total, err := repo.List(ctx, domain.VulnFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Errorf("total: got %d, want 1 (deduped per component)", total)
	}
	if len(rows) != 1 {
		t.Fatalf("rows: got %d, want 1", len(rows))
	}
	// Newest scan wins (Critical=2).
	if rows[0].Critical != 2 {
		t.Errorf("Critical: got %d, want 2 (newest scan)", rows[0].Critical)
	}
}
