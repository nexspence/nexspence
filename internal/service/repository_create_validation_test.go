package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func newRepoService() *service.RepositoryService {
	return service.NewRepositoryService(
		testutil.NewRepoRepo(),
		testutil.NewBlobStoreRepo(),
		testutil.NewBlobStore(),
		testutil.NewCleanupPolicyRepo(),
	)
}

// #592: a name a client cannot put into a URL as one path segment lands its
// requests on another repository; it is refused for every format.
func TestRepositoryService_Create_RejectsUnaddressableNames(t *testing.T) {
	for _, name := range []string{"demo#2", "p40 space", "p40/slash", `p40\back`, "p40%2", "p40-q?x", "tab\tname", "ctl\x01", ".", ".."} {
		err := newRepoService().Create(context.Background(), &domain.Repository{
			Name: name, Format: domain.FormatRaw, Type: domain.TypeHosted,
		})
		if !errors.Is(err, service.ErrInvalidInput) {
			t.Errorf("Create(%q) = %v, want ErrInvalidInput", name, err)
		}
	}
	for _, name := range []string{"ok.name-1", "My.Repo_2", "a..b"} {
		if err := newRepoService().Create(context.Background(), &domain.Repository{
			Name: name, Format: domain.FormatRaw, Type: domain.TypeHosted,
		}); err != nil {
			t.Errorf("Create(%q) = %v, want accepted", name, err)
		}
	}
}

// #593: an unknown format or type is invalid input, not a constraint
// violation surfacing as a 500.
func TestRepositoryService_Create_RejectsUnknownFormatAndType(t *testing.T) {
	for _, r := range []*domain.Repository{
		{Name: "x1", Format: "apk", Type: domain.TypeHosted},
		{Name: "x2", Format: "foo", Type: domain.TypeProxy},
		{Name: "x3", Format: domain.FormatRaw, Type: "virtual"},
	} {
		err := newRepoService().Create(context.Background(), r)
		if !errors.Is(err, service.ErrInvalidInput) {
			t.Errorf("Create(%s/%s) = %v, want ErrInvalidInput", r.Format, r.Type, err)
		}
	}
}
