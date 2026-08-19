package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A docker/oci repository whose name fails the distribution path-component
// grammar exists but can never be pushed to or pulled from (#262), so Create
// refuses it with an error naming the rule.
func TestRepositoryService_Create_DockerNameGrammar(t *testing.T) {
	rejected := []string{
		"docker test", // the report's repository: a space
		"Docker-Test", // uppercase
		"-leading",    // separator first
		"trailing.",   // separator last
		"a..b",        // empty component between separators
		"под-докер",   // non-ASCII
	}
	accepted := []string{
		"docker-hosted",
		"a",
		"team.registry_2",
		"a--b", // multiple dashes join as one separator run
	}

	for _, name := range rejected {
		svc := newRepoSvcFull(testutil.NewRepoRepo(), testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())
		err := svc.Create(context.Background(), &domain.Repository{
			Name: name, Format: domain.FormatDocker, Type: domain.TypeHosted,
		})
		if !errors.Is(err, service.ErrInvalidInput) {
			t.Errorf("Create(%q): got %v, want ErrInvalidInput", name, err)
		}
		if err != nil && !strings.Contains(err.Error(), "docker clients") {
			t.Errorf("Create(%q): error %q does not explain the docker grammar", name, err)
		}
	}
	for _, name := range accepted {
		svc := newRepoSvcFull(testutil.NewRepoRepo(), testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())
		if err := svc.Create(context.Background(), &domain.Repository{
			Name: name, Format: domain.FormatOCI, Type: domain.TypeHosted,
		}); err != nil {
			t.Errorf("Create(%q): unexpected error %v", name, err)
		}
	}
}

// Only the OCI-registry formats are gated: other formats merely produce
// percent-encoded URLs from an odd name, and existing deployments hold such
// repositories that keep working.
func TestRepositoryService_Create_OtherFormatsKeepLooseNames(t *testing.T) {
	svc := newRepoSvcFull(testutil.NewRepoRepo(), testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())
	err := svc.Create(context.Background(), &domain.Repository{
		Name: "Gemma3", Format: domain.FormatConan, Type: domain.TypeHosted,
	})
	if err != nil {
		t.Fatalf("Create(conan \"Gemma3\"): unexpected error %v", err)
	}
}

// The /v2/ surface has static routes (the long-form dispatch, the token
// endpoint, the instance catalog): gin matches those before /v2/:repoName, so
// a repository named after one is created successfully and then unreachable
// on the short docker URL — the same dead end #262 is about.
func TestRepositoryService_Create_RejectsReservedV2Names(t *testing.T) {
	for _, name := range []string{"repository", "token", "_catalog"} {
		svc := newRepoSvcFull(testutil.NewRepoRepo(), testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())
		err := svc.Create(context.Background(), &domain.Repository{
			Name: name, Format: domain.FormatDocker, Type: domain.TypeHosted,
		})
		if !errors.Is(err, service.ErrInvalidInput) {
			t.Errorf("Create(%q): got %v, want ErrInvalidInput", name, err)
		}
	}

	// Only the /v2/ surface is gated: an npm repository named "token" is
	// addressed under /repository/<name>/ and works fine.
	svc := newRepoSvcFull(testutil.NewRepoRepo(), testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())
	if err := svc.Create(context.Background(), &domain.Repository{
		Name: "token", Format: domain.FormatNPM, Type: domain.TypeHosted,
	}); err != nil {
		t.Fatalf("Create(npm \"token\"): unexpected error %v", err)
	}
}
