package service_test

import (
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/service"
)

func TestWithTrivy_ReplacesTheDefaults(t *testing.T) {
	svc := service.NewScanService(nil, "http://localhost:8081").
		WithTrivy(service.TrivyOptions{Enabled: true, Bin: "/opt/trivy/trivy"})

	got := svc.TrivyOptions()
	if !got.Enabled {
		t.Error("Enabled was not carried through")
	}
	if got.Bin != "/opt/trivy/trivy" {
		t.Errorf("Bin = %q, want /opt/trivy/trivy", got.Bin)
	}
}

func TestNewScanService_DefaultBinIsTrivyOnPath(t *testing.T) {
	svc := service.NewScanService(nil, "http://localhost:8081")
	if got := svc.TrivyOptions().Bin; got != "trivy" {
		t.Errorf("default Bin = %q, want \"trivy\"", got)
	}
	if svc.TrivyOptions().Enabled {
		t.Error("scanning must be off until an operator turns it on")
	}
}

func TestBinOrDefault(t *testing.T) {
	if got := (service.TrivyOptions{}).BinOrDefault(); got != "trivy" {
		t.Errorf("empty Bin: BinOrDefault() = %q, want \"trivy\"", got)
	}
	if got := (service.TrivyOptions{Bin: "/x"}).BinOrDefault(); got != "/x" {
		t.Errorf("Bin=/x: BinOrDefault() = %q, want \"/x\"", got)
	}
}

func TestTrivyArgs_OmitsEveryUnsetFlag(t *testing.T) {
	args := service.TrivyScanArgs(service.TrivyOptions{Enabled: true, Bin: "trivy"}, "reg/img:1", false)

	for _, unwanted := range []string{"--db-repository", "--java-db-repository", "--skip-db-update", "--skip-java-db-update", "--cache-dir", "--insecure"} {
		for _, got := range args {
			if got == unwanted {
				t.Errorf("argv carries %s for an unset option; an empty value must mean \"do not pass the flag\"", unwanted)
			}
		}
	}
	if args[len(args)-1] != "reg/img:1" {
		t.Errorf("last arg = %q, want the image reference", args[len(args)-1])
	}
}

func TestTrivyArgs_PassesConfiguredDatabases(t *testing.T) {
	args := service.TrivyScanArgs(service.TrivyOptions{
		Enabled:          true,
		DBRepository:     []string{"nexspence.example.com/repository/ghcr/aquasecurity/trivy-db:2", "ghcr.io/aquasecurity/trivy-db:2"},
		JavaDBRepository: []string{"nexspence.example.com/repository/ghcr/aquasecurity/trivy-java-db:1"},
		SkipDBUpdate:     true,
		CacheDir:         "/var/cache/trivy",
	}, "reg/img:1", true)

	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--db-repository nexspence.example.com/repository/ghcr/aquasecurity/trivy-db:2,ghcr.io/aquasecurity/trivy-db:2",
		"--java-db-repository nexspence.example.com/repository/ghcr/aquasecurity/trivy-java-db:1",
		"--skip-db-update",
		"--skip-java-db-update",
		"--cache-dir /var/cache/trivy",
		"--insecure",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q is missing %q", joined, want)
		}
	}
}

func TestTrivyEnv_CarriesCredentialsOutOfArgv(t *testing.T) {
	env := service.TrivyEnv([]string{"PATH=/usr/bin"}, "scanner", "s3cr3t")

	joined := strings.Join(env, " ")
	if !strings.Contains(joined, "TRIVY_USERNAME=scanner") || !strings.Contains(joined, "TRIVY_PASSWORD=s3cr3t") {
		t.Errorf("env = %v, want the credentials in it", env)
	}
	if !strings.Contains(joined, "PATH=/usr/bin") {
		t.Error("the parent environment must be preserved — dropping PATH breaks the binary lookup")
	}

	args := service.TrivyScanArgs(service.TrivyOptions{Enabled: true}, "reg/img:1", false)
	for _, a := range args {
		if strings.Contains(a, "s3cr3t") || a == "--password" {
			t.Error("the password must never reach argv: it is readable through the process table")
		}
	}
}

func TestTrivyEnv_OmitsEmptyCredentials(t *testing.T) {
	env := service.TrivyEnv([]string{"PATH=/usr/bin"}, "", "")
	for _, e := range env {
		if strings.HasPrefix(e, "TRIVY_USERNAME=") || strings.HasPrefix(e, "TRIVY_PASSWORD=") {
			t.Errorf("env carries %q for empty credentials; an empty username must not authenticate as one", e)
		}
	}
}
