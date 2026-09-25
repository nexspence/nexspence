package domain

import "testing"

func TestRepoWritePolicy(t *testing.T) {
	hosted := func(cfg map[string]any) *Repository {
		return &Repository{Name: "r", Format: FormatRaw, Type: TypeHosted, FormatConfig: cfg}
	}
	cases := []struct {
		name string
		repo *Repository
		want WritePolicy
	}{
		{"nil repo", nil, WritePolicyAllow},
		{"no config", hosted(nil), WritePolicyAllow},
		{"absent key", hosted(map[string]any{"other": 1}), WritePolicyAllow},
		{"allow", hosted(map[string]any{WritePolicyKey: "allow"}), WritePolicyAllow},
		{"allow_once", hosted(map[string]any{WritePolicyKey: "allow_once"}), WritePolicyAllowOnce},
		{"deny", hosted(map[string]any{WritePolicyKey: "deny"}), WritePolicyDeny},
		{"unknown value", hosted(map[string]any{WritePolicyKey: "ALLOW_ONCE"}), WritePolicyAllow},
		{"wrong type", hosted(map[string]any{WritePolicyKey: true}), WritePolicyAllow},
		{"proxy ignores policy", &Repository{Type: TypeProxy, FormatConfig: map[string]any{WritePolicyKey: "deny"}}, WritePolicyAllow},
		{"group ignores policy", &Repository{Type: TypeGroup, FormatConfig: map[string]any{WritePolicyKey: "deny"}}, WritePolicyAllow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RepoWritePolicy(tc.repo); got != tc.want {
				t.Errorf("RepoWritePolicy = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWritePolicyValid(t *testing.T) {
	for _, p := range []WritePolicy{WritePolicyAllow, WritePolicyAllowOnce, WritePolicyDeny} {
		if !p.Valid() {
			t.Errorf("%q should be valid", p)
		}
	}
	for _, p := range []WritePolicy{"", "Allow", "read_only"} {
		if p.Valid() {
			t.Errorf("%q should be invalid", p)
		}
	}
}

func TestRepoAllowsRedeployLatest(t *testing.T) {
	cfg := map[string]any{AllowRedeployLatestKey: true}
	if !RepoAllowsRedeployLatest(&Repository{Format: FormatDocker, FormatConfig: cfg}) {
		t.Error("docker with the flag set should allow redeploying latest")
	}
	if !RepoAllowsRedeployLatest(&Repository{Format: FormatOCI, FormatConfig: cfg}) {
		t.Error("oci with the flag set should allow redeploying latest")
	}
	if RepoAllowsRedeployLatest(&Repository{Format: FormatRaw, FormatConfig: cfg}) {
		t.Error("a non-registry format has no latest tag")
	}
	if RepoAllowsRedeployLatest(&Repository{Format: FormatDocker}) {
		t.Error("absent flag must read as false")
	}
	if RepoAllowsRedeployLatest(&Repository{Format: FormatDocker, FormatConfig: map[string]any{AllowRedeployLatestKey: "true"}}) {
		t.Error("a non-boolean flag must read as false")
	}
	if RepoAllowsRedeployLatest(nil) {
		t.Error("nil repo")
	}
}
