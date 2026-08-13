package domain

import (
	"encoding/json"
	"testing"
	"time"
)

// The replication handlers serialize these structs straight to the wire, and the
// Replication tab reads snake_case keys off them, so the JSON shape is part of
// the API contract.

func TestReplicationRuleMarshalsSnakeCase(t *testing.T) {
	finished := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	rule := ReplicationRule{
		ID:                "rule-1",
		Name:              "nightly",
		SourceRepo:        "maven-hosted",
		TargetURL:         "https://remote.example.com",
		TargetRepo:        "maven-mirror",
		TargetUsername:    "svc-replication",
		TargetPasswordEnc: "SECRET",
		CronExpr:          "0 2 * * *",
		Enabled:           true,
		LastRunAt:         &finished,
		LastRunStatus:     "ok",
		CreatedAt:         finished,
	}

	var got map[string]any
	raw, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := map[string]any{
		"id":              "rule-1",
		"name":            "nightly",
		"source_repo":     "maven-hosted",
		"target_url":      "https://remote.example.com",
		"target_repo":     "maven-mirror",
		"target_username": "svc-replication",
		"cron_expr":       "0 2 * * *",
		"enabled":         true,
		"last_run_status": "ok",
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Errorf("key %q = %#v, want %#v", key, got[key], expected)
		}
	}
	for _, key := range []string{"last_run_at", "created_at"} {
		if _, ok := got[key]; !ok {
			t.Errorf("key %q missing from payload", key)
		}
	}
}

func TestReplicationRuleNeverExposesEncryptedPassword(t *testing.T) {
	raw, err := json.Marshal(ReplicationRule{TargetPasswordEnc: "SECRET"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for key := range got {
		if key == "target_password_enc" || key == "TargetPasswordEnc" {
			t.Fatalf("encrypted password serialized under %q: %s", key, raw)
		}
	}
}

func TestReplicationHistoryMarshalsSnakeCase(t *testing.T) {
	started := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	entry := ReplicationHistory{
		ID:               "run-1",
		RuleID:           "rule-1",
		StartedAt:        started,
		DurationMs:       1500,
		PushedCount:      12,
		SkippedCount:     3,
		FailedCount:      1,
		TransferredBytes: 4096,
		Error:            "one asset failed",
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := map[string]any{
		"id":                "run-1",
		"rule_id":           "rule-1",
		"duration_ms":       float64(1500),
		"pushed_count":      float64(12),
		"skipped_count":     float64(3),
		"failed_count":      float64(1),
		"transferred_bytes": float64(4096),
		"error":             "one asset failed",
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Errorf("key %q = %#v, want %#v", key, got[key], expected)
		}
	}
	for _, key := range []string{"started_at", "finished_at"} {
		if _, ok := got[key]; !ok {
			t.Errorf("key %q missing from payload", key)
		}
	}
}
