package testutil_test

import (
	"context"
	"sort"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func TestListPathsByRepo_prefixExtraction(t *testing.T) {
	repo := testutil.NewAssetRepo()
	ctx := context.Background()

	// Seed assets in "my-repo"
	for _, a := range []*domain.Asset{
		{Repository: "my-repo", Path: "/da/devops/app-1.0.jar"},
		{Repository: "my-repo", Path: "/da/devops/sub/lib.jar"},
		{Repository: "my-repo", Path: "/other/tool.jar"},
		// asset in a different repo — must not appear
		{Repository: "other-repo", Path: "/da/devops/should-not-appear.jar"},
	} {
		if err := repo.Create(ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	paths, err := repo.ListPathsByRepo(ctx, "my-repo", "")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"/da/", "/da/devops/", "/da/devops/sub/", "/other/"}
	sort.Strings(want)
	sort.Strings(paths)

	if len(paths) != len(want) {
		t.Fatalf("want %v, got %v", want, paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("paths[%d]: want %q, got %q", i, want[i], paths[i])
		}
	}
}

func TestListPathsByRepo_qFilter(t *testing.T) {
	repo := testutil.NewAssetRepo()
	ctx := context.Background()

	for _, a := range []*domain.Asset{
		{Repository: "r", Path: "/da/devops/foo.jar"},
		{Repository: "r", Path: "/other/tool.jar"},
	} {
		if err := repo.Create(ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	paths, err := repo.ListPathsByRepo(ctx, "r", "dev")
	if err != nil {
		t.Fatal(err)
	}
	// only /da/devops/ contains "dev"
	if len(paths) != 1 || paths[0] != "/da/devops/" {
		t.Errorf("want [\"/da/devops/\"], got %v", paths)
	}
}
