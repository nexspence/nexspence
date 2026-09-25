package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func TestRepositoryService_Create_ValidatesWritePolicy(t *testing.T) {
	cases := []struct {
		name    string
		format  domain.RepoFormat
		typ     domain.RepoType
		cfg     map[string]any
		wantErr bool
	}{
		{"absent", domain.FormatRaw, domain.TypeHosted, nil, false},
		{"allow", domain.FormatRaw, domain.TypeHosted, map[string]any{"write_policy": "allow"}, false},
		{"allow_once", domain.FormatMaven2, domain.TypeHosted, map[string]any{"write_policy": "allow_once"}, false},
		{"deny", domain.FormatNPM, domain.TypeHosted, map[string]any{"write_policy": "deny"}, false},
		{"null value", domain.FormatRaw, domain.TypeHosted, map[string]any{"write_policy": nil}, false},
		{"unknown value", domain.FormatRaw, domain.TypeHosted, map[string]any{"write_policy": "read_only"}, true},
		{"wrong type", domain.FormatRaw, domain.TypeHosted, map[string]any{"write_policy": 1}, true},
		{"deny on group", domain.FormatRaw, domain.TypeGroup, map[string]any{"write_policy": "deny"}, true},
		{"allow on group is a no-op", domain.FormatRaw, domain.TypeGroup, map[string]any{"write_policy": "allow"}, false},
		{"latest on docker", domain.FormatDocker, domain.TypeHosted, map[string]any{"write_policy": "allow_once", "allow_redeploy_latest": true}, false},
		{"latest on oci", domain.FormatOCI, domain.TypeHosted, map[string]any{"allow_redeploy_latest": true}, false},
		{"latest false anywhere", domain.FormatRaw, domain.TypeHosted, map[string]any{"allow_redeploy_latest": false}, false},
		{"latest on raw", domain.FormatRaw, domain.TypeHosted, map[string]any{"allow_redeploy_latest": true}, true},
		{"latest not a bool", domain.FormatDocker, domain.TypeHosted, map[string]any{"allow_redeploy_latest": "yes"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repos := testutil.NewRepoRepo()
			svc := routingSvc(repos)
			r := &domain.Repository{Name: "wp", Format: tc.format, Type: tc.typ, FormatConfig: tc.cfg}
			if tc.typ == domain.TypeGroup {
				// Group validation needs a member; the policy check runs first.
				_ = repos.Create(context.Background(), &domain.Repository{Name: "m", Format: tc.format, Type: domain.TypeHosted})
				if r.FormatConfig == nil {
					r.FormatConfig = map[string]any{}
				}
				r.FormatConfig["member_names"] = []any{"m"}
			}
			err := svc.Create(context.Background(), r)
			if tc.wantErr {
				if !errors.Is(err, service.ErrInvalidInput) {
					t.Fatalf("err = %v, want ErrInvalidInput", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
		})
	}
}

func TestRepositoryService_Update_ValidatesAndPersistsWritePolicy(t *testing.T) {
	repos := testutil.NewRepoRepo()
	svc := routingSvc(repos)
	existingRepo(t, repos, svc, nil)
	ctx := context.Background()

	updated, err := svc.Update(ctx, "r1", &domain.Repository{Online: true,
		FormatConfig: map[string]any{"write_policy": "allow_once"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if domain.RepoWritePolicy(updated) != domain.WritePolicyAllowOnce {
		t.Fatalf("policy after update = %v", updated.FormatConfig)
	}

	if _, err := svc.Update(ctx, "r1", &domain.Repository{Online: true,
		FormatConfig: map[string]any{"write_policy": "sometimes"}}); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("invalid update: err = %v, want ErrInvalidInput", err)
	}
	stored, _ := repos.Get(ctx, "r1")
	if domain.RepoWritePolicy(stored) != domain.WritePolicyAllowOnce {
		t.Errorf("a rejected update changed the stored policy: %v", stored.FormatConfig)
	}
}
