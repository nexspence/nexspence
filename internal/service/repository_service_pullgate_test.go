package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// pull_block_severity (#641): docker/oci only, hosted/proxy/group alike, one of
// the pull gate's thresholds or empty for off.
func TestRepositoryService_Create_ValidatesPullBlockSeverity(t *testing.T) {
	cases := []struct {
		name    string
		format  domain.RepoFormat
		typ     domain.RepoType
		value   any
		want    string
		wantErr bool
	}{
		{"hosted docker", domain.FormatDocker, domain.TypeHosted, "high", "high", false},
		{"proxy docker, normalized", domain.FormatDocker, domain.TypeProxy, " Critical ", "critical", false},
		{"group docker", domain.FormatDocker, domain.TypeGroup, "malicious", "malicious", false},
		{"hosted oci", domain.FormatOCI, domain.TypeHosted, "low", "low", false},
		{"empty is off", domain.FormatDocker, domain.TypeHosted, "", "", false},
		{"empty on raw is a no-op", domain.FormatRaw, domain.TypeHosted, "", "", false},
		{"negligible never blocks", domain.FormatDocker, domain.TypeHosted, "negligible", "", true},
		{"unknown never blocks", domain.FormatDocker, domain.TypeHosted, "unknown", "", true},
		{"not a string", domain.FormatDocker, domain.TypeHosted, 7, "", true},
		{"on a raw repository", domain.FormatRaw, domain.TypeHosted, "high", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repos := testutil.NewRepoRepo()
			svc := routingSvc(repos)
			r := &domain.Repository{Name: "pg", Format: tc.format, Type: tc.typ,
				FormatConfig: map[string]any{domain.PullBlockSeverityKey: tc.value}}
			switch tc.typ {
			case domain.TypeGroup:
				_ = repos.Create(context.Background(), &domain.Repository{Name: "m", Format: tc.format, Type: domain.TypeHosted})
				r.FormatConfig["member_names"] = []any{"m"}
			case domain.TypeProxy:
				r.ProxyConfig = map[string]any{"remote_url": "https://registry-1.docker.io"}
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
			if got := r.FormatConfig[domain.PullBlockSeverityKey]; got != tc.want {
				t.Errorf("stored %s = %v, want %q", domain.PullBlockSeverityKey, got, tc.want)
			}
		})
	}
}

func TestRepositoryService_Update_ValidatesPullBlockSeverity(t *testing.T) {
	repos := testutil.NewRepoRepo()
	svc := routingSvc(repos)
	ctx := context.Background()
	if err := svc.Create(ctx, &domain.Repository{Name: "img", Format: domain.FormatDocker, Type: domain.TypeHosted}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := svc.Update(ctx, "img", &domain.Repository{Online: true,
		FormatConfig: map[string]any{domain.PullBlockSeverityKey: "HIGH"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := domain.RepoPullBlockSeverity(updated); got != "high" {
		t.Fatalf("threshold after update = %q (%v)", got, updated.FormatConfig)
	}

	if _, err := svc.Update(ctx, "img", &domain.Repository{Online: true,
		FormatConfig: map[string]any{domain.PullBlockSeverityKey: "negligible"}}); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("invalid update: err = %v, want ErrInvalidInput", err)
	}
	stored, _ := repos.Get(ctx, "img")
	if got := domain.RepoPullBlockSeverity(stored); got != "high" {
		t.Errorf("a rejected update changed the stored threshold: %v", stored.FormatConfig)
	}
}
