package postgres_test

import (
	"testing"

	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func TestListPathsByRepo_mock(t *testing.T) {
	mock := testutil.NewAssetRepo()
	// mock returns empty by default — integration tests use real DB
	paths, err := mock.ListPathsByRepo(t.Context(), "my-repo", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = paths // just checking interface compiles
}
